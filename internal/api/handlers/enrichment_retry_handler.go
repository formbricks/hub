package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

// maxRetryBodyBytes caps the request body. The body is a short list of enrichment names, so
// anything larger is a mistake or an attempt to make the server read something unbounded.
const maxRetryBodyBytes = 4 << 10

// EnrichmentRetryService clears terminal failure markers so the reconciler will try again.
type EnrichmentRetryService interface {
	Retry(ctx context.Context, tenantID string, enrichments []string) (*models.EnrichmentRetryResponse, error)
}

// EnrichmentRetryHandler handles manual enrichment retry requests.
type EnrichmentRetryHandler struct {
	service EnrichmentRetryService
}

// NewEnrichmentRetryHandler creates a new enrichment retry handler.
func NewEnrichmentRetryHandler(service EnrichmentRetryService) *EnrichmentRetryHandler {
	return &EnrichmentRetryHandler{service: service}
}

// enrichmentRetryRequest is the optional body: which enrichments to clear. Omitted or empty means
// all of them.
type enrichmentRetryRequest struct {
	Enrichments []string `json:"enrichments"`
}

// Retry handles POST /v1/tenants/{tenant_id}/enrichments/retry.
//
// Under /v1/tenants/ rather than /v1/feedback-records/ for the same reason both tenant purges are:
// the gateway publicly routes the /v1/feedback-records prefix and injects Hub credentials into it,
// so an endpoint there is internet-reachable and defended only by the ext_authz route allowlist.
// Nothing routes /v1/tenants. A bulk operation that spends provider money on records already known
// to fail does not belong on the public side of that line.
//
// Taking the tenant as a required path segment means it cannot be omitted: drop it and the request
// routes somewhere else entirely, rather than widening into "every tenant" the way a missing filter
// would.
//
// Responds 202. Clearing the markers is synchronous, but the retrying is not — the records are
// picked up by the next reconcile sweep — so the work this request causes has not happened yet.
func (h *EnrichmentRetryHandler) Retry(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenant_id")

	enrichments, ok := decodeRetryBody(w, r)
	if !ok {
		return
	}

	accepted, err := h.service.Retry(r.Context(), tenantID, enrichments)
	if err != nil {
		response.RespondErrorWithLogAttrs(w, r, err,
			"tenant_id_present", tenantID != "",
			"tenant_id_length", len(tenantID),
		)

		return
	}

	response.RespondJSON(w, http.StatusAccepted, accepted)
}

// decodeRetryBody reads the optional body via the same MaxBytesReader idiom every other handler
// uses (see decodeSettingsBody), so an oversized body is a 413 here like everywhere else rather
// than this endpoint's own 400. An absent or empty body means "all enrichments" — the common case
// from a UI button — so it must not be an error.
func decodeRetryBody(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRetryBodyBytes)

	decoder := json.NewDecoder(r.Body)
	// Unknown fields are rejected rather than ignored: a caller who misspells the key would
	// otherwise get a 202 that silently cleared everything instead of the one thing they named.
	decoder.DisallowUnknownFields()

	var parsed enrichmentRetryRequest

	if err := decoder.Decode(&parsed); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, true // no body at all — every enrichment
		}

		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			response.RespondProblem(w, r, http.StatusRequestEntityTooLarge, "request body too large")

			return nil, false
		}

		response.RespondError(w, r, huberrors.NewValidationError("body", "request body must be a JSON object"))

		return nil, false
	}

	return parsed.Enrichments, true
}
