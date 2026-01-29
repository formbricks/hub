package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/api/validation"
	apperrors "github.com/formbricks/hub/internal/errors"
	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
)

// KnowledgeRecordsService defines the interface for knowledge records business logic.
type KnowledgeRecordsService interface {
	CreateKnowledgeRecord(ctx context.Context, req *models.CreateKnowledgeRecordRequest) (*models.KnowledgeRecord, error)
	GetKnowledgeRecord(ctx context.Context, id uuid.UUID) (*models.KnowledgeRecord, error)
	ListKnowledgeRecords(ctx context.Context, filters *models.ListKnowledgeRecordsFilters) (*models.ListKnowledgeRecordsResponse, error)
	UpdateKnowledgeRecord(ctx context.Context, id uuid.UUID, req *models.UpdateKnowledgeRecordRequest) (*models.KnowledgeRecord, error)
	DeleteKnowledgeRecord(ctx context.Context, id uuid.UUID) error
	BulkDeleteKnowledgeRecords(ctx context.Context, tenantID string) (int64, error)
}

// KnowledgeRecordsHandler handles HTTP requests for knowledge records
type KnowledgeRecordsHandler struct {
	service KnowledgeRecordsService
}

// NewKnowledgeRecordsHandler creates a new knowledge records handler
func NewKnowledgeRecordsHandler(service KnowledgeRecordsService) *KnowledgeRecordsHandler {
	return &KnowledgeRecordsHandler{service: service}
}

// Create handles POST /v1/knowledge-records
func (h *KnowledgeRecordsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req models.CreateKnowledgeRecordRequest
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

	record, err := h.service.CreateKnowledgeRecord(r.Context(), &req)
	if err != nil {
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusCreated, record)
}

// Get handles GET /v1/knowledge-records/{id}
func (h *KnowledgeRecordsHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		response.RespondBadRequest(w, "Knowledge Record ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		response.RespondBadRequest(w, "Invalid UUID format")
		return
	}

	record, err := h.service.GetKnowledgeRecord(r.Context(), id)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			response.RespondNotFound(w, "Knowledge record not found")
			return
		}
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusOK, record)
}

// List handles GET /v1/knowledge-records
func (h *KnowledgeRecordsHandler) List(w http.ResponseWriter, r *http.Request) {
	filters := &models.ListKnowledgeRecordsFilters{}

	// Decode and validate query parameters
	if err := validation.ValidateAndDecodeQueryParams(r, filters); err != nil {
		validation.RespondValidationError(w, err)
		return
	}

	result, err := h.service.ListKnowledgeRecords(r.Context(), filters)
	if err != nil {
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

// Update handles PATCH /v1/knowledge-records/{id}
func (h *KnowledgeRecordsHandler) Update(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		response.RespondBadRequest(w, "Knowledge Record ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		response.RespondBadRequest(w, "Invalid UUID format")
		return
	}

	var req models.UpdateKnowledgeRecordRequest
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

	record, err := h.service.UpdateKnowledgeRecord(r.Context(), id, &req)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			response.RespondNotFound(w, "Knowledge record not found")
			return
		}
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusOK, record)
}

// Delete handles DELETE /v1/knowledge-records/{id}
func (h *KnowledgeRecordsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		response.RespondBadRequest(w, "Knowledge Record ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		response.RespondBadRequest(w, "Invalid UUID format")
		return
	}

	if err := h.service.DeleteKnowledgeRecord(r.Context(), id); err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			response.RespondNotFound(w, "Knowledge record not found")
			return
		}
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// BulkDelete handles DELETE /v1/knowledge-records?tenant_id=...
func (h *KnowledgeRecordsHandler) BulkDelete(w http.ResponseWriter, r *http.Request) {
	filters := &models.BulkDeleteKnowledgeRecordsFilters{}

	// Decode and validate query parameters
	if err := validation.ValidateAndDecodeQueryParams(r, filters); err != nil {
		validation.RespondValidationError(w, err)
		return
	}

	deletedCount, err := h.service.BulkDeleteKnowledgeRecords(r.Context(), filters.TenantID)
	if err != nil {
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	resp := models.BulkDeleteKnowledgeRecordsResponse{
		DeletedCount: deletedCount,
		Message:      fmt.Sprintf("Successfully deleted %d knowledge records", deletedCount),
	}

	response.RespondJSON(w, http.StatusOK, resp)
}
