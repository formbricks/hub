package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/api/validation"
	apperrors "github.com/formbricks/hub/internal/errors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/service"
	"github.com/google/uuid"
)

// TaxonomyService defines the interface for taxonomy operations.
type TaxonomyService interface {
	TriggerClustering(ctx context.Context, tenantID string, config *service.ClusterConfig) (*service.ClusteringJobResponse, error)
	GetClusteringStatus(ctx context.Context, tenantID string, jobID *uuid.UUID) (*service.ClusteringJobResponse, error)
	GenerateTaxonomySync(ctx context.Context, tenantID string, config *service.ClusterConfig) (*service.ClusteringJobResponse, error)
	HealthCheck(ctx context.Context) error
}

// ScheduleRepository defines the interface for schedule data access.
type ScheduleRepository interface {
	CreateOrUpdate(ctx context.Context, req *models.CreateClusteringJobRequest) (*models.ClusteringJob, error)
	GetByTenantID(ctx context.Context, tenantID string) (*models.ClusteringJob, error)
	Delete(ctx context.Context, tenantID string) error
	List(ctx context.Context, filters *models.ListClusteringJobsFilters) ([]models.ClusteringJob, error)
	Count(ctx context.Context, filters *models.ListClusteringJobsFilters) (int64, error)
}

// TaxonomyHandler handles HTTP requests for taxonomy operations.
type TaxonomyHandler struct {
	client       TaxonomyService
	scheduleRepo ScheduleRepository
}

// NewTaxonomyHandler creates a new taxonomy handler.
func NewTaxonomyHandler(client TaxonomyService) *TaxonomyHandler {
	return &TaxonomyHandler{client: client}
}

// NewTaxonomyHandlerWithSchedule creates a taxonomy handler with schedule support.
func NewTaxonomyHandlerWithSchedule(client TaxonomyService, scheduleRepo ScheduleRepository) *TaxonomyHandler {
	return &TaxonomyHandler{
		client:       client,
		scheduleRepo: scheduleRepo,
	}
}

// GenerateTaxonomyRequest is the request body for taxonomy generation.
type GenerateTaxonomyRequest struct {
	// Optional clustering configuration overrides
	UMAPNComponents       *int     `json:"umap_n_components,omitempty"`
	UMAPNNeighbors        *int     `json:"umap_n_neighbors,omitempty"`
	UMAPMinDist           *float64 `json:"umap_min_dist,omitempty"`
	HDBSCANMinClusterSize *int     `json:"hdbscan_min_cluster_size,omitempty"`
	HDBSCANMinSamples     *int     `json:"hdbscan_min_samples,omitempty"`
	MaxEmbeddings         *int     `json:"max_embeddings,omitempty"`
	GenerateLevel2        *bool    `json:"generate_level2,omitempty"`
	Level2MinClusterSize  *int     `json:"level2_min_cluster_size,omitempty"`
}

// Generate handles POST /v1/taxonomy/{tenant_id}/generate
// Triggers async taxonomy generation for a tenant.
func (h *TaxonomyHandler) Generate(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenant_id")
	if tenantID == "" {
		response.RespondBadRequest(w, "tenant_id is required")
		return
	}

	var req GenerateTaxonomyRequest
	if r.Body != nil && r.ContentLength > 0 {
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			response.RespondBadRequest(w, "Invalid request body")
			return
		}
	}

	// Convert to service config
	config := &service.ClusterConfig{
		UMAPNComponents:       req.UMAPNComponents,
		UMAPNNeighbors:        req.UMAPNNeighbors,
		UMAPMinDist:           req.UMAPMinDist,
		HDBSCANMinClusterSize: req.HDBSCANMinClusterSize,
		HDBSCANMinSamples:     req.HDBSCANMinSamples,
		MaxEmbeddings:         req.MaxEmbeddings,
		GenerateLevel2:        req.GenerateLevel2,
		Level2MinClusterSize:  req.Level2MinClusterSize,
	}

	result, err := h.client.TriggerClustering(r.Context(), tenantID, config)
	if err != nil {
		response.RespondInternalServerError(w, "Failed to trigger taxonomy generation: "+err.Error())
		return
	}

	response.RespondJSON(w, http.StatusAccepted, result)
}

// GenerateSync handles POST /v1/taxonomy/{tenant_id}/generate/sync
// Synchronously generates taxonomy (blocking call).
func (h *TaxonomyHandler) GenerateSync(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenant_id")
	if tenantID == "" {
		response.RespondBadRequest(w, "tenant_id is required")
		return
	}

	var req GenerateTaxonomyRequest
	if r.Body != nil && r.ContentLength > 0 {
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			response.RespondBadRequest(w, "Invalid request body")
			return
		}
	}

	// Convert to service config
	config := &service.ClusterConfig{
		UMAPNComponents:       req.UMAPNComponents,
		UMAPNNeighbors:        req.UMAPNNeighbors,
		UMAPMinDist:           req.UMAPMinDist,
		HDBSCANMinClusterSize: req.HDBSCANMinClusterSize,
		HDBSCANMinSamples:     req.HDBSCANMinSamples,
		MaxEmbeddings:         req.MaxEmbeddings,
		GenerateLevel2:        req.GenerateLevel2,
		Level2MinClusterSize:  req.Level2MinClusterSize,
	}

	result, err := h.client.GenerateTaxonomySync(r.Context(), tenantID, config)
	if err != nil {
		response.RespondInternalServerError(w, "Taxonomy generation failed: "+err.Error())
		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

// Status handles GET /v1/taxonomy/{tenant_id}/status
// Gets the status of the most recent clustering job for a tenant.
func (h *TaxonomyHandler) Status(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenant_id")
	if tenantID == "" {
		response.RespondBadRequest(w, "tenant_id is required")
		return
	}

	// Optional job_id query parameter
	var jobID *uuid.UUID
	if jobIDStr := r.URL.Query().Get("job_id"); jobIDStr != "" {
		id, err := uuid.Parse(jobIDStr)
		if err != nil {
			response.RespondBadRequest(w, "Invalid job_id format")
			return
		}
		jobID = &id
	}

	result, err := h.client.GetClusteringStatus(r.Context(), tenantID, jobID)
	if err != nil {
		response.RespondNotFound(w, "Job not found: "+err.Error())
		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

// Health handles GET /v1/taxonomy/health
// Checks if the taxonomy service is available.
func (h *TaxonomyHandler) Health(w http.ResponseWriter, r *http.Request) {
	if err := h.client.HealthCheck(r.Context()); err != nil {
		response.RespondJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "unavailable",
			"error":  err.Error(),
		})
		return
	}

	response.RespondJSON(w, http.StatusOK, map[string]string{
		"status": "healthy",
	})
}

// ScheduleRequest is the request body for creating/updating a schedule.
type ScheduleRequest struct {
	Interval string `json:"interval" validate:"required,oneof=daily weekly monthly"`
}

// CreateSchedule handles POST /v1/taxonomy/{tenant_id}/schedule
// Creates or updates a periodic clustering schedule for a tenant.
func (h *TaxonomyHandler) CreateSchedule(w http.ResponseWriter, r *http.Request) {
	if h.scheduleRepo == nil {
		response.RespondInternalServerError(w, "Scheduling not configured")
		return
	}

	tenantID := r.PathValue("tenant_id")
	if tenantID == "" {
		response.RespondBadRequest(w, "tenant_id is required")
		return
	}

	var req ScheduleRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		response.RespondBadRequest(w, "Invalid request body")
		return
	}

	if err := validation.ValidateStruct(&req); err != nil {
		validation.RespondValidationError(w, err)
		return
	}

	interval := models.ScheduleInterval(req.Interval)
	job, err := h.scheduleRepo.CreateOrUpdate(r.Context(), &models.CreateClusteringJobRequest{
		TenantID:         tenantID,
		ScheduleInterval: &interval,
	})
	if err != nil {
		response.RespondInternalServerError(w, "Failed to create schedule: "+err.Error())
		return
	}

	response.RespondJSON(w, http.StatusCreated, job)
}

// GetSchedule handles GET /v1/taxonomy/{tenant_id}/schedule
// Gets the current schedule for a tenant.
func (h *TaxonomyHandler) GetSchedule(w http.ResponseWriter, r *http.Request) {
	if h.scheduleRepo == nil {
		response.RespondInternalServerError(w, "Scheduling not configured")
		return
	}

	tenantID := r.PathValue("tenant_id")
	if tenantID == "" {
		response.RespondBadRequest(w, "tenant_id is required")
		return
	}

	job, err := h.scheduleRepo.GetByTenantID(r.Context(), tenantID)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			response.RespondNotFound(w, "No schedule found for tenant")
			return
		}
		response.RespondInternalServerError(w, "Failed to get schedule: "+err.Error())
		return
	}

	response.RespondJSON(w, http.StatusOK, job)
}

// DeleteSchedule handles DELETE /v1/taxonomy/{tenant_id}/schedule
// Deletes the schedule for a tenant.
func (h *TaxonomyHandler) DeleteSchedule(w http.ResponseWriter, r *http.Request) {
	if h.scheduleRepo == nil {
		response.RespondInternalServerError(w, "Scheduling not configured")
		return
	}

	tenantID := r.PathValue("tenant_id")
	if tenantID == "" {
		response.RespondBadRequest(w, "tenant_id is required")
		return
	}

	if err := h.scheduleRepo.Delete(r.Context(), tenantID); err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			response.RespondNotFound(w, "No schedule found for tenant")
			return
		}
		response.RespondInternalServerError(w, "Failed to delete schedule: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ListSchedules handles GET /v1/taxonomy/schedules
// Lists all clustering schedules.
func (h *TaxonomyHandler) ListSchedules(w http.ResponseWriter, r *http.Request) {
	if h.scheduleRepo == nil {
		response.RespondInternalServerError(w, "Scheduling not configured")
		return
	}

	filters := &models.ListClusteringJobsFilters{}
	if err := validation.ValidateAndDecodeQueryParams(r, filters); err != nil {
		validation.RespondValidationError(w, err)
		return
	}

	if filters.Limit <= 0 {
		filters.Limit = 100
	}

	jobs, err := h.scheduleRepo.List(r.Context(), filters)
	if err != nil {
		response.RespondInternalServerError(w, "Failed to list schedules: "+err.Error())
		return
	}

	total, err := h.scheduleRepo.Count(r.Context(), filters)
	if err != nil {
		response.RespondInternalServerError(w, "Failed to count schedules: "+err.Error())
		return
	}

	response.RespondJSON(w, http.StatusOK, models.ListClusteringJobsResponse{
		Data:   jobs,
		Total:  total,
		Limit:  filters.Limit,
		Offset: filters.Offset,
	})
}
