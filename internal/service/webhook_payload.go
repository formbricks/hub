package service

import (
	"time"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/models"
)

// WebhookPayload represents a generic webhook payload structure for all event types.
// The Data field can contain FeedbackRecord, Webhook, or other event data types.
type WebhookPayload struct {
	ID            uuid.UUID `json:"id"`                       // Unique event id (UUID v7)
	Type          string    `json:"type"`                     // Event type as string (e.g., "feedback_record.created", "webhook.created")
	Timestamp     time.Time `json:"timestamp"`                // Event creation timestamp
	TenantID      *string   `json:"tenant_id,omitempty"`      // Tenant boundary for the event
	Data          any       `json:"data"`                     // Event data (FeedbackRecord, Webhook, etc.)
	ChangedFields []string  `json:"changed_fields,omitempty"` // Only for update events (optional)
}

// NewWebhookPayload builds the public webhook payload from internal dispatch args.
func NewWebhookPayload(args WebhookDispatchArgs) *WebhookPayload {
	tenantID := clonePayloadTenantID(args.TenantID)
	if tenantID == nil {
		tenantID = TenantIDPointerFromEventData(args.Data)
	}

	return &WebhookPayload{
		ID:            args.EventID,
		Type:          args.EventType,
		Timestamp:     args.Timestamp,
		TenantID:      tenantID,
		Data:          publicWebhookData(args.Data),
		ChangedFields: args.ChangedFields,
	}
}

func publicWebhookData(data any) any {
	switch payload := data.(type) {
	case models.DeletedIDsEventData:
		return cloneUUIDs(payload.IDs)
	case *models.DeletedIDsEventData:
		if payload == nil {
			return nil
		}

		return cloneUUIDs(payload.IDs)
	default:
		return data
	}
}

func clonePayloadTenantID(tenantID *string) *string {
	if tenantID == nil {
		return nil
	}

	return stringPointer(*tenantID)
}

func cloneUUIDs(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return nil
	}

	return append([]uuid.UUID(nil), ids...)
}
