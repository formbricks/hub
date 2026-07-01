package handlers

import (
	"context"
	"net/http"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/models"
)

// TenantDataService defines the interface for tenant data purge business logic.
type TenantDataService interface {
	DeleteTenantData(ctx context.Context, tenantID string) (*models.TenantDataDeleteResult, error)
}

// TenantDataHandler handles tenant data purge requests.
type TenantDataHandler struct {
	service TenantDataService
}

// NewTenantDataHandler creates a new tenant data handler.
func NewTenantDataHandler(service TenantDataService) *TenantDataHandler {
	return &TenantDataHandler{service: service}
}

// Delete handles DELETE /v1/tenants/{tenant_id}/data.
func (h *TenantDataHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenant_id")

	result, err := h.service.DeleteTenantData(r.Context(), tenantID)
	if err != nil {
		response.RespondError(w, r, err)

		return
	}

	resp := models.TenantDataDeleteResponse{
		TenantID:                          result.TenantID,
		DeletedFeedbackRecords:            result.DeletedFeedbackRecords,
		DeletedEmbeddings:                 result.DeletedEmbeddings,
		DeletedWebhooks:                   result.DeletedWebhooks,
		DeletedTaxonomyRuns:               result.DeletedTaxonomyRuns,
		DeletedTaxonomyClusters:           result.DeletedTaxonomyClusters,
		DeletedTaxonomyClusterMemberships: result.DeletedTaxonomyClusterMemberships,
		DeletedTaxonomyNodes:              result.DeletedTaxonomyNodes,
		DeletedTaxonomyActiveRuns:         result.DeletedTaxonomyActiveRuns,
		DeletedTaxonomyNodeEvents:         result.DeletedTaxonomyNodeEvents,
		Message:                           "Successfully deleted tenant data for " + result.TenantID,
	}

	response.RespondJSON(w, http.StatusOK, resp)
}
