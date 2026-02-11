// Package models defines request/response and domain types for feedback records.
package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidFieldType is returned when a field type string is invalid (err113).
var ErrInvalidFieldType = errors.New("invalid field type")

// FieldType represents the type of feedback field.
type FieldType string

// Valid FieldType constants for feedback fields (NPS, CSAT, CES, rating, etc.).
const (
	FieldTypeText        FieldType = "text"
	FieldTypeCategorical FieldType = "categorical"
	FieldTypeNPS         FieldType = "nps"
	FieldTypeCSAT        FieldType = "csat"
	FieldTypeCES         FieldType = "ces"
	FieldTypeRating      FieldType = "rating"
	FieldTypeNumber      FieldType = "number"
	FieldTypeBoolean     FieldType = "boolean"
	FieldTypeDate        FieldType = "date"
)

// ValidFieldTypes contains all valid field type values (set membership).
var ValidFieldTypes = map[FieldType]struct{}{
	FieldTypeText:        {},
	FieldTypeCategorical: {},
	FieldTypeNPS:         {},
	FieldTypeCSAT:        {},
	FieldTypeCES:         {},
	FieldTypeRating:      {},
	FieldTypeNumber:      {},
	FieldTypeBoolean:     {},
	FieldTypeDate:        {},
}

// IsValid returns true if the FieldType is valid.
func (ft *FieldType) IsValid() bool {
	if ft == nil {
		return false
	}

	_, valid := ValidFieldTypes[*ft]

	return valid
}

// ParseFieldType parses a string to FieldType, returns error if invalid.
func ParseFieldType(s string) (FieldType, error) {
	ft := FieldType(s)
	if _, valid := ValidFieldTypes[ft]; !valid {
		return "", fmt.Errorf("%w: %s", ErrInvalidFieldType, s)
	}

	return ft, nil
}

// UnmarshalJSON implements json.Unmarshaler to validate field type during JSON unmarshaling.
func (ft *FieldType) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("unmarshal field type: %w", err)
	}

	parsed, err := ParseFieldType(s)
	if err != nil {
		return err
	}

	*ft = parsed

	return nil
}

// FeedbackRecord represents a single feedback record.
type FeedbackRecord struct {
	ID              uuid.UUID       `json:"id"`
	CollectedAt     time.Time       `json:"collected_at"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	SourceType      string          `json:"source_type"`
	SourceID        *string         `json:"source_id,omitempty"`
	SourceName      *string         `json:"source_name,omitempty"`
	FieldID         string          `json:"field_id"`
	FieldLabel      *string         `json:"field_label,omitempty"`
	FieldType       FieldType       `json:"field_type"`
	FieldGroupID    *string         `json:"field_group_id,omitempty"`
	FieldGroupLabel *string         `json:"field_group_label,omitempty"`
	ValueText       *string         `json:"value_text,omitempty"`
	ValueNumber     *float64        `json:"value_number,omitempty"`
	ValueBoolean    *bool           `json:"value_boolean,omitempty"`
	ValueDate       *time.Time      `json:"value_date,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	Language        *string         `json:"language,omitempty"`
	UserIdentifier  *string         `json:"user_identifier,omitempty"`
	TenantID        *string         `json:"tenant_id,omitempty"`
}

// CreateFeedbackRecordRequest represents the request to create a feedback record.
type CreateFeedbackRecordRequest struct {
	CollectedAt     *time.Time      `json:"collected_at,omitempty"`
	SourceType      string          `json:"source_type"                 validate:"required,no_null_bytes,min=1,max=255"`
	SourceID        *string         `json:"source_id,omitempty"         validate:"omitempty,no_null_bytes"`
	SourceName      *string         `json:"source_name,omitempty"`
	FieldID         string          `json:"field_id"                    validate:"required,no_null_bytes,min=1,max=255"`
	FieldLabel      *string         `json:"field_label,omitempty"`
	FieldType       FieldType       `json:"field_type"                  validate:"required,field_type"`
	FieldGroupID    *string         `json:"field_group_id,omitempty"    validate:"omitempty,no_null_bytes,max=255"`
	FieldGroupLabel *string         `json:"field_group_label,omitempty"`
	ValueText       *string         `json:"value_text,omitempty"        validate:"omitempty,no_null_bytes"`
	ValueNumber     *float64        `json:"value_number,omitempty"`
	ValueBoolean    *bool           `json:"value_boolean,omitempty"`
	ValueDate       *time.Time      `json:"value_date,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	Language        *string         `json:"language,omitempty"          validate:"omitempty,no_null_bytes,max=10"`
	UserIdentifier  *string         `json:"user_identifier,omitempty"`
	TenantID        *string         `json:"tenant_id,omitempty"         validate:"omitempty,no_null_bytes,max=255"`
}

// UpdateFeedbackRecordRequest represents the request to update a feedback record
// Only value fields, metadata, language, and user_identifier can be updated.
type UpdateFeedbackRecordRequest struct {
	ValueText      *string         `json:"value_text,omitempty"      validate:"omitempty,no_null_bytes"`
	ValueNumber    *float64        `json:"value_number,omitempty"`
	ValueBoolean   *bool           `json:"value_boolean,omitempty"`
	ValueDate      *time.Time      `json:"value_date,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	Language       *string         `json:"language,omitempty"        validate:"omitempty,no_null_bytes,max=10"`
	UserIdentifier *string         `json:"user_identifier,omitempty"`
}

// ChangedFields returns the names of fields that are set (non-nil) in the update request.
func (r *UpdateFeedbackRecordRequest) ChangedFields() []string {
	var fields []string
	if r.ValueText != nil {
		fields = append(fields, "value_text")
	}

	if r.ValueNumber != nil {
		fields = append(fields, "value_number")
	}

	if r.ValueBoolean != nil {
		fields = append(fields, "value_boolean")
	}

	if r.ValueDate != nil {
		fields = append(fields, "value_date")
	}

	if r.Metadata != nil {
		fields = append(fields, "metadata")
	}

	if r.Language != nil {
		fields = append(fields, "language")
	}

	if r.UserIdentifier != nil {
		fields = append(fields, "user_identifier")
	}

	return fields
}

// ListFeedbackRecordsFilters represents filters for listing feedback records.
type ListFeedbackRecordsFilters struct {
	TenantID       *string    `form:"tenant_id"       validate:"omitempty,no_null_bytes"`
	SourceType     *string    `form:"source_type"     validate:"omitempty,no_null_bytes"`
	SourceID       *string    `form:"source_id"       validate:"omitempty,no_null_bytes"`
	FieldID        *string    `form:"field_id"        validate:"omitempty,no_null_bytes"`
	FieldGroupID   *string    `form:"field_group_id"  validate:"omitempty,no_null_bytes"`
	FieldType      *FieldType `form:"field_type"      validate:"omitempty,field_type"`
	UserIdentifier *string    `form:"user_identifier" validate:"omitempty,no_null_bytes"`
	Since          *time.Time `form:"since"           validate:"omitempty"`
	Until          *time.Time `form:"until"           validate:"omitempty"`
	Limit          int        `form:"limit"           validate:"omitempty,min=1,max=1000"`
	Offset         int        `form:"offset"          validate:"omitempty,min=0"`
}

// ListFeedbackRecordsResponse represents the response for listing feedback records.
type ListFeedbackRecordsResponse struct {
	Data   []FeedbackRecord `json:"data"`
	Total  int64            `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

// BulkDeleteFilters represents query parameters for bulk delete operation.
type BulkDeleteFilters struct {
	UserIdentifier string  `form:"user_identifier" validate:"required,no_null_bytes,min=1"`
	TenantID       *string `form:"tenant_id"       validate:"omitempty,no_null_bytes"`
}

// BulkDeleteResponse represents the response for bulk delete operation.
type BulkDeleteResponse struct {
	DeletedCount int64  `json:"deleted_count"`
	Message      string `json:"message"`
}
