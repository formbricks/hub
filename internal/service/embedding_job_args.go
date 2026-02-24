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
// Uniqueness is by (FeedbackRecordID, Model, ValueTextHash) so that edits within the uniqueness window
// get a new job when value_text changes; same content within 24h is deduped; one job per record+model.
type FeedbackEmbeddingArgs struct {
	FeedbackRecordID uuid.UUID `json:"feedback_record_id" river:"unique"`
	// Model is the embedding model name (e.g. text-embedding-3-small); stored in embeddings.model.
	Model string `json:"model" river:"unique"`
	// ValueTextHash is a hash of the input (trimmed value_text, or "empty"/"backfill") for dedupe semantics.
	ValueTextHash string `json:"value_text_hash" river:"unique"`
}

// Kind returns the River job kind.
func (FeedbackEmbeddingArgs) Kind() string { return feedbackEmbeddingKind }

var _ river.JobArgs = FeedbackEmbeddingArgs{}
