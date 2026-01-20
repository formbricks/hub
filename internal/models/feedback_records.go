package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ValidFieldTypes is a map of valid field types
var ValidFieldTypes = map[string]struct{}{
	"text":        {},
	"categorical": {},
	"nps":         {},
	"csat":        {},
	"ces":         {},
	"rating":      {},
	"number":      {},
	"boolean":     {},
	"date":        {},
}

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
	ValueJSON      json.RawMessage `json:"value_json,omitempty" swaggertype:"object"`
	Metadata       json.RawMessage `json:"metadata,omitempty" swaggertype:"object"`
	Language       *string         `json:"language,omitempty"`
	UserIdentifier *string         `json:"user_identifier,omitempty"`
	TenantID       *string         `json:"tenant_id,omitempty"`
	ResponseID     *string         `json:"response_id,omitempty"`
}

// CreateFeedbackRecordRequest represents the request to create a feedback record
type CreateFeedbackRecordRequest struct {
	CollectedAt    *time.Time      `json:"collected_at,omitempty"`
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
	ValueJSON      json.RawMessage `json:"value_json,omitempty" swaggertype:"object"`
	Metadata       json.RawMessage `json:"metadata,omitempty" swaggertype:"object"`
	Language       *string         `json:"language,omitempty"`
	UserIdentifier *string         `json:"user_identifier,omitempty"`
	TenantID       *string         `json:"tenant_id,omitempty"`
	ResponseID     *string         `json:"response_id,omitempty"`
}

// UpdateFeedbackRecordRequest represents the request to update a feedback record
// Only value fields, metadata, language, and user_identifier can be updated
type UpdateFeedbackRecordRequest struct {
	ValueText      *string         `json:"value_text,omitempty"`
	ValueNumber    *float64        `json:"value_number,omitempty"`
	ValueBoolean   *bool           `json:"value_boolean,omitempty"`
	ValueDate      *time.Time      `json:"value_date,omitempty"`
	ValueJSON      json.RawMessage `json:"value_json,omitempty" swaggertype:"object"`
	Metadata       json.RawMessage `json:"metadata,omitempty" swaggertype:"object"`
	Language       *string         `json:"language,omitempty"`
	UserIdentifier *string         `json:"user_identifier,omitempty"`
}

// ListFeedbackRecordsFilters represents filters for listing feedback records
type ListFeedbackRecordsFilters struct {
	TenantID       *string
	ResponseID     *string
	SourceType     *string
	SourceID       *string
	FieldID        *string
	FieldType      *string
	UserIdentifier *string
	Since          *time.Time
	Until          *time.Time
	Limit          int
	Offset         int
}

// ListFeedbackRecordsResponse represents the response for listing feedback records
type ListFeedbackRecordsResponse struct {
	Data   []FeedbackRecord `json:"data"`
	Total  int64            `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

// SearchFeedbackRecordsRequest represents search parameters for feedback records
type SearchFeedbackRecordsRequest struct {
	Query      *string    `json:"query,omitempty"`       // Full-text search query (required)
	SourceType *string    `json:"source_type,omitempty"` // Filter by source type
	Since      *time.Time `json:"since,omitempty"`       // Filter by collected_at >= since (ISO 8601)
	Until      *time.Time `json:"until,omitempty"`       // Filter by collected_at <= until (ISO 8601)
	Limit      int        `json:"limit,omitempty"`       // Maximum number of results (default 10, max 100)
}

// SearchResultItem represents a single search result with similarity score
type SearchResultItem struct {
	FeedbackRecord
	SimilarityScore float64 `json:"similarity_score"`
}

// SearchFeedbackRecordsResponse represents search results
type SearchFeedbackRecordsResponse struct {
	Results []SearchResultItem `json:"results"`
	Query   string             `json:"query"`
	Count   int64              `json:"count"`
}

// BulkDeleteResponse represents the response for bulk delete operation
type BulkDeleteResponse struct {
	DeletedCount int64  `json:"deleted_count"`
	Message      string `json:"message"`
}
