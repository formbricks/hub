// Package models provides domain models that represent business entities independent of
// API versions or database representations. Models serve as a transformation layer between
// API DTOs and database entities.
package models

import (
	"time"

	"github.com/google/uuid"

	"github.com/formbricks/hub/apps/hub/internal/ent"
)

// FeedbackRecord represents a feedback record in the domain.
// This is the canonical representation of feedback data, independent
// of API versions or database representations.
type FeedbackRecord struct {
	ID             uuid.UUID              `json:"id"`
	CollectedAt    time.Time              `json:"collected_at"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
	TenantID       *string                `json:"tenant_id,omitempty"`
	ResponseID     *string                `json:"response_id,omitempty"`
	SourceType     string                 `json:"source_type"`
	SourceID       *string                `json:"source_id,omitempty"`
	SourceName     *string                `json:"source_name,omitempty"`
	FieldID        string                 `json:"field_id"`
	FieldLabel     *string                `json:"field_label,omitempty"`
	FieldType      string                 `json:"field_type"`
	ValueText      *string                `json:"value_text,omitempty"`
	ValueNumber    *float64               `json:"value_number,omitempty"`
	ValueBoolean   *bool                  `json:"value_boolean,omitempty"`
	ValueDate      *time.Time             `json:"value_date,omitempty"`
	ValueJSON      map[string]interface{} `json:"value_json,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	Language       *string                `json:"language,omitempty"`
	UserIdentifier *string                `json:"user_identifier,omitempty"`
	// AI Enrichment (optional)
	Sentiment      *string  `json:"sentiment,omitempty"`
	SentimentScore *float64 `json:"sentiment_score,omitempty"`
	Emotion        *string  `json:"emotion,omitempty"`
	Topics         []string `json:"topics,omitempty"`
}

// FromEnt converts an Ent entity to a domain model.
func FromEnt(e *ent.FeedbackRecord) *FeedbackRecord {
	return &FeedbackRecord{
		ID:             e.ID,
		CollectedAt:    e.CollectedAt,
		CreatedAt:      e.CreatedAt,
		UpdatedAt:      e.UpdatedAt,
		TenantID:       stringToPtr(e.TenantID),
		ResponseID:     stringToPtr(e.ResponseID),
		SourceType:     e.SourceType,
		SourceID:       stringToPtr(e.SourceID),
		SourceName:     stringToPtr(e.SourceName),
		FieldID:        e.FieldID,
		FieldLabel:     stringToPtr(e.FieldLabel),
		FieldType:      e.FieldType,
		ValueText:      e.ValueText,
		ValueNumber:    e.ValueNumber,
		ValueBoolean:   e.ValueBoolean,
		ValueDate:      e.ValueDate,
		ValueJSON:      e.ValueJSON,
		Metadata:       e.Metadata,
		Language:       stringToPtr(e.Language),
		UserIdentifier: stringToPtr(e.UserIdentifier),
		// Enrichment fields
		Sentiment:      e.Sentiment,
		SentimentScore: e.SentimentScore,
		Emotion:        e.Emotion,
		Topics:         e.Topics,
	}
}

// ToEnt converts the domain model to an Ent entity for persistence.
// Note: This is used for updates. For creates, use the Ent builder directly.
func (e *FeedbackRecord) ToEnt(entity *ent.FeedbackRecord) {
	entity.ID = e.ID
	entity.CollectedAt = e.CollectedAt
	entity.CreatedAt = e.CreatedAt
	entity.UpdatedAt = e.UpdatedAt
	entity.TenantID = ptrToString(e.TenantID)
	entity.ResponseID = ptrToString(e.ResponseID)
	entity.SourceType = e.SourceType
	entity.SourceID = ptrToString(e.SourceID)
	entity.SourceName = ptrToString(e.SourceName)
	entity.FieldID = e.FieldID
	entity.FieldLabel = ptrToString(e.FieldLabel)
	entity.FieldType = e.FieldType
	entity.ValueText = e.ValueText
	entity.ValueNumber = e.ValueNumber
	entity.ValueBoolean = e.ValueBoolean
	entity.ValueDate = e.ValueDate
	entity.ValueJSON = e.ValueJSON
	entity.Metadata = e.Metadata
	entity.Language = ptrToString(e.Language)
	entity.UserIdentifier = ptrToString(e.UserIdentifier)
}

// Helper functions for string pointer conversion

// stringToPtr converts a string to a pointer, returning nil if empty
func stringToPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ptrToString converts a string pointer to a string, returning empty if nil
func ptrToString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
