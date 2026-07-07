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
	"github.com/formbricks/hub/internal/models"
)

// FeedbackRecordsService defines the interface for feedback records business logic.
type FeedbackRecordsService interface {
	CreateFeedbackRecord(ctx context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	GetFeedbackRecord(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	ListFeedbackRecords(ctx context.Context, filters *models.ListFeedbackRecordsFilters) (*models.ListFeedbackRecordsResponse, error)
	UpdateFeedbackRecord(ctx context.Context, id uuid.UUID, req *models.UpdateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	DeleteFeedbackRecord(ctx context.Context, id uuid.UUID) error
	CountFeedbackRecords(ctx context.Context, filters *models.ListFeedbackRecordsFilters) (int, error)
	DeleteFeedbackRecordsByUser(ctx context.Context, filters *models.DeleteFeedbackRecordsByUserFilters) (int, error)
}

// FeedbackRecordsHandler handles HTTP requests for feedback records.
type FeedbackRecordsHandler struct {
	service FeedbackRecordsService
}

// NewFeedbackRecordsHandler creates a new feedback records handler.
func NewFeedbackRecordsHandler(service FeedbackRecordsService) *FeedbackRecordsHandler {
	return &FeedbackRecordsHandler{service: service}
}

// maxFeedbackRecordBodyBytes caps the create and update request bodies. Nothing else bounds
// the payload end to end, and every accepted byte of value_text is re-sent to the LLM and
// embedding providers by up to four enrichment pipelines (× retry attempts, re-triggered per
// edit) — so an unbounded body is an unbounded provider bill. 512 KiB comfortably fits any
// legitimate record (value_text is separately capped at 30k characters; the rest is metadata)
// while blocking multi-megabyte abuse before it is read into memory.
const maxFeedbackRecordBodyBytes = 512 << 10

// decodeRecordBody bounds, decodes (rejecting unknown fields), and validates a feedback-record
// request body. It writes the matching problem response — 413 for an oversized body, 400 for
// malformed JSON, unknown fields, or invalid values — and returns false when it has already
// responded, so callers just `return`. Mirrors decodeSettingsBody.
func decodeRecordBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxFeedbackRecordBodyBytes)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			response.RespondProblem(w, r, http.StatusRequestEntityTooLarge, "request body too large")

			return false
		}

		response.RespondError(w, r, response.NewRequestJSONDecodeError(err))

		return false
	}

	if err := validation.ValidateStruct(dst); err != nil {
		response.RespondError(w, r, err)

		return false
	}

	return true
}

// Create handles POST /v1/feedback-records.
func (h *FeedbackRecordsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req models.CreateFeedbackRecordRequest

	if !decodeRecordBody(w, r, &req) {
		return
	}

	record, err := h.service.CreateFeedbackRecord(r.Context(), &req)
	if err != nil {
		response.RespondError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusCreated, record)
}

// Get handles GET /v1/feedback-records/{id}.
func (h *FeedbackRecordsHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		response.RespondInvalidParams(w, r, response.InvalidParam{Name: "id", Reason: "is required"})

		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		response.RespondInvalidParams(w, r, response.InvalidParam{Name: "id", Reason: "must be a valid UUID"})

		return
	}

	record, err := h.service.GetFeedbackRecord(r.Context(), id)
	if err != nil {
		response.RespondError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, record)
}

// List handles GET /v1/feedback-records.
func (h *FeedbackRecordsHandler) List(w http.ResponseWriter, r *http.Request) {
	filters := &models.ListFeedbackRecordsFilters{}

	if err := validation.ValidateAndDecodeQueryParams(r, filters); err != nil {
		response.RespondError(w, r, err)

		return
	}

	result, err := h.service.ListFeedbackRecords(r.Context(), filters)
	if err != nil {
		response.RespondError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

// Update handles PATCH /v1/feedback-records/{id}.
func (h *FeedbackRecordsHandler) Update(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		response.RespondInvalidParams(w, r, response.InvalidParam{Name: "id", Reason: "is required"})

		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		response.RespondInvalidParams(w, r, response.InvalidParam{Name: "id", Reason: "must be a valid UUID"})

		return
	}

	var req models.UpdateFeedbackRecordRequest

	if !decodeRecordBody(w, r, &req) {
		return
	}

	record, err := h.service.UpdateFeedbackRecord(r.Context(), id, &req)
	if err != nil {
		response.RespondError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, record)
}

// Delete handles DELETE /v1/feedback-records/{id}.
func (h *FeedbackRecordsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		response.RespondInvalidParams(w, r, response.InvalidParam{Name: "id", Reason: "is required"})

		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		response.RespondInvalidParams(w, r, response.InvalidParam{Name: "id", Reason: "must be a valid UUID"})

		return
	}

	if err := h.service.DeleteFeedbackRecord(r.Context(), id); err != nil {
		response.RespondError(w, r, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// DeleteByUser handles DELETE /v1/feedback-records?user_id=<id>[&tenant_id=<id>].
func (h *FeedbackRecordsHandler) DeleteByUser(w http.ResponseWriter, r *http.Request) {
	filters := &models.DeleteFeedbackRecordsByUserFilters{}

	if err := validation.ValidateAndDecodeQueryParams(r, filters); err != nil {
		response.RespondError(w, r, err)

		return
	}

	deletedCount, err := h.service.DeleteFeedbackRecordsByUser(r.Context(), filters)
	if err != nil {
		tenantIDLength := 0
		if filters.TenantID != nil {
			tenantIDLength = len(*filters.TenantID)
		}

		response.RespondErrorWithLogAttrs(w, r, err,
			"user_id_present", filters.UserID != "",
			"user_id_length", len(filters.UserID),
			"tenant_id_present", tenantIDLength > 0,
			"tenant_id_length", tenantIDLength,
		)

		return
	}

	resp := models.DeleteFeedbackRecordsByUserResponse{
		DeletedCount: int64(deletedCount),
		Message:      fmt.Sprintf("Successfully deleted %d feedback records", deletedCount),
	}

	response.RespondJSON(w, http.StatusOK, resp)
}

// Count handles GET /v1/feedback-records/count.
func (h *FeedbackRecordsHandler) Count(w http.ResponseWriter, r *http.Request) {
	filters := &models.ListFeedbackRecordsFilters{}

	if err := validation.ValidateAndDecodeQueryParams(r, filters); err != nil {
		response.RespondError(w, r, err)

		return
	}

	count, err := h.service.CountFeedbackRecords(r.Context(), filters)
	if err != nil {
		response.RespondError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, map[string]int{"count": count})
}
