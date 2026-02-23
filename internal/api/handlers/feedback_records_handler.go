// Package handlers provides HTTP handlers for feedback records and health.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/api/validation"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

// FeedbackRecordsService defines the interface for feedback records business logic.
type FeedbackRecordsService interface {
	CreateFeedbackRecord(ctx context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	GetFeedbackRecord(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	ListFeedbackRecords(ctx context.Context, filters *models.ListFeedbackRecordsFilters) (*models.ListFeedbackRecordsResponse, error)
	UpdateFeedbackRecord(ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	DeleteFeedbackRecord(ctx context.Context, id uuid.UUID) error
	BulkDeleteFeedbackRecords(ctx context.Context, userIdentifier string, tenantID *string) (int, error)
}

// FeedbackRecordsHandler handles HTTP requests for feedback records.
type FeedbackRecordsHandler struct {
	service FeedbackRecordsService
}

// NewFeedbackRecordsHandler creates a new feedback records handler.
func NewFeedbackRecordsHandler(service FeedbackRecordsService) *FeedbackRecordsHandler {
	return &FeedbackRecordsHandler{service: service}
}

// Create handles POST /v1/feedback-records.
func (h *FeedbackRecordsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req models.CreateFeedbackRecordRequest

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&req); err != nil {
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
		if errors.Is(err, huberrors.ErrNotFound) {
			response.RespondNotFound(w, "Feedback record not found")

			return
		}

		if errors.Is(err, huberrors.ErrConflict) {
			response.RespondConflict(w, err.Error())

			return
		}

		response.RespondInternalServerError(w, "An unexpected error occurred")

		return
	}

	response.RespondJSON(w, http.StatusCreated, record)
}

// Get handles GET /v1/feedback-records/{id}.
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
		if errors.Is(err, huberrors.ErrNotFound) {
			response.RespondNotFound(w, "Feedback record not found")

			return
		}

		response.RespondInternalServerError(w, "An unexpected error occurred")

		return
	}

	response.RespondJSON(w, http.StatusOK, record)
}

// List handles GET /v1/feedback-records.
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

// Update handles PATCH /v1/feedback-records/{id}.
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

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&req); err != nil {
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
		if errors.Is(err, huberrors.ErrNotFound) {
			response.RespondNotFound(w, "Feedback record not found")

			return
		}

		response.RespondInternalServerError(w, "An unexpected error occurred")

		return
	}

	response.RespondJSON(w, http.StatusOK, record)
}

// Delete handles DELETE /v1/feedback-records/{id}.
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
		if errors.Is(err, huberrors.ErrNotFound) {
			response.RespondNotFound(w, "Feedback record not found")

			return
		}

		response.RespondInternalServerError(w, "An unexpected error occurred")

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// BulkDelete handles DELETE /v1/feedback-records?user_identifier=<id>.
func (h *FeedbackRecordsHandler) BulkDelete(w http.ResponseWriter, r *http.Request) {
	filters := &models.BulkDeleteFilters{}

	// Decode and validate query parameters
	if err := validation.ValidateAndDecodeQueryParams(r, filters); err != nil {
		validation.RespondValidationError(w, err)

		return
	}

	deletedCount, err := h.service.BulkDeleteFeedbackRecords(r.Context(), filters.UserIdentifier, filters.TenantID)
	if err != nil {
		response.RespondInternalServerError(w, "An unexpected error occurred")

		return
	}

	resp := models.BulkDeleteResponse{
		DeletedCount: int64(deletedCount),
		Message:      fmt.Sprintf("Successfully deleted %d feedback records", deletedCount),
	}

	response.RespondJSON(w, http.StatusOK, resp)
}
