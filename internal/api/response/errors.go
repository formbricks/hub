package response

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/iancoleman/strcase"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/pkg/cursor"
)

const (
	detailInternal      = "An unexpected error occurred"
	detailValidation    = "One or more request parameters are invalid"
	reasonInvalidCursor = "omit it for the first page, or use the exact next_cursor value from the previous response"
)

// problemFromError translates a Go error into an RFC 9457 problem. Domain and
// sentinel errors map to specific statuses, codes, and invalid_params; anything
// unrecognized maps to a generic 500 whose cause is logged but not exposed.
func problemFromError(err error) ProblemDetails {
	if err == nil {
		return newProblem(http.StatusInternalServerError, detailInternal)
	}

	var validationErrs validator.ValidationErrors
	if errors.As(err, &validationErrs) {
		problem := newValidationProblem()
		problem.InvalidParams = invalidParamsFromValidator(validationErrs)

		return problem
	}

	var validationErr *huberrors.ValidationError
	if errors.As(err, &validationErr) {
		problem := newValidationProblem()
		problem.InvalidParams = []InvalidParam{validationErrorParam(validationErr)}

		return problem
	}

	var notFoundErr *huberrors.NotFoundError
	if errors.As(err, &notFoundErr) {
		problem := newProblem(http.StatusNotFound, notFoundErr.Error())
		if notFoundErr.Resource != "" {
			problem.Details = map[string]any{"resource_type": notFoundErr.Resource}
		}

		return problem
	}

	var conflictErr *huberrors.ConflictError
	if errors.As(err, &conflictErr) {
		return newProblem(http.StatusConflict, conflictErr.Error())
	}

	var limitErr *huberrors.LimitExceededError
	if errors.As(err, &limitErr) {
		return newProblem(http.StatusForbidden, limitErr.Error())
	}

	if errors.Is(err, cursor.ErrInvalidCursor) {
		problem := newValidationProblem()
		problem.InvalidParams = []InvalidParam{{Name: "cursor", Reason: reasonInvalidCursor}}

		return problem
	}

	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return newProblem(http.StatusRequestEntityTooLarge,
			fmt.Sprintf("Request body exceeds the maximum allowed size of %d bytes", maxBytesErr.Limit))
	}

	if problem, ok := problemFromJSONDecodeError(err); ok {
		return problem
	}

	return newProblem(http.StatusInternalServerError, detailInternal)
}

// problemFromJSONDecodeError recognizes errors from decoding a JSON request body
// and turns them into actionable 400 problems. Reports ok=false for errors that
// are not JSON-decode failures so the caller can fall through to other mappings.
func problemFromJSONDecodeError(err error) (ProblemDetails, bool) {
	if param, ok := invalidFieldTypeParam(err); ok {
		problem := newValidationProblem()
		problem.InvalidParams = []InvalidParam{param}

		return problem, true
	}

	// json.SyntaxError covers malformed JSON; io.ErrUnexpectedEOF covers truncated
	// payloads (e.g. `{"x":`). Both are client mistakes, not server failures.
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) || errors.Is(err, io.ErrUnexpectedEOF) {
		return newProblem(http.StatusBadRequest, "Invalid JSON: "+err.Error()), true
	}

	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := fieldNameForAPI(typeErr.Field)
		problem := newValidationProblem()
		problem.InvalidParams = []InvalidParam{{Name: field, Reason: "must be " + typeErr.Type.String()}}

		return problem, true
	}

	// unknownJSONField is anchored on the json decoder's exact `unknown field "X"`
	// format, so an unrelated error that happens to contain those words won't be
	// misclassified as a validation problem.
	if field, ok := unknownJSONField(err); ok {
		problem := newValidationProblem()
		problem.InvalidParams = []InvalidParam{{Name: field, Reason: "is not a recognized request field"}}

		return problem, true
	}

	return ProblemDetails{}, false
}

// unknownJSONField extracts the field name from the standard library's
// "json: unknown field \"x\"" decode error.
func unknownJSONField(err error) (string, bool) {
	const marker = `unknown field "`

	_, after, found := strings.Cut(err.Error(), marker)
	if !found {
		return "", false
	}

	field, _, found := strings.Cut(after, `"`)

	return field, found && field != ""
}

// invalidParamsFromValidator converts go-playground validator errors into
// invalid_params entries with dotted field paths and self-correcting reasons.
func invalidParamsFromValidator(validationErrs validator.ValidationErrors) []InvalidParam {
	params := make([]InvalidParam, 0, len(validationErrs))
	for _, fieldErr := range validationErrs {
		params = append(params, InvalidParam{
			Name:   fieldPath(fieldErr),
			Reason: FormatFieldError(fieldErr),
		})
	}

	return params
}

func validationErrorParam(err *huberrors.ValidationError) InvalidParam {
	reason := err.Message
	if reason == "" {
		reason = "is invalid"
	}

	return InvalidParam{Name: err.Field, Reason: reason}
}

// fieldPath returns the dotted path to the offending field using API (JSON/form)
// names, e.g. "field_type" or "items[0].field_type", so a client can map the
// error straight back to the input it sent.
func fieldPath(fieldErr validator.FieldError) string {
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

func invalidFieldTypeParam(err error) (InvalidParam, bool) {
	var invalidFieldType *models.InvalidFieldTypeError
	if !errors.As(err, &invalidFieldType) {
		return InvalidParam{}, false
	}

	return InvalidParam{
		Name: "field_type",
		Reason: fmt.Sprintf(
			"has invalid value %q; must be one of: %s",
			invalidFieldType.Value,
			models.ValidFieldTypeValuesString(),
		),
	}, true
}

// fieldNameForAPI converts a struct field path (e.g. "TenantID" or "X.Y") to API-style snake_case.
func fieldNameForAPI(fieldPath string) string {
	if i := strings.LastIndex(fieldPath, "."); i >= 0 && i+1 < len(fieldPath) {
		fieldPath = fieldPath[i+1:]
	}

	return strcase.ToSnake(fieldPath)
}
