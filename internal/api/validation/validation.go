package validation

import (
	"bytes"
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
	if err := validate.RegisterValidation("json_object", validateJSONObject); err != nil {
		slog.Error("Failed to register json_object validator", "error", err)
	}
	if err := validate.RegisterValidation("date_range", validateDateRange); err != nil {
		slog.Error("Failed to register date_range validator", "error", err)
	}
	if err := validate.RegisterValidation("numeric_range", validateNumericRange); err != nil {
		slog.Error("Failed to register numeric_range validator", "error", err)
	}
	if err := validate.RegisterValidation("no_null_bytes", validateNoNullBytes); err != nil {
		slog.Error("Failed to register no_null_bytes validator", "error", err)
	}
	if err := validate.RegisterValidation("json_no_null_bytes", validateJSONNoNullBytes); err != nil {
		slog.Error("Failed to register json_no_null_bytes validator", "error", err)
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
	case "json_object":
		return fmt.Sprintf("%s must be a valid JSON object", field)
	case "date_range":
		return fmt.Sprintf("%s must be between 1970-01-01 and 2080-12-31", field)
	case "numeric_range":
		return fmt.Sprintf("%s must be between -1e15 and +1e15", field)
	case "no_null_bytes":
		return fmt.Sprintf("%s contains invalid character encoding (NULL bytes are not allowed)", field)
	case "json_no_null_bytes":
		return fmt.Sprintf("%s contains invalid character encoding (NULL bytes are not allowed in JSON)", field)
	case "uuid":
		return fmt.Sprintf("%s must be a valid UUID", field)
	case "rfc3339":
		return fmt.Sprintf("%s must be in RFC3339 format (ISO 8601)", field)
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

// validateJSONObject validates that a json.RawMessage field is a valid JSON object
// (not null, not an array, not a primitive). If the field is empty/nil, validation passes
// (use with omitempty for optional fields).
func validateJSONObject(fl validator.FieldLevel) bool {
	// Get the field value as json.RawMessage (which is []byte)
	rawMsg := fl.Field().Interface().(json.RawMessage)

	// If empty, validation passes (use with omitempty for optional fields)
	if len(rawMsg) == 0 {
		return true
	}

	// Trim whitespace to check for "null"
	valueStr := strings.TrimSpace(string(rawMsg))
	if valueStr == "" || valueStr == "null" {
		return false
	}

	// Must start with '{' to be a JSON object
	if !strings.HasPrefix(valueStr, "{") {
		return false
	}

	// Try to unmarshal as a map to ensure it's valid JSON and an object
	var test map[string]interface{}
	if err := json.Unmarshal(rawMsg, &test); err != nil {
		return false
	}

	// Ensure it's not nil (shouldn't happen after successful unmarshal, but be safe)
	return test != nil
}

// validateDateRange validates that a time.Time is within the allowed range (1970-01-01 to 2080-12-31)
func validateDateRange(fl validator.FieldLevel) bool {
	field := fl.Field()
	if !field.IsValid() || field.IsZero() {
		return true // Let omitempty handle empty values
	}

	// Handle *time.Time (pointer)
	if field.Kind() == reflect.Ptr {
		if field.IsNil() {
			return true // nil is valid (omitempty)
		}
		field = field.Elem()
	}

	if field.Kind() != reflect.Struct {
		return false
	}

	// Extract time.Time value
	timeValue, ok := field.Interface().(time.Time)
	if !ok {
		return false
	}

	minDate := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	maxDate := time.Date(2080, 12, 31, 23, 59, 59, 999999999, time.UTC)

	return !timeValue.Before(minDate) && !timeValue.After(maxDate)
}

// validateNumericRange validates that a numeric value is within the allowed range (-1e15 to +1e15)
func validateNumericRange(fl validator.FieldLevel) bool {
	field := fl.Field()
	if !field.IsValid() || field.IsZero() {
		return true // Let omitempty handle empty values
	}

	// Handle *float64 (pointer)
	if field.Kind() == reflect.Ptr {
		if field.IsNil() {
			return true // nil is valid (omitempty)
		}
		field = field.Elem()
	}

	var value float64
	switch field.Kind() {
	case reflect.Float32, reflect.Float64:
		value = field.Float()
	default:
		return false
	}

	const minValue = -1e15
	const maxValue = 1e15

	// Check for NaN
	if value != value {
		return false
	}

	// Check for Infinity (approximate)
	if value > 1e308 || value < -1e308 {
		return false
	}

	return value >= minValue && value <= maxValue
}

// validateNoNullBytes validates that a string does not contain NULL bytes
func validateNoNullBytes(fl validator.FieldLevel) bool {
	field := fl.Field()
	if !field.IsValid() || field.IsZero() {
		return true // Let omitempty handle empty values
	}

	// Handle *string (pointer)
	if field.Kind() == reflect.Ptr {
		if field.IsNil() {
			return true // nil is valid (omitempty)
		}
		field = field.Elem()
	}

	if field.Kind() != reflect.String {
		return false
	}

	value := field.String()
	return !strings.Contains(value, "\x00")
}

// validateJSONNoNullBytes validates that a json.RawMessage does not contain NULL bytes or \u0000
func validateJSONNoNullBytes(fl validator.FieldLevel) bool {
	field := fl.Field()
	if !field.IsValid() || field.IsZero() {
		return true // Let omitempty handle empty values
	}

	// json.RawMessage is []byte, not a pointer
	if field.Kind() != reflect.Slice || field.Type() != reflect.TypeOf(json.RawMessage{}) {
		return false
	}

	value := field.Bytes()
	if len(value) == 0 {
		return true // Empty JSON is valid
	}

	// Check for NULL bytes
	if bytes.Contains(value, []byte("\x00")) {
		return false
	}

	// Check for \u0000 escape sequence
	if bytes.Contains(value, []byte("\\u0000")) {
		return false
	}

	return true
}
