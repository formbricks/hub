package handlers

import (
	"context"
	"net/http"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/models"
)

// FeedbackRecordsPurgeService defines the interface for accepting a tenant feedback-records purge.
// Only the enqueue half lives here; performing the purge is hub-worker's job.
type FeedbackRecordsPurgeService interface {
	Enqueue(ctx context.Context, tenantID string) (*models.FeedbackRecordsPurgeAcceptedResponse, error)
}

// FeedbackRecordsPurgeHandler handles tenant feedback-records purge requests.
type FeedbackRecordsPurgeHandler struct {
	service FeedbackRecordsPurgeService
}

// NewFeedbackRecordsPurgeHandler creates a new feedback-records purge handler.
func NewFeedbackRecordsPurgeHandler(service FeedbackRecordsPurgeService) *FeedbackRecordsPurgeHandler {
	return &FeedbackRecordsPurgeHandler{service: service}
}

// Purge handles DELETE /v1/tenants/{tenant_id}/feedback-records.
//
// Sits under /v1/tenants/ rather than /v1/feedback-records/ for two reasons, both about blast
// radius. The gateway publicly routes the /v1/feedback-records prefix and injects Hub credentials
// into it, so an endpoint there is exposed to the internet and defended only by the ext_authz route
// allowlist; nothing routes /v1/tenants, which is why the offboarding purge lives there too. And
// taking the tenant as a required path segment means it cannot be omitted: drop it and the request
// routes somewhere else entirely, rather than widening into "every tenant" the way a missing filter
// on a collection delete would.
//
// Responds 202: the purge is unbounded and runs as a background job, so acceptance is all that can
// honestly be reported here. Callers observe completion by polling
// GET /v1/feedback-records/count?tenant_id=... for the tenant.
func (h *FeedbackRecordsPurgeHandler) Purge(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenant_id")

	accepted, err := h.service.Enqueue(r.Context(), tenantID)
	if err != nil {
		response.RespondErrorWithLogAttrs(w, r, err,
			"tenant_id_present", tenantID != "",
			"tenant_id_length", len(tenantID),
		)

		return
	}

	response.RespondJSON(w, http.StatusAccepted, accepted)
}
