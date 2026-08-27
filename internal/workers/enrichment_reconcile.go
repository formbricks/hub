package workers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/service"
)

// enrichmentReconcileTimeout bounds one sweep. Generous relative to the work — the pending-set
// queries are index-backed and the inserts are batched — but a sweep that has not finished in this
// long is not going to, and holding a worker slot past the next tick only stacks them up.
const enrichmentReconcileTimeout = 5 * time.Minute

// ReconcileSweeper is the service half, declared here so the worker depends on the behaviour
// rather than the implementation. Exported because hub-worker has to name the type to convert a
// nil *EnrichmentReconcileService into a nil interface — a typed nil in an interface would
// register the worker and panic on the first sweep.
type ReconcileSweeper interface {
	Sweep(ctx context.Context) (service.ReconcileResult, error)
}

// EnrichmentReconcileWorker runs one reconcile sweep.
//
// The job carries no state of its own: everything it needs is the database's current contents and
// the deployment config, both read at run time. That is what lets the periodic schedule be simple —
// a missed tick costs nothing, because the next one sees the same backlog plus whatever arrived.
type EnrichmentReconcileWorker struct {
	river.WorkerDefaults[service.EnrichmentReconcileArgs]

	sweeper ReconcileSweeper
}

// NewEnrichmentReconcileWorker creates the reconcile worker.
func NewEnrichmentReconcileWorker(sweeper ReconcileSweeper) *EnrichmentReconcileWorker {
	return &EnrichmentReconcileWorker{sweeper: sweeper}
}

// Timeout bounds one sweep at enrichmentReconcileTimeout.
//
// Declaring it is what makes that constant real. River falls back to Config.JobTimeout when a
// worker returns zero, and RIVER_JOB_TIMEOUT_SECONDS defaults to 0, which River in turn reads as
// its own one-minute default -- so without this the sweep would be cancelled at a minute and the
// context.WithTimeout in Work could not extend it, a child context being unable to outlive its
// parent. With MaxAttempts 1 that cancellation is a discarded job and an error log per tick,
// which is exactly the "run of them" signal Work describes as meaning coverage has stopped.
func (w *EnrichmentReconcileWorker) Timeout(*river.Job[service.EnrichmentReconcileArgs]) time.Duration {
	return enrichmentReconcileTimeout
}

// Work runs the sweep.
//
// A failed sweep returns an error so River retries it, but the retry is not what makes the system
// converge — the next scheduled tick is. That matters for how alarming a failure here should look:
// one is noise, a run of them means coverage has stopped being guaranteed.
func (w *EnrichmentReconcileWorker) Work(
	ctx context.Context, _ *river.Job[service.EnrichmentReconcileArgs],
) error {
	// Redundant with Timeout above under River, and deliberately kept: it bounds the sweep for
	// any caller that invokes Work directly (tests do) and keeps the guarantee local to the code
	// that relies on it.
	ctx, cancel := context.WithTimeout(ctx, enrichmentReconcileTimeout)
	defer cancel()

	start := time.Now()

	result, err := w.sweeper.Sweep(ctx)

	total := 0
	for _, count := range result.Enqueued {
		total += count
	}

	if err != nil {
		slog.ErrorContext(ctx, "enrichment reconcile: sweep failed",
			"enqueued_before_failure", total, "duration", time.Since(start), "error", err)

		return fmt.Errorf("enrichment reconcile: %w", err)
	}

	slog.InfoContext(ctx, "enrichment reconcile: sweep complete",
		"enqueued", total, "by_enrichment", result.Enqueued,
		"at_target_depth", result.Skipped, "duration", time.Since(start))

	return nil
}

var _ river.Worker[service.EnrichmentReconcileArgs] = (*EnrichmentReconcileWorker)(nil)
