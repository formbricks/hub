// Package validation provides request validation and custom validators.
package validation

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/go-playground/form/v4"
	"github.com/go-playground/validator/v10"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/models"
)

var (
	// validate and decoder are package-level singletons that are safe for concurrent
	// read-only access (validate.Struct() and decoder.Decode() are thread-safe).
	// All registrations (RegisterValidation, RegisterCustomTypeFunc, etc.) MUST happen
	// in init() only, as these methods are NOT thread-safe. Do NOT modify these
	// instances after init() completes.
	validate *validator.Validate
	decoder  *form.Decoder
)

func init() {
	validate = validator.New()
	decoder = form.NewDecoder()

	// Register custom validators
	if err := validate.RegisterValidation("field_type", validateFieldType); err != nil {
		slog.Error("Failed to register field_type validator", "error", err)
	}

	if err := validate.RegisterValidation("no_null_bytes", validateNoNullBytes); err != nil {
		slog.Error("Failed to register no_null_bytes validator", "error", err)
	}

	// Register custom type converters for form decoding
	// Handle *time.Time (pointer type used in our models)
	decoder.RegisterCustomTypeFunc(func(vals []string) (any, error) {
		if len(vals) == 0 || vals[0] == "" {
			return (*time.Time)(nil), nil
		}

		t, err := time.Parse(time.RFC3339, vals[0])
		if err != nil {
			return nil, fmt.Errorf("invalid date format, expected RFC3339 (ISO 8601): %w", err)
		}

		return &t, nil
	}, (*time.Time)(nil))

	// Handle *models.FieldType (pointer type used in filters)
	decoder.RegisterCustomTypeFunc(func(vals []string) (any, error) {
		if len(vals) == 0 || vals[0] == "" {
			return (*models.FieldType)(nil), nil
		}

		ft, err := models.ParseFieldType(vals[0])
		if err != nil {
			return nil, fmt.Errorf("invalid field type: %w", err)
		}

		return &ft, nil
	}, (*models.FieldType)(nil))
}

// ValidateStruct validates a struct using go-playground/validator
// Returns validation errors formatted as RFC 7807 Problem Details.
func ValidateStruct(s any) error {
	if err := validate.Struct(s); err != nil {
		return formatValidationErrors(err)
	}

	return nil
}

// formatValidationErrors converts validator errors to a formatted error message
// that can be used in RFC 7807 Problem Details responses.
func formatValidationErrors(err error) error {
	var validationErrors validator.ValidationErrors
	if errors.As(err, &validationErrors) {
		messages := make([]string, 0, len(validationErrors))
		for _, fieldError := range validationErrors {
			messages = append(messages, formatFieldError(fieldError))
		}

		return fmt.Errorf("validation failed: %s", strings.Join(messages, "; "))
	}

	return err
}

// formatFieldError formats a single field validation error.
func formatFieldError(fieldError validator.FieldError) string {
	field := fieldError.Field()
	tag := fieldError.Tag()

	switch tag {
	case "required":
		return field + " is required"
	case "min":
		return fmt.Sprintf("%s must be at least %s", field, fieldError.Param())
	case "max":
		return fmt.Sprintf("%s must be at most %s", field, fieldError.Param())
	case "gte":
		return fmt.Sprintf("%s must be greater than or equal to %s", field, fieldError.Param())
	case "lte":
		return fmt.Sprintf("%s must be less than or equal to %s", field, fieldError.Param())
	case "oneof":
		return fmt.Sprintf("%s must be one of: %s", field, fieldError.Param())
	case "field_type":
		return field + " must be one of: text, categorical, nps, csat, ces, rating, number, boolean, date"
	case "uuid":
		return field + " must be a valid UUID"
	case "rfc3339":
		return field + " must be in RFC3339 format (ISO 8601)"
	case "no_null_bytes":
		return field + " must not contain NULL bytes"
	default:
		return field + " is invalid"
	}
}

// GetValidationErrorDetails extracts field-level error details from validation errors
// Returns a slice of ErrorDetail for RFC 7807 Problem Details.
func GetValidationErrorDetails(err error) []response.ErrorDetail {
	var details []response.ErrorDetail

	var validationErrors validator.ValidationErrors
	if errors.As(err, &validationErrors) {
		for _, fieldError := range validationErrors {
			details = append(details, response.ErrorDetail{
				Location: fieldError.Field(),
				Message:  formatFieldError(fieldError),
				Value:    fieldError.Value(),
			})
		}
	}

	return details
}

// RespondValidationError writes a validation error response with RFC 7807 Problem Details.
func RespondValidationError(w http.ResponseWriter, err error) {
	details := GetValidationErrorDetails(err)

	problem := response.ProblemDetails{
		Type:   "about:blank",
		Title:  "Validation Error",
		Status: http.StatusBadRequest,
		Detail: err.Error(),
		Errors: details,
	}

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusBadRequest)

	if err := json.NewEncoder(w).Encode(problem); err != nil {
		slog.Error("Failed to encode validation error response", "error", err)
	}
}

// DecodeQueryParams decodes URL query parameters into a struct.
func DecodeQueryParams(r *http.Request, dst any) error {
	if err := decoder.Decode(dst, r.URL.Query()); err != nil {
		return fmt.Errorf("failed to decode query parameters: %w", err)
	}

	return nil
}

// ValidateAndDecodeQueryParams decodes and validates query parameters in one step.
func ValidateAndDecodeQueryParams(r *http.Request, dst any) error {
	if err := DecodeQueryParams(r, dst); err != nil {
		return err
	}

	return ValidateStruct(dst)
}

// validateFieldType is a custom validator for field_type enum
// It validates both string and FieldType types.
func validateFieldType(fl validator.FieldLevel) bool {
	field := fl.Field()

	// Handle FieldType enum type directly
	if field.Type() == reflect.TypeFor[models.FieldType]() {
		ft := models.FieldType(field.String())

		return ft.IsValid()
	}

	// Handle string type (from JSON/query params)
	if field.Kind() == reflect.String {
		_, err := models.ParseFieldType(field.String())

		return err == nil
	}

	return false
}

// validateNoNullBytes checks that a string field does not contain NULL bytes
// Handles both string and *string types.
func validateNoNullBytes(fl validator.FieldLevel) bool {
	field := fl.Field()

	// Handle pointer types
	if field.Kind() == reflect.Ptr {
		if field.IsNil() {
			return true // nil pointer is valid (handled by omitempty)
		}

		field = field.Elem()
	}

	// Must be a string type
	if field.Kind() != reflect.String {
		return true // Not a string, skip validation
	}

	value := field.String()

	return !strings.Contains(value, "\x00")
}
