package jobs

import (
	"context"
)

// JobInserter is an interface for inserting jobs into the queue.
// This allows services to enqueue jobs without knowing about River directly.
type JobInserter interface {
	// InsertEmbeddingJob enqueues an embedding generation job.
	// Returns an error if the job could not be inserted.
	InsertEmbeddingJob(ctx context.Context, args EmbeddingJobArgs) error
}
