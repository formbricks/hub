package response

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// ErrorDetail represents a single error detail in RFC 7807 Problem Details
type ErrorDetail struct {
	Location string      `json:"location,omitempty"`
	Message  string      `json:"message,omitempty"`
	Value    interface{} `json:"value,omitempty"`
}

// ProblemDetails represents an RFC 7807 Problem Details error response
type ProblemDetails struct {
	Type     string        `json:"type,omitempty"`
	Title    string        `json:"title"`
	Status   int           `json:"status"`
	Detail   string        `json:"detail,omitempty"`
	Instance string        `json:"instance,omitempty"`
	Errors   []ErrorDetail `json:"errors,omitempty"`
}

// RespondError writes an RFC 7807 Problem Details error response
func RespondError(w http.ResponseWriter, statusCode int, title string, detail string) {
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

// RespondBadRequest writes a 400 Bad Request error response
func RespondBadRequest(w http.ResponseWriter, detail string) {
	RespondError(w, http.StatusBadRequest, "Bad Request", detail)
}

// RespondUnauthorized writes a 401 Unauthorized error response
func RespondUnauthorized(w http.ResponseWriter, detail string) {
	RespondError(w, http.StatusUnauthorized, "Unauthorized", detail)
}

// RespondNotFound writes a 404 Not Found error response
func RespondNotFound(w http.ResponseWriter, detail string) {
	RespondError(w, http.StatusNotFound, "Not Found", detail)
}

// RespondInternalServerError writes a 500 Internal Server Error response
func RespondInternalServerError(w http.ResponseWriter, detail string) {
	RespondError(w, http.StatusInternalServerError, "Internal Server Error", detail)
}

// RespondUnprocessableEntity writes a 422 Unprocessable Entity error response
func RespondUnprocessableEntity(w http.ResponseWriter, detail string) {
	RespondError(w, http.StatusUnprocessableEntity, "Validation Error", detail)
}

// RespondJSON writes a JSON response directly without wrapping
func RespondJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("Failed to encode JSON response", "error", err)
	}
}
