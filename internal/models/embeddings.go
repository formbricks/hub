package models

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// EmbeddingVectorDimensions is the fixed size for all embedding vectors (DB column, index, and provider APIs).
const EmbeddingVectorDimensions = 768

// Embedding represents one embedding row: one vector per feedback record per model.
type Embedding struct {
	ID               uuid.UUID `json:"id"`
	FeedbackRecordID uuid.UUID `json:"feedback_record_id"`
	Embedding        []float32 `json:"embedding"`
	Model            string    `json:"model"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// EmbeddingInputKind identifies which record text was embedded.
type EmbeddingInputKind string

const (
	// EmbeddingInputKindRaw embeds the original feedback value_text.
	EmbeddingInputKindRaw EmbeddingInputKind = "raw"
	// EmbeddingInputKindTaxonomyTranslated embeds translated text when present, falling back to value_text.
	EmbeddingInputKindTaxonomyTranslated EmbeddingInputKind = "taxonomy_translated"
)

// NormalizeEmbeddingInputKind maps empty/unknown job values to raw for backward-compatible queued jobs.
func NormalizeEmbeddingInputKind(kind EmbeddingInputKind) EmbeddingInputKind {
	switch EmbeddingInputKind(strings.TrimSpace(string(kind))) {
	case EmbeddingInputKindRaw:
		return EmbeddingInputKindRaw
	case EmbeddingInputKindTaxonomyTranslated:
		return EmbeddingInputKindTaxonomyTranslated
	default:
		return EmbeddingInputKindRaw
	}
}

// FeedbackRecordWithScore is a feedback record ID, similarity score, and the record's field_label and value_text for display.
// Embeddings exist only for text, so ValueText is always set for any search result.
type FeedbackRecordWithScore struct {
	FeedbackRecordID uuid.UUID `json:"feedback_record_id"`
	Score            float64   `json:"score"`
	FieldLabel       string    `json:"field_label"` // label of the field (included in embedding for context)
	ValueText        string    `json:"value_text"`  // text that was embedded (with field_label)
	// Distance is the raw cosine distance the row was ordered by, carried for the keyset cursor.
	// Score is derived (1 - distance) for display; re-deriving distance from the score loses a
	// ulp and would duplicate or skip boundary rows across pages. Internal only, not in the API.
	Distance float64 `json:"-"`
}
