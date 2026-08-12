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

// Purge handles POST /v1/feedback-records/purge.
//
// The tenant is taken from the body and is required. This is deliberately its own path rather than
// a tenant_id filter on DELETE /v1/feedback-records: that collection delete erases one data
// subject's records (GDPR) and is reachable by callers who should never be able to empty a whole
// dataset, and a filter that widens the blast radius when omitted is the classic accidental
// mass-deletion shape. A distinct path cannot be reached by dropping a parameter.
//
// Responds 202: the purge is unbounded and runs as a background job, so acceptance is all that can
// honestly be reported here. Callers observe completion by polling GET /v1/feedback-records/count
// for the tenant.
func (h *FeedbackRecordsPurgeHandler) Purge(w http.ResponseWriter, r *http.Request) {
	req := &models.FeedbackRecordsPurgeRequest{}
	if !decodeRecordBody(w, r, req) {
		return
	}

	accepted, err := h.service.Enqueue(r.Context(), req.TenantID)
	if err != nil {
		response.RespondErrorWithLogAttrs(w, r, err,
			"tenant_id_present", req.TenantID != "",
			"tenant_id_length", len(req.TenantID),
		)

		return
	}

	response.RespondJSON(w, http.StatusAccepted, accepted)
}
