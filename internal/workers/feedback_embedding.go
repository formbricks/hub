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
	maxInputBytes    int    // cap on the text sent to the provider, in bytes; 0 = unlimited
	metrics          observability.EmbeddingMetrics
	jobTimeout       time.Duration
	failures         FailureRecorder
	failureMetrics   observability.EnrichmentFailureMetrics
	trimmedMetrics   observability.EnrichmentTrimmedMetrics
}

const defaultEmbeddingJobTimeout = 60 * time.Second

// feedbackEmbeddingService is the minimal interface needed by the worker.
type feedbackEmbeddingService interface {
	GetFeedbackRecord(ctx context.Context, id uuid.UUID) (*models.FeedbackRecord, error)
	SetEmbedding(
		ctx context.Context, feedbackRecordID uuid.UUID, model string, inputKind models.EmbeddingInputKind,
		embedding []float32, stillCurrent func(fieldLabel, valueText, valueTextTranslated *string) bool,
	) error
	EmbeddingIsCurrent(
		ctx context.Context, feedbackRecordID uuid.UUID, model string, inputKind models.EmbeddingInputKind,
	) (bool, error)
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
		defaultEmbeddingJobTimeout, nil, nil, 0, nil,
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
	maxInputBytes int,
	trimmedMetrics observability.EnrichmentTrimmedMetrics,
) *FeedbackEmbeddingWorker {
	if jobTimeout <= 0 {
		jobTimeout = defaultEmbeddingJobTimeout
	}

	return &FeedbackEmbeddingWorker{
		embeddingService: embeddingService,
		embeddingClient:  embeddingClient,
		docPrefix:        docPrefix,
		maxInputBytes:    maxInputBytes,
		metrics:          metrics,
		jobTimeout:       jobTimeout,
		failures:         failures,
		failureMetrics:   failureMetrics,
		trimmedMetrics:   trimmedMetrics,
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
	inputCap := service.TextCap{MaxBytes: w.maxInputBytes}

	text, trimmed := service.BuildEmbeddingInputForKind(record, inputKind, w.docPrefix, inputCap)
	if trimmed {
		recordTrimmed(ctx, w.trimmedMetrics, inputKind.EnrichmentName())
	}

	// stillCurrent lets the repository verify, atomically with the write, that the content this
	// job embedded is still the record's content — so of two concurrent jobs for one record, the
	// stale one skips instead of clobbering the newer vector (last-write-wins would attach an old
	// text's embedding until the repair sweep noticed its source_hash). It rebuilds
	// through the same capped builder, so the comparison is between the two strings the provider
	// would actually have been sent.
	stillCurrent := func(fieldLabel, valueText, valueTextTranslated *string) bool {
		current, _ := service.BuildEmbeddingInputFromValues(
			fieldLabel, valueText, valueTextTranslated, inputKind, w.docPrefix, inputCap)

		return current == text
	}

	if text == "" {
		return w.handleEmptyText(ctx, job, record, log, start, stillCurrent)
	}

	// A repair or backfill job that lands after a live job already embedded this content, or a job
	// re-run after a worker died between the write and River marking it complete, finds the vector
	// current: skip the provider call. A failed check only costs the call it would have saved.
	current, err := w.embeddingService.EmbeddingIsCurrent(ctx, args.FeedbackRecordID, args.Model, inputKind)
	if err != nil {
		log.Warn("embedding: could not check whether the vector is current, embedding anyway", "error", err)
	} else if current {
		if w.metrics != nil {
			w.metrics.RecordEmbeddingOutcome(ctx, "skipped")
			w.metrics.RecordEmbeddingDuration(ctx, time.Since(start), "skipped")
		}

		log.Info("embedding: vector already current, skipping")

		return nil
	}

	embedding, err := w.embeddingClient.CreateEmbedding(ctx, text)
	if err != nil {
		return w.handleEmbedError(ctx, err, job, record, log, start)
	}

	err = w.embeddingService.SetEmbedding(ctx, args.FeedbackRecordID, args.Model, inputKind, embedding, stillCurrent)
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
			w.failureMetrics.RecordTerminalFailure(ctx, inputKind.EnrichmentName(), string(reason))
		}

		w.markEmbeddingFailed(
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
		w.markEmbeddingFailed(
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
			w.markEmbeddingFailed(
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

// markEmbeddingFailed writes the durable failure marker the repair sweep reads, keyed by the exact
// model and the record revision, for both the raw search embedding and the taxonomy embedding.
func (w *FeedbackEmbeddingWorker) markEmbeddingFailed(
	ctx context.Context,
	log *slog.Logger,
	record *models.FeedbackRecord,
	inputKind models.EmbeddingInputKind,
	model string,
	attempts int,
	terminal bool,
	reason string,
) {
	if w.failures == nil || record == nil {
		return
	}

	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), enrichmentFailureWriteTimeout)
	defer cancel()

	err := w.failures.RecordFailure(writeCtx, models.EnrichmentFailure{
		FeedbackRecordID: record.ID,
		TenantID:         record.TenantID,
		Enrichment:       inputKind.EnrichmentName(),
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

	log.Error("embedding: could not record embedding failure",
		"terminal", terminal,
		"reason", reason,
		"error", err,
	)
}

// handleEmptyText clears the embedding when the record no longer has embeddable text, whatever its
// field type: the live path embeds any record carrying text (choice and matrix answers store their
// option label on categorical rows), so any of them can be left holding a vector of text it no
// longer has.
func (w *FeedbackEmbeddingWorker) handleEmptyText(
	ctx context.Context,
	job *river.Job[service.FeedbackEmbeddingArgs],
	record *models.FeedbackRecord,
	log *slog.Logger,
	start time.Time,
	stillCurrent func(fieldLabel, valueText, valueTextTranslated *string) bool,
) error {
	inputKind := models.NormalizeEmbeddingInputKind(job.Args.InputKind)

	err := w.embeddingService.SetEmbedding(ctx, job.Args.FeedbackRecordID, job.Args.Model, inputKind, nil, stillCurrent)
	if err != nil {
		isLastAttempt := job.Attempt >= job.MaxAttempts

		return w.handleSetEmbeddingError(
			ctx,
			err,
			log,
			start,
			record,
			job.Args.Model,
			inputKind,
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
