package service

import (
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

const (
	feedbackSentimentKind = "feedback_sentiment"
	// SentimentsQueueName is the River queue used for feedback sentiment jobs.
	SentimentsQueueName = "sentiments"
)

// FeedbackSentimentArgs is the job payload for classifying one feedback record's value_text.
// The river:"unique" tags define the dedupe key (FeedbackRecordID, ValueTextHash) but only take
// effect where the insert passes UniqueOpts — the backfill path, whose per-run ValueTextHash
// discriminator stops a rescued fan-out from double-enqueuing. The event-driven path deliberately
// inserts WITHOUT UniqueOpts (River's completed unique state would swallow legitimate
// re-enrichment after an edit). Unlike translation there is no target language — sentiment is
// classified directly from the text, independent of language.
type FeedbackSentimentArgs struct {
	FeedbackRecordID uuid.UUID `json:"feedback_record_id" river:"unique"`
	// ValueTextHash is a hash of the normalized value_text, or "empty" when value_text is blank.
	ValueTextHash string `json:"value_text_hash" river:"unique"`
}

// Kind returns the River job kind.
func (FeedbackSentimentArgs) Kind() string { return feedbackSentimentKind }

var _ river.JobArgs = FeedbackSentimentArgs{}
