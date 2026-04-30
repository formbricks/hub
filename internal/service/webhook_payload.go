package service

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/datatypes"
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
		Data:          publicWebhookData(args.EventType, args.Data),
		ChangedFields: args.ChangedFields,
	}
}

func publicWebhookData(eventType string, data any) any {
	if !isDeletedIDsEvent(eventType) {
		return data
	}

	ids, ok := deletedIDsFromEventData(data)
	if !ok {
		return data
	}

	return ids
}

func isDeletedIDsEvent(eventType string) bool {
	return eventType == datatypes.FeedbackRecordDeleted.String() ||
		eventType == datatypes.WebhookDeleted.String()
}

func deletedIDsFromEventData(data any) ([]uuid.UUID, bool) {
	switch payload := data.(type) {
	case models.DeletedIDsEventData:
		return cloneUUIDs(payload.IDs), true
	case *models.DeletedIDsEventData:
		if payload == nil {
			return nil, true
		}

		return cloneUUIDs(payload.IDs), true
	case map[string]any:
		return deletedIDsFromValue(payload["ids"])
	case map[string][]uuid.UUID:
		return cloneUUIDs(payload["ids"]), true
	case map[string][]string:
		return deletedIDsFromStrings(payload["ids"])
	case []uuid.UUID:
		return cloneUUIDs(payload), true
	case []string:
		return deletedIDsFromStrings(payload)
	case []any:
		return deletedIDsFromValues(payload)
	case json.RawMessage:
		return deletedIDsFromRawJSON(payload)
	default:
		return deletedIDsFromJSON(data)
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

func deletedIDsFromValue(value any) ([]uuid.UUID, bool) {
	switch ids := value.(type) {
	case []uuid.UUID:
		return cloneUUIDs(ids), true
	case []string:
		return deletedIDsFromStrings(ids)
	case []any:
		return deletedIDsFromValues(ids)
	default:
		return nil, false
	}
}

func deletedIDsFromStrings(values []string) ([]uuid.UUID, bool) {
	ids := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		id, err := uuid.Parse(value)
		if err != nil {
			return nil, false
		}

		ids = append(ids, id)
	}

	return ids, true
}

func deletedIDsFromValues(values []any) ([]uuid.UUID, bool) {
	ids := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		switch id := value.(type) {
		case uuid.UUID:
			ids = append(ids, id)
		case string:
			parsed, err := uuid.Parse(id)
			if err != nil {
				return nil, false
			}

			ids = append(ids, parsed)
		default:
			return nil, false
		}
	}

	return ids, true
}

func deletedIDsFromJSON(data any) ([]uuid.UUID, bool) {
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, false
	}

	return deletedIDsFromRawJSON(payload)
}

func deletedIDsFromRawJSON(payload []byte) ([]uuid.UUID, bool) {
	var envelope struct {
		IDs []uuid.UUID `json:"ids"`
	}

	if err := json.Unmarshal(payload, &envelope); err == nil && envelope.IDs != nil {
		return cloneUUIDs(envelope.IDs), true
	}

	var ids []uuid.UUID
	if err := json.Unmarshal(payload, &ids); err != nil {
		return nil, false
	}

	return cloneUUIDs(ids), true
}
