package service

import (
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

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

// FeedbackRecordsPurgeArgs deletes every feedback record for one tenant, everything derived from
// those records, and the taxonomy built on them — leaving the tenant's configuration (its webhooks
// and its settings) intact.
//
// The purge runs as a job rather than on the request path because it is unbounded: feedback_records
// has no per-tenant cap, and the API server's write timeout would cut the response long before a
// large tenant finished deleting.
//
// Uniqueness is by TenantID across the in-flight states only, spelled out explicitly at the enqueue
// site (see feedbackRecordsPurgeUniqueStates). A second request while a purge is running collapses
// into it; a purge requested after the previous one finished starts a new run.
//
// Do not fall back to River's default state set here. It includes `completed`, and with no ByPeriod
// the uniqueness window is unbounded, so the first purge of a tenant would be the only one that
// ever runs — every later request skipped as a duplicate while still returning 202.
//
// The worker re-reads the tenant from these args and scopes every statement by it; nothing about
// the purge is resolved at enqueue time.
type FeedbackRecordsPurgeArgs struct {
	TenantID string `json:"tenant_id" river:"unique"`
}

// feedbackRecordsPurgeUniqueStates is the in-flight state set the purge dedupes across: the four
// states River requires ByState to contain, plus `retryable`.
//
// `retryable` is included so a request arriving while a purge sits in backoff between attempts
// collapses into it, like every other in-flight state. Without it that request inserts a second
// purge job, and the two then contend for the same exclusive tenant lock — each one's timeouts
// eating the other's retry budget.
//
// `completed` is the one that must stay out: River's default set includes it, and with no ByPeriod
// the window is unbounded, so the first purge of a tenant would be the only one that ever ran.
func feedbackRecordsPurgeUniqueStates() []rivertype.JobState { return inFlightUniqueStates() }

// inFlightUniqueStates is the state set every unique enqueue in this package uses: the states a
// job can be in while it still has work left to do.
//
// It exists as one helper because getting it wrong is silent in both directions. Include
// `completed` — as River's default set does — and with no ByPeriod the uniqueness window is
// unbounded, so the first job for a given argument is the only one that ever runs while every
// later enqueue is skipped as a duplicate. Omit `retryable` and a job waiting out its backoff is
// enqueued a second time, doubling the work for something already being handled.
func inFlightUniqueStates() []rivertype.JobState {
	return []rivertype.JobState{
		rivertype.JobStateAvailable,
		rivertype.JobStatePending,
		rivertype.JobStateRetryable,
		rivertype.JobStateRunning,
		rivertype.JobStateScheduled,
	}
}

// Kind returns the River job kind.
func (FeedbackRecordsPurgeArgs) Kind() string { return feedbackRecordsPurgeKind }

var _ river.JobArgs = FeedbackRecordsPurgeArgs{}
