package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/api/validation"
	apperrors "github.com/formbricks/hub/internal/errors"
	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
)

// FeedbackRecordsService defines the interface for feedback records business logic.
type FeedbackRecordsService interface {
	CreateFeedbackRecord(ctx context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	GetFeedbackRecord(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	ListFeedbackRecords(ctx context.Context, filters *models.ListFeedbackRecordsFilters) (*models.ListFeedbackRecordsResponse, error)
	UpdateFeedbackRecord(ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	DeleteFeedbackRecord(ctx context.Context, id uuid.UUID) error
	BulkDeleteFeedbackRecords(ctx context.Context, userIdentifier string, tenantID *string) (int64, error)
}

// FeedbackRecordsHandler handles HTTP requests for feedback records
type FeedbackRecordsHandler struct {
	service FeedbackRecordsService
}

// NewFeedbackRecordsHandler creates a new feedback records handler
func NewFeedbackRecordsHandler(service FeedbackRecordsService) *FeedbackRecordsHandler {
	return &FeedbackRecordsHandler{service: service}
}

// handleServiceError handles errors from the service layer and returns appropriate HTTP responses
func handleServiceError(w http.ResponseWriter, err error, operation string, context map[string]interface{}) {
	if errors.Is(err, apperrors.ErrNotFound) {
		response.RespondNotFound(w, "Feedback record not found")
		return
	}

	// Check for database errors that should return 400 instead of 500
	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "violates") ||
		strings.Contains(errStr, "constraint") ||
		strings.Contains(errStr, "value too long") ||
		strings.Contains(errStr, "invalid byte sequence") ||
		strings.Contains(errStr, "encoding") ||
		strings.Contains(errStr, "invalid input") ||
		strings.Contains(errStr, "numeric value out of range") ||
		strings.Contains(errStr, "out of range") ||
		strings.Contains(errStr, "invalid") {
		// Log the actual error for server-side debugging (contains sensitive DB details)
		logFields := []interface{}{"error", err, "operation", operation}
		for k, v := range context {
			logFields = append(logFields, k, v)
		}
		slog.Warn("Database error", logFields...)
		// Return sanitized error message to client (no DB internals exposed)
		sanitizedMsg := sanitizeDatabaseError(err)
		response.RespondBadRequest(w, sanitizedMsg)
		return
	}

	// Log unexpected errors for debugging
	logFields := []interface{}{"error", err, "operation", operation}
	for k, v := range context {
		logFields = append(logFields, k, v)
	}
	slog.Error("Unexpected error", logFields...)
	response.RespondInternalServerError(w, "An unexpected error occurred")
}

// sanitizeDatabaseError extracts a user-friendly error message from database errors
// without exposing internal database details (table names, constraint names, etc.)
func sanitizeDatabaseError(err error) string {
	if err == nil {
		return ""
	}
	errStr := strings.ToLower(err.Error())

	// Check for specific error types and return generic messages
	if strings.Contains(errStr, "value too long") || strings.Contains(errStr, "character varying") {
		return "Field value exceeds maximum allowed length"
	}
	if strings.Contains(errStr, "invalid byte sequence") || strings.Contains(errStr, "encoding") {
		return "Invalid character encoding in field value"
	}
	if strings.Contains(errStr, "violates") || strings.Contains(errStr, "constraint") {
		return "Invalid data provided"
	}
	if strings.Contains(errStr, "duplicate key") || strings.Contains(errStr, "unique constraint") {
		return "A record with this data already exists"
	}
	if strings.Contains(errStr, "foreign key") || strings.Contains(errStr, "references") {
		return "Invalid reference to related data"
	}
	if strings.Contains(errStr, "not null") || strings.Contains(errStr, "null value") {
		return "Required field is missing"
	}
	if strings.Contains(errStr, "numeric value out of range") || strings.Contains(errStr, "out of range") {
		return "Numeric value is out of valid range"
	}
	if strings.Contains(errStr, "invalid input") || strings.Contains(errStr, "invalid") {
		return "Invalid data provided"
	}

	// For any other database error, return a generic message
	return "Invalid data provided"
}

// Create handles POST /v1/feedback-records
func (h *FeedbackRecordsHandler) Create(w http.ResponseWriter, r *http.Request) {
	// Read body to check if it's null or an array
	bodyBytes, err := io.ReadAll(r.Body)
	// Close body immediately after reading (it's fully consumed by ReadAll)
	// This ensures cleanup in all code paths, even if ReadAll fails
	r.Body.Close()
	if err != nil {
		response.RespondBadRequest(w, "Invalid request body")
		return
	}

	bodyStr := strings.TrimSpace(string(bodyBytes))
	if bodyStr == "" || bodyStr == "null" {
		response.RespondBadRequest(w, "Request body is required")
		return
	}

	// Check if it's an array (starts with '[')
	if strings.HasPrefix(bodyStr, "[") {
		response.RespondBadRequest(w, "Request body must be a JSON object, not an array")
		return
	}

	// Decode into the struct with strict mode (reject unknown fields)
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.DisallowUnknownFields()
	var req models.CreateFeedbackRecordRequest
	if err := decoder.Decode(&req); err != nil {
		response.RespondBadRequest(w, "Invalid request body: "+err.Error())
		return
	}

	// Validate request (all validations are handled by struct tags and custom validators)
	if err := validation.ValidateStruct(&req); err != nil {
		validation.RespondValidationError(w, err)
		return
	}

	record, err := h.service.CreateFeedbackRecord(r.Context(), &req)
	if err != nil {
		handleServiceError(w, err, "create_feedback_record", map[string]interface{}{
			"field_id":    req.FieldID,
			"source_type": req.SourceType,
		})
		return
	}

	response.RespondJSON(w, http.StatusCreated, record)
}

// Get handles GET /v1/feedback-records/{id}
func (h *FeedbackRecordsHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		response.RespondBadRequest(w, "Feedback Record ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		response.RespondBadRequest(w, "Invalid UUID format")
		return
	}

	record, err := h.service.GetFeedbackRecord(r.Context(), id)
	if err != nil {
		handleServiceError(w, err, "get_feedback_record", map[string]interface{}{
			"record_id": id.String(),
		})
		return
	}

	response.RespondJSON(w, http.StatusOK, record)
}

// List handles GET /v1/feedback-records
func (h *FeedbackRecordsHandler) List(w http.ResponseWriter, r *http.Request) {
	// Check for unknown query parameters
	allowedParams := map[string]bool{
		"tenant_id":       true,
		"response_id":     true,
		"source_type":     true,
		"source_id":       true,
		"field_id":        true,
		"field_type":      true,
		"user_identifier": true,
		"since":           true,
		"until":           true,
		"limit":           true,
		"offset":          true,
	}
	for key := range r.URL.Query() {
		if !allowedParams[key] {
			response.RespondBadRequest(w, "Unknown query parameter: "+key)
			return
		}
	}

	// Validate integer query parameters before decoding
	// This ensures invalid types are rejected early
	// Check if parameter exists (even if empty) and validate it
	if _, exists := r.URL.Query()["offset"]; exists {
		offsetStr := r.URL.Query().Get("offset")
		if offsetStr == "" {
			response.RespondBadRequest(w, "offset must be a valid integer")
			return
		}
		if _, err := strconv.Atoi(offsetStr); err != nil {
			response.RespondBadRequest(w, "offset must be a valid integer")
			return
		}
	}
	if _, exists := r.URL.Query()["limit"]; exists {
		limitStr := r.URL.Query().Get("limit")
		if limitStr == "" {
			response.RespondBadRequest(w, "limit must be a valid integer")
			return
		}
		if _, err := strconv.Atoi(limitStr); err != nil {
			response.RespondBadRequest(w, "limit must be a valid integer")
			return
		}
	}

	// Validate date query parameters - reject empty strings (must be valid date-time format)
	if _, exists := r.URL.Query()["since"]; exists {
		sinceStr := r.URL.Query().Get("since")
		if sinceStr == "" {
			response.RespondBadRequest(w, "since must be a valid date-time (RFC3339 format)")
			return
		}
	}
	if _, exists := r.URL.Query()["until"]; exists {
		untilStr := r.URL.Query().Get("until")
		if untilStr == "" {
			response.RespondBadRequest(w, "until must be a valid date-time (RFC3339 format)")
			return
		}
	}

	filters := &models.ListFeedbackRecordsFilters{}

	// Decode and validate query parameters (all validations handled by struct tags)
	if err := validation.ValidateAndDecodeQueryParams(r, filters); err != nil {
		validation.RespondValidationError(w, err)
		return
	}

	// Explicitly validate limit >= 1 if provided
	// The validator's omitempty might skip validation for 0, so we check explicitly
	if _, exists := r.URL.Query()["limit"]; exists {
		if filters.Limit < 1 {
			response.RespondBadRequest(w, "limit must be at least 1")
			return
		}
	}

	result, err := h.service.ListFeedbackRecords(r.Context(), filters)
	if err != nil {
		handleServiceError(w, err, "list_feedback_records", nil)
		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

// Update handles PATCH /v1/feedback-records/{id}
func (h *FeedbackRecordsHandler) Update(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		response.RespondBadRequest(w, "Feedback Record ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		response.RespondBadRequest(w, "Invalid UUID format")
		return
	}

	// Read body to check if it's null or an array
	bodyBytes, err := io.ReadAll(r.Body)
	// Close body immediately after reading (it's fully consumed by ReadAll)
	// This ensures cleanup in all code paths, even if ReadAll fails
	r.Body.Close()
	if err != nil {
		response.RespondBadRequest(w, "Invalid request body")
		return
	}

	bodyStr := strings.TrimSpace(string(bodyBytes))
	if bodyStr == "" || bodyStr == "null" {
		response.RespondBadRequest(w, "Request body is required")
		return
	}

	// Check if it's an array (starts with '[')
	if strings.HasPrefix(bodyStr, "[") {
		response.RespondBadRequest(w, "Request body must be a JSON object, not an array")
		return
	}

	// Decode into the struct with strict mode (reject unknown fields)
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.DisallowUnknownFields()
	var req models.UpdateFeedbackRecordRequest
	if err := decoder.Decode(&req); err != nil {
		response.RespondBadRequest(w, "Invalid request body: "+err.Error())
		return
	}

	// Validate request (all validations are handled by struct tags and custom validators)
	if err := validation.ValidateStruct(&req); err != nil {
		validation.RespondValidationError(w, err)
		return
	}

	record, err := h.service.UpdateFeedbackRecord(r.Context(), id, &req)
	if err != nil {
		handleServiceError(w, err, "update_feedback_record", map[string]interface{}{
			"record_id": id.String(),
		})
		return
	}

	response.RespondJSON(w, http.StatusOK, record)
}

// Delete handles DELETE /v1/feedback-records/{id}
func (h *FeedbackRecordsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		response.RespondBadRequest(w, "Feedback Record ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		response.RespondBadRequest(w, "Invalid UUID format")
		return
	}

	if err := h.service.DeleteFeedbackRecord(r.Context(), id); err != nil {
		handleServiceError(w, err, "delete_feedback_record", map[string]interface{}{
			"record_id": id.String(),
		})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// BulkDelete handles DELETE /v1/feedback-records?user_identifier=<id>
func (h *FeedbackRecordsHandler) BulkDelete(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	userIdentifier := query.Get("user_identifier")
	if userIdentifier == "" {
		response.RespondBadRequest(w, "user_identifier is required")
		return
	}

	// Validate user_identifier for NULL bytes (validation library handles this, but we check early for query params)
	if strings.Contains(userIdentifier, "\x00") {
		response.RespondBadRequest(w, "user_identifier contains invalid character encoding (NULL bytes are not allowed)")
		return
	}

	var tenantID *string
	if tenantIDStr := query.Get("tenant_id"); tenantIDStr != "" {
		if strings.Contains(tenantIDStr, "\x00") {
			response.RespondBadRequest(w, "tenant_id contains invalid character encoding (NULL bytes are not allowed)")
			return
		}
		tenantID = &tenantIDStr
	}

	deletedCount, err := h.service.BulkDeleteFeedbackRecords(r.Context(), userIdentifier, tenantID)
	if err != nil {
		handleServiceError(w, err, "bulk_delete_feedback_records", map[string]interface{}{
			"user_identifier": userIdentifier,
		})
		return
	}

	resp := models.BulkDeleteResponse{
		DeletedCount: deletedCount,
		Message:      "Successfully deleted feedback records",
	}

	response.RespondJSON(w, http.StatusOK, resp)
}
