package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/models"
)

// maxTaxonomyResultBodyBytes bounds the only internal taxonomy request whose size scales with
// the selected input. A maximum run has 10,000 memberships plus at most 80 clusters and a bounded
// hierarchy; 16 MiB leaves ample JSON overhead while preventing an authenticated caller from
// making Hub decode an unbounded body into memory.
const maxTaxonomyResultBodyBytes = 16 << 20

// TaxonomyInternalService is the application service used by internal taxonomy endpoints.
type TaxonomyInternalService interface {
	GetRunInput(ctx context.Context, runID uuid.UUID) (*models.TaxonomyRunInputResponse, error)
	CompleteRun(ctx context.Context, runID uuid.UUID, req models.TaxonomyRunResultRequest) (*models.TaxonomyRun, error)
	FailRun(
		ctx context.Context,
		runID uuid.UUID,
		req models.TaxonomyRunFailedRequest,
	) (*models.TaxonomyRun, error)
	Heartbeat(ctx context.Context, runID uuid.UUID) error
}

// TaxonomyInternalHandler hosts internal taxonomy service endpoints.
type TaxonomyInternalHandler struct {
	service TaxonomyInternalService
}

// NewTaxonomyInternalHandler creates a taxonomy internal handler.
func NewTaxonomyInternalHandler(services ...TaxonomyInternalService) *TaxonomyInternalHandler {
	var service TaxonomyInternalService
	if len(services) > 0 {
		service = services[0]
	}

	return &TaxonomyInternalHandler{service: service}
}

// AuthCheck returns success after middleware.Auth enforces the internal Hub API token.
func (h *TaxonomyInternalHandler) AuthCheck(w http.ResponseWriter, _ *http.Request) {
	response.RespondJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "hub-taxonomy-internal",
	})
}

// GetRunInput returns run-scoped input for the taxonomy service.
func (h *TaxonomyInternalHandler) GetRunInput(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		response.RespondServiceUnavailable(w, r, "Taxonomy internals are not available.")

		return
	}

	runID, ok := parseUUIDPathValue(w, r, "run_id")
	if !ok {
		return
	}

	result, err := h.service.GetRunInput(r.Context(), runID)
	if err != nil {
		respondTaxonomyError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

// CompleteRun stores successful taxonomy output from the taxonomy service.
func (h *TaxonomyInternalHandler) CompleteRun(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		response.RespondServiceUnavailable(w, r, "Taxonomy internals are not available.")

		return
	}

	runID, ok := parseUUIDPathValue(w, r, "run_id")
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxTaxonomyResultBodyBytes)

	var req models.TaxonomyRunResultRequest
	if err := decodeAndValidateJSON(r, &req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			response.RespondProblem(w, r, http.StatusRequestEntityTooLarge, "request body too large")

			return
		}

		response.RespondError(w, r, err)

		return
	}

	result, err := h.service.CompleteRun(r.Context(), runID, req)
	if err != nil {
		respondTaxonomyError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

// FailRun records a failed taxonomy run from the taxonomy service.
func (h *TaxonomyInternalHandler) FailRun(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		response.RespondServiceUnavailable(w, r, "Taxonomy internals are not available.")

		return
	}

	runID, ok := parseUUIDPathValue(w, r, "run_id")
	if !ok {
		return
	}

	var req models.TaxonomyRunFailedRequest
	if err := decodeAndValidateJSON(r, &req); err != nil {
		response.RespondError(w, r, err)

		return
	}

	result, err := h.service.FailRun(r.Context(), runID, req)
	if err != nil {
		respondTaxonomyError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

// Heartbeat records that a taxonomy run is still alive so the stuck-run reaper does not fail it.
func (h *TaxonomyInternalHandler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		response.RespondServiceUnavailable(w, r, "Taxonomy internals are not available.")

		return
	}

	runID, ok := parseUUIDPathValue(w, r, "run_id")
	if !ok {
		return
	}

	if err := h.service.Heartbeat(r.Context(), runID); err != nil {
		respondTaxonomyError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
