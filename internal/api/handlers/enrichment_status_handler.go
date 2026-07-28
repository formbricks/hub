package handlers

import (
	"context"
	"net/http"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/models"
)

// EnrichmentStatusService is the application service used by the enrichment status handler.
type EnrichmentStatusService interface {
	GetEnrichmentStatus(ctx context.Context, tenantID string) (*models.EnrichmentStatusResponse, error)
}

// EnrichmentStatusHandler hosts the public enrichment-status endpoint.
type EnrichmentStatusHandler struct {
	service EnrichmentStatusService
}

// NewEnrichmentStatusHandler creates an enrichment status handler.
func NewEnrichmentStatusHandler(service EnrichmentStatusService) *EnrichmentStatusHandler {
	return &EnrichmentStatusHandler{service: service}
}

// GetStatus handles GET /v1/enrichment-status?tenant_id=<id>. It returns per-enrichment
// eligible/done counts for the tenant. tenant_id is required; a missing/blank value yields 400.
func (h *EnrichmentStatusHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		response.RespondServiceUnavailable(w, r, "Enrichment status is not available.")

		return
	}

	tenantID := r.URL.Query().Get("tenant_id")

	result, err := h.service.GetEnrichmentStatus(r.Context(), tenantID)
	if err != nil {
		// RespondError maps a missing/blank tenant_id (huberrors.ValidationError) to 400 and
		// any repository/DB failure to a generic 500 — no internals leak to the client.
		response.RespondError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}
