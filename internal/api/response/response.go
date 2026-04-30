// Package response provides HTTP response helpers and RFC 7807 problem details.
package response

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/iancoleman/strcase"

	"github.com/formbricks/hub/internal/models"
)

const (
	// ProblemTypeBadRequest identifies malformed request problems.
	ProblemTypeBadRequest = "https://hub.formbricks.com/problems/bad-request"
	// ProblemTypeValidationError identifies request validation problems.
	ProblemTypeValidationError = "https://hub.formbricks.com/problems/validation-error"
	// ProblemTypeClientError identifies unclassified client-side request problems.
	ProblemTypeClientError = "https://hub.formbricks.com/problems/client-error"
	// ProblemTypeUnauthorized identifies authentication problems.
	ProblemTypeUnauthorized = "https://hub.formbricks.com/problems/unauthorized"
	// ProblemTypeNotFound identifies missing resource problems.
	ProblemTypeNotFound = "https://hub.formbricks.com/problems/not-found"
	// ProblemTypeConflict identifies resource conflict problems.
	ProblemTypeConflict = "https://hub.formbricks.com/problems/conflict"
	// ProblemTypeForbidden identifies authorization problems.
	ProblemTypeForbidden = "https://hub.formbricks.com/problems/forbidden"
	// ProblemTypeMethodNotAllowed identifies unsupported HTTP method problems.
	ProblemTypeMethodNotAllowed = "https://hub.formbricks.com/problems/method-not-allowed"
	// ProblemTypeServiceUnavailable identifies temporary dependency problems.
	ProblemTypeServiceUnavailable = "https://hub.formbricks.com/problems/service-unavailable"
	// ProblemTypeInternalServerError identifies unexpected server problems.
	ProblemTypeInternalServerError = "https://hub.formbricks.com/problems/internal-server-error"
)

// ErrorDetail represents a single error detail in RFC 7807 Problem Details.
type ErrorDetail struct {
	Location string `json:"location,omitempty"`
	Message  string `json:"message,omitempty"`
	Value    any    `json:"value,omitempty"`
}

// ProblemDetails represents an RFC 7807 Problem Details error response.
type ProblemDetails struct {
	Type     string        `json:"type,omitempty"`
	Title    string        `json:"title"`
	Status   int           `json:"status"`
	Detail   string        `json:"detail,omitempty"`
	Instance string        `json:"instance,omitempty"`
	Errors   []ErrorDetail `json:"errors,omitempty"`
}

// RespondError writes an RFC 7807 Problem Details error response.
func RespondError(w http.ResponseWriter, statusCode int, title, detail string) {
	problem := ProblemDetails{
		Type:   problemTypeForStatus(statusCode),
		Title:  title,
		Status: statusCode,
		Detail: detail,
	}

	respondProblem(w, statusCode, problem)
}

// RespondInvalidRequestBody writes a 400 response for JSON request body decoding failures.
func RespondInvalidRequestBody(w http.ResponseWriter, err error) {
	problemType := jsonDecodeProblemType(err)

	problem := ProblemDetails{
		Type:   problemType,
		Title:  jsonDecodeProblemTitle(problemType),
		Status: http.StatusBadRequest,
		Detail: JSONDecodeErrorDetail(err),
		Errors: JSONDecodeErrorDetails(err),
	}

	respondProblem(w, http.StatusBadRequest, problem)
}

func jsonDecodeProblemType(err error) string {
	if _, ok := invalidFieldTypeErrorDetail(err); ok {
		return ProblemTypeValidationError
	}

	return ProblemTypeBadRequest
}

func jsonDecodeProblemTitle(problemType string) string {
	if problemType == ProblemTypeValidationError {
		return "Validation Error"
	}

	return "Bad Request"
}

func respondProblem(w http.ResponseWriter, statusCode int, problem ProblemDetails) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(problem); err != nil {
		slog.Error("Failed to encode error response", "error", err)
	}
}

func problemTypeForStatus(statusCode int) string {
	switch statusCode {
	case http.StatusBadRequest:
		return ProblemTypeBadRequest
	case http.StatusUnauthorized:
		return ProblemTypeUnauthorized
	case http.StatusForbidden:
		return ProblemTypeForbidden
	case http.StatusMethodNotAllowed:
		return ProblemTypeMethodNotAllowed
	case http.StatusNotFound:
		return ProblemTypeNotFound
	case http.StatusConflict:
		return ProblemTypeConflict
	case http.StatusServiceUnavailable:
		return ProblemTypeServiceUnavailable
	case http.StatusInternalServerError:
		return ProblemTypeInternalServerError
	default:
		if statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError {
			return ProblemTypeClientError
		}

		return ProblemTypeInternalServerError
	}
}

// RespondBadRequest writes a 400 Bad Request error response.
func RespondBadRequest(w http.ResponseWriter, detail string) {
	RespondError(w, http.StatusBadRequest, "Bad Request", detail)
}

// JSONDecodeErrorDetail returns a user-friendly message for json.Decode errors.
// Use this when decoding request bodies to give clients actionable feedback.
// Note: Missing fields do not cause Decode to fail; validate required fields after decode.
func JSONDecodeErrorDetail(err error) string {
	if err == nil {
		return "Invalid request body"
	}

	if detail, ok := invalidFieldTypeErrorDetail(err); ok {
		return detail.Message
	}

	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return "Invalid JSON: " + err.Error()
	}

	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := fieldNameForAPI(typeErr.Field)

		return fmt.Sprintf("field %q must be %s", field, typeErr.Type.String())
	}

	if strings.Contains(err.Error(), "unknown field") {
		return err.Error()
	}

	return "Invalid request body"
}

// JSONDecodeErrorDetails returns field-level details for JSON request body decoding failures.
func JSONDecodeErrorDetails(err error) []ErrorDetail {
	if detail, ok := invalidFieldTypeErrorDetail(err); ok {
		return []ErrorDetail{detail}
	}

	return nil
}

func invalidFieldTypeErrorDetail(err error) (ErrorDetail, bool) {
	var invalidFieldType *models.InvalidFieldTypeError
	if !errors.As(err, &invalidFieldType) {
		return ErrorDetail{}, false
	}

	return ErrorDetail{
		Location: "field_type",
		Message: fmt.Sprintf(
			"field_type has invalid value %q; must be one of: %s",
			invalidFieldType.Value,
			models.ValidFieldTypeValuesString(),
		),
		Value: invalidFieldType.Value,
	}, true
}

// fieldNameForAPI converts a struct field path (e.g. "TenantID" or "X.Y") to API-style snake_case.
func fieldNameForAPI(fieldPath string) string {
	if i := strings.LastIndex(fieldPath, "."); i >= 0 && i+1 < len(fieldPath) {
		fieldPath = fieldPath[i+1:]
	}

	return strcase.ToSnake(fieldPath)
}

// RespondUnauthorized writes a 401 Unauthorized error response.
func RespondUnauthorized(w http.ResponseWriter, detail string) {
	RespondError(w, http.StatusUnauthorized, "Unauthorized", detail)
}

// RespondNotFound writes a 404 Not Found error response.
func RespondNotFound(w http.ResponseWriter, detail string) {
	RespondError(w, http.StatusNotFound, "Not Found", detail)
}

// RespondConflict writes a 409 Conflict error response.
func RespondConflict(w http.ResponseWriter, detail string) {
	RespondError(w, http.StatusConflict, "Conflict", detail)
}

// RespondInternalServerError writes a 500 Internal Server Error response.
func RespondInternalServerError(w http.ResponseWriter, detail string) {
	RespondError(w, http.StatusInternalServerError, "Internal Server Error", detail)
}

// RespondServiceUnavailable writes a 503 Service Unavailable error response.
func RespondServiceUnavailable(w http.ResponseWriter, detail string) {
	RespondError(w, http.StatusServiceUnavailable, "Service Unavailable", detail)
}

// RespondJSON writes a JSON response directly without wrapping.
func RespondJSON(w http.ResponseWriter, statusCode int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("Failed to encode JSON response", "error", err)
	}
}
