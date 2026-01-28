package models

import (
	"time"

	"github.com/google/uuid"
)

// ScheduleInterval represents the interval for periodic clustering.
type ScheduleInterval string

const (
	ScheduleDaily   ScheduleInterval = "daily"
	ScheduleWeekly  ScheduleInterval = "weekly"
	ScheduleMonthly ScheduleInterval = "monthly"
)

// ClusteringJobStatus represents the status of a clustering job schedule.
type ClusteringJobStatus string

const (
	JobStatusPending  ClusteringJobStatus = "pending"
	JobStatusRunning  ClusteringJobStatus = "running"
	JobStatusComplete ClusteringJobStatus = "completed"
	JobStatusFailed   ClusteringJobStatus = "failed"
	JobStatusDisabled ClusteringJobStatus = "disabled"
)

// ClusteringJob represents a scheduled clustering job for a tenant.
type ClusteringJob struct {
	ID               uuid.UUID            `json:"id"`
	TenantID         string               `json:"tenant_id"`
	Status           ClusteringJobStatus  `json:"status"`
	ScheduleInterval *ScheduleInterval    `json:"schedule_interval,omitempty"`
	NextRunAt        *time.Time           `json:"next_run_at,omitempty"`
	LastRunAt        *time.Time           `json:"last_run_at,omitempty"`
	LastJobID        *uuid.UUID           `json:"last_job_id,omitempty"`
	LastError        *string              `json:"last_error,omitempty"`
	TopicsGenerated  int                  `json:"topics_generated"`
	RecordsProcessed int                  `json:"records_processed"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
}

// CreateClusteringJobRequest represents the request to create/update a clustering schedule.
type CreateClusteringJobRequest struct {
	TenantID         string            `json:"tenant_id" validate:"required,no_null_bytes,min=1,max=255"`
	ScheduleInterval *ScheduleInterval `json:"schedule_interval,omitempty" validate:"omitempty,oneof=daily weekly monthly"`
}

// UpdateClusteringJobRequest represents the request to update a clustering job after execution.
type UpdateClusteringJobRequest struct {
	Status           ClusteringJobStatus `json:"status"`
	LastJobID        *uuid.UUID          `json:"last_job_id,omitempty"`
	LastError        *string             `json:"last_error,omitempty"`
	TopicsGenerated  *int                `json:"topics_generated,omitempty"`
	RecordsProcessed *int                `json:"records_processed,omitempty"`
}

// ListClusteringJobsFilters represents filters for listing clustering jobs.
type ListClusteringJobsFilters struct {
	TenantID *string              `form:"tenant_id" validate:"omitempty,no_null_bytes"`
	Status   *ClusteringJobStatus `form:"status" validate:"omitempty"`
	Limit    int                  `form:"limit" validate:"omitempty,min=1,max=1000"`
	Offset   int                  `form:"offset" validate:"omitempty,min=0"`
}

// ListClusteringJobsResponse represents the response for listing clustering jobs.
type ListClusteringJobsResponse struct {
	Data   []ClusteringJob `json:"data"`
	Total  int64           `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}
