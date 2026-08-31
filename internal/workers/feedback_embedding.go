// Package workers provides River job workers (e.g. webhook delivery, feedback embedding).
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
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/service"
)

// FeedbackEmbeddingWorker generates and stores embeddings for feedback records.
type FeedbackEmbeddingWorker struct {
	river.WorkerDefaults[service.FeedbackEmbeddingArgs]

	embeddingService feedbackEmbeddingService
	embeddingClient  service.EmbeddingClient
	docPrefix        string // model-specific prefix for document embedding
	metrics          observability.EmbeddingMetrics
	jobTimeout       time.Duration
	failures         FailureRecorder
	failureMetrics   observability.EnrichmentFailureMetrics
}

const defaultEmbeddingJobTimeout = 60 * time.Second

// feedbackEmbeddingService is the minimal interface needed by the worker.
type feedbackEmbeddingService interface {
	GetFeedbackRecord(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	SetEmbedding(
		ctx context.Context, feedbackRecordID uuid.UUID, model string, embedding []float32,
		stillCurrent func(fieldLabel, valueText, valueTextTranslated *string) bool,
	) error
}

// NewFeedbackEmbeddingWorker creates a worker that fetches the record, calls the embedding client, and stores the result.
// docPrefix is the prefix for document text. Can be empty for some providers.
// metrics may be nil when metrics are disabled.
func NewFeedbackEmbeddingWorker(
	embeddingService feedbackEmbeddingService,
	embeddingClient service.EmbeddingClient,
	docPrefix string,
	metrics observability.EmbeddingMetrics,
) *FeedbackEmbeddingWorker {
	return NewFeedbackEmbeddingWorkerWithOptions(
		embeddingService, embeddingClient, docPrefix, metrics,
		defaultEmbeddingJobTimeout, nil, nil,
	)
}

// NewFeedbackEmbeddingWorkerWithOptions creates an embedding worker with an explicit job deadline
// and durable failure recording. The simple constructor remains for tools/tests that do not need
// deployment wiring.
func NewFeedbackEmbeddingWorkerWithOptions(
	embeddingService feedbackEmbeddingService,
	embeddingClient service.EmbeddingClient,
	docPrefix string,
	metrics observability.EmbeddingMetrics,
	jobTimeout time.Duration,
	failures FailureRecorder,
	failureMetrics observability.EnrichmentFailureMetrics,
) *FeedbackEmbeddingWorker {
	if jobTimeout <= 0 {
		jobTimeout = defaultEmbeddingJobTimeout
	}

	return &FeedbackEmbeddingWorker{
		embeddingService: embeddingService,
		embeddingClient:  embeddingClient,
		docPrefix:        docPrefix,
		metrics:          metrics,
		jobTimeout:       jobTimeout,
		failures:         failures,
		failureMetrics:   failureMetrics,
	}
}

// Timeout limits how long a single embedding job can run.
func (w *FeedbackEmbeddingWorker) Timeout(*river.Job[service.FeedbackEmbeddingArgs]) time.Duration {
	return w.jobTimeout
}

// Work loads the record, generates or clears the embedding, and persists it.
func (w *FeedbackEmbeddingWorker) Work(ctx context.Context, job *river.Job[service.FeedbackEmbeddingArgs]) error {
	args := job.Args
	start := time.Now()

	log := slog.With("feedback_record_id", args.FeedbackRecordID, "event_id", args.EventID)

	record, err := w.embeddingService.GetFeedbackRecord(ctx, args.FeedbackRecordID)
	if err != nil {
		// Not-found means the record was deleted or its tenant purged between enqueue and
		// now: a benign race, not a terminal failure. Record it as skipped (consistent with
		// the not-found-on-write path) so it does not trip failure alerts.
		if errors.Is(err, huberrors.ErrNotFound) {
			if w.metrics != nil {
				w.metrics.RecordEmbeddingOutcome(ctx, "skipped")
				w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), "skipped")
			}

			log.Info("embedding: record gone before embed, skipping")

			return nil
		}

		// A non-not-found read error is transient (e.g. a DB blip): River retries while attempts
		// remain, so only the last attempt is a final failure — recording failed_final on every
		// attempt overcounts it (matches the API-failure and write branches).
		isLastAttempt := job.Attempt >= job.MaxAttempts

		outcome := "retry"
		if isLastAttempt {
			outcome = "failed_final"
		}

		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "get_record_failed")
			w.metrics.RecordEmbeddingOutcome(ctx, outcome)
			w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), outcome)
		}

		log.Error("embedding: get record failed",
			"final_attempt", isLastAttempt,
			"error", err,
		)

		return fmt.Errorf("get feedback record: %w", err)
	}

	inputKind := models.NormalizeEmbeddingInputKind(args.InputKind)
	text := service.BuildEmbeddingInputForKind(record, inputKind, w.docPrefix)

	// stillCurrent lets the repository verify, atomically with the write, that the content this
	// job embedded is still the record's content — so of two concurrent jobs for one record, the
	// stale one skips instead of clobbering the newer vector (last-write-wins would attach an old
	// text's embedding forever; the missing-rows-only backfill cannot repair that).
	stillCurrent := func(fieldLabel, valueText, valueTextTranslated *string) bool {
		return service.BuildEmbeddingInputFromValues(fieldLabel, valueText, valueTextTranslated, inputKind, w.docPrefix) == text
	}

	if text == "" {
		return w.handleEmptyText(ctx, job, record, log, start, stillCurrent)
	}

	embedding, err := w.embeddingClient.CreateEmbedding(ctx, text)
	if err != nil {
		return w.handleEmbedError(ctx, err, job, record, log, start)
	}

	err = w.embeddingService.SetEmbedding(ctx, args.FeedbackRecordID, args.Model, embedding, stillCurrent)
	if err != nil {
		isLastAttempt := job.Attempt >= job.MaxAttempts

		return w.handleSetEmbeddingError(
			ctx, err, log, start, record, job.Args.Model, inputKind, job.Attempt, isLastAttempt,
			"set feedback record embedding",
		)
	}

	log.Info("embedding: stored")

	if w.metrics != nil {
		w.metrics.RecordEmbeddingOutcome(ctx, "success")
		w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), "success")
	}

	return nil
}

// handleEmbedError maps an embedding-API failure to a worker outcome: a provider 429 snoozes
// instead of consuming a retry attempt — critical for the backfill, which can enqueue far more
// jobs than the provider's rate limit and would otherwise mass-discard them as failed_final
// (mirrors the classify workers) — while anything else retries, failing on the last attempt.
func (w *FeedbackEmbeddingWorker) handleEmbedError(
	ctx context.Context,
	err error,
	job *river.Job[service.FeedbackEmbeddingArgs],
	record *models.FeedbackRecord,
	log *slog.Logger,
	start time.Time,
) error {
	if delay, ok := rateLimitSnoozeDelay(err, job.CreatedAt); ok {
		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "rate_limited")
			w.metrics.RecordEmbeddingOutcome(ctx, "retry")
			w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), "retry")
		}

		log.Warn("embedding: provider rate limited, snoozing",
			"retry_after", delay,
		)

		//nolint:wrapcheck // river sentinel: JobSnooze must be returned unwrapped for River to detect the snooze
		return river.JobSnooze(delay)
	}

	inputKind := models.NormalizeEmbeddingInputKind(job.Args.InputKind)

	if reason, terminal := huberrors.TerminalReasonOf(err); terminal {
		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "embedding_api_failed")
			w.metrics.RecordEmbeddingOutcome(ctx, "failed_final")
			w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), "failed_final")
		}

		if w.failureMetrics != nil {
			w.failureMetrics.RecordTerminalFailure(
				ctx, models.EnrichmentNameTaxonomyEmbedding, string(reason))
		}

		w.markTaxonomyEmbeddingFailed(
			ctx, log, record, inputKind, job.Args.Model, job.Attempt, true, string(reason))
		log.Error("embedding: provider failed permanently for this record, not retrying",
			"reason", string(reason),
			"attempt", job.Attempt,
			"error", err,
		)

		//nolint:wrapcheck // River must see JobCancel directly to suppress remaining attempts.
		return river.JobCancel(fmt.Errorf("embedding API (terminal, %s): %w", reason, err))
	}

	isLastAttempt := job.Attempt >= job.MaxAttempts

	if w.metrics != nil {
		w.metrics.RecordWorkerError(ctx, "embedding_api_failed")

		outcome := "retry"
		if isLastAttempt {
			outcome = "failed_final"
		}

		w.metrics.RecordEmbeddingOutcome(ctx, outcome)
		w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), outcome)
	}

	if isLastAttempt {
		w.markTaxonomyEmbeddingFailed(
			ctx, log, record, inputKind, job.Args.Model, job.Attempt, false,
			models.EnrichmentFailureReasonProviderError,
		)
		log.Error("embedding: API failed (final attempt)",
			"error", err,
		)
		// Return error so River marks the job as failed; otherwise these records never get embeddings and don't show as failed in River UI.
		return fmt.Errorf("embedding API (final attempt): %w", err)
	}

	return fmt.Errorf("embedding API: %w", err)
}

// handleSetEmbeddingError maps embedding write failures to worker outcomes.
// A missing record means it was deleted or its tenant purged between fetch and
// write: the job completes (nothing left to embed). A tenant write conflict
// means a tenant data purge is in progress: the job retries via River, and the
// post-purge attempt finds the record gone and completes. Anything else fails
// the job as before.
func (w *FeedbackEmbeddingWorker) handleSetEmbeddingError(
	ctx context.Context,
	err error,
	log *slog.Logger,
	start time.Time,
	record *models.FeedbackRecord,
	model string,
	inputKind models.EmbeddingInputKind,
	attempt int,
	isLastAttempt bool,
	action string,
) error {
	switch {
	case errors.Is(err, huberrors.ErrNotFound):
		if w.metrics != nil {
			w.metrics.RecordEmbeddingOutcome(ctx, "skipped")
			w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), "skipped")
		}

		log.Info("embedding: record gone before write, skipping")

		return nil
	case errors.Is(err, huberrors.ErrEmbeddingSuperseded):
		// The record's content changed while this job ran; the job holding the current content
		// owns the row. A benign no-op — record it under a distinct label so write races stay
		// observable.
		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "superseded")
			w.metrics.RecordEmbeddingOutcome(ctx, "skipped")
			w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), "skipped")
		}

		log.Info("embedding: content changed mid-job, superseded write skipped")

		return nil
	case errors.Is(err, huberrors.ErrTenantWriteConflict):
		outcome := "retry"
		if isLastAttempt {
			outcome = "failed_final"
		}

		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "tenant_write_conflict")
			w.metrics.RecordEmbeddingOutcome(ctx, outcome)
			w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), outcome)
		}

		log.Warn("embedding: tenant data purge in progress, deferring write")

		return fmt.Errorf("%s: %w", action, err)
	default:
		// The returned error makes River retry, so a transient write failure is outcome
		// "retry" until the final attempt (matches the shared enrichment worker).
		outcome := "retry"
		if isLastAttempt {
			outcome = "failed_final"
		}

		if w.metrics != nil {
			w.metrics.RecordWorkerError(ctx, "update_failed")
			w.metrics.RecordEmbeddingOutcome(ctx, outcome)
			w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), outcome)
		}

		if isLastAttempt {
			w.markTaxonomyEmbeddingFailed(
				ctx, log, record, inputKind, model, attempt, false,
				models.EnrichmentFailureReasonWriteFailed,
			)
		}

		log.Error("embedding: "+action+" failed",
			"final_attempt", isLastAttempt,
			"error", err,
		)

		return fmt.Errorf("%s: %w", action, err)
	}
}

// markTaxonomyEmbeddingFailed writes failure bookkeeping only for the translated taxonomy model.
// Raw search embeddings are intentionally outside the taxonomy progress/reconcile contract.
func (w *FeedbackEmbeddingWorker) markTaxonomyEmbeddingFailed(
	ctx context.Context,
	log *slog.Logger,
	record *models.FeedbackRecord,
	inputKind models.EmbeddingInputKind,
	model string,
	attempts int,
	terminal bool,
	reason string,
) {
	if w.failures == nil || record == nil ||
		models.NormalizeEmbeddingInputKind(inputKind) != models.EmbeddingInputKindTaxonomyTranslated {
		return
	}

	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), enrichmentFailureWriteTimeout)
	defer cancel()

	err := w.failures.RecordFailure(writeCtx, models.EnrichmentFailure{
		FeedbackRecordID: record.ID,
		TenantID:         record.TenantID,
		Enrichment:       models.EnrichmentNameTaxonomyEmbedding,
		Terminal:         terminal,
		Reason:           reason,
		Attempts:         attempts,
		ContextKey:       model,
		SourceUpdatedAt:  &record.UpdatedAt,
	})
	if err == nil || errors.Is(err, huberrors.ErrNotFound) || errors.Is(err, huberrors.ErrTenantWriteConflict) {
		return
	}

	if w.metrics != nil {
		w.metrics.RecordWorkerError(ctx, "failure_marker_write_failed")
	}

	log.Error("embedding: could not record taxonomy embedding failure",
		"terminal", terminal,
		"reason", reason,
		"error", err,
	)
}

// handleEmptyText clears the embedding for text fields when value_text is empty, or records skip for non-text.
func (w *FeedbackEmbeddingWorker) handleEmptyText(
	ctx context.Context,
	job *river.Job[service.FeedbackEmbeddingArgs],
	record *models.FeedbackRecord,
	log *slog.Logger,
	start time.Time,
	stillCurrent func(fieldLabel, valueText, valueTextTranslated *string) bool,
) error {
	feedbackRecordID := job.Args.FeedbackRecordID

	if record.FieldType == models.FieldTypeText {
		err := w.embeddingService.SetEmbedding(ctx, feedbackRecordID, job.Args.Model, nil, stillCurrent)
		if err != nil {
			isLastAttempt := job.Attempt >= job.MaxAttempts

			return w.handleSetEmbeddingError(
				ctx,
				err,
				log,
				start,
				record,
				job.Args.Model,
				models.NormalizeEmbeddingInputKind(job.Args.InputKind),
				job.Attempt,
				isLastAttempt,
				"clear feedback record embedding",
			)
		}

		if w.metrics != nil {
			w.metrics.RecordEmbeddingOutcome(ctx, "success")
			w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), "success")
		}

		log.Info("embedding: cleared (empty value_text)")

		return nil
	}

	if w.metrics != nil {
		w.metrics.RecordEmbeddingOutcome(ctx, "skipped")
		w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), "skipped")
	}

	log.Info("embedding: skipped (no value_text)")

	return nil
}
