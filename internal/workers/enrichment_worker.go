package workers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

// enrichmentWorkerMetrics records the worker-side metrics as function fields rather than an
// interface: the observability layer names the outcome/duration methods per type
// (RecordSentimentOutcome, RecordEmbeddingOutcome, …), so no single interface can capture them —
// each per-type constructor binds the matching method values here. The funcs are always non-nil
// (the constructor installs no-ops when metrics are disabled), so the worker never nil-checks.
type enrichmentWorkerMetrics struct {
	outcome     func(ctx context.Context, status string)
	duration    func(ctx context.Context, d time.Duration, status string)
	workerError func(ctx context.Context, reason string)
}

// enrichmentWorkerConfig configures an EnrichmentWorker: how to read the record and extract its id,
// decide eligibility and content, classify, and persist — plus the per-type behaviors (rate-limit
// snooze, supersession skip) and metric labels. The shared Work body is identical across the
// enrichments; only these hooks differ.
type enrichmentWorkerConfig[A river.JobArgs, R any] struct {
	name    string // enrichment name, for log messages
	timeout time.Duration

	recordID   func(args A) uuid.UUID
	getRecord  func(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	eligible   func(record *models.FeedbackRecord) bool // nil ⇒ always eligible
	hasContent func(record *models.FeedbackRecord) bool
	classify   func(ctx context.Context, record *models.FeedbackRecord) (R, error)
	persist    func(ctx context.Context, id uuid.UUID, result *R) error // result nil ⇒ clear

	// isSuperseded reports whether a persist error is a benign "already superseded" skip
	// (translation's stale-target case). nil ⇒ this enrichment has no supersession.
	isSuperseded func(err error) bool
	// rateLimited applies the shared rate-limit snooze to classify failures (LLM callers do; the
	// embedding worker does not).
	rateLimited bool
	// apiErrorReason is the worker-error metric reason recorded for a classify failure.
	apiErrorReason string
	// logResult returns extra slog attributes for the classify-success log (e.g. the sentiment
	// label and score), or nil to log only the record id.
	logResult func(result R) []any

	metrics enrichmentWorkerMetrics
}

// EnrichmentWorker is the shared River worker body for the enrichment pipelines: load the record,
// clear-on-empty or classify, persist, and map every error to the right outcome (rate-limit
// snooze, not-found skip, tenant-write-conflict retry, supersession skip, final-attempt fail). The
// per-type differences live entirely in the config hooks; concrete workers are aliases of a
// configured instantiation (see FeedbackSentimentWorker).
type EnrichmentWorker[A river.JobArgs, R any] struct {
	river.WorkerDefaults[A]

	cfg enrichmentWorkerConfig[A, R]
}

// newEnrichmentWorker builds a worker from cfg.
func newEnrichmentWorker[A river.JobArgs, R any](cfg enrichmentWorkerConfig[A, R]) *EnrichmentWorker[A, R] {
	return &EnrichmentWorker[A, R]{cfg: cfg}
}

// Timeout limits how long a single job can run.
func (w *EnrichmentWorker[A, R]) Timeout(*river.Job[A]) time.Duration {
	return w.cfg.timeout
}

// Work loads the record, classifies its content (or clears a stale result when the content is
// empty), and persists the result.
func (w *EnrichmentWorker[A, R]) Work(ctx context.Context, job *river.Job[A]) error {
	cfg := w.cfg
	start := time.Now()
	id := cfg.recordID(job.Args)

	record, err := cfg.getRecord(ctx, id)
	if err != nil {
		// Not-found means the record was deleted or its tenant purged between enqueue and now: a
		// benign race, recorded as skipped so it does not trip failure alerts.
		if errors.Is(err, huberrors.ErrNotFound) {
			w.recordOutcome(ctx, "skipped", start)
			slog.Info(cfg.name+": record gone before classify, skipping", "feedback_record_id", id)

			return nil
		}

		cfg.metrics.workerError(ctx, "get_record_failed")
		w.recordOutcome(ctx, "failed_final", start)
		slog.Error(cfg.name+": get record failed", "feedback_record_id", id, "error", err)

		return fmt.Errorf("get feedback record: %w", err)
	}

	if cfg.eligible != nil && !cfg.eligible(record) {
		w.recordOutcome(ctx, "skipped", start)
		slog.Info(cfg.name+": skipped, record not eligible", "feedback_record_id", id)

		return nil
	}

	// Content became empty since enqueue (e.g. an edit cleared it): clear any stale result rather
	// than classify empty text.
	if !cfg.hasContent(record) {
		if err := cfg.persist(ctx, id, nil); err != nil {
			return w.handlePersistError(ctx, err, id, start, job.Attempt >= job.MaxAttempts)
		}

		w.recordOutcome(ctx, "success", start)
		slog.Info(cfg.name+": cleared (empty content)", "feedback_record_id", id)

		return nil
	}

	result, err := cfg.classify(ctx, record)
	if err != nil {
		return w.handleClassifyError(ctx, err, id, start, job)
	}

	if err := cfg.persist(ctx, id, &result); err != nil {
		return w.handlePersistError(ctx, err, id, start, job.Attempt >= job.MaxAttempts)
	}

	w.recordOutcome(ctx, "success", start)

	attrs := []any{"feedback_record_id", id}
	if cfg.logResult != nil {
		attrs = append(attrs, cfg.logResult(result)...)
	}

	slog.Info(cfg.name+": stored", attrs...)

	return nil
}

// handleClassifyError maps a provider failure to the right outcome: for rate-limited callers a 429
// snoozes (re-queues without consuming an attempt, so a burst defers rather than drops work); any
// other error retries until the attempts are spent, then fails.
func (w *EnrichmentWorker[A, R]) handleClassifyError(
	ctx context.Context, err error, id uuid.UUID, start time.Time, job *river.Job[A],
) error {
	cfg := w.cfg

	if cfg.rateLimited {
		if delay, ok := rateLimitSnoozeDelay(err, job.CreatedAt); ok {
			cfg.metrics.workerError(ctx, "rate_limited")
			w.recordOutcome(ctx, "retry", start)
			slog.Warn(cfg.name+": provider rate-limited, snoozing",
				"feedback_record_id", id, "retry_after", delay.String())

			//nolint:wrapcheck // river sentinel: JobSnooze must be returned unwrapped for River to detect the snooze
			return river.JobSnooze(delay)
		}
	}

	isLastAttempt := job.Attempt >= job.MaxAttempts

	cfg.metrics.workerError(ctx, cfg.apiErrorReason)

	if isLastAttempt {
		w.recordOutcome(ctx, "failed_final", start)
		slog.Error(cfg.name+": provider failed (final attempt)", "feedback_record_id", id, "error", err)

		return fmt.Errorf("classify (final attempt): %w", err)
	}

	w.recordOutcome(ctx, "retry", start)

	return fmt.Errorf("classify: %w", err)
}

// handlePersistError maps a write failure to an outcome: a missing record or a superseded result
// completes the job (nothing to write), a tenant write conflict retries (the post-purge attempt
// finds the record gone), and anything else fails the job.
func (w *EnrichmentWorker[A, R]) handlePersistError(
	ctx context.Context, err error, id uuid.UUID, start time.Time, isLastAttempt bool,
) error {
	cfg := w.cfg

	switch {
	case errors.Is(err, huberrors.ErrNotFound):
		w.recordOutcome(ctx, "skipped", start)
		slog.Info(cfg.name+": record gone before write, skipping", "feedback_record_id", id)

		return nil
	case cfg.isSuperseded != nil && cfg.isSuperseded(err):
		w.recordOutcome(ctx, "skipped", start)
		slog.Info(cfg.name+": result superseded, skipping", "feedback_record_id", id)

		return nil
	case errors.Is(err, huberrors.ErrTenantWriteConflict):
		outcome := "retry"
		if isLastAttempt {
			outcome = "failed_final"
		}

		cfg.metrics.workerError(ctx, "tenant_write_conflict")
		w.recordOutcome(ctx, outcome, start)
		slog.Warn(cfg.name+": tenant data purge in progress, deferring write",
			"feedback_record_id", id, "final_attempt", isLastAttempt)

		return fmt.Errorf("set feedback record %s: %w", cfg.name, err)
	default:
		cfg.metrics.workerError(ctx, "update_failed")
		w.recordOutcome(ctx, "failed_final", start)
		slog.Error(cfg.name+": set result failed", "feedback_record_id", id, "error", err)

		return fmt.Errorf("set feedback record %s: %w", cfg.name, err)
	}
}

// recordOutcome records the job outcome and duration under the same status label.
func (w *EnrichmentWorker[A, R]) recordOutcome(ctx context.Context, status string, start time.Time) {
	w.cfg.metrics.outcome(ctx, status)
	w.cfg.metrics.duration(ctx, time.Since(start), status)
}
