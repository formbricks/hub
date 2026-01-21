package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

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
	SearchFeedbackRecords(ctx context.Context, req *models.SearchFeedbackRecordsRequest) (*models.SearchFeedbackRecordsResponse, error)
}

// FeedbackRecordsHandler handles HTTP requests for feedback records
type FeedbackRecordsHandler struct {
	service FeedbackRecordsService
}

// NewFeedbackRecordsHandler creates a new feedback records handler
func NewFeedbackRecordsHandler(service FeedbackRecordsService) *FeedbackRecordsHandler {
	return &FeedbackRecordsHandler{service: service}
}

// Create handles POST /v1/feedback-records
// @Summary Create feedback record
// @Description Create a new feedback record data point
// @Tags Feedback Records
// @Accept json
// @Produce json
// @Param request body CreateFeedbackRecordRequest true "Feedback record data to create"
// @Success 201 {object} FeedbackRecord
// @Failure 400 {object} ProblemDetails
// @Failure 401 {object} ProblemDetails "Unauthorized - Invalid or missing API key"
// @Security BearerAuth
// @Router /v1/feedback-records [post]
func (h *FeedbackRecordsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req models.CreateFeedbackRecordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.RespondBadRequest(w, "Invalid request body")
		return
	}

	// Validate request
	if err := validation.ValidateStruct(&req); err != nil {
		validation.RespondValidationError(w, err)
		return
	}

	record, err := h.service.CreateFeedbackRecord(r.Context(), &req)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			response.RespondNotFound(w, "Feedback record not found")
			return
		}
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusCreated, record)
}

// Get handles GET /v1/feedback-records/{id}
// @Summary Get a feedback record by ID
// @Description Retrieves a single feedback record data point by its UUID
// @Tags Feedback Records
// @Produce json
// @Param id path string true "Feedback Record ID (UUID)"
// @Success 200 {object} FeedbackRecord
// @Failure 400 {object} ProblemDetails "Invalid UUID format"
// @Failure 401 {object} ProblemDetails "Unauthorized - Invalid or missing API key"
// @Failure 404 {object} ProblemDetails "Feedback record not found"
// @Security BearerAuth
// @Router /v1/feedback-records/{id} [get]
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
		if errors.Is(err, apperrors.ErrNotFound) {
			response.RespondNotFound(w, "Feedback record not found")
			return
		}
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusOK, record)
}

// List handles GET /v1/feedback-records
// @Summary List feedback records with filters
// @Description Lists feedback records with optional filters and pagination
// @Tags Feedback Records
// @Produce json
// @Param tenant_id query string false "Filter by tenant ID"
// @Param response_id query string false "Filter by response ID"
// @Param source_type query string false "Filter by source type"
// @Param source_id query string false "Filter by source ID"
// @Param field_type query string false "Filter by field type"
// @Param user_identifier query string false "Filter by user identifier"
// @Param since query string false "Filter by collected_at >= since (ISO 8601 format)"
// @Param until query string false "Filter by collected_at <= until (ISO 8601 format)"
// @Param limit query int false "Number of results to return (max 1000)"
// @Param offset query int false "Number of results to skip"
// @Success 200 {object} ListFeedbackRecordsResponse
// @Failure 401 {object} ProblemDetails "Unauthorized - Invalid or missing API key"
// @Failure 500 {object} ProblemDetails "Internal server error"
// @Security BearerAuth
// @Router /v1/feedback-records [get]
func (h *FeedbackRecordsHandler) List(w http.ResponseWriter, r *http.Request) {
	filters := &models.ListFeedbackRecordsFilters{}

	// Decode and validate query parameters
	if err := validation.ValidateAndDecodeQueryParams(r, filters); err != nil {
		validation.RespondValidationError(w, err)
		return
	}

	result, err := h.service.ListFeedbackRecords(r.Context(), filters)
	if err != nil {
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

// Update handles PATCH /v1/feedback-records/{id}
// @Summary Update a feedback record
// @Description Updates specific fields of a feedback record data point
// @Tags Feedback Records
// @Accept json
// @Produce json
// @Param id path string true "Feedback Record ID (UUID)"
// @Param request body UpdateFeedbackRecordRequest true "Fields to update"
// @Success 200 {object} FeedbackRecord
// @Failure 400 {object} ProblemDetails "Invalid request or UUID format"
// @Failure 401 {object} ProblemDetails "Unauthorized - Invalid or missing API key"
// @Failure 404 {object} ProblemDetails "Feedback record not found"
// @Security BearerAuth
// @Router /v1/feedback-records/{id} [patch]
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

	var req models.UpdateFeedbackRecordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.RespondBadRequest(w, "Invalid request body")
		return
	}

	// Validate request (all fields are optional for update, but validate if provided)
	if err := validation.ValidateStruct(&req); err != nil {
		validation.RespondValidationError(w, err)
		return
	}

	record, err := h.service.UpdateFeedbackRecord(r.Context(), id, &req)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			response.RespondNotFound(w, "Feedback record not found")
			return
		}
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusOK, record)
}

// Delete handles DELETE /v1/feedback-records/{id}
// @Summary Delete a feedback record
// @Description Permanently deletes a feedback record data point
// @Tags Feedback Records
// @Param id path string true "Feedback Record ID (UUID)"
// @Success 204 "No Content"
// @Failure 400 {object} ProblemDetails "Invalid UUID format"
// @Failure 401 {object} ProblemDetails "Unauthorized - Invalid or missing API key"
// @Failure 404 {object} ProblemDetails "Feedback record not found"
// @Security BearerAuth
// @Router /v1/feedback-records/{id} [delete]
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
		if errors.Is(err, apperrors.ErrNotFound) {
			response.RespondNotFound(w, "Feedback record not found")
			return
		}
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// BulkDelete handles DELETE /v1/feedback-records?user_identifier=<id>
// @Summary Bulk delete feedback records by user identifier
// @Description Permanently deletes all feedback record data points matching the specified user_identifier. This endpoint supports GDPR Article 17 (Right to Erasure) requests.
// @Tags Feedback Records
// @Produce json
// @Param user_identifier query string true "Delete all records matching this user identifier"
// @Param tenant_id query string false "Filter by tenant ID (optional, for multi-tenant deployments)"
// @Success 200 {object} BulkDeleteResponse
// @Failure 400 {object} ProblemDetails "user_identifier is required"
// @Failure 401 {object} ProblemDetails "Unauthorized - Invalid or missing API key"
// @Failure 500 {object} ProblemDetails "Internal server error"
// @Security BearerAuth
// @Router /v1/feedback-records [delete]
func (h *FeedbackRecordsHandler) BulkDelete(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	userIdentifier := query.Get("user_identifier")
	if userIdentifier == "" {
		response.RespondBadRequest(w, "user_identifier is required")
		return
	}

	var tenantID *string
	if tenantIDStr := query.Get("tenant_id"); tenantIDStr != "" {
		tenantID = &tenantIDStr
	}

	deletedCount, err := h.service.BulkDeleteFeedbackRecords(r.Context(), userIdentifier, tenantID)
	if err != nil {
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	resp := models.BulkDeleteResponse{
		DeletedCount: deletedCount,
		Message:      "Successfully deleted feedback records",
	}

	response.RespondJSON(w, http.StatusOK, resp)
}

// Search handles GET /v1/feedback-records/search
// @Summary Search feedback records using semantic search
// @Description Performs vector similarity search on feedback record data using OpenAI embeddings. Only returns text records that have been embedded.
// @Tags Feedback Records
// @Produce json
// @Param query query string true "Natural language search query"
// @Param limit query int false "Maximum number of results to return (default 10, max 100)"
// @Param source_type query string false "Filter by source type"
// @Param since query string false "Filter by collection date (ISO 8601)"
// @Param until query string false "Filter by collection date (ISO 8601)"
// @Success 200 {object} SearchFeedbackRecordsResponse
// @Failure 400 {object} ProblemDetails "Invalid request parameters or missing query"
// @Failure 401 {object} ProblemDetails "Unauthorized - Invalid or missing API key"
// @Failure 500 {object} ProblemDetails "Internal server error"
// @Security BearerAuth
// @Router /v1/feedback-records/search [get]
func (h *FeedbackRecordsHandler) Search(w http.ResponseWriter, r *http.Request) {
	req := &models.SearchFeedbackRecordsRequest{}

	// Decode and validate query parameters
	if err := validation.ValidateAndDecodeQueryParams(r, req); err != nil {
		validation.RespondValidationError(w, err)
		return
	}

	// Call service to search
	result, err := h.service.SearchFeedbackRecords(r.Context(), req)
	if err != nil {
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}
