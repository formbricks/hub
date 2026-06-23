package service

import (
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

const (
	feedbackTranslationKind = "feedback_translation"
	// TranslationsQueueName is the River queue used for feedback translation jobs.
	TranslationsQueueName = "translations"
)

// FeedbackTranslationArgs is the job payload for translating one feedback record's
// value_text into the tenant's target language. Uniqueness is by (FeedbackRecordID,
// TargetLang, ValueTextHash): a value_text edit or a target-language change yields a
// new job, while identical content for the same target within the window is deduped.
type FeedbackTranslationArgs struct {
	FeedbackRecordID uuid.UUID `json:"feedback_record_id" river:"unique"`
	// TargetLang is the tenant's configured target language (BCP-47) at enqueue time.
	TargetLang string `json:"target_lang" river:"unique"`
	// ValueTextHash is a hash of the trimmed, NFC-normalized value_text (or "empty"/"backfill").
	ValueTextHash string `json:"value_text_hash" river:"unique"`
}

// Kind returns the River job kind.
func (FeedbackTranslationArgs) Kind() string { return feedbackTranslationKind }

var _ river.JobArgs = FeedbackTranslationArgs{}
