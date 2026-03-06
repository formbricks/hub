package models

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/datatypes"
)

// eventTypesToStrings converts []datatypes.EventType to []string for JSON.
func eventTypesToStrings(et []datatypes.EventType) []string {
	return datatypes.EventTypeStrings(et)
}

// parseEventTypes converts []string to []datatypes.EventType.
func parseEventTypes(ss []string) ([]datatypes.EventType, error) {
	return datatypes.ParseEventTypes(ss) //nolint:wrapcheck // callers add context
}

// Webhook represents a webhook endpoint.
type Webhook struct {
	ID             uuid.UUID             `json:"id"`
	URL            string                `json:"url"`
	SigningKey     string                `json:"signing_key"`
	Enabled        bool                  `json:"enabled"`
	TenantID       *string               `json:"tenant_id,omitempty"`
	EventTypes     []datatypes.EventType `json:"event_types,omitempty"`
	CreatedAt      time.Time             `json:"created_at"`
	UpdatedAt      time.Time             `json:"updated_at"`
	DisabledReason *string               `json:"disabled_reason,omitempty"`
	DisabledAt     *time.Time            `json:"disabled_at,omitempty"`
}

// MarshalJSON converts []datatypes.EventType to JSON string array.
func (w *Webhook) MarshalJSON() ([]byte, error) {
	type Alias Webhook

	aux := &struct {
		*Alias

		EventTypes []string `json:"event_types,omitempty"`
	}{
		Alias: (*Alias)(w),
	}
	aux.EventTypes = eventTypesToStrings(w.EventTypes)

	data, err := json.Marshal(aux)
	if err != nil {
		return nil, fmt.Errorf("marshal webhook: %w", err)
	}

	return data, nil
}

// UnmarshalJSON converts JSON string array to []datatypes.EventType.
func (w *Webhook) UnmarshalJSON(data []byte) error {
	type Alias Webhook

	aux := &struct {
		*Alias

		EventTypes []string `json:"event_types,omitempty"`
	}{
		Alias: (*Alias)(w),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("unmarshal webhook: %w", err)
	}

	parsed, err := parseEventTypes(aux.EventTypes)
	if err != nil {
		return fmt.Errorf("parse event types: %w", err)
	}

	w.EventTypes = parsed

	return nil
}

// WebhookPublic is a webhook DTO for GET and LIST responses; it omits signing_key.
type WebhookPublic struct {
	ID             uuid.UUID             `json:"id"`
	URL            string                `json:"url"`
	Enabled        bool                  `json:"enabled"`
	TenantID       *string               `json:"tenant_id,omitempty"`
	EventTypes     []datatypes.EventType `json:"event_types,omitempty"`
	CreatedAt      time.Time             `json:"created_at"`
	UpdatedAt      time.Time             `json:"updated_at"`
	DisabledReason *string               `json:"disabled_reason,omitempty"`
	DisabledAt     *time.Time            `json:"disabled_at,omitempty"`
}

// MarshalJSON converts []datatypes.EventType to JSON string array.
func (w *WebhookPublic) MarshalJSON() ([]byte, error) {
	type Alias WebhookPublic

	aux := &struct {
		*Alias

		EventTypes []string `json:"event_types,omitempty"`
	}{
		Alias: (*Alias)(w),
	}
	aux.EventTypes = eventTypesToStrings(w.EventTypes)

	data, err := json.Marshal(aux)
	if err != nil {
		return nil, fmt.Errorf("marshal webhook public: %w", err)
	}

	return data, nil
}

// UnmarshalJSON converts JSON string array to []datatypes.EventType.
func (w *WebhookPublic) UnmarshalJSON(data []byte) error {
	type Alias WebhookPublic

	aux := &struct {
		*Alias

		EventTypes []string `json:"event_types,omitempty"`
	}{
		Alias: (*Alias)(w),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("unmarshal webhook public: %w", err)
	}

	parsed, err := parseEventTypes(aux.EventTypes)
	if err != nil {
		return fmt.Errorf("parse event types: %w", err)
	}

	w.EventTypes = parsed

	return nil
}

// ToWebhookPublic converts a Webhook to WebhookPublic (omits signing_key).
// Returns detached copies (no shared references with the input).
func ToWebhookPublic(w Webhook) WebhookPublic {
	var tenantID *string

	if w.TenantID != nil {
		v := *w.TenantID
		tenantID = &v
	}

	var disabledReason *string

	if w.DisabledReason != nil {
		v := *w.DisabledReason
		disabledReason = &v
	}

	var disabledAt *time.Time

	if w.DisabledAt != nil {
		v := *w.DisabledAt
		disabledAt = &v
	}

	eventTypes := append([]datatypes.EventType(nil), w.EventTypes...)

	return WebhookPublic{
		ID:             w.ID,
		URL:            w.URL,
		Enabled:        w.Enabled,
		TenantID:       tenantID,
		EventTypes:     eventTypes,
		CreatedAt:      w.CreatedAt,
		UpdatedAt:      w.UpdatedAt,
		DisabledReason: disabledReason,
		DisabledAt:     disabledAt,
	}
}

// CreateWebhookRequest represents the request to create a webhook.
type CreateWebhookRequest struct {
	URL        string                `json:"url"                   validate:"required,no_null_bytes,http_url,min=1,max=2048"`
	SigningKey string                `json:"signing_key,omitempty" validate:"omitempty,max=255"`
	Enabled    *bool                 `json:"enabled,omitempty"`
	TenantID   *string               `json:"tenant_id,omitempty"   validate:"omitempty,no_null_bytes,max=255"`
	EventTypes []datatypes.EventType `json:"event_types,omitempty"`
}

// UnmarshalJSON converts JSON string array to []datatypes.EventType.
func (r *CreateWebhookRequest) UnmarshalJSON(data []byte) error {
	type Alias CreateWebhookRequest

	aux := &struct {
		*Alias

		EventTypes []string `json:"event_types,omitempty"`
	}{
		Alias: (*Alias)(r),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("unmarshal create webhook request: %w", err)
	}

	parsed, err := parseEventTypes(aux.EventTypes)
	if err != nil {
		return fmt.Errorf("parse event types: %w", err)
	}

	r.EventTypes = parsed

	return nil
}

// UpdateWebhookRequest represents the request to update a webhook.
// DisabledReason and DisabledAt are read-only in the API (json:"-" so clients cannot set them);
// the system sets them when a webhook is disabled. Re-enabling (enabled: true) clears them in the repo.
type UpdateWebhookRequest struct {
	URL            *string                `json:"url,omitempty"         validate:"omitempty,no_null_bytes,http_url,min=1,max=2048"`
	SigningKey     *string                `json:"signing_key,omitempty" validate:"omitempty,no_null_bytes,min=1,max=255"`
	Enabled        *bool                  `json:"enabled,omitempty"`
	TenantID       *string                `json:"tenant_id,omitempty"   validate:"omitempty,no_null_bytes,max=255"`
	EventTypes     *[]datatypes.EventType `json:"event_types,omitempty"`
	DisabledReason *string                `json:"-"` // read-only; set by system when disabling
	DisabledAt     *time.Time             `json:"-"` // read-only; set by system when disabling
}

// UnmarshalJSON converts JSON string array to *[]datatypes.EventType.
func (r *UpdateWebhookRequest) UnmarshalJSON(data []byte) error {
	type Alias UpdateWebhookRequest

	aux := &struct {
		*Alias

		EventTypes []string `json:"event_types,omitempty"`
	}{
		Alias: (*Alias)(r),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("unmarshal update webhook request: %w", err)
	}

	if aux.EventTypes != nil {
		parsed, err := parseEventTypes(aux.EventTypes)
		if err != nil {
			return fmt.Errorf("parse event types: %w", err)
		}

		r.EventTypes = &parsed
	}

	return nil
}

// MarshalJSON converts *[]datatypes.EventType to JSON string array.
func (r *UpdateWebhookRequest) MarshalJSON() ([]byte, error) {
	type Alias UpdateWebhookRequest

	aux := &struct {
		*Alias

		EventTypes []string `json:"event_types,omitempty"`
	}{
		Alias: (*Alias)(r),
	}
	if r.EventTypes != nil {
		aux.EventTypes = eventTypesToStrings(*r.EventTypes)
	}

	data, err := json.Marshal(aux)
	if err != nil {
		return nil, fmt.Errorf("marshal update webhook request: %w", err)
	}

	return data, nil
}

// ChangedFields returns the names of fields that are set (non-nil) in the update request.
func (r *UpdateWebhookRequest) ChangedFields() []string {
	var fields []string
	if r.URL != nil {
		fields = append(fields, "url")
	}

	if r.SigningKey != nil {
		fields = append(fields, "signing_key")
	}

	if r.Enabled != nil {
		fields = append(fields, "enabled")
	}

	if r.TenantID != nil {
		fields = append(fields, "tenant_id")
	}

	if r.EventTypes != nil {
		fields = append(fields, "event_types")
	}

	return fields
}

// ListWebhooksFilters represents filters for listing webhooks.
type ListWebhooksFilters struct {
	Enabled  *bool   `form:"enabled"`
	TenantID *string `form:"tenant_id" validate:"omitempty,no_null_bytes"`
	Limit    int     `form:"limit"     validate:"omitempty,min=1,max=1000"`
	Cursor   string  `form:"cursor"    validate:"omitempty"` // keyset cursor; omit for first page, use next_cursor for subsequent pages
}

// ListWebhooksResponse represents the response for listing webhooks (internal; service returns full Webhooks).
type ListWebhooksResponse struct {
	Data       []Webhook `json:"data"`
	Limit      int       `json:"limit"`
	NextCursor string    `json:"next_cursor,omitempty"` // present when there may be more results
}

// ListWebhooksPublicResponse is the API response for GET /v1/webhooks; Data omits signing_key.
// Total and Offset are omitted when using cursor-based pagination.
type ListWebhooksPublicResponse struct {
	Data       []WebhookPublic `json:"data"`
	Total      *int64          `json:"total,omitempty"`
	Limit      int             `json:"limit"`
	Offset     *int            `json:"offset,omitempty"`
	NextCursor string          `json:"next_cursor,omitempty"`
}
