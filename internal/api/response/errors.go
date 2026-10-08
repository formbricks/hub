package response

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"

	"github.com/formbricks/hub/internal/api/validation"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/pkg/cursor"
)

const (
	detailInternal   = "An unexpected error occurred"
	detailValidation = "One or more request parameters are invalid"
)

// InvalidCursorReason explains how clients should recover from a malformed pagination cursor.
const InvalidCursorReason = "omit it for the first page, or use the exact next_cursor value from the previous response"

// InvalidCursorSortReason explains how clients should recover from presenting a cursor under a
// different ordering than the one it was issued for. A cursor is a position within one specific
// ordering, so it cannot be carried across a change of sort or order.
const InvalidCursorSortReason = "was issued for a different sort/order; " +
	"restart pagination without a cursor, or keep sort and order unchanged"

// problemFromError translates a Go error into an RFC 9457 problem. Domain and
// sentinel errors map to specific statuses, codes, and invalid_params; anything
// unrecognized maps to a generic 500 whose cause is logged but not exposed.
func problemFromError(err error) ProblemDetails {
	if err == nil {
		return newProblem(http.StatusInternalServerError, detailInternal)
	}

	if param, ok := invalidFieldTypeParam(err); ok {
		problem := newValidationProblem()
		problem.InvalidParams = []InvalidParam{param}

		return problem
	}

	if requestJSONErr, ok := errors.AsType[*RequestJSONDecodeError](err); ok {
		if problem, ok := problemFromJSONDecodeError(requestJSONErr.Unwrap()); ok {
			return problem
		}

		return newProblem(http.StatusBadRequest, "Invalid request body")
	}

	if validationErrs, ok := errors.AsType[validator.ValidationErrors](err); ok {
		problem := newValidationProblem()
		problem.InvalidParams = invalidParamsFromValidator(validationErrs)

		return problem
	}

	if queryDecodeErr, ok := errors.AsType[*validation.QueryDecodeError](err); ok {
		problem := newValidationProblem()
		problem.InvalidParams = invalidParamsFromValidationParams(queryDecodeErr.InvalidParams())

		return problem
	}

	if validationErr, ok := errors.AsType[*huberrors.ValidationError](err); ok {
		problem := newValidationProblem()
		problem.InvalidParams = []InvalidParam{validationErrorParam(validationErr)}

		return problem
	}

	if notFoundErr, ok := errors.AsType[*huberrors.NotFoundError](err); ok {
		problem := newProblem(http.StatusNotFound, notFoundErr.Error())
		if notFoundErr.Resource != "" {
			problem.Details = map[string]any{"resource_type": notFoundErr.Resource}
		}

		return problem
	}

	if tenantWriteConflictErr, ok := errors.AsType[*huberrors.TenantWriteConflictError](err); ok {
		problem := newProblem(http.StatusConflict, tenantWriteConflictErr.Error())
		problem.Type = ProblemTypeTenantWriteConflict
		problem.Code = CodeTenantWriteConflict

		return problem
	}

	if conflictErr, ok := errors.AsType[*huberrors.ConflictError](err); ok {
		return newProblem(http.StatusConflict, conflictErr.Error())
	}

	if limitErr, ok := errors.AsType[*huberrors.LimitExceededError](err); ok {
		return newProblem(http.StatusForbidden, limitErr.Error())
	}

	// Checked before ErrInvalidCursor purely for clarity — ErrCursorSortMismatch is a standalone
	// sentinel that does not wrap it, so the order is not load-bearing. Keep it that way: making
	// one wrap the other would silently route mismatches to the malformed-cursor advice.
	if errors.Is(err, cursor.ErrCursorSortMismatch) {
		problem := newValidationProblem()
		problem.InvalidParams = []InvalidParam{{Name: "cursor", Reason: InvalidCursorSortReason}}

		return problem
	}

	if errors.Is(err, cursor.ErrInvalidCursor) {
		problem := newValidationProblem()
		problem.InvalidParams = []InvalidParam{{Name: "cursor", Reason: InvalidCursorReason}}

		return problem
	}

	return newProblem(http.StatusInternalServerError, detailInternal)
}

// RequestJSONDecodeError marks an error as coming from decoding an HTTP request
// JSON body. This keeps JSON-specific 400 mappings scoped to request parsing,
// so downstream service or repository errors cannot be misclassified as client
// body errors merely because they wrap io.ErrUnexpectedEOF or contain decoder-like text.
type RequestJSONDecodeError struct {
	err error
}

// NewRequestJSONDecodeError wraps a request-body JSON decode failure.
func NewRequestJSONDecodeError(err error) error {
	if err == nil {
		return nil
	}

	return &RequestJSONDecodeError{err: err}
}

func (e *RequestJSONDecodeError) Error() string {
	return e.err.Error()
}

func (e *RequestJSONDecodeError) Unwrap() error {
	return e.err
}

// Reasons reported in invalid_params for a request body member.
const (
	ReasonJSONMemberUnknown  = "is not a recognized request field"
	ReasonJSONMemberRepeated = "appears more than once; each member may appear only once"
)

// maxReportedJSONNameRunes bounds how much of a caller-chosen member path is echoed back, and
// maxReportedJSONReasonRunes how much of a decoder's reason, which can quote the caller's value.
const (
	maxReportedJSONNameRunes   = 64
	maxReportedJSONReasonRunes = 256
)

// problemFromJSONDecodeError recognizes errors from decoding a JSON request body with
// encoding/json/v2 (see handlers.decodeJSONBody) and turns them into actionable problems. Reports
// ok=false for errors that are not JSON-decode failures so the caller can fall through to other
// mappings.
//
// Member names are caller-controlled, so they are reported only in invalid_params, which is not
// logged — never in the problem detail, which is (see logProblem).
func problemFromJSONDecodeError(err error) (ProblemDetails, bool) {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return newProblem(http.StatusRequestEntityTooLarge, "request body too large"), true
	}

	if param, ok := invalidFieldTypeParam(err); ok {
		problem := newValidationProblem()
		problem.InvalidParams = []InvalidParam{param}

		return problem, true
	}

	// An empty or whitespace-only body.
	if errors.Is(err, io.EOF) {
		return newProblem(http.StatusBadRequest, "Invalid request body"), true
	}

	if syntaxErr, ok := errors.AsType[*jsontext.SyntacticError](err); ok {
		return problemFromJSONSyntaxError(syntaxErr), true
	}

	if semanticErr, ok := errors.AsType[*json.SemanticError](err); ok {
		return problemFromJSONSemanticError(semanticErr), true
	}

	return ProblemDetails{}, false
}

// problemFromJSONSyntaxError maps a body that is not well-formed: malformed or truncated JSON,
// invalid UTF-8, data after the top-level value, or a member name repeated within an object.
func problemFromJSONSyntaxError(err *jsontext.SyntacticError) ProblemDetails {
	if errors.Is(err.Err, jsontext.ErrDuplicateName) {
		problem := newValidationProblem()
		problem.InvalidParams = []InvalidParam{{
			Name:   jsonPointerName(err.JSONPointer),
			Reason: ReasonJSONMemberRepeated,
		}}

		return problem
	}

	if errors.Is(err.Err, io.ErrUnexpectedEOF) {
		return newProblem(http.StatusBadRequest, "Invalid JSON: unexpected end of input")
	}

	// err.Error() would name the member path, which is caller-controlled and would be logged with the
	// detail; the underlying cause and the offset carry no names.
	cause := "malformed"
	if err.Err != nil {
		cause = err.Err.Error()
	}

	return newProblem(http.StatusBadRequest, fmt.Sprintf("Invalid JSON: %s at byte offset %d", cause, err.ByteOffset))
}

// problemFromJSONSemanticError maps well-formed JSON that does not fit the request type: an unknown
// member, a value of the wrong kind, a body that is not an object, or a custom decoder's refusal.
func problemFromJSONSemanticError(err *json.SemanticError) ProblemDetails {
	var reason string

	switch {
	case errors.Is(err.Err, json.ErrUnknownName):
		reason = ReasonJSONMemberUnknown
	case errors.Is(err.Err, strconv.ErrSyntax) && err.GoType != nil:
		// A number of the wrong form for its field (1.5 or 1e2 into an int): name the expected type.
		reason = "must be " + goTypeForAPI(err.GoType)
	case err.Err != nil:
		reason = truncateRunes(err.Err.Error(), maxReportedJSONReasonRunes)
	case err.JSONPointer == "":
		return newProblem(http.StatusBadRequest, "Invalid request body: must be a JSON object")
	case err.GoType != nil:
		reason = "must be " + goTypeForAPI(err.GoType)
	default:
		reason = "is invalid"
	}

	problem := newValidationProblem()
	problem.InvalidParams = []InvalidParam{{Name: jsonPointerName(err.JSONPointer), Reason: reason}}

	return problem
}

// jsonPointerName renders a JSON Pointer the way invalid_params names fields elsewhere — the
// validator's field paths and the OpenAPI examples: "/diagnostics/model" → "diagnostics.model",
// "/memberships/0/feedback_record_id" → "memberships[0].feedback_record_id". A pointer cannot tell
// an array index from an all-digit object key, so such a key inside a free-form object (`metadata`)
// renders as an index too. Truncated so a caller-chosen name cannot make the response arbitrarily
// large.
func jsonPointerName(pointer jsontext.Pointer) string {
	var name strings.Builder

	for token := range pointer.Tokens() {
		switch {
		case name.Len() > 0 && isJSONArrayIndex(token):
			name.WriteString("[" + token + "]")
		case name.Len() > 0:
			name.WriteString("." + token)
		default:
			name.WriteString(token)
		}
	}

	return truncateRunes(name.String(), maxReportedJSONNameRunes)
}

// isJSONArrayIndex reports whether a pointer token is an array index (all ASCII digits).
func isJSONArrayIndex(token string) bool {
	if token == "" {
		return false
	}

	for i := range len(token) {
		if token[i] < '0' || token[i] > '9' {
			return false
		}
	}

	return true
}

// truncateRunes shortens s to at most limit runes, marking the cut.
func truncateRunes(s string, limit int) string {
	if runes := []rune(s); len(runes) > limit {
		return string(runes[:limit]) + "…"
	}

	return s
}

// goTypeForAPI names the Go type a value had to decode into, looking through pointers the way the
// API documents optional fields.
func goTypeForAPI(goType reflect.Type) string {
	for goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}

	return goType.String()
}

// invalidParamsFromValidator converts go-playground validator errors into
// invalid_params entries with dotted field paths and self-correcting reasons.
func invalidParamsFromValidator(validationErrs validator.ValidationErrors) []InvalidParam {
	params := make([]InvalidParam, 0, len(validationErrs))
	for _, fieldErr := range validationErrs {
		params = append(params, InvalidParam{
			Name:   validation.FieldPath(fieldErr),
			Reason: validation.FormatFieldError(fieldErr),
		})
	}

	return params
}

func invalidParamsFromValidationParams(validationParams []validation.InvalidParam) []InvalidParam {
	params := make([]InvalidParam, 0, len(validationParams))
	for _, param := range validationParams {
		params = append(params, InvalidParam{Name: param.Name, Reason: param.Reason})
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
