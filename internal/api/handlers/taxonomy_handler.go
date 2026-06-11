package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/api/validation"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/service"
)

type TaxonomyService interface {
	ListFieldOptions(ctx context.Context, tenantID string) (*models.TaxonomyFieldsResponse, error)
	StartManualRun(ctx context.Context, req models.CreateTaxonomyRunRequest) (*models.CreateTaxonomyRunResponse, error)
	ListRuns(ctx context.Context, filters models.ListTaxonomyRunsFilters) (*models.ListTaxonomyRunsResponse, error)
	GetRun(ctx context.Context, runID uuid.UUID) (*models.TaxonomyRun, error)
	GetActiveTree(ctx context.Context, scope models.TaxonomyScope) (*models.TaxonomyTreeResponse, error)
	GetTree(ctx context.Context, runID uuid.UUID) (*models.TaxonomyTreeResponse, error)
	RenameNode(ctx context.Context, nodeID uuid.UUID, req models.RenameTaxonomyNodeRequest) (*models.TaxonomyNode, error)
	RemoveNode(ctx context.Context, nodeID uuid.UUID, filters models.RemoveTaxonomyNodeFilters) (*models.TaxonomyNode, error)
	ListNodeRecords(
		ctx context.Context,
		nodeID uuid.UUID,
		filters models.TaxonomyNodeRecordsFilters,
	) (*models.TaxonomyNodeRecordsResponse, error)
}

type TaxonomyHandler struct {
	service TaxonomyService
}

func NewTaxonomyHandler(service TaxonomyService) *TaxonomyHandler {
	return &TaxonomyHandler{service: service}
}

func (h *TaxonomyHandler) ListFields(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		response.RespondServiceUnavailable(w, r, "Taxonomy is not available.")

		return
	}

	tenantID := r.URL.Query().Get("tenant_id")
	result, err := h.service.ListFieldOptions(r.Context(), tenantID)
	if err != nil {
		respondTaxonomyError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

func (h *TaxonomyHandler) CreateRun(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		response.RespondServiceUnavailable(w, r, "Taxonomy is not available.")

		return
	}

	var req models.CreateTaxonomyRunRequest
	if err := decodeAndValidateJSON(r, &req); err != nil {
		response.RespondError(w, r, err)

		return
	}

	result, err := h.service.StartManualRun(r.Context(), req)
	if err != nil {
		respondTaxonomyError(w, r, err)

		return
	}

	status := http.StatusAccepted
	if result.InProgress {
		status = http.StatusOK
	}

	response.RespondJSON(w, status, result)
}

func (h *TaxonomyHandler) ListRuns(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		response.RespondServiceUnavailable(w, r, "Taxonomy is not available.")

		return
	}

	filters := models.ListTaxonomyRunsFilters{}
	if err := validation.ValidateAndDecodeQueryParams(r, &filters); err != nil {
		response.RespondError(w, r, err)

		return
	}

	result, err := h.service.ListRuns(r.Context(), filters)
	if err != nil {
		respondTaxonomyError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

func (h *TaxonomyHandler) GetRun(w http.ResponseWriter, r *http.Request) {
	runID, ok := parseUUIDPathValue(w, r, "run_id")
	if !ok {
		return
	}

	result, err := h.service.GetRun(r.Context(), runID)
	if err != nil {
		respondTaxonomyError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

func (h *TaxonomyHandler) GetActiveTree(w http.ResponseWriter, r *http.Request) {
	scope, ok := taxonomyScopeFromQuery(w, r)
	if !ok {
		return
	}

	result, err := h.service.GetActiveTree(r.Context(), scope)
	if err != nil {
		respondTaxonomyError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

func (h *TaxonomyHandler) GetTree(w http.ResponseWriter, r *http.Request) {
	runID, ok := parseUUIDPathValue(w, r, "run_id")
	if !ok {
		return
	}

	result, err := h.service.GetTree(r.Context(), runID)
	if err != nil {
		respondTaxonomyError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

func (h *TaxonomyHandler) RenameNode(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := parseUUIDPathValue(w, r, "node_id")
	if !ok {
		return
	}

	var req models.RenameTaxonomyNodeRequest
	if err := decodeAndValidateJSON(r, &req); err != nil {
		response.RespondError(w, r, err)

		return
	}

	result, err := h.service.RenameNode(r.Context(), nodeID, req)
	if err != nil {
		respondTaxonomyError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

func (h *TaxonomyHandler) RemoveNode(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := parseUUIDPathValue(w, r, "node_id")
	if !ok {
		return
	}

	filters := models.RemoveTaxonomyNodeFilters{}
	if err := validation.ValidateAndDecodeQueryParams(r, &filters); err != nil {
		response.RespondError(w, r, err)

		return
	}

	result, err := h.service.RemoveNode(r.Context(), nodeID, filters)
	if err != nil {
		respondTaxonomyError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

func (h *TaxonomyHandler) ListNodeRecords(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := parseUUIDPathValue(w, r, "node_id")
	if !ok {
		return
	}

	filters := models.TaxonomyNodeRecordsFilters{}
	if err := validation.ValidateAndDecodeQueryParams(r, &filters); err != nil {
		response.RespondError(w, r, err)

		return
	}

	result, err := h.service.ListNodeRecords(r.Context(), nodeID, filters)
	if err != nil {
		respondTaxonomyError(w, r, err)

		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

func decodeAndValidateJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return response.NewRequestJSONDecodeError(err)
	}

	return validation.ValidateStruct(dst)
}

func parseUUIDPathValue(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	raw := r.PathValue(name)
	if raw == "" {
		response.RespondInvalidParams(w, r, response.InvalidParam{Name: name, Reason: "is required"})

		return uuid.Nil, false
	}

	id, err := uuid.Parse(raw)
	if err != nil {
		response.RespondInvalidParams(w, r, response.InvalidParam{Name: name, Reason: "must be a valid UUID"})

		return uuid.Nil, false
	}

	return id, true
}

func taxonomyScopeFromQuery(w http.ResponseWriter, r *http.Request) (models.TaxonomyScope, bool) {
	scope := models.TaxonomyScope{
		TenantID:   r.URL.Query().Get("tenant_id"),
		SourceType: r.URL.Query().Get("source_type"),
		SourceID:   r.URL.Query().Get("source_id"),
		FieldID:    r.URL.Query().Get("field_id"),
	}
	if err := validation.ValidateStruct(scope); err != nil {
		response.RespondError(w, r, err)

		return models.TaxonomyScope{}, false
	}

	return scope, true
}

func respondTaxonomyError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, service.ErrTaxonomyEmbeddingsNotConfigured) {
		response.RespondServiceUnavailable(w, r, "Taxonomy requires Hub embeddings to be configured.")

		return
	}

	if errors.Is(err, service.ErrTaxonomyServiceNotConfigured) || errors.Is(err, service.ErrTaxonomyServiceStartFailed) {
		response.RespondServiceUnavailable(w, r, "Taxonomy service is not available.")

		return
	}

	response.RespondError(w, r, err)
}
