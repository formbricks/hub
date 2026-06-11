package handlers

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/models"
)

type TaxonomyInternalService interface {
	GetRunInput(ctx context.Context, runID uuid.UUID) (*models.TaxonomyRunInputResponse, error)
	CompleteRun(ctx context.Context, runID uuid.UUID, req models.TaxonomyRunResultRequest) (*models.TaxonomyRun, error)
	FailRun(ctx context.Context, runID uuid.UUID, message string) (*models.TaxonomyRun, error)
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

func (h *TaxonomyInternalHandler) CompleteRun(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		response.RespondServiceUnavailable(w, r, "Taxonomy internals are not available.")

		return
	}

	runID, ok := parseUUIDPathValue(w, r, "run_id")
	if !ok {
		return
	}

	var req models.TaxonomyRunResultRequest
	if err := decodeAndValidateJSON(r, &req); err != nil {
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

	result, err := h.service.FailRun(r.Context(), runID, req.Error)
	if err != nil {
		respondTaxonomyError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}
