package service

import (
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

const (
	feedbackEmotionsKind = "feedback_emotions"
	// EmotionsQueueName is the River queue used for feedback emotion jobs. It is distinct from the
	// sentiment queue: River uniqueness is per-kind, so emotions and sentiment dedupe independently.
	EmotionsQueueName = "emotions"
)

// FeedbackEmotionsArgs is the job payload for classifying one feedback record's value_text into
// emotion labels. The river:"unique" tags define the dedupe key (FeedbackRecordID, ValueTextHash)
// but only take effect where the insert passes UniqueOpts — the backfill path, whose per-run
// ValueTextHash discriminator stops a rescued fan-out from double-enqueuing. The event-driven
// path deliberately inserts WITHOUT UniqueOpts (River's completed unique state would swallow
// legitimate re-enrichment after an edit). Like sentiment there is no target language — emotions
// are classified directly from the text, independent of language.
type FeedbackEmotionsArgs struct {
	FeedbackRecordID uuid.UUID `json:"feedback_record_id" river:"unique"`
	// ValueTextHash is a hash of the normalized value_text, or "empty" when value_text is blank.
	ValueTextHash string `json:"value_text_hash" river:"unique"`
}

// Kind returns the River job kind.
func (FeedbackEmotionsArgs) Kind() string { return feedbackEmotionsKind }

var _ river.JobArgs = FeedbackEmotionsArgs{}
