package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

const (
	feedbackEmbeddingKind = "feedback_embedding"
	// EmbeddingsQueueName is the River queue used for feedback embedding jobs.
	EmbeddingsQueueName = "embeddings"
)

// FeedbackEmbeddingInserter inserts embedding jobs (e.g. River client). Used by BackfillEmbeddings.
type FeedbackEmbeddingInserter interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// FeedbackEmbeddingArgs is the job payload for generating and storing an embedding for one feedback record.
// Used by EmbeddingProvider and the backfill flow to enqueue, and by FeedbackEmbeddingWorker to run.
// Uniqueness is by FeedbackRecordID so duplicate events for the same record do not create duplicate jobs.
type FeedbackEmbeddingArgs struct {
	FeedbackRecordID uuid.UUID `json:"feedback_record_id" river:"unique"`
}

// Kind returns the River job kind.
func (FeedbackEmbeddingArgs) Kind() string { return feedbackEmbeddingKind }

var _ river.JobArgs = FeedbackEmbeddingArgs{}
