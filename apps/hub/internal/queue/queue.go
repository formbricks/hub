// Package queue provides job queue abstraction for asynchronous background processing.
// The Queue interface allows swapping implementations (PostgreSQL, Redis, RabbitMQ, etc.)
// without changing worker or API code.
package queue

import (
	"context"
)

// JobType defines the type of job to process
type JobType string

const (
	JobTypeEnrichment JobType = "enrichment" // Sentiment/emotion/topics analysis
	JobTypeEmbedding  JobType = "embedding"  // Vector embedding generation
)

// EnrichmentJob represents a job to process text (enrichment or embedding)
type EnrichmentJob struct {
	ID               string
	FeedbackRecordID string
	JobType          JobType
	Text             string
}

// Queue defines the interface for job queue operations.
// This abstraction allows swapping PostgreSQL with Redis, RabbitMQ, etc. in the future
// without changing the worker or API code.
type Queue interface {
	// Enqueue adds a new enrichment job to the queue
	Enqueue(ctx context.Context, feedbackRecordID, text string) error

	// EnqueueEmbedding adds a new embedding job to the queue
	EnqueueEmbedding(ctx context.Context, feedbackRecordID, text string) error

	// Dequeue retrieves and locks the next pending job for processing.
	// Returns nil if no jobs are available.
	Dequeue(ctx context.Context) (*EnrichmentJob, error)

	// MarkComplete marks a job as successfully completed
	MarkComplete(ctx context.Context, jobID string) error

	// MarkFailed marks a job as failed with an error message
	MarkFailed(ctx context.Context, jobID string, err error) error
}
