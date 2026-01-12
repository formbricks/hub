package queue

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/formbricks/hub/apps/hub/internal/ent"
	"github.com/google/uuid"
)

// PostgresQueue implements the Queue interface using PostgreSQL and Ent ORM
type PostgresQueue struct {
	client *ent.Client
}

// NewPostgresQueue creates a new PostgreSQL-backed queue
func NewPostgresQueue(client *ent.Client) *PostgresQueue {
	return &PostgresQueue{
		client: client,
	}
}

// Enqueue adds a new enrichment job to the queue
func (q *PostgresQueue) Enqueue(ctx context.Context, feedbackRecordID, text string) error {
	return q.enqueueJob(ctx, feedbackRecordID, text, JobTypeEnrichment)
}

// EnqueueEmbedding adds a new embedding job to the queue
func (q *PostgresQueue) EnqueueEmbedding(ctx context.Context, feedbackRecordID, text string) error {
	return q.enqueueJob(ctx, feedbackRecordID, text, JobTypeEmbedding)
}

// enqueueJob is a helper to enqueue jobs of any type
func (q *PostgresQueue) enqueueJob(ctx context.Context, feedbackRecordID, text string, jobType JobType) error {
	frID, err := uuid.Parse(feedbackRecordID)
	if err != nil {
		return fmt.Errorf("invalid feedback record ID: %w", err)
	}

	_, err = q.client.EnrichmentJob.
		Create().
		SetFeedbackRecordID(frID).
		SetJobType(string(jobType)).
		SetText(text).
		SetStatus("pending").
		Save(ctx)

	if err != nil {
		return fmt.Errorf("failed to enqueue %s job: %w", jobType, err)
	}

	return nil
}

// Dequeue retrieves and locks the next pending job for processing.
// Uses a query+update loop to prevent race conditions between workers.
// Returns nil if no jobs are available.
func (q *PostgresQueue) Dequeue(ctx context.Context) (*EnrichmentJob, error) {
	// Try to find and claim a pending job using a query+update approach:
	// 1. Query for pending jobs
	// 2. Try to update the first one
	// 3. If successful, return it; if it fails (race condition), return nil

	jobs, err := q.client.EnrichmentJob.
		Query().
		Where(func(s *sql.Selector) {
			s.Where(sql.EQ("status", "pending"))
		}).
		Order(ent.Asc("created_at")).
		Limit(1).
		All(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed to query jobs: %w", err)
	}

	if len(jobs) == 0 {
		return nil, nil // No jobs available
	}

	job := jobs[0]

	// Try to claim the job by updating it
	// This might fail if another worker claims it first (race condition)
	updatedJob, err := q.client.EnrichmentJob.
		UpdateOneID(job.ID).
		Where(func(s *sql.Selector) {
			s.Where(sql.EQ("status", "pending"))
		}).
		SetStatus("processing").
		SetAttempts(job.Attempts + 1).
		Save(ctx)

	if err != nil {
		if ent.IsNotFound(err) {
			// Another worker claimed it, return nil to try again
			return nil, nil
		}
		return nil, fmt.Errorf("failed to update job: %w", err)
	}

	return &EnrichmentJob{
		ID:               updatedJob.ID.String(),
		FeedbackRecordID: updatedJob.FeedbackRecordID.String(),
		JobType:          JobType(updatedJob.JobType),
		Text:             updatedJob.Text,
	}, nil
}

// MarkComplete marks a job as successfully completed
func (q *PostgresQueue) MarkComplete(ctx context.Context, jobID string) error {
	id, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("invalid job ID: %w", err)
	}

	err = q.client.EnrichmentJob.
		UpdateOneID(id).
		SetStatus("completed").
		SetProcessedAt(time.Now()).
		Exec(ctx)

	if err != nil {
		return fmt.Errorf("failed to mark job as complete: %w", err)
	}

	return nil
}

// MarkFailed marks a job as failed with an error message
func (q *PostgresQueue) MarkFailed(ctx context.Context, jobID string, jobErr error) error {
	id, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("invalid job ID: %w", err)
	}

	// Guard against nil errors
	errorMsg := "unknown error"
	if jobErr != nil {
		errorMsg = jobErr.Error()
	}

	err = q.client.EnrichmentJob.
		UpdateOneID(id).
		SetStatus("failed").
		SetError(errorMsg).
		SetProcessedAt(time.Now()).
		Exec(ctx)

	if err != nil {
		return fmt.Errorf("failed to mark job as failed: %w", err)
	}

	return nil
}
