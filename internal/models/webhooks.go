package models

import (
	"time"

	"github.com/google/uuid"
)

// Webhook represents a webhook endpoint
type Webhook struct {
	ID         uuid.UUID `json:"id"`
	URL        string    `json:"url"`
	SigningKey string    `json:"signing_key"`
	Enabled    bool      `json:"enabled"`
	TenantID   *string   `json:"tenant_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// CreateWebhookRequest represents the request to create a webhook
type CreateWebhookRequest struct {
	URL        string  `json:"url" validate:"required,no_null_bytes,min=1,max=2048"`
	SigningKey string  `json:"signing_key" validate:"required,no_null_bytes,min=1,max=255"`
	Enabled    *bool   `json:"enabled,omitempty"`
	TenantID   *string `json:"tenant_id,omitempty" validate:"omitempty,no_null_bytes,max=255"`
}

// UpdateWebhookRequest represents the request to update a webhook
type UpdateWebhookRequest struct {
	URL        *string `json:"url,omitempty" validate:"omitempty,no_null_bytes,min=1,max=2048"`
	SigningKey *string `json:"signing_key,omitempty" validate:"omitempty,no_null_bytes,min=1,max=255"`
	Enabled    *bool   `json:"enabled,omitempty"`
}

// ListWebhooksFilters represents filters for listing webhooks
type ListWebhooksFilters struct {
	Enabled  *bool   `form:"enabled"`
	TenantID *string `form:"tenant_id" validate:"omitempty,no_null_bytes"`
	Limit    int     `form:"limit" validate:"omitempty,min=1,max=1000"`
	Offset   int     `form:"offset" validate:"omitempty,min=0"`
}

// ListWebhooksResponse represents the response for listing webhooks
type ListWebhooksResponse struct {
	Data   []Webhook `json:"data"`
	Total  int64     `json:"total"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}
