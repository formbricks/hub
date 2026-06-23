package service

import "github.com/riverqueue/river"

const (
	tenantTranslationBackfillKind = "tenant_translation_backfill"
	// TranslationBackfillsQueueName is the River queue for per-tenant translation
	// backfill fan-out jobs. It is kept separate from TranslationsQueueName so a large
	// fan-out does not starve live per-record translation throughput.
	TranslationBackfillsQueueName = "translation_backfills"
)

// TenantTranslationBackfillArgs fans out a re-translation backfill for one tenant: the
// worker lists the tenant's stale text records and enqueues a FeedbackTranslationArgs
// job for each. It is enqueued when a tenant's translation-relevant settings change
// (today: target_language).
//
// Uniqueness is by TenantID across the default (in-flight) states — the enqueue site
// sets ByArgs without a ByPeriod — so rapid repeated settings changes collapse to a
// single in-flight backfill, while a change after the previous backfill has completed
// re-triggers. The worker resolves the tenant's current target at run time, so a
// coalesced backfill always targets the latest configured language.
type TenantTranslationBackfillArgs struct {
	TenantID string `json:"tenant_id" river:"unique"`
}

// Kind returns the River job kind.
func (TenantTranslationBackfillArgs) Kind() string { return tenantTranslationBackfillKind }

var _ river.JobArgs = TenantTranslationBackfillArgs{}
