package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/api/validation"
	"github.com/formbricks/hub/internal/models"
)

// maxSettingsRequestBodyBytes caps the PUT request body. A settings payload is
// tiny (a short locale string), so 8 KiB is generous; larger bodies are rejected
// with 413 before being read into memory.
const maxSettingsRequestBodyBytes = 8 << 10

// TenantSettingsService defines the business logic for tenant-scoped settings.
type TenantSettingsService interface {
	GetSettings(ctx context.Context, tenantID string) (*models.TenantSettings, error)
	UpdateSettings(
		ctx context.Context, tenantID string, req *models.UpdateTenantSettingsRequest,
	) (*models.TenantSettings, error)
}

// TenantSettingsHandler handles HTTP requests for tenant settings.
type TenantSettingsHandler struct {
	service TenantSettingsService
}

// NewTenantSettingsHandler creates a new tenant settings handler.
func NewTenantSettingsHandler(service TenantSettingsService) *TenantSettingsHandler {
	return &TenantSettingsHandler{service: service}
}

// Get handles GET /v1/tenants/{tenant_id}/settings. A tenant with no stored
// settings returns 200 with default (unset) values, not 404.
func (h *TenantSettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenant_id")

	settings, err := h.service.GetSettings(r.Context(), tenantID)
	if err != nil {
		response.RespondError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, settings)
}

// Update handles PUT /v1/tenants/{tenant_id}/settings. The body replaces the
// tenant's full settings object; tenant_id is taken from the path (never the
// body), so a request can only ever modify its own tenant's settings.
func (h *TenantSettingsHandler) Update(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenant_id")

	// Cap the request body; the settings payload is tiny, so anything larger is
	// rejected with 413 rather than read into memory.
	r.Body = http.MaxBytesReader(w, r.Body, maxSettingsRequestBodyBytes)

	var req models.UpdateTenantSettingsRequest

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			response.RespondProblem(w, r, http.StatusRequestEntityTooLarge, "request body too large")

			return
		}

		response.RespondError(w, r, response.NewRequestJSONDecodeError(err))

		return
	}

	if err := validation.ValidateStruct(&req); err != nil {
		response.RespondError(w, r, err)

		return
	}

	settings, err := h.service.UpdateSettings(r.Context(), tenantID, &req)
	if err != nil {
		response.RespondError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, settings)
}
