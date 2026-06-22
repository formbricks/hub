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

// maxSettingsRequestBodyBytes caps the PUT and PATCH request bodies. A settings
// payload is tiny (a short locale string), so 8 KiB is generous; larger bodies are
// rejected with 413 before being read into memory.
const maxSettingsRequestBodyBytes = 8 << 10

// TenantSettingsService defines the business logic for tenant-scoped settings.
type TenantSettingsService interface {
	GetSettings(ctx context.Context, tenantID string) (*models.TenantSettings, error)
	UpdateSettings(
		ctx context.Context, tenantID string, req *models.UpdateTenantSettingsRequest,
	) (*models.TenantSettings, error)
	PatchSettings(
		ctx context.Context, tenantID string, req *models.PatchTenantSettingsRequest,
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

	var req models.UpdateTenantSettingsRequest
	if !decodeSettingsBody(w, r, &req) {
		return
	}

	settings, err := h.service.UpdateSettings(r.Context(), tenantID, &req)
	if err != nil {
		response.RespondError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, settings)
}

// Patch handles PATCH /v1/tenants/{tenant_id}/settings. The body is an RFC 7396
// JSON Merge Patch (Content-Type application/merge-patch+json): a member with a
// value sets that setting, a member with JSON null removes it, and an omitted
// member is left unchanged. tenant_id is taken from the path, so a request can
// only ever modify its own tenant's settings.
func (h *TenantSettingsHandler) Patch(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenant_id")

	var req models.PatchTenantSettingsRequest
	if !decodeSettingsBody(w, r, &req) {
		return
	}

	settings, err := h.service.PatchSettings(r.Context(), tenantID, &req)
	if err != nil {
		response.RespondError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, settings)
}

// decodeSettingsBody caps the request body, decodes it as JSON (rejecting unknown
// fields), and validates the struct. It writes the matching problem response — 413
// for an oversized body, 400 for malformed JSON, unknown fields, or invalid values
// — and returns false when it has already responded, so callers just `return`.
func decodeSettingsBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	// The settings payload is tiny, so anything larger than the cap is rejected
	// with 413 rather than read into memory.
	r.Body = http.MaxBytesReader(w, r.Body, maxSettingsRequestBodyBytes)

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
