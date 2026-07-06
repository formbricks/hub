package workers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/service"
)

// tenantTranslationBackfillService is the minimal interface the worker needs.
type tenantTranslationBackfillService interface {
	BackfillTranslationsForTenant(
		ctx context.Context, inserter service.RiverJobInserter, queueName string, maxAttempts int, tenantID, runID string,
	) (int, error)
}

// TenantTranslationBackfillWorker fans out a per-tenant re-translation: it lists the
// tenant's stale text records and enqueues a FeedbackTranslationArgs job for each. It is
// enqueued when a tenant's translation settings change (see
// service.TenantTranslationBackfillArgs) and runs off the request path.
type TenantTranslationBackfillWorker struct {
	river.WorkerDefaults[service.TenantTranslationBackfillArgs]

	service     tenantTranslationBackfillService
	maxAttempts int
}

// NewTenantTranslationBackfillWorker creates the worker. maxAttempts is applied to the
// per-record translation jobs it enqueues.
func NewTenantTranslationBackfillWorker(
	svc tenantTranslationBackfillService, maxAttempts int,
) *TenantTranslationBackfillWorker {
	return &TenantTranslationBackfillWorker{service: svc, maxAttempts: maxAttempts}
}

// tenantTranslationBackfillTimeout bounds a single tenant fan-out. River rescues a job that
// exceeds it; on retry the idempotent backfill query re-lists only the not-yet-enqueued
// tail, so a killed fan-out self-heals without continuation bookkeeping.
const tenantTranslationBackfillTimeout = 5 * time.Minute

// Timeout limits how long a single tenant backfill fan-out can run.
func (w *TenantTranslationBackfillWorker) Timeout(*river.Job[service.TenantTranslationBackfillArgs]) time.Duration {
	return tenantTranslationBackfillTimeout
}

// Work lists the tenant's stale records and enqueues per-record translation jobs onto the
// translations queue. The River client is obtained from the context (the only place River
// sets it) and handed to the service as the inserter, preserving the injected-inserter seam.
func (w *TenantTranslationBackfillWorker) Work(
	ctx context.Context, job *river.Job[service.TenantTranslationBackfillArgs],
) error {
	client := river.ClientFromContext[pgx.Tx](ctx)

	// The run discriminator is derived from this backfill job's ID: stable across the job's own
	// retries (so a rescued fan-out still dedupes its re-inserted pages) but distinct from any
	// earlier fan-out for the same tenant, so a previous run's completed jobs cannot swallow
	// this run's re-translations (e.g. a same-day target flip-flop).
	runID := fmt.Sprintf("job-%d", job.ID)

	enqueued, err := w.service.BackfillTranslationsForTenant(
		ctx, client, service.TranslationsQueueName, w.maxAttempts, job.Args.TenantID, runID)
	if err != nil {
		return fmt.Errorf("backfill translations for tenant %s: %w", job.Args.TenantID, err)
	}

	slog.Info("translation backfill: tenant fan-out complete",
		"tenant_id", job.Args.TenantID, "enqueued", enqueued)

	return nil
}
