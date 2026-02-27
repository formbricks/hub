package service

import (
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

const webhookDispatchKind = "webhook_dispatch"

// WebhookDispatchArgs is the job payload for one (event, webhook) delivery.
// Used by WebhookProvider to enqueue and by WebhookDispatchWorker to run.
// Only event_id and webhook_id are used for River uniqueness (river:"unique")
// so the hash is fast and does not include the potentially large data payload.
type WebhookDispatchArgs struct {
	EventID       uuid.UUID `json:"event_id"                 river:"unique"`
	EventType     string    `json:"event_type"`
	Timestamp     time.Time `json:"timestamp"`
	Data          any       `json:"data"`
	ChangedFields []string  `json:"changed_fields,omitempty"`
	WebhookID     uuid.UUID `json:"webhook_id"               river:"unique"`
}

// Kind returns the River job kind.
func (WebhookDispatchArgs) Kind() string { return webhookDispatchKind }

var _ river.JobArgs = WebhookDispatchArgs{}
