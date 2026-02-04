package service

import (
	"time"

	"github.com/google/uuid"
)

// WebhookPayload represents a generic webhook payload structure for all event types.
// The Data field can contain FeedbackRecord, Webhook, or other event data types.
type WebhookPayload struct {
	ID            uuid.UUID `json:"id"`                       // Unique event id (UUID v7)
	Type          string    `json:"type"`                     // Event type as string (e.g., "feedback_record.created", "webhook.created")
	Timestamp     time.Time `json:"timestamp"`                // Event creation timestamp
	Data          any       `json:"data"`                     // Event data (FeedbackRecord, Webhook, etc.)
	ChangedFields []string  `json:"changed_fields,omitempty"` // Only for update events (optional)
}
