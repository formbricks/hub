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
	ValueJSON      json.RawMessage `json:"value_json,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	Language       *string         `json:"language,omitempty"`
	UserIdentifier *string         `json:"user_identifier,omitempty"`
	TenantID       *string         `json:"tenant_id,omitempty"`
	ResponseID     *string         `json:"response_id,omitempty"`
	// AI-enriched fields
	Emotion        *string  `json:"emotion,omitempty"`
	Sentiment      *string  `json:"sentiment,omitempty"`
	SentimentScore *float64 `json:"sentiment_score,omitempty"`
	Topics         []string `json:"topics,omitempty"`
}

// CreateFeedbackRecordRequest represents the request to create a feedback record
type CreateFeedbackRecordRequest struct {
	CollectedAt    *time.Time      `json:"collected_at,omitempty" validate:"omitempty,date_range"`
	SourceType     string          `json:"source_type" validate:"required,min=1,max=255,no_null_bytes"`
	SourceID       *string         `json:"source_id,omitempty" validate:"omitempty,no_null_bytes"`
	SourceName     *string         `json:"source_name,omitempty" validate:"omitempty,no_null_bytes"`
	FieldID        string          `json:"field_id" validate:"required,min=1,max=255,no_null_bytes"`
	FieldLabel     *string         `json:"field_label,omitempty" validate:"omitempty,no_null_bytes"`
	FieldType      string          `json:"field_type" validate:"required,field_type,min=1,max=255,no_null_bytes"`
	ValueText      *string         `json:"value_text,omitempty" validate:"omitempty,no_null_bytes"`
	ValueNumber    *float64        `json:"value_number,omitempty" validate:"omitempty,numeric_range"`
	ValueBoolean   *bool           `json:"value_boolean,omitempty"`
	ValueDate      *time.Time      `json:"value_date,omitempty" validate:"omitempty,date_range"`
	ValueJSON      json.RawMessage `json:"value_json,omitempty" validate:"omitempty,json_object,json_no_null_bytes"`
	Metadata       json.RawMessage `json:"metadata,omitempty" validate:"omitempty,json_object,json_no_null_bytes"`
	Language       *string         `json:"language,omitempty" validate:"omitempty,max=10,no_null_bytes"`
	UserIdentifier *string         `json:"user_identifier,omitempty" validate:"omitempty,no_null_bytes"`
	TenantID       *string         `json:"tenant_id,omitempty" validate:"omitempty,max=255,no_null_bytes"`
	ResponseID     *string         `json:"response_id,omitempty" validate:"omitempty,max=255,no_null_bytes"`
}

// UpdateFeedbackRecordRequest represents the request to update a feedback record
// Only value fields, metadata, language, and user_identifier can be updated
type UpdateFeedbackRecordRequest struct {
	ValueText      *string         `json:"value_text,omitempty" validate:"omitempty,no_null_bytes"`
	ValueNumber    *float64        `json:"value_number,omitempty" validate:"omitempty,numeric_range"`
	ValueBoolean   *bool           `json:"value_boolean,omitempty"`
	ValueDate      *time.Time      `json:"value_date,omitempty" validate:"omitempty,date_range"`
	ValueJSON      json.RawMessage `json:"value_json,omitempty" validate:"omitempty,json_object,json_no_null_bytes"`
	Metadata       json.RawMessage `json:"metadata,omitempty" validate:"omitempty,json_object,json_no_null_bytes"`
	Language       *string         `json:"language,omitempty" validate:"omitempty,max=10,no_null_bytes"`
	UserIdentifier *string         `json:"user_identifier,omitempty" validate:"omitempty,no_null_bytes"`
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
	Since          *time.Time `form:"since" validate:"omitempty,date_range"`
	Until          *time.Time `form:"until" validate:"omitempty,date_range"`
	Limit          int        `form:"limit" validate:"omitempty,min=1,max=1000"`
	Offset         int        `form:"offset" validate:"omitempty,min=0,max=2147483647"`
}

// ListFeedbackRecordsResponse represents the response for listing feedback records
type ListFeedbackRecordsResponse struct {
	Data   []FeedbackRecord `json:"data"`
	Total  int64            `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

// BulkDeleteResponse represents the response for bulk delete operation
type BulkDeleteResponse struct {
	DeletedCount int64  `json:"deleted_count"`
	Message      string `json:"message"`
}
