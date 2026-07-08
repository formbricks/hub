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
}

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
	return &FeedbackEmbeddingWorker{
		embeddingService: embeddingService,
		embeddingClient:  embeddingClient,
		docPrefix:        docPrefix,
		metrics:          metrics,
	}
}

// Timeout limits how long a single embedding job can run.
func (w *FeedbackEmbeddingWorker) Timeout(*river.Job[service.FeedbackEmbeddingArgs]) time.Duration {
	return enrichmentJobTimeout
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
		return w.handleEmbedError(ctx, err, job, log, start)
	}

	err = w.embeddingService.SetEmbedding(ctx, args.FeedbackRecordID, args.Model, embedding, stillCurrent)
	if err != nil {
		isLastAttempt := job.Attempt >= job.MaxAttempts

		return w.handleSetEmbeddingError(ctx, err, log, start, isLastAttempt, "set feedback record embedding")
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
	ctx context.Context, err error, job *river.Job[service.FeedbackEmbeddingArgs], log *slog.Logger, start time.Time,
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

		log.Error("embedding: "+action+" failed",
			"final_attempt", isLastAttempt,
			"error", err,
		)

		return fmt.Errorf("%s: %w", action, err)
	}
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

			return w.handleSetEmbeddingError(ctx, err, log, start, isLastAttempt, "clear feedback record embedding")
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
