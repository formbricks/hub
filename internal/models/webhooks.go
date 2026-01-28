package models

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/google/uuid"
)

// Webhook represents a webhook endpoint
type Webhook struct {
	ID         uuid.UUID             `json:"id"`
	URL        string                `json:"url"`
	SigningKey string                `json:"signing_key"`
	Enabled    bool                  `json:"enabled"`
	TenantID   *string               `json:"tenant_id,omitempty"`
	EventTypes []datatypes.EventType `json:"event_types,omitempty"`
	CreatedAt  time.Time             `json:"created_at"`
	UpdatedAt  time.Time             `json:"updated_at"`
}

// MarshalJSON converts []datatypes.EventType to JSON string array
func (w *Webhook) MarshalJSON() ([]byte, error) {
	type Alias Webhook
	aux := &struct {
		EventTypes []string `json:"event_types,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(w),
	}
	if len(w.EventTypes) > 0 {
		aux.EventTypes = make([]string, len(w.EventTypes))
		for i, et := range w.EventTypes {
			aux.EventTypes[i] = et.String()
		}
	}
	return json.Marshal(aux)
}

// UnmarshalJSON converts JSON string array to []datatypes.EventType
func (w *Webhook) UnmarshalJSON(data []byte) error {
	type Alias Webhook
	aux := &struct {
		EventTypes []string `json:"event_types,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(w),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.EventTypes != nil {
		w.EventTypes = make([]datatypes.EventType, 0, len(aux.EventTypes))
		seen := make(map[string]bool)
		for _, s := range aux.EventTypes {
			if len(s) > 64 {
				return fmt.Errorf("event type exceeds 64 characters: %s", s)
			}
			if !datatypes.IsValidEventType(s) {
				return fmt.Errorf("invalid event type: %s", s)
			}
			if seen[s] {
				return fmt.Errorf("duplicate event type: %s", s)
			}
			seen[s] = true
			et, _ := datatypes.ParseEventType(s) // Safe - already validated
			w.EventTypes = append(w.EventTypes, et)
		}
	}
	return nil
}

// CreateWebhookRequest represents the request to create a webhook
type CreateWebhookRequest struct {
	URL        string                `json:"url" validate:"required,no_null_bytes,min=1,max=2048"`
	SigningKey string                `json:"signing_key,omitempty"` // Optional - auto-generated if not provided
	Enabled    *bool                 `json:"enabled,omitempty"`
	TenantID   *string               `json:"tenant_id,omitempty" validate:"omitempty,no_null_bytes,max=255"`
	EventTypes []datatypes.EventType `json:"event_types,omitempty"`
}

// UnmarshalJSON converts JSON string array to []datatypes.EventType
func (r *CreateWebhookRequest) UnmarshalJSON(data []byte) error {
	type Alias CreateWebhookRequest
	aux := &struct {
		EventTypes []string `json:"event_types,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(r),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.EventTypes != nil {
		r.EventTypes = make([]datatypes.EventType, 0, len(aux.EventTypes))
		seen := make(map[string]bool)
		for _, s := range aux.EventTypes {
			if len(s) > 64 {
				return fmt.Errorf("event type exceeds 64 characters: %s", s)
			}
			if !datatypes.IsValidEventType(s) {
				return fmt.Errorf("invalid event type: %s", s)
			}
			if seen[s] {
				return fmt.Errorf("duplicate event type: %s", s)
			}
			seen[s] = true
			et, _ := datatypes.ParseEventType(s) // Safe - already validated
			r.EventTypes = append(r.EventTypes, et)
		}
	}
	return nil
}

// UpdateWebhookRequest represents the request to update a webhook
type UpdateWebhookRequest struct {
	URL        *string                `json:"url,omitempty" validate:"omitempty,no_null_bytes,min=1,max=2048"`
	SigningKey *string                `json:"signing_key,omitempty" validate:"omitempty,no_null_bytes,min=1,max=255"`
	Enabled    *bool                  `json:"enabled,omitempty"`
	EventTypes *[]datatypes.EventType `json:"event_types,omitempty"` // Pointer to allow clearing (nil = NULL in DB)
}

// UnmarshalJSON converts JSON string array to *[]datatypes.EventType
func (r *UpdateWebhookRequest) UnmarshalJSON(data []byte) error {
	type Alias UpdateWebhookRequest
	aux := &struct {
		EventTypes []string `json:"event_types,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(r),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.EventTypes != nil {
		eventTypes := make([]datatypes.EventType, 0, len(aux.EventTypes))
		seen := make(map[string]bool)
		for _, s := range aux.EventTypes {
			if len(s) > 64 {
				return fmt.Errorf("event type exceeds 64 characters: %s", s)
			}
			if !datatypes.IsValidEventType(s) {
				return fmt.Errorf("invalid event type: %s", s)
			}
			if seen[s] {
				return fmt.Errorf("duplicate event type: %s", s)
			}
			seen[s] = true
			et, _ := datatypes.ParseEventType(s) // Safe - already validated
			eventTypes = append(eventTypes, et)
		}
		r.EventTypes = &eventTypes
	}
	return nil
}

// MarshalJSON converts *[]datatypes.EventType to JSON string array
func (r *UpdateWebhookRequest) MarshalJSON() ([]byte, error) {
	type Alias UpdateWebhookRequest
	aux := &struct {
		EventTypes []string `json:"event_types,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(r),
	}
	if r.EventTypes != nil && len(*r.EventTypes) > 0 {
		aux.EventTypes = make([]string, len(*r.EventTypes))
		for i, et := range *r.EventTypes {
			aux.EventTypes[i] = et.String()
		}
	}
	return json.Marshal(aux)
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
