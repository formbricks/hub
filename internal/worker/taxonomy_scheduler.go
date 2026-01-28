// Package worker provides background workers for the Hub API.
package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/service"
	"github.com/google/uuid"
)

// ClusteringJobsRepository defines the interface for clustering jobs data access.
type ClusteringJobsRepository interface {
	GetDueJobs(ctx context.Context, limit int) ([]models.ClusteringJob, error)
	MarkRunning(ctx context.Context, id uuid.UUID) error
	UpdateAfterRun(ctx context.Context, id uuid.UUID, req *models.UpdateClusteringJobRequest) error
}

// TaxonomyClient defines the interface for taxonomy service calls.
type TaxonomyClient interface {
	TriggerClustering(ctx context.Context, tenantID string, config *service.ClusterConfig) (*service.ClusteringJobResponse, error)
	GetClusteringStatus(ctx context.Context, tenantID string, jobID *uuid.UUID) (*service.ClusteringJobResponse, error)
}

// TaxonomyScheduler is a background worker that periodically checks for
// due clustering jobs and triggers them.
type TaxonomyScheduler struct {
	repo          ClusteringJobsRepository
	client        TaxonomyClient
	pollInterval  time.Duration
	batchSize     int
	checkInterval time.Duration // How often to check job status
}

// NewTaxonomyScheduler creates a new taxonomy scheduler worker.
func NewTaxonomyScheduler(
	repo ClusteringJobsRepository,
	client TaxonomyClient,
	pollInterval time.Duration,
	batchSize int,
) *TaxonomyScheduler {
	if pollInterval <= 0 {
		pollInterval = 1 * time.Minute
	}
	if batchSize <= 0 {
		batchSize = 5
	}

	return &TaxonomyScheduler{
		repo:          repo,
		client:        client,
		pollInterval:  pollInterval,
		batchSize:     batchSize,
		checkInterval: 10 * time.Second,
	}
}

// Start begins the background worker loop. It runs until the context is cancelled.
func (w *TaxonomyScheduler) Start(ctx context.Context) {
	slog.Info("taxonomy scheduler started",
		"poll_interval", w.pollInterval,
		"batch_size", w.batchSize,
	)

	// Run immediately on startup
	w.runOnce(ctx)

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("taxonomy scheduler stopped")
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

// runOnce checks for due jobs and triggers them.
func (w *TaxonomyScheduler) runOnce(ctx context.Context) {
	// Get jobs that are due to run
	jobs, err := w.repo.GetDueJobs(ctx, w.batchSize)
	if err != nil {
		slog.Error("failed to get due jobs", "error", err)
		return
	}

	if len(jobs) == 0 {
		slog.Debug("no due clustering jobs found")
		return
	}

	slog.Info("found due clustering jobs", "count", len(jobs))

	for _, job := range jobs {
		w.processJob(ctx, job)
	}
}

// processJob triggers clustering for a single job and updates its status.
func (w *TaxonomyScheduler) processJob(ctx context.Context, job models.ClusteringJob) {
	logger := slog.With("job_id", job.ID, "tenant_id", job.TenantID)
	logger.Info("processing scheduled clustering job")

	// Mark as running
	if err := w.repo.MarkRunning(ctx, job.ID); err != nil {
		logger.Error("failed to mark job running", "error", err)
		return
	}

	// Trigger clustering
	result, err := w.client.TriggerClustering(ctx, job.TenantID, nil)
	if err != nil {
		logger.Error("failed to trigger clustering", "error", err)
		errMsg := err.Error()
		w.repo.UpdateAfterRun(ctx, job.ID, &models.UpdateClusteringJobRequest{
			Status:    models.JobStatusFailed,
			LastError: &errMsg,
		})
		return
	}

	logger.Info("clustering triggered", "remote_job_id", result.JobID)

	// Poll for completion (async jobs)
	if result.Status == service.ClusteringStatusPending || result.Status == service.ClusteringStatusRunning {
		w.waitForCompletion(ctx, job, result.JobID, logger)
	} else {
		// Job completed immediately
		w.updateJobResult(ctx, job.ID, result, logger)
	}
}

// waitForCompletion polls the taxonomy service for job completion.
func (w *TaxonomyScheduler) waitForCompletion(ctx context.Context, job models.ClusteringJob, remoteJobID uuid.UUID, logger *slog.Logger) {
	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()

	// Timeout after 30 minutes
	timeout := time.After(30 * time.Minute)

	for {
		select {
		case <-ctx.Done():
			return
		case <-timeout:
			logger.Error("job timed out")
			errMsg := "job timed out after 30 minutes"
			w.repo.UpdateAfterRun(ctx, job.ID, &models.UpdateClusteringJobRequest{
				Status:    models.JobStatusFailed,
				LastJobID: &remoteJobID,
				LastError: &errMsg,
			})
			return
		case <-ticker.C:
			result, err := w.client.GetClusteringStatus(ctx, job.TenantID, &remoteJobID)
			if err != nil {
				logger.Error("failed to get job status", "error", err)
				continue
			}

			if result.Status == service.ClusteringStatusCompleted || result.Status == service.ClusteringStatusFailed {
				w.updateJobResult(ctx, job.ID, result, logger)
				return
			}

			logger.Debug("job still running", "progress", result.Progress)
		}
	}
}

// updateJobResult updates the local job record with the final result.
func (w *TaxonomyScheduler) updateJobResult(ctx context.Context, jobID uuid.UUID, result *service.ClusteringJobResponse, logger *slog.Logger) {
	var status models.ClusteringJobStatus
	switch result.Status {
	case service.ClusteringStatusCompleted:
		status = models.JobStatusComplete
	case service.ClusteringStatusFailed:
		status = models.JobStatusFailed
	default:
		status = models.JobStatusComplete
	}

	req := &models.UpdateClusteringJobRequest{
		Status:    status,
		LastJobID: &result.JobID,
	}

	if result.Result != nil {
		req.TopicsGenerated = &result.Result.NumClusters
		req.RecordsProcessed = &result.Result.TotalRecords

		if result.Result.ErrorMessage != nil {
			req.LastError = result.Result.ErrorMessage
		}
	}

	if result.Message != nil && status == models.JobStatusFailed {
		req.LastError = result.Message
	}

	if err := w.repo.UpdateAfterRun(ctx, jobID, req); err != nil {
		logger.Error("failed to update job result", "error", err)
		return
	}

	logger.Info("clustering job completed",
		"status", status,
		"topics_generated", req.TopicsGenerated,
		"records_processed", req.RecordsProcessed,
	)
}
