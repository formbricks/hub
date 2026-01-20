package handlers

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse represents an API error response
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// SuccessResponse represents a generic success response
type SuccessResponse struct {
	Data interface{} `json:"data,omitempty"`
}

// RespondJSON writes a JSON response
func RespondJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

// RespondError writes an error JSON response
func RespondError(w http.ResponseWriter, statusCode int, errorType string, message string) {
	RespondJSON(w, statusCode, ErrorResponse{
		Error:   errorType,
		Message: message,
	})
}

// RespondSuccess writes a success JSON response
func RespondSuccess(w http.ResponseWriter, statusCode int, data interface{}) {
	RespondJSON(w, statusCode, SuccessResponse{
		Data: data,
	})
}
