package models

import (
	"time"

	"github.com/google/uuid"
)

// APIKey represents an API key stored in the database
type APIKey struct {
	ID         uuid.UUID  `json:"id"`
	KeyHash    string     `json:"key_hash"`
	Name       *string    `json:"name,omitempty"`
	IsActive   bool       `json:"is_active"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}
