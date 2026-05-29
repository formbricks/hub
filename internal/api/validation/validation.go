// Package validation provides request validation and custom validators.
package validation

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/go-playground/form/v4"
	"github.com/go-playground/validator/v10"

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

const tagSplitParts = 2

// ErrValidationFailed is returned when struct validation fails (err113).
var ErrValidationFailed = errors.New("validation failed")

// ErrQueryDecodeFailed is returned when query parameters cannot be decoded into
// the target filter struct.
var ErrQueryDecodeFailed = errors.New("query parameter decode failed")

// InvalidParam describes a request parameter that failed validation or decoding.
type InvalidParam struct {
	Name   string
	Reason string
}

// QueryDecodeError preserves the original form decoder error while exposing
// field-level invalid params for RFC 9457 responses.
type QueryDecodeError struct {
	err    error
	params []InvalidParam
}

func (e *QueryDecodeError) Error() string {
	if e == nil || e.err == nil {
		return ErrQueryDecodeFailed.Error()
	}

	return ErrQueryDecodeFailed.Error() + ": " + e.err.Error()
}

func (e *QueryDecodeError) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.err
}

// Is reports whether target is ErrQueryDecodeFailed.
func (e *QueryDecodeError) Is(target error) bool {
	return target == ErrQueryDecodeFailed
}

// InvalidParams returns a copy of the query parameters that failed decoding.
func (e *QueryDecodeError) InvalidParams() []InvalidParam {
	if e == nil {
		return nil
	}

	return append([]InvalidParam(nil), e.params...)
}

func init() {
	validate = validator.New()
	decoder = form.NewDecoder()

	validate.RegisterTagNameFunc(func(field reflect.StructField) string {
		if name := tagName(field, "json"); name != "" {
			return name
		}

		if name := tagName(field, "form"); name != "" {
			return name
		}

		return field.Name
	})

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
// Returns validation errors formatted for RFC 9457 Problem Details.
func ValidateStruct(s any) error {
	if err := validate.Struct(s); err != nil {
		return formatValidationErrors(err)
	}

	return nil
}

// formatValidationErrors wraps validator errors so they carry both a readable
// joined message (for logs) and the underlying validator.ValidationErrors, which
// the response layer extracts to build RFC 9457 invalid_params.
func formatValidationErrors(err error) error {
	var validationErrors validator.ValidationErrors
	if errors.As(err, &validationErrors) {
		messages := make([]string, 0, len(validationErrors))
		for _, fieldError := range validationErrors {
			messages = append(messages, fieldError.Field()+" "+FormatFieldError(fieldError))
		}

		return requestValidationError{
			message:          fmt.Sprintf("%s: %s", ErrValidationFailed, strings.Join(messages, "; ")),
			validationErrors: validationErrors,
		}
	}

	return err
}

type requestValidationError struct {
	message          string
	validationErrors validator.ValidationErrors
}

func (e requestValidationError) Error() string {
	return e.message
}

func (e requestValidationError) Is(target error) bool {
	return target == ErrValidationFailed
}

func (e requestValidationError) As(target any) bool {
	validationErrors, ok := target.(*validator.ValidationErrors)
	if !ok {
		return false
	}

	*validationErrors = e.validationErrors

	return true
}

// DecodeQueryParams decodes URL query parameters into a struct.
func DecodeQueryParams(r *http.Request, dst any) error {
	if err := decoder.Decode(dst, r.URL.Query()); err != nil {
		return newQueryDecodeError(err)
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

func newQueryDecodeError(err error) *QueryDecodeError {
	return &QueryDecodeError{
		err:    err,
		params: invalidParamsFromQueryDecodeError(err),
	}
}

func invalidParamsFromQueryDecodeError(err error) []InvalidParam {
	var decodeErrors form.DecodeErrors
	if !errors.As(err, &decodeErrors) {
		return []InvalidParam{{Name: "query", Reason: "contains invalid query parameters"}}
	}

	fields := make([]string, 0, len(decodeErrors))
	for field := range decodeErrors {
		fields = append(fields, field)
	}

	sort.Strings(fields)

	params := make([]InvalidParam, 0, len(fields))
	for _, field := range fields {
		params = append(params, InvalidParam{
			Name:   strings.TrimPrefix(field, "."),
			Reason: queryDecodeReason(decodeErrors[field]),
		})
	}

	return params
}

// queryDecodeReason classifies a form decoder error into an agent-readable
// reason. The custom-converter branch above uses errors.As on our own typed
// error and is robust; the cases below string-match against go-playground/form's
// stdlib-type error wording. If that library changes its messages, these
// branches silently fall back to the generic reason — the tests in
// TestValidateAndDecodeQueryParamsReturnsInvalidParams cover each branch and
// will catch a regression on upgrade.
func queryDecodeReason(err error) string {
	var invalidFieldType *models.InvalidFieldTypeError
	if errors.As(err, &invalidFieldType) {
		return "must be one of: " + models.ValidFieldTypeValuesString()
	}

	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "invalid date format"):
		return "must be in RFC3339 (ISO 8601) format"
	case strings.Contains(text, "invalid boolean value"):
		return "must be a valid boolean"
	case strings.Contains(text, "invalid integer value"), strings.Contains(text, "invalid unsigned integer value"):
		return "must be a valid integer"
	case strings.Contains(text, "invalid float value"):
		return "must be a valid number"
	default:
		return "must be a valid query parameter value"
	}
}

// FieldPath returns the dotted path to the offending field using API (JSON/form)
// names, e.g. "field_type" or "items[0].field_type", so a client can map the
// error straight back to the input it sent.
func FieldPath(fieldErr validator.FieldError) string {
	if _, after, found := strings.Cut(fieldErr.Namespace(), "."); found {
		return after
	}

	return fieldErr.Field()
}

// FormatFieldError returns a self-correcting reason fragment for a single field
// validation failure, naming allowed values or constraints where applicable.
// The field name is carried separately in InvalidParam.Name, so the fragment
// does not repeat it.
func FormatFieldError(fieldErr validator.FieldError) string {
	switch fieldErr.Tag() {
	case "required":
		return "is required"
	case "min":
		return "must be at least " + fieldErr.Param()
	case "max":
		return "must be at most " + fieldErr.Param()
	case "gte":
		return "must be greater than or equal to " + fieldErr.Param()
	case "lte":
		return "must be less than or equal to " + fieldErr.Param()
	case "oneof":
		return "must be one of: " + fieldErr.Param()
	case "field_type":
		return "must be one of: " + models.ValidFieldTypeValuesString()
	case "uuid":
		return "must be a valid UUID"
	case "rfc3339":
		return "must be in RFC3339 (ISO 8601) format"
	case "no_null_bytes":
		return "must not contain NULL bytes"
	case "http_url":
		return "must be a valid HTTP or HTTPS URL"
	case "url":
		return "must be a valid URL"
	default:
		return "is invalid"
	}
}

// validateFieldType is a custom validator for field_type enum
// It validates both string and FieldType types.
func validateFieldType(fl validator.FieldLevel) bool {
	field := fl.Field()

	// Handle FieldType enum type directly
	if field.Type() == reflect.TypeFor[models.FieldType]() {
		ft := models.FieldType(field.String())

		return (&ft).IsValid()
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

func tagName(field reflect.StructField, key string) string {
	name := strings.SplitN(field.Tag.Get(key), ",", tagSplitParts)[0]
	if name == "-" {
		return ""
	}

	return name
}
