package service

import "github.com/riverqueue/river"

const (
	feedbackRecordsPurgeKind = "feedback_records_purge"
	// FeedbackRecordsPurgeQueueName is the River queue for tenant feedback-record purges. It is
	// kept separate from the enrichment queues so a large purge cannot starve live ingestion
	// throughput, mirroring TranslationBackfillsQueueName.
	FeedbackRecordsPurgeQueueName = "feedback_records_purges"
	// FeedbackRecordsPurgeMaxAttempts is the retry budget for a purge. There is no config knob for
	// it: the purge is idempotent and tenant-scoped, so a retry is always safe and always makes
	// progress, and the failures worth retrying (a lock timeout while writes drain, a rescued job)
	// clear on their own within a few attempts.
	FeedbackRecordsPurgeMaxAttempts = 5
)

// FeedbackRecordsPurgeArgs deletes every feedback record for one tenant, plus the data derived
// from those records, leaving the tenant's taxonomy structure, webhooks and settings intact.
//
// The purge runs as a job rather than on the request path because it is unbounded: feedback_records
// has no per-tenant cap, and the API server's write timeout would cut the response long before a
// large tenant finished deleting.
//
// Uniqueness is by TenantID across the default (in-flight) states — the enqueue site sets ByArgs
// without a ByPeriod — so a second purge request while one is already running collapses into it,
// while a purge requested after the previous one completed runs again. ByPeriod must not be added:
// River's default unique states include `completed` and cannot be narrowed, so a period-based
// dedupe would silently swallow a legitimate later purge (the same trap documented on the
// enrichment event path).
//
// The worker re-reads the tenant from these args and scopes every statement by it; nothing about
// the purge is resolved at enqueue time.
type FeedbackRecordsPurgeArgs struct {
	TenantID string `json:"tenant_id" river:"unique"`
}

// Kind returns the River job kind.
func (FeedbackRecordsPurgeArgs) Kind() string { return feedbackRecordsPurgeKind }

var _ river.JobArgs = FeedbackRecordsPurgeArgs{}
