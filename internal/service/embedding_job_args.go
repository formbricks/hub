package service

import (
	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/models"
)

const (
	feedbackEmbeddingKind = "feedback_embedding"
	// EmbeddingsQueueName is the River queue used for feedback embedding jobs.
	EmbeddingsQueueName = "embeddings"
)

// ValueTextHash values for jobs that do not come from a content event. Content events carry a hash
// of the input they saw; these name where the job came from instead.
const (
	// ValueTextHashTranslation marks the taxonomy re-embed a translation write enqueues.
	ValueTextHashTranslation = "translation"
	// ValueTextHashRepair marks a repair, whether the sweep or the operator backfill queued it. One
	// shared value makes their jobs one River unique key, so the two cannot queue the same repair
	// twice even when both select the record at the same moment (queue is not part of the key).
	ValueTextHashRepair = "repair"
)

// FeedbackEmbeddingArgs is the job payload for generating and storing an embedding for one feedback
// record. EmbeddingEnqueuer inserts it with the write that owes it, the repair sweep and the operator
// backfill insert it for repairs, and FeedbackEmbeddingWorker runs it.
//
// Live jobs carry no unique options: each content event is a real transition worth embedding.
// Repair jobs are unique on (FeedbackRecordID, Model, ValueTextHash) while in flight only, so a
// completed repair never swallows a later one.
type FeedbackEmbeddingArgs struct {
	FeedbackRecordID uuid.UUID `json:"feedback_record_id" river:"unique"`
	// EventID correlates this job to the domain event that enqueued it (uuid.Nil for backfill
	// jobs, which have no originating event). Not part of the dedupe key — observability only.
	EventID uuid.UUID `json:"event_id"`
	// Model is the embedding model name; stored in embeddings.model.
	Model string `json:"model" river:"unique"`
	// InputKind selects which feedback text is embedded. Empty is treated as raw for legacy queued jobs.
	InputKind models.EmbeddingInputKind `json:"input_kind,omitempty"`
	// ValueTextHash is a hash of the input a content event saw, or ValueTextHashTranslation /
	// ValueTextHashRepair for jobs with no content event. Part of the unique key.
	ValueTextHash string `json:"value_text_hash" river:"unique"`
}

// Kind returns the River job kind.
func (FeedbackEmbeddingArgs) Kind() string { return feedbackEmbeddingKind }

var _ river.JobArgs = FeedbackEmbeddingArgs{}
