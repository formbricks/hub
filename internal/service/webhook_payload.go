package service

import (
	"time"

	"github.com/formbricks/hub/internal/models"
)

// FeedbackRecordWebhookPayload represents the webhook payload structure
type FeedbackRecordWebhookPayload struct {
	Type          string                `json:"type"` // "feedback_record.created", "feedback_record.updated", "feedback_record.deleted"
	Timestamp     time.Time             `json:"timestamp"`
	Data          models.FeedbackRecord `json:"data"`
	ChangedFields []string              `json:"changed_fields,omitempty"` // Only for updates
}
