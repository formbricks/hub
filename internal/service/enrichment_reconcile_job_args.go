package service

import (
	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/models"
)

const enrichmentReconcileKind = "enrichment_reconcile"

// The reconcile queues. One per enrichment, separate from the live queue that the event path
// feeds — and named `_reconcile` rather than `_backfill` deliberately, because the pre-existing
// `translation_backfills` (the tenant fan-out) is one character-transposition away from a
// `translations_backfill`, and a mix-up there compiles, passes the parity test, and silently
// swaps the two queues' concurrency budgets.
//
// Separation is the rate control. The reconciler can enqueue a large sweep without a record
// submitted right now having to queue behind it, because the two draw from different MaxWorkers
// budgets — so a backlog drains in the background at whatever rate its own workers allow while
// live enrichment keeps its latency. Trying to achieve the same thing by pacing inserts onto the
// live queue means guessing a rate that matches drain speed, and guessing wrong in the slow
// direction is indistinguishable from the reconciler not running.
//
// It also fixes something that predates this: the one-off backfill commands enqueue onto the LIVE
// queues today (see cmd/backfill-classify), so running one already starves live enrichment.
const (
	// EnrichmentReconcileQueueName carries the sweep itself. Its own queue with MaxWorkers 1, so
	// "one sweep at a time" is structural rather than only enforced by the job's uniqueness, and a
	// long scan cannot occupy a worker the webhook dispatcher needs.
	EnrichmentReconcileQueueName = "enrichment_reconcile"

	SentimentsReconcileQueueName   = "sentiments_reconcile"
	EmotionsReconcileQueueName     = "emotions_reconcile"
	TranslationsReconcileQueueName = "translations_reconcile"
)

// ReconcileQueueFor maps an enrichment to the queue its reconciled work belongs on. Unknown names
// return "" — callers treat that as a wiring mistake rather than routing to the live queue by
// accident, which would defeat the separation above.
func ReconcileQueueFor(enrichment string) string {
	switch enrichment {
	case models.EnrichmentNameSentiment:
		return SentimentsReconcileQueueName
	case models.EnrichmentNameEmotions:
		return EmotionsReconcileQueueName
	case models.EnrichmentNameTranslation:
		return TranslationsReconcileQueueName
	default:
		return ""
	}
}

// EnrichmentReconcileArgs is one sweep: find records still owed enrichments and top the reconcile
// queues up towards their target depth.
//
// It carries no arguments. The sweep's inputs are the database's current state and the deployment
// config, both read at run time, so an argument would only be a chance for a queued job to act on
// a stale view.
//
// Uniqueness is a bare marker across the in-flight states, so a tick that arrives while the
// previous sweep is still running collapses into it rather than running two scans concurrently.
// `completed` is deliberately NOT in that set: with no ByPeriod the window is unbounded, so
// including it would mean the first sweep is the only one that ever runs.
//
// Deliberately an EMPTY struct: every insert encodes identically, so every insert site shares the
// one unique key and there is nothing a second site could get wrong. A discriminator field was
// tried and rejected — it created the drift channel it claimed to document, since an insert
// spelling the value differently would silently stop collapsing into the scheduled job.
type EnrichmentReconcileArgs struct{}

// Kind returns the River job kind.
func (EnrichmentReconcileArgs) Kind() string { return enrichmentReconcileKind }

var _ river.JobArgs = EnrichmentReconcileArgs{}
