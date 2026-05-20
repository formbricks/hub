package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/api/validation"
	"github.com/formbricks/hub/internal/huberrors"
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
		if errors.Is(err, huberrors.ErrValidation) {
			validation.RespondValidationError(w, err)

			return
		}

		slog.Error("Failed to delete tenant data", // #nosec G706 -- slog key-values
			"method", r.Method,
			"path", r.URL.Path,
			"tenant_id", tenantID,
			"error", err,
		)

		response.RespondInternalServerError(w, "An unexpected error occurred")

		return
	}

	resp := models.TenantDataDeleteResponse{
		TenantID:               result.TenantID,
		DeletedFeedbackRecords: result.DeletedFeedbackRecords,
		DeletedEmbeddings:      result.DeletedEmbeddings,
		DeletedWebhooks:        result.DeletedWebhooks,
		Message:                "Successfully deleted tenant data for " + result.TenantID,
	}

	response.RespondJSON(w, http.StatusOK, resp)
}
