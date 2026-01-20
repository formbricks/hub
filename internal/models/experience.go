package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ExperienceData represents a single experience data record
type ExperienceData struct {
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
}

// CreateExperienceRequest represents the request to create experience data
type CreateExperienceRequest struct {
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
}

// UpdateExperienceRequest represents the request to update experience data
type UpdateExperienceRequest struct {
	SourceType     *string         `json:"source_type,omitempty"`
	SourceID       *string         `json:"source_id,omitempty"`
	SourceName     *string         `json:"source_name,omitempty"`
	FieldID        *string         `json:"field_id,omitempty"`
	FieldLabel     *string         `json:"field_label,omitempty"`
	FieldType      *string         `json:"field_type,omitempty"`
	ValueText      *string         `json:"value_text,omitempty"`
	ValueNumber    *float64        `json:"value_number,omitempty"`
	ValueBoolean   *bool           `json:"value_boolean,omitempty"`
	ValueDate      *time.Time      `json:"value_date,omitempty"`
	ValueJSON      json.RawMessage `json:"value_json,omitempty" swaggertype:"object"`
	Metadata       json.RawMessage `json:"metadata,omitempty" swaggertype:"object"`
	Language       *string         `json:"language,omitempty"`
	UserIdentifier *string         `json:"user_identifier,omitempty"`
}

// ListExperiencesFilters represents filters for listing experiences
type ListExperiencesFilters struct {
	SourceType     *string
	SourceID       *string
	FieldID        *string
	UserIdentifier *string
	Limit          int
	Offset         int
}

// SearchExperiencesRequest represents search parameters for experiences
type SearchExperiencesRequest struct {
	Query          *string    `json:"query,omitempty"`           // Full-text search query
	SourceType     *string    `json:"source_type,omitempty"`     // Filter by source type
	SourceID       *string    `json:"source_id,omitempty"`       // Filter by source ID
	FieldID        *string    `json:"field_id,omitempty"`        // Filter by field ID
	FieldType      *string    `json:"field_type,omitempty"`      // Filter by field type
	UserIdentifier *string    `json:"user_identifier,omitempty"` // Filter by user identifier
	StartDate      *time.Time `json:"start_date,omitempty"`      // Filter by collected_at >= start_date
	EndDate        *time.Time `json:"end_date,omitempty"`        // Filter by collected_at <= end_date
	PageSize       int        `json:"page_size,omitempty"`       // Number of results per page (default 20, max 40)
	Page           int        `json:"page,omitempty"`            // Page number (starts at 0)
}

// SearchExperiencesResponse represents paginated search results
type SearchExperiencesResponse struct {
	Data       []ExperienceData `json:"data"`
	Page       int              `json:"page"`
	PageSize   int              `json:"page_size"`
	TotalCount int              `json:"total_count"`
	TotalPages int              `json:"total_pages"`
}
