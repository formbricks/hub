package models

import (
	"time"

	"github.com/google/uuid"
)

// Topic represents a single topic
// Level 1 topics are broad categories, Level 2 topics are specific subtopics
// Level 2 topics have an explicit parent_id linking to their Level 1 parent
type Topic struct {
	ID        uuid.UUID  `json:"id"`
	Title     string     `json:"title"`
	Level     int        `json:"level"`
	ParentID  *uuid.UUID `json:"parent_id,omitempty"`
	TenantID  *string    `json:"tenant_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// CreateTopicRequest represents the request to create a topic
type CreateTopicRequest struct {
	Title    string     `json:"title" validate:"required,no_null_bytes,min=1,max=255"`
	Level    int        `json:"level" validate:"required,min=1,max=2"`
	ParentID *uuid.UUID `json:"parent_id,omitempty"` // Required for Level 2 topics
	TenantID *string    `json:"tenant_id,omitempty" validate:"omitempty,no_null_bytes,max=255"`
}

// UpdateTopicRequest represents the request to update a topic
// Only title can be updated - parent_id is immutable
type UpdateTopicRequest struct {
	Title *string `json:"title,omitempty" validate:"omitempty,no_null_bytes,min=1,max=255"`
}

// ListTopicsFilters represents filters for listing topics
type ListTopicsFilters struct {
	Level    *int       `form:"level" validate:"omitempty,min=1"`
	ParentID *uuid.UUID `form:"parent_id" validate:"omitempty"`
	Title    *string    `form:"title" validate:"omitempty,no_null_bytes"`
	TenantID *string    `form:"tenant_id" validate:"omitempty,no_null_bytes"`
	Limit    int        `form:"limit" validate:"omitempty,min=1,max=1000"`
	Offset   int        `form:"offset" validate:"omitempty,min=0"`
}

// ListTopicsResponse represents the response for listing topics
type ListTopicsResponse struct {
	Data   []Topic `json:"data"`
	Total  int64   `json:"total"`
	Limit  int     `json:"limit"`
	Offset int     `json:"offset"`
}

// TopicMatch represents a topic matched by vector similarity search
type TopicMatch struct {
	TopicID    uuid.UUID `json:"topic_id"`
	Title      string    `json:"title"`
	Level      int       `json:"level"`
	Similarity float64   `json:"similarity"`
}

// SimilarTopic represents a Level 2 topic similar to a Level 1 topic
type SimilarTopic struct {
	ID         uuid.UUID `json:"id"`
	Title      string    `json:"title"`
	Similarity float64   `json:"similarity"`
}
