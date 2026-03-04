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
		Type:   "about:blank",
		Title:  title,
		Status: statusCode,
		Detail: detail,
	}

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(problem); err != nil {
		slog.Error("Failed to encode error response", "error", err)
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
