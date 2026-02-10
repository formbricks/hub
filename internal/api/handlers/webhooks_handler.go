package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/api/validation"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

// WebhooksService defines the interface for webhooks business logic.
type WebhooksService interface {
	CreateWebhook(ctx context.Context, req *models.CreateWebhookRequest) (*models.Webhook, error)
	GetWebhook(ctx context.Context, id uuid.UUID) (*models.Webhook, error)
	ListWebhooks(ctx context.Context, filters *models.ListWebhooksFilters) (*models.ListWebhooksResponse, error)
	UpdateWebhook(ctx context.Context, id uuid.UUID, req *models.UpdateWebhookRequest) (*models.Webhook, error)
	DeleteWebhook(ctx context.Context, id uuid.UUID) error
}

// WebhooksHandler handles HTTP requests for webhooks.
type WebhooksHandler struct {
	service WebhooksService
}

// NewWebhooksHandler creates a new webhooks handler.
func NewWebhooksHandler(service WebhooksService) *WebhooksHandler {
	return &WebhooksHandler{service: service}
}

// Create handles POST /v1/webhooks.
func (h *WebhooksHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req models.CreateWebhookRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		slog.Warn("Invalid request body", "method", r.Method, "path", r.URL.Path, "error", err)
		response.RespondBadRequest(w, "Invalid request body")
		return
	}

	if err := validation.ValidateStruct(&req); err != nil {
		validation.RespondValidationError(w, err)
		return
	}

	webhook, err := h.service.CreateWebhook(r.Context(), &req)
	if err != nil {
		if errors.Is(err, huberrors.ErrLimitExceeded) {
			response.RespondError(w, http.StatusForbidden, "Forbidden", err.Error())
			return
		}
		slog.Error("Failed to create webhook", "method", r.Method, "path", r.URL.Path, "error", err)
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusCreated, webhook)
}

// Get handles GET /v1/webhooks/{id}.
func (h *WebhooksHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		response.RespondBadRequest(w, "Webhook ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		response.RespondBadRequest(w, "Invalid UUID format")
		return
	}

	webhook, err := h.service.GetWebhook(r.Context(), id)
	if err != nil {
		if errors.Is(err, huberrors.ErrNotFound) {
			response.RespondNotFound(w, "Webhook not found")
			return
		}
		slog.Error("Failed to get webhook", "method", r.Method, "path", r.URL.Path, "id", id, "error", err)
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusOK, webhook)
}

// List handles GET /v1/webhooks.
func (h *WebhooksHandler) List(w http.ResponseWriter, r *http.Request) {
	filters := &models.ListWebhooksFilters{}

	if err := validation.ValidateAndDecodeQueryParams(r, filters); err != nil {
		validation.RespondValidationError(w, err)
		return
	}

	result, err := h.service.ListWebhooks(r.Context(), filters)
	if err != nil {
		slog.Error("Failed to list webhooks", "method", r.Method, "path", r.URL.Path, "error", err)
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

// Update handles PATCH /v1/webhooks/{id}.
func (h *WebhooksHandler) Update(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		response.RespondBadRequest(w, "Webhook ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		response.RespondBadRequest(w, "Invalid UUID format")
		return
	}

	var req models.UpdateWebhookRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		slog.Warn("Invalid request body for update", "method", r.Method, "path", r.URL.Path, "id", id, "error", err)
		response.RespondBadRequest(w, "Invalid request body")
		return
	}

	if err := validation.ValidateStruct(&req); err != nil {
		validation.RespondValidationError(w, err)
		return
	}

	webhook, err := h.service.UpdateWebhook(r.Context(), id, &req)
	if err != nil {
		if errors.Is(err, huberrors.ErrNotFound) {
			response.RespondNotFound(w, "Webhook not found")
			return
		}
		slog.Error("Failed to update webhook", "method", r.Method, "path", r.URL.Path, "id", id, "error", err)
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	response.RespondJSON(w, http.StatusOK, webhook)
}

// Delete handles DELETE /v1/webhooks/{id}.
func (h *WebhooksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		response.RespondBadRequest(w, "Webhook ID is required")
		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		response.RespondBadRequest(w, "Invalid UUID format")
		return
	}

	if err := h.service.DeleteWebhook(r.Context(), id); err != nil {
		if errors.Is(err, huberrors.ErrNotFound) {
			response.RespondNotFound(w, "Webhook not found")
			return
		}
		slog.Error("Failed to delete webhook", "method", r.Method, "path", r.URL.Path, "id", id, "error", err)
		response.RespondInternalServerError(w, "An unexpected error occurred")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
