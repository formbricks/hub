package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/api/validation"
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
		response.RespondError(w, r, err)

		return
	}

	if err := validation.ValidateStruct(&req); err != nil {
		response.RespondError(w, r, err)

		return
	}

	webhook, err := h.service.CreateWebhook(r.Context(), &req)
	if err != nil {
		response.RespondError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusCreated, webhook)
}

// Get handles GET /v1/webhooks/{id}.
func (h *WebhooksHandler) Get(w http.ResponseWriter, r *http.Request) {
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

	webhook, err := h.service.GetWebhook(r.Context(), id)
	if err != nil {
		response.RespondError(w, r, err)

		return
	}

	public := models.ToWebhookPublic(*webhook)
	response.RespondJSON(w, http.StatusOK, &public)
}

// List handles GET /v1/webhooks.
func (h *WebhooksHandler) List(w http.ResponseWriter, r *http.Request) {
	filters := &models.ListWebhooksFilters{}

	if err := validation.ValidateAndDecodeQueryParams(r, filters); err != nil {
		response.RespondError(w, r, err)

		return
	}

	result, err := h.service.ListWebhooks(r.Context(), filters)
	if err != nil {
		response.RespondError(w, r, err)

		return
	}

	publicData := make([]models.WebhookPublic, len(result.Data))
	for i := range result.Data {
		publicData[i] = models.ToWebhookPublic(result.Data[i])
	}

	response.RespondJSON(w, http.StatusOK, &models.ListWebhooksPublicResponse{
		Data:       publicData,
		Limit:      result.Limit,
		NextCursor: result.NextCursor,
	})
}

// Update handles PATCH /v1/webhooks/{id}.
func (h *WebhooksHandler) Update(w http.ResponseWriter, r *http.Request) {
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

	var req models.UpdateWebhookRequest

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&req); err != nil {
		response.RespondError(w, r, err)

		return
	}

	if err := validation.ValidateStruct(&req); err != nil {
		response.RespondError(w, r, err)

		return
	}

	webhook, err := h.service.UpdateWebhook(r.Context(), id, &req)
	if err != nil {
		response.RespondError(w, r, err)

		return
	}

	public := models.ToWebhookPublic(*webhook)
	response.RespondJSON(w, http.StatusOK, &public)
}

// Delete handles DELETE /v1/webhooks/{id}.
func (h *WebhooksHandler) Delete(w http.ResponseWriter, r *http.Request) {
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

	if err := h.service.DeleteWebhook(r.Context(), id); err != nil {
		response.RespondError(w, r, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
