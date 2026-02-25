package models

import (
	"time"

	"github.com/google/uuid"
)

// Embedding represents one embedding row: one vector per feedback record per model.
type Embedding struct {
	ID               uuid.UUID `json:"id"`
	FeedbackRecordID uuid.UUID `json:"feedback_record_id"`
	Embedding        []float32 `json:"embedding"`
	Model            string    `json:"model"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// FeedbackRecordWithScore is a feedback record ID, similarity score, and the record's value_text for display.
// Embeddings exist only for text, so ValueText is always set for any search result.
type FeedbackRecordWithScore struct {
	FeedbackRecordID uuid.UUID `json:"feedback_record_id"`
	Score            float64   `json:"score"`
	ValueText        string    `json:"value_text"` // text that was embedded
}
