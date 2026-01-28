package models

import (
	"time"

	"github.com/google/uuid"
)

// Topic represents a single topic
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
// Note: Level is NOT included - it's calculated automatically from parent
type CreateTopicRequest struct {
	Title    string     `json:"title" validate:"required,no_null_bytes,min=1,max=255"`
	ParentID *uuid.UUID `json:"parent_id,omitempty"`
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
	ParentID *uuid.UUID `form:"parent_id"`
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
	TopicID    uuid.UUID  `json:"topic_id"`
	Title      string     `json:"title"`
	Level      int        `json:"level"`
	ParentID   *uuid.UUID `json:"parent_id,omitempty"`
	Similarity float64    `json:"similarity"`
}

// LevelThresholds maps topic hierarchy levels to minimum similarity thresholds.
// Higher levels (more specific topics) require higher similarity for matches.
// These values can be tuned based on production feedback.
var LevelThresholds = map[int]float64{
	1: 0.30, // Level 1: broadest topics (e.g., "Performance")
	2: 0.40, // Level 2: (e.g., "Performance > API")
	3: 0.50, // Level 3: (e.g., "Performance > API > Latency")
	4: 0.60, // Level 4: more specific
	5: 0.70, // Level 5: most specific
}

// DefaultThreshold is used for levels not in the map (fallback)
const DefaultThreshold = 0.50

// SimilarityThresholdForLevel returns the minimum similarity threshold for a given topic level.
func SimilarityThresholdForLevel(level int) float64 {
	if threshold, ok := LevelThresholds[level]; ok {
		return threshold
	}
	return DefaultThreshold
}
