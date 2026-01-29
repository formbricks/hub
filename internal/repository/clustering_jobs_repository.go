package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	apperrors "github.com/formbricks/hub/internal/errors"
	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ClusteringJobsRepository handles data access for clustering jobs.
type ClusteringJobsRepository struct {
	db *pgxpool.Pool
}

// NewClusteringJobsRepository creates a new clustering jobs repository.
func NewClusteringJobsRepository(db *pgxpool.Pool) *ClusteringJobsRepository {
	return &ClusteringJobsRepository{db: db}
}

// CreateOrUpdate creates a new clustering job schedule or updates an existing one for the tenant.
func (r *ClusteringJobsRepository) CreateOrUpdate(ctx context.Context, req *models.CreateClusteringJobRequest) (*models.ClusteringJob, error) {
	// Calculate next run time based on interval
	var nextRunAt *time.Time
	if req.ScheduleInterval != nil {
		next := calculateNextRun(*req.ScheduleInterval)
		nextRunAt = &next
	}

	query := `
		INSERT INTO clustering_jobs (tenant_id, status, schedule_interval, next_run_at)
		VALUES ($1, 'pending', $2, $3)
		ON CONFLICT (tenant_id) DO UPDATE SET
			schedule_interval = EXCLUDED.schedule_interval,
			next_run_at = EXCLUDED.next_run_at,
			status = CASE 
				WHEN EXCLUDED.schedule_interval IS NULL THEN 'disabled'
				ELSE 'pending'
			END,
			updated_at = NOW()
		RETURNING id, tenant_id, status, schedule_interval, next_run_at, last_run_at, 
		          last_job_id, last_error, topics_generated, records_processed, created_at, updated_at
	`

	var job models.ClusteringJob
	var scheduleInterval *string
	err := r.db.QueryRow(ctx, query, req.TenantID, req.ScheduleInterval, nextRunAt).Scan(
		&job.ID, &job.TenantID, &job.Status, &scheduleInterval, &job.NextRunAt, &job.LastRunAt,
		&job.LastJobID, &job.LastError, &job.TopicsGenerated, &job.RecordsProcessed, &job.CreatedAt, &job.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create/update clustering job: %w", err)
	}

	if scheduleInterval != nil {
		si := models.ScheduleInterval(*scheduleInterval)
		job.ScheduleInterval = &si
	}

	return &job, nil
}

// GetByTenantID retrieves the clustering job for a tenant.
func (r *ClusteringJobsRepository) GetByTenantID(ctx context.Context, tenantID string) (*models.ClusteringJob, error) {
	query := `
		SELECT id, tenant_id, status, schedule_interval, next_run_at, last_run_at,
		       last_job_id, last_error, topics_generated, records_processed, created_at, updated_at
		FROM clustering_jobs
		WHERE tenant_id = $1
	`

	var job models.ClusteringJob
	var scheduleInterval *string
	err := r.db.QueryRow(ctx, query, tenantID).Scan(
		&job.ID, &job.TenantID, &job.Status, &scheduleInterval, &job.NextRunAt, &job.LastRunAt,
		&job.LastJobID, &job.LastError, &job.TopicsGenerated, &job.RecordsProcessed, &job.CreatedAt, &job.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.NewNotFoundError("clustering_job", "no schedule found for tenant")
		}
		return nil, fmt.Errorf("failed to get clustering job: %w", err)
	}

	if scheduleInterval != nil {
		si := models.ScheduleInterval(*scheduleInterval)
		job.ScheduleInterval = &si
	}

	return &job, nil
}

// GetDueJobs retrieves all jobs that are due to run (next_run_at <= now).
func (r *ClusteringJobsRepository) GetDueJobs(ctx context.Context, limit int) ([]models.ClusteringJob, error) {
	if limit <= 0 {
		limit = 10
	}

	query := `
		SELECT id, tenant_id, status, schedule_interval, next_run_at, last_run_at,
		       last_job_id, last_error, topics_generated, records_processed, created_at, updated_at
		FROM clustering_jobs
		WHERE status != 'disabled' 
		  AND status != 'running'
		  AND next_run_at IS NOT NULL 
		  AND next_run_at <= NOW()
		ORDER BY next_run_at ASC
		LIMIT $1
	`

	rows, err := r.db.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get due jobs: %w", err)
	}
	defer rows.Close()

	jobs := []models.ClusteringJob{}
	for rows.Next() {
		var job models.ClusteringJob
		var scheduleInterval *string
		err := rows.Scan(
			&job.ID, &job.TenantID, &job.Status, &scheduleInterval, &job.NextRunAt, &job.LastRunAt,
			&job.LastJobID, &job.LastError, &job.TopicsGenerated, &job.RecordsProcessed, &job.CreatedAt, &job.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan clustering job: %w", err)
		}

		if scheduleInterval != nil {
			si := models.ScheduleInterval(*scheduleInterval)
			job.ScheduleInterval = &si
		}

		jobs = append(jobs, job)
	}

	return jobs, nil
}

// MarkRunning marks a job as running.
func (r *ClusteringJobsRepository) MarkRunning(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE clustering_jobs SET status = 'running' WHERE id = $1`
	result, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to mark job running: %w", err)
	}
	if result.RowsAffected() == 0 {
		return apperrors.NewNotFoundError("clustering_job", "job not found")
	}
	return nil
}

// UpdateAfterRun updates a job after execution completes.
func (r *ClusteringJobsRepository) UpdateAfterRun(ctx context.Context, id uuid.UUID, req *models.UpdateClusteringJobRequest) error {
	// Get current job to calculate next run
	var scheduleInterval *string
	err := r.db.QueryRow(ctx, `SELECT schedule_interval FROM clustering_jobs WHERE id = $1`, id).Scan(&scheduleInterval)
	if err != nil {
		return fmt.Errorf("failed to get job for update: %w", err)
	}

	// Calculate next run time
	var nextRunAt *time.Time
	if scheduleInterval != nil && req.Status == models.JobStatusComplete {
		next := calculateNextRun(models.ScheduleInterval(*scheduleInterval))
		nextRunAt = &next
	}

	query := `
		UPDATE clustering_jobs 
		SET status = $1, 
		    last_run_at = NOW(),
		    last_job_id = $2,
		    last_error = $3,
		    topics_generated = COALESCE($4, topics_generated),
		    records_processed = COALESCE($5, records_processed),
		    next_run_at = COALESCE($6, next_run_at)
		WHERE id = $7
	`

	result, err := r.db.Exec(ctx, query,
		req.Status,
		req.LastJobID,
		req.LastError,
		req.TopicsGenerated,
		req.RecordsProcessed,
		nextRunAt,
		id,
	)
	if err != nil {
		return fmt.Errorf("failed to update job after run: %w", err)
	}
	if result.RowsAffected() == 0 {
		return apperrors.NewNotFoundError("clustering_job", "job not found")
	}
	return nil
}

// Delete removes a clustering job schedule.
func (r *ClusteringJobsRepository) Delete(ctx context.Context, tenantID string) error {
	query := `DELETE FROM clustering_jobs WHERE tenant_id = $1`
	result, err := r.db.Exec(ctx, query, tenantID)
	if err != nil {
		return fmt.Errorf("failed to delete clustering job: %w", err)
	}
	if result.RowsAffected() == 0 {
		return apperrors.NewNotFoundError("clustering_job", "schedule not found")
	}
	return nil
}

// List retrieves clustering jobs with optional filters.
func (r *ClusteringJobsRepository) List(ctx context.Context, filters *models.ListClusteringJobsFilters) ([]models.ClusteringJob, error) {
	query := `
		SELECT id, tenant_id, status, schedule_interval, next_run_at, last_run_at,
		       last_job_id, last_error, topics_generated, records_processed, created_at, updated_at
		FROM clustering_jobs
	`

	var conditions []string
	var args []interface{}
	argCount := 1

	if filters.TenantID != nil {
		conditions = append(conditions, fmt.Sprintf("tenant_id = $%d", argCount))
		args = append(args, *filters.TenantID)
		argCount++
	}

	if filters.Status != nil {
		conditions = append(conditions, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *filters.Status)
		argCount++
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	query += " ORDER BY created_at DESC"

	if filters.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argCount)
		args = append(args, filters.Limit)
		argCount++
	}

	if filters.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argCount)
		args = append(args, filters.Offset)
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list clustering jobs: %w", err)
	}
	defer rows.Close()

	jobs := []models.ClusteringJob{}
	for rows.Next() {
		var job models.ClusteringJob
		var scheduleInterval *string
		err := rows.Scan(
			&job.ID, &job.TenantID, &job.Status, &scheduleInterval, &job.NextRunAt, &job.LastRunAt,
			&job.LastJobID, &job.LastError, &job.TopicsGenerated, &job.RecordsProcessed, &job.CreatedAt, &job.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan clustering job: %w", err)
		}

		if scheduleInterval != nil {
			si := models.ScheduleInterval(*scheduleInterval)
			job.ScheduleInterval = &si
		}

		jobs = append(jobs, job)
	}

	return jobs, nil
}

// Count returns the total count of clustering jobs matching the filters.
func (r *ClusteringJobsRepository) Count(ctx context.Context, filters *models.ListClusteringJobsFilters) (int64, error) {
	query := `SELECT COUNT(*) FROM clustering_jobs`

	var conditions []string
	var args []interface{}
	argCount := 1

	if filters.TenantID != nil {
		conditions = append(conditions, fmt.Sprintf("tenant_id = $%d", argCount))
		args = append(args, *filters.TenantID)
		argCount++
	}

	if filters.Status != nil {
		conditions = append(conditions, fmt.Sprintf("status = $%d", argCount))
		args = append(args, *filters.Status)
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	var count int64
	err := r.db.QueryRow(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count clustering jobs: %w", err)
	}

	return count, nil
}

// calculateNextRun calculates the next run time based on the interval.
func calculateNextRun(interval models.ScheduleInterval) time.Time {
	now := time.Now()
	switch interval {
	case models.ScheduleDaily:
		return now.Add(24 * time.Hour)
	case models.ScheduleWeekly:
		return now.Add(7 * 24 * time.Hour)
	case models.ScheduleMonthly:
		return now.AddDate(0, 1, 0)
	default:
		return now.Add(24 * time.Hour)
	}
}
