package workers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/service"
)

const embeddingReconcileTimeout = 2 * time.Minute

// EmbeddingReconcileSweeper is the service boundary used by the periodic worker.
type EmbeddingReconcileSweeper interface {
	Sweep(ctx context.Context) (service.EmbeddingReconcileResult, error)
}

// EmbeddingReconcileWorker runs one bounded, level-triggered taxonomy embedding sweep.
type EmbeddingReconcileWorker struct {
	river.WorkerDefaults[service.EmbeddingReconcileArgs]

	sweeper EmbeddingReconcileSweeper
	metrics observability.EmbeddingMetrics
}

// NewEmbeddingReconcileWorker creates the periodic sweep worker.
func NewEmbeddingReconcileWorker(
	sweeper EmbeddingReconcileSweeper,
	metrics observability.EmbeddingMetrics,
) *EmbeddingReconcileWorker {
	return &EmbeddingReconcileWorker{sweeper: sweeper, metrics: metrics}
}

// Timeout gives the database scan and bounded insert batch its own deadline rather than inheriting
// River's unrelated global job timeout.
func (w *EmbeddingReconcileWorker) Timeout(*river.Job[service.EmbeddingReconcileArgs]) time.Duration {
	return embeddingReconcileTimeout
}

// Work executes one sweep. A failure is returned for observability, but scheduled jobs use one
// attempt because the next interval is the retry and sees the same level-triggered backlog.
func (w *EmbeddingReconcileWorker) Work(
	ctx context.Context,
	_ *river.Job[service.EmbeddingReconcileArgs],
) error {
	ctx, cancel := context.WithTimeout(ctx, embeddingReconcileTimeout)
	defer cancel()

	result, err := w.sweeper.Sweep(ctx)
	if err != nil {
		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "reconcile_failed")
		}

		slog.ErrorContext(ctx, "embedding reconcile: sweep failed", "error", err)

		return fmt.Errorf("embedding reconcile: %w", err)
	}

	if w.metrics != nil && result.Enqueued > 0 {
		w.metrics.RecordJobsEnqueued(ctx, int64(result.Enqueued))
	}

	slog.InfoContext(ctx, "embedding reconcile: sweep complete",
		"found", result.Found,
		"enqueued", result.Enqueued,
		"queue_depth_before", result.Depth,
		"at_target_depth", result.AtTarget,
	)

	return nil
}

var _ river.Worker[service.EmbeddingReconcileArgs] = (*EmbeddingReconcileWorker)(nil)
