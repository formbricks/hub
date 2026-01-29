package validation

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/go-playground/form/v4"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
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
	decoder.RegisterCustomTypeFunc(func(vals []string) (interface{}, error) {
		if len(vals) == 0 || vals[0] == "" {
			return (*time.Time)(nil), nil
		}
		t, err := time.Parse(time.RFC3339, vals[0])
		if err != nil {
			return nil, fmt.Errorf("invalid date format, expected RFC3339 (ISO 8601): %w", err)
		}
		return &t, nil
	}, (*time.Time)(nil))

	// Handle *uuid.UUID (pointer type used for topic_id filters)
	decoder.RegisterCustomTypeFunc(func(vals []string) (interface{}, error) {
		if len(vals) == 0 || vals[0] == "" {
			return (*uuid.UUID)(nil), nil
		}
		id, err := uuid.Parse(vals[0])
		if err != nil {
			return nil, fmt.Errorf("invalid UUID format: %w", err)
		}
		return &id, nil
	}, (*uuid.UUID)(nil))
}

// ValidateStruct validates a struct using go-playground/validator
// Returns validation errors formatted as RFC 7807 Problem Details
func ValidateStruct(s interface{}) error {
	if err := validate.Struct(s); err != nil {
		return formatValidationErrors(err)
	}
	return nil
}

// formatValidationErrors converts validator errors to a formatted error message
// that can be used in RFC 7807 Problem Details responses
func formatValidationErrors(err error) error {
	if validationErrors, ok := err.(validator.ValidationErrors); ok {
		var messages []string
		for _, fieldError := range validationErrors {
			messages = append(messages, formatFieldError(fieldError))
		}
		return fmt.Errorf("validation failed: %s", strings.Join(messages, "; "))
	}
	return err
}

// formatFieldError formats a single field validation error
func formatFieldError(fieldError validator.FieldError) string {
	field := fieldError.Field()
	tag := fieldError.Tag()

	switch tag {
	case "required":
		return fmt.Sprintf("%s is required", field)
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
		return fmt.Sprintf("%s must be one of: text, categorical, nps, csat, ces, rating, number, boolean, date", field)
	case "uuid":
		return fmt.Sprintf("%s must be a valid UUID", field)
	case "rfc3339":
		return fmt.Sprintf("%s must be in RFC3339 format (ISO 8601)", field)
	case "no_null_bytes":
		return fmt.Sprintf("%s must not contain NULL bytes", field)
	default:
		return fmt.Sprintf("%s is invalid", field)
	}
}

// GetValidationErrorDetails extracts field-level error details from validation errors
// Returns a slice of ErrorDetail for RFC 7807 Problem Details
func GetValidationErrorDetails(err error) []response.ErrorDetail {
	var details []response.ErrorDetail

	if validationErrors, ok := err.(validator.ValidationErrors); ok {
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

// RespondValidationError writes a validation error response with RFC 7807 Problem Details
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

// DecodeQueryParams decodes URL query parameters into a struct
func DecodeQueryParams(r *http.Request, dst interface{}) error {
	if err := decoder.Decode(dst, r.URL.Query()); err != nil {
		return fmt.Errorf("failed to decode query parameters: %w", err)
	}
	return nil
}

// ValidateAndDecodeQueryParams decodes and validates query parameters in one step
func ValidateAndDecodeQueryParams(r *http.Request, dst interface{}) error {
	if err := DecodeQueryParams(r, dst); err != nil {
		return err
	}
	return ValidateStruct(dst)
}

// validateFieldType is a custom validator for field_type enum
func validateFieldType(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	validTypes := map[string]bool{
		"text":        true,
		"categorical": true,
		"nps":         true,
		"csat":        true,
		"ces":         true,
		"rating":      true,
		"number":      true,
		"boolean":     true,
		"date":        true,
	}
	return validTypes[value]
}

// validateNoNullBytes checks that a string field does not contain NULL bytes
// Handles both string and *string types
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
