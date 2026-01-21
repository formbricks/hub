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
} //@name FeedbackRecord

// CreateFeedbackRecordRequest represents the request to create a feedback record
type CreateFeedbackRecordRequest struct {
	CollectedAt    *time.Time      `json:"collected_at,omitempty"`
	SourceType     string          `json:"source_type" validate:"required"`
	SourceID       *string         `json:"source_id,omitempty"`
	SourceName     *string         `json:"source_name,omitempty"`
	FieldID        string          `json:"field_id" validate:"required"`
	FieldLabel     *string         `json:"field_label,omitempty"`
	FieldType      string          `json:"field_type" validate:"required,field_type"`
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
} //@name CreateFeedbackRecordRequest

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
} //@name UpdateFeedbackRecordRequest

// ListFeedbackRecordsFilters represents filters for listing feedback records
type ListFeedbackRecordsFilters struct {
	TenantID       *string    `form:"tenant_id"`
	ResponseID     *string    `form:"response_id"`
	SourceType     *string    `form:"source_type"`
	SourceID       *string    `form:"source_id"`
	FieldID        *string    `form:"field_id"`
	FieldType      *string    `form:"field_type"`
	UserIdentifier *string    `form:"user_identifier"`
	Since          *time.Time `form:"since" validate:"omitempty"`
	Until          *time.Time `form:"until" validate:"omitempty"`
	Limit          int        `form:"limit" validate:"omitempty,min=1,max=1000"`
	Offset         int        `form:"offset" validate:"omitempty,min=0"`
}

// ListFeedbackRecordsResponse represents the response for listing feedback records
type ListFeedbackRecordsResponse struct {
	Data   []FeedbackRecord `json:"data"`
	Total  int64            `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
} //@name ListFeedbackRecordsResponse

// SearchFeedbackRecordsRequest represents search parameters for feedback records
type SearchFeedbackRecordsRequest struct {
	Query          *string    `form:"query" validate:"required"`        // Full-text search query (required)
	SourceType     *string    `form:"source_type"`                      // Filter by source type
	SourceID       *string    `form:"source_id"`                        // Filter by source ID
	FieldType      *string    `form:"field_type"`                       // Filter by field type
	UserIdentifier *string    `form:"user_identifier"`                  // Filter by user identifier
	Since          *time.Time `form:"since" validate:"omitempty"`       // Filter by collected_at >= since (ISO 8601)
	Until          *time.Time `form:"until" validate:"omitempty"`       // Filter by collected_at <= until (ISO 8601)
	Limit          int        `form:"limit" validate:"omitempty,min=1"` // Maximum number of results (default 10, max 100, capped in service)
}

// SearchResultItem represents a single search result with similarity score
type SearchResultItem struct {
	FeedbackRecord
	SimilarityScore float64 `json:"similarity_score"`
} //@name SearchResultItem

// SearchFeedbackRecordsResponse represents search results
type SearchFeedbackRecordsResponse struct {
	Results []SearchResultItem `json:"results"`
	Query   string             `json:"query"`
	Count   int64              `json:"count"`
} //@name SearchFeedbackRecordsResponse

// BulkDeleteResponse represents the response for bulk delete operation
type BulkDeleteResponse struct {
	DeletedCount int64  `json:"deleted_count"`
	Message      string `json:"message"`
} //@name BulkDeleteResponse
