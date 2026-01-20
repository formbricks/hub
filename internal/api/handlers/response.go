package handlers

import (
	"encoding/json"
	"net/http"
)

// ErrorDetail represents a single error detail in RFC 7807 Problem Details
type ErrorDetail struct {
	Location string      `json:"location,omitempty"`
	Message  string      `json:"message,omitempty"`
	Value    interface{} `json:"value,omitempty"`
} //@name ErrorDetail

// ProblemDetails represents an RFC 7807 Problem Details error response
type ProblemDetails struct {
	Type     string        `json:"type,omitempty"`
	Title    string        `json:"title"`
	Status   int           `json:"status"`
	Detail   string        `json:"detail,omitempty"`
	Instance string        `json:"instance,omitempty"`
	Errors   []ErrorDetail `json:"errors,omitempty"`
} //@name ProblemDetails

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
	json.NewEncoder(w).Encode(problem)
}

// RespondBadRequest writes a 400 Bad Request error response
func RespondBadRequest(w http.ResponseWriter, detail string) {
	RespondError(w, http.StatusBadRequest, "Bad Request", detail)
}

// DataResponse wraps a single data object in a consistent response format
type DataResponse struct {
	Data interface{} `json:"data"`
}

// RespondSuccess wraps a single object in a {"data": ...} structure
// Use this for single-object responses (Create, Get, Update) to maintain consistency
func RespondSuccess(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(DataResponse{Data: data})
}
