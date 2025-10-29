package api

import (
	"time"

	"github.com/google/uuid"

	"github.com/formbricks/hub/apps/hub/internal/models"
)

// CreateExperienceInput represents the input for creating an experience
type CreateExperienceInput struct {
	Body struct {
		// Source tracking
		SourceType string  `json:"source_type" example:"survey" doc:"Type of feedback source (e.g., survey, review, feedback_form)" minLength:"1" maxLength:"255"`
		SourceID   *string `json:"source_id,omitempty" example:"survey-123" doc:"Reference to survey/form/ticket ID"`
		SourceName *string `json:"source_name,omitempty" example:"Q1 NPS Survey" doc:"Human-readable name"`

		// Question/Field identification
		FieldID    string  `json:"field_id" example:"q1" doc:"Identifier for the question/field" minLength:"1" maxLength:"255"`
		FieldLabel *string `json:"field_label,omitempty" example:"How satisfied are you?" doc:"The actual question text"`
		FieldType  string  `json:"field_type" example:"rating" doc:"Field type: text (enrichable), categorical, nps, csat, rating, number, boolean, date" enum:"text,categorical,nps,csat,rating,number,boolean,date" minLength:"1" maxLength:"255"`

		// Response values
		ValueText    *string                `json:"value_text,omitempty" example:"Great service!" doc:"For open-ended text responses"`
		ValueNumber  *float64               `json:"value_number,omitempty" example:"9" doc:"For ratings, NPS scores, numeric responses"`
		ValueBoolean *bool                  `json:"value_boolean,omitempty" example:"true" doc:"For yes/no questions"`
		ValueDate    *time.Time             `json:"value_date,omitempty" doc:"For date responses"`
		ValueJSON    map[string]interface{} `json:"value_json,omitempty" doc:"For complex responses like multiple choice arrays"`

		// Context & enrichment
		CollectedAt    *time.Time             `json:"collected_at,omitempty" doc:"When the feedback was collected (defaults to now)"`
		Metadata       map[string]interface{} `json:"metadata,omitempty" doc:"User agent, device, location, referrer, tags, etc."`
		Language       *string                `json:"language,omitempty" example:"en" doc:"ISO language code" maxLength:"10"`
		UserIdentifier *string                `json:"user_identifier,omitempty" example:"user-abc-123" doc:"Anonymous ID or email hash"`
	}
}

// UpdateExperienceInput represents the input for updating an experience
type UpdateExperienceInput struct {
	ID   string `path:"id" doc:"Experience ID (UUID)" format:"uuid"`
	Body struct {
		ValueText      *string                `json:"value_text,omitempty" doc:"Update text response"`
		ValueNumber    *float64               `json:"value_number,omitempty" doc:"Update numeric response"`
		ValueBoolean   *bool                  `json:"value_boolean,omitempty" doc:"Update boolean response"`
		ValueDate      *time.Time             `json:"value_date,omitempty" doc:"Update date response"`
		ValueJSON      map[string]interface{} `json:"value_json,omitempty" doc:"Update complex response"`
		Metadata       map[string]interface{} `json:"metadata,omitempty" doc:"Update metadata"`
		Language       *string                `json:"language,omitempty" doc:"Update language"`
		UserIdentifier *string                `json:"user_identifier,omitempty" doc:"Update user identifier"`
	}
}

// GetExperienceInput represents the input for getting a single experience
type GetExperienceInput struct {
	ID string `path:"id" doc:"Experience ID (UUID)" format:"uuid"`
}

// DeleteExperienceInput represents the input for deleting an experience
type DeleteExperienceInput struct {
	ID string `path:"id" doc:"Experience ID (UUID)" format:"uuid"`
}

// ListExperiencesInput represents the input for listing experiences
type ListExperiencesInput struct {
	SourceType     string `query:"source_type" doc:"Filter by source type"`
	SourceID       string `query:"source_id" doc:"Filter by source ID"`
	FieldType      string `query:"field_type" doc:"Filter by field type"`
	UserIdentifier string `query:"user_identifier" doc:"Filter by user identifier"`
	Since          string `query:"since" doc:"Filter by collected_at >= since (ISO 8601 format)"`
	Until          string `query:"until" doc:"Filter by collected_at <= until (ISO 8601 format)"`
	Limit          int    `query:"limit" default:"100" doc:"Number of results to return (max 1000)" minimum:"1" maximum:"1000"`
	Offset         int    `query:"offset" default:"0" doc:"Number of results to skip" minimum:"0"`
}

// ExperienceData represents an experience data record for API responses
type ExperienceData struct {
	ID             uuid.UUID              `json:"id" doc:"UUIDv7 primary key"`
	CollectedAt    time.Time              `json:"collected_at" doc:"When the feedback was collected"`
	CreatedAt      time.Time              `json:"created_at" doc:"When this record was created"`
	UpdatedAt      time.Time              `json:"updated_at" doc:"When this record was last updated"`
	SourceType     string                 `json:"source_type" doc:"Type of feedback source"`
	SourceID       *string                `json:"source_id,omitempty" doc:"Reference to survey/form/ticket ID"`
	SourceName     *string                `json:"source_name,omitempty" doc:"Human-readable name"`
	FieldID        string                 `json:"field_id" doc:"Identifier for the question/field"`
	FieldLabel     *string                `json:"field_label,omitempty" doc:"The actual question text"`
	FieldType      string                 `json:"field_type" doc:"Type of field"`
	ValueText      *string                `json:"value_text,omitempty" doc:"Text response"`
	ValueNumber    *float64               `json:"value_number,omitempty" doc:"Numeric response"`
	ValueBoolean   *bool                  `json:"value_boolean,omitempty" doc:"Boolean response"`
	ValueDate      *time.Time             `json:"value_date,omitempty" doc:"Date response"`
	ValueJSON      map[string]interface{} `json:"value_json,omitempty" doc:"Complex response"`
	Metadata       map[string]interface{} `json:"metadata,omitempty" doc:"Additional context"`
	Language       *string                `json:"language,omitempty" doc:"ISO language code"`
	UserIdentifier *string                `json:"user_identifier,omitempty" doc:"User identifier"`
	// AI Enrichment (optional)
	Sentiment      *string  `json:"sentiment,omitempty" doc:"AI-detected sentiment: positive, negative, neutral"`
	SentimentScore *float64 `json:"sentiment_score,omitempty" doc:"Sentiment intensity from -1 (negative) to +1 (positive)"`
	Emotion        *string  `json:"emotion,omitempty" doc:"AI-detected emotion: joy, anger, frustration, sadness, neutral"`
	Topics         []string `json:"topics,omitempty" doc:"Key topics extracted by AI"`
}

// ExperienceOutput represents the output for a single experience
type ExperienceOutput struct {
	Body ExperienceData
}

// ListExperiencesOutput represents the output for listing experiences
type ListExperiencesOutput struct {
	Body struct {
		Data   []ExperienceData `json:"data" doc:"List of experiences"`
		Total  int              `json:"total" doc:"Total count of experiences matching filters"`
		Limit  int              `json:"limit" doc:"Limit used in query"`
		Offset int              `json:"offset" doc:"Offset used in query"`
	}
}

// FromModel converts a domain model to API response type
func (e *ExperienceData) FromModel(m *models.Experience) {
	e.ID = m.ID
	e.CollectedAt = m.CollectedAt
	e.CreatedAt = m.CreatedAt
	e.UpdatedAt = m.UpdatedAt
	e.SourceType = m.SourceType
	e.SourceID = m.SourceID
	e.SourceName = m.SourceName
	e.FieldID = m.FieldID
	e.FieldLabel = m.FieldLabel
	e.FieldType = m.FieldType
	e.ValueText = m.ValueText
	e.ValueNumber = m.ValueNumber
	e.ValueBoolean = m.ValueBoolean
	e.ValueDate = m.ValueDate
	e.ValueJSON = m.ValueJSON
	e.Metadata = m.Metadata
	e.Language = m.Language
	e.UserIdentifier = m.UserIdentifier
	// Enrichment fields
	e.Sentiment = m.Sentiment
	e.SentimentScore = m.SentimentScore
	e.Emotion = m.Emotion
	e.Topics = m.Topics
}
