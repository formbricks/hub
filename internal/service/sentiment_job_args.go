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
// Uniqueness is by (FeedbackRecordID, ValueTextHash): a value_text edit yields a new job, while
// identical content within the window is deduped. Unlike translation there is no target
// language — sentiment is classified directly from the text, independent of language.
type FeedbackSentimentArgs struct {
	FeedbackRecordID uuid.UUID `json:"feedback_record_id" river:"unique"`
	// ValueTextHash is a hash of the normalized value_text, or "empty" when value_text is blank.
	ValueTextHash string `json:"value_text_hash" river:"unique"`
}

// Kind returns the River job kind.
func (FeedbackSentimentArgs) Kind() string { return feedbackSentimentKind }

var _ river.JobArgs = FeedbackSentimentArgs{}
