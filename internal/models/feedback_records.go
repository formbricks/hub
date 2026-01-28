package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// FeedbackRecord represents a single feedback record
type FeedbackRecord struct {
	ID             uuid.UUID       `json:"id"`
	CollectedAt    time.Time       `json:"collected_at"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	SourceType     string          `json:"source_type"`
	SourceID       *string         `json:"source_id,omitempty"`
	SourceName     *string         `json:"source_name,omitempty"`
	FieldID        string          `json:"field_id"`
	FieldLabel     *string         `json:"field_label,omitempty"`
	FieldType      string          `json:"field_type"`
	ValueText      *string         `json:"value_text,omitempty"`
	ValueNumber    *float64        `json:"value_number,omitempty"`
	ValueBoolean   *bool           `json:"value_boolean,omitempty"`
	ValueDate      *time.Time      `json:"value_date,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	Language       *string         `json:"language,omitempty"`
	UserIdentifier *string         `json:"user_identifier,omitempty"`
	TenantID       *string         `json:"tenant_id,omitempty"`
	ResponseID     *string         `json:"response_id,omitempty"`

	// Similarity is populated at query time when filtering by topic_id
	// It represents the cosine similarity between this feedback's embedding and the topic's embedding
	Similarity *float64 `json:"similarity,omitempty"`
}

// CreateFeedbackRecordRequest represents the request to create a feedback record
type CreateFeedbackRecordRequest struct {
	CollectedAt    *time.Time      `json:"collected_at,omitempty"`
	SourceType     string          `json:"source_type" validate:"required,no_null_bytes,min=1,max=255"`
	SourceID       *string         `json:"source_id,omitempty" validate:"omitempty,no_null_bytes"`
	SourceName     *string         `json:"source_name,omitempty"`
	FieldID        string          `json:"field_id" validate:"required,no_null_bytes,min=1,max=255"`
	FieldLabel     *string         `json:"field_label,omitempty"`
	FieldType      string          `json:"field_type" validate:"required,field_type,min=1,max=255"`
	ValueText      *string         `json:"value_text,omitempty" validate:"omitempty,no_null_bytes"`
	ValueNumber    *float64        `json:"value_number,omitempty"`
	ValueBoolean   *bool           `json:"value_boolean,omitempty"`
	ValueDate      *time.Time      `json:"value_date,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	Language       *string         `json:"language,omitempty" validate:"omitempty,no_null_bytes,max=10"`
	UserIdentifier *string         `json:"user_identifier,omitempty"`
	TenantID       *string         `json:"tenant_id,omitempty" validate:"omitempty,no_null_bytes,max=255"`
	ResponseID     *string         `json:"response_id,omitempty" validate:"omitempty,max=255"`
}

// UpdateFeedbackRecordRequest represents the request to update a feedback record
// Only value fields, metadata, language, and user_identifier can be updated
type UpdateFeedbackRecordRequest struct {
	ValueText      *string         `json:"value_text,omitempty" validate:"omitempty,no_null_bytes"`
	ValueNumber    *float64        `json:"value_number,omitempty"`
	ValueBoolean   *bool           `json:"value_boolean,omitempty"`
	ValueDate      *time.Time      `json:"value_date,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	Language       *string         `json:"language,omitempty" validate:"omitempty,no_null_bytes,max=10"`
	UserIdentifier *string         `json:"user_identifier,omitempty"`
}

// ListFeedbackRecordsFilters represents filters for listing feedback records
type ListFeedbackRecordsFilters struct {
	TenantID       *string    `form:"tenant_id" validate:"omitempty,no_null_bytes"`
	ResponseID     *string    `form:"response_id" validate:"omitempty,no_null_bytes"`
	SourceType     *string    `form:"source_type" validate:"omitempty,no_null_bytes"`
	SourceID       *string    `form:"source_id" validate:"omitempty,no_null_bytes"`
	FieldID        *string    `form:"field_id" validate:"omitempty,no_null_bytes"`
	FieldType      *string    `form:"field_type" validate:"omitempty,no_null_bytes"`
	UserIdentifier *string    `form:"user_identifier" validate:"omitempty,no_null_bytes"`
	Since          *time.Time `form:"since" validate:"omitempty"`
	Until          *time.Time `form:"until" validate:"omitempty"`
	Limit          int        `form:"limit" validate:"omitempty,min=1,max=1000"`
	Offset         int        `form:"offset" validate:"omitempty,min=0"`

	// TopicID filters feedback records by topic assignment
	// By default, uses direct topic_id lookup (fast, pre-computed)
	// Set UseSimilarity=true to use vector similarity search instead
	TopicID *uuid.UUID `form:"topic_id" validate:"omitempty"`

	// UseSimilarity when true, uses vector similarity search instead of direct topic_id lookup
	// This is slower but can find matches for unclassified feedback
	UseSimilarity bool `form:"use_similarity"`

	// MinSimilarity overrides the default threshold when using similarity search
	// Value between 0 and 1 (e.g., 0.5 = 50% similarity minimum)
	// Only used when UseSimilarity=true. If not set, uses automatic thresholds based on topic level
	MinSimilarity *float64 `form:"min_similarity" validate:"omitempty,min=0,max=1"`
}

// ListFeedbackRecordsResponse represents the response for listing feedback records
type ListFeedbackRecordsResponse struct {
	Data   []FeedbackRecord `json:"data"`
	Total  int64            `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

// BulkDeleteFilters represents query parameters for bulk delete operation
type BulkDeleteFilters struct {
	UserIdentifier string  `form:"user_identifier" validate:"required,no_null_bytes,min=1"`
	TenantID       *string `form:"tenant_id" validate:"omitempty,no_null_bytes"`
}

// BulkDeleteResponse represents the response for bulk delete operation
type BulkDeleteResponse struct {
	DeletedCount int64  `json:"deleted_count"`
	Message      string `json:"message"`
}

// UpdateFeedbackEnrichmentRequest represents internal request to update AI-enriched fields
// Used by the service layer, not exposed via API
type UpdateFeedbackEnrichmentRequest struct {
	Embedding []float32
}
