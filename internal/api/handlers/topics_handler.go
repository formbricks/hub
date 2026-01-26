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

// TopicsService defines the interface for topics business logic.
type TopicsService interface {
	CreateTopic(ctx context.Context, req *models.CreateTopicRequest) (*models.Topic, error)
	GetTopic(ctx context.Context, id uuid.UUID) (*models.Topic, error)
	ListTopics(ctx context.Context, filters *models.ListTopicsFilters) (*models.ListTopicsResponse, error)
	UpdateTopic(ctx context.Context, id uuid.UUID, req *models.UpdateTopicRequest) (*models.Topic, error)
	DeleteTopic(ctx context.Context, id uuid.UUID) error
}

// TopicsHandler handles HTTP requests for topics
type TopicsHandler struct {
	service TopicsService
}

// NewTopicsHandler creates a new topics handler
func NewTopicsHandler(service TopicsService) *TopicsHandler {
	return &TopicsHandler{service: service}
}

// Create handles POST /v1/topics
func (h *TopicsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req models.CreateTopicRequest
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

	topic, err := h.service.CreateTopic(r.Context(), &req)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			response.RespondNotFound(w, "Parent topic not found")
			return
		}
		if errors.Is(err, apperrors.ErrValidation) {
			response.RespondBadRequest(w, err.Error())
			return
		}
		if errors.Is(err, apperrors.ErrConflict) {
			response.RespondConflict(w, err.Error())
			return
		}
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusCreated, topic)
}

// Get handles GET /v1/topics/{id}
func (h *TopicsHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		response.RespondBadRequest(w, "Topic ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		response.RespondBadRequest(w, "Invalid UUID format")
		return
	}

	topic, err := h.service.GetTopic(r.Context(), id)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			response.RespondNotFound(w, "Topic not found")
			return
		}
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusOK, topic)
}

// List handles GET /v1/topics
func (h *TopicsHandler) List(w http.ResponseWriter, r *http.Request) {
	filters := &models.ListTopicsFilters{}

	// Decode and validate query parameters
	if err := validation.ValidateAndDecodeQueryParams(r, filters); err != nil {
		validation.RespondValidationError(w, err)
		return
	}

	result, err := h.service.ListTopics(r.Context(), filters)
	if err != nil {
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

// Update handles PATCH /v1/topics/{id}
func (h *TopicsHandler) Update(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		response.RespondBadRequest(w, "Topic ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		response.RespondBadRequest(w, "Invalid UUID format")
		return
	}

	var req models.UpdateTopicRequest
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

	topic, err := h.service.UpdateTopic(r.Context(), id, &req)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			response.RespondNotFound(w, "Topic not found")
			return
		}
		if errors.Is(err, apperrors.ErrConflict) {
			response.RespondConflict(w, err.Error())
			return
		}
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusOK, topic)
}

// Delete handles DELETE /v1/topics/{id}
func (h *TopicsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		response.RespondBadRequest(w, "Topic ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		response.RespondBadRequest(w, "Invalid UUID format")
		return
	}

	if err := h.service.DeleteTopic(r.Context(), id); err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			response.RespondNotFound(w, "Topic not found")
			return
		}
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
