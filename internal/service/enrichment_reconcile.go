package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/riverqueue/river"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
)

// ErrReconcileInserterUnset marks a sweep attempted before the River client was attached.
var ErrReconcileInserterUnset = errors.New("enrichment reconcile: inserter not set")

// EnrichmentReconcileRepository is the pending-set half: which records still owe an enrichment,
// and how deep each backfill queue currently is.
type EnrichmentReconcileRepository interface {
	ListPendingEnrichment(
		ctx context.Context, enrichment, defaultLang string, limit int,
	) ([]repository.PendingEnrichmentTarget, error)
	CountRunnableByQueue(ctx context.Context, queues []string) (map[string]int64, error)
}

// EnrichmentReconcileService tops the backfill queues up towards a target depth with records that
// are still owed an enrichment.
//
// It is the level-triggered half of enrichment coverage. The event path enqueues a job when a
// record changes and is what makes enrichment fast; this finds what that path missed — a record
// created while the provider was unconfigured, a job lost to a crash, a transient failure that
// used up its retries — and is what makes enrichment eventually COMPLETE.
type EnrichmentReconcileService struct {
	repo        EnrichmentReconcileRepository
	inserter    RiverBatchInserter
	defaultLang string
	targetDepth int
	// enabled lists the enrichments this deployment has a provider for. An enrichment nobody
	// configured is not pending work, it is switched off, and sweeping for it would enqueue jobs
	// whose worker is not even registered.
	enabled []string
	// maxAttempts per enrichment, mirroring the live path so a reconciled job is retried the same
	// number of times as an event-driven one.
	maxAttempts map[string]int
}

// NewEnrichmentReconcileServiceParams configures the sweep.
type NewEnrichmentReconcileServiceParams struct {
	Repo        EnrichmentReconcileRepository
	Inserter    RiverBatchInserter
	DefaultLang string
	TargetDepth int
	Enabled     []string
	MaxAttempts map[string]int
}

// NewEnrichmentReconcileService creates the reconcile service.
func NewEnrichmentReconcileService(params NewEnrichmentReconcileServiceParams) *EnrichmentReconcileService {
	return &EnrichmentReconcileService{
		repo:        params.Repo,
		inserter:    params.Inserter,
		defaultLang: params.DefaultLang,
		targetDepth: params.TargetDepth,
		enabled:     params.Enabled,
		maxAttempts: params.MaxAttempts,
	}
}

// SetInserter attaches the River client after construction.
//
// The client cannot exist yet when this service is built: River needs the worker registry, the
// registry needs this service as its sweeper, and this service needs the client to enqueue. The
// same knot is untied the same way for the embedding inserter (see SetEmbeddingInserter).
//
// A sweep with no inserter would silently enqueue nothing, so Sweep refuses rather than reporting
// a successful zero.
func (s *EnrichmentReconcileService) SetInserter(inserter RiverBatchInserter) {
	s.inserter = inserter
}

// ReconcileResult reports what one sweep did, per enrichment.
type ReconcileResult struct {
	// Enqueued counts jobs actually inserted — after River dropped the ones already queued, so
	// this is new work rather than attempts.
	Enqueued map[string]int
	// Skipped names enrichments whose queue was already at or above the target depth. A sweep that
	// skips everything is the steady state once a backlog is draining, not a problem.
	Skipped []string
}

// Sweep tops up every configured enrichment's backfill queue.
//
// Topping up TO a depth rather than enqueueing AT a rate is what makes this self-regulating: each
// tick can only add what the workers have already drained, so river_job stays bounded whether the
// backlog is a thousand records or fifty million. A rate would have to be guessed against drain
// speed, and guessing low is indistinguishable from the reconciler not running at all.
//
// An error on one enrichment does not abandon the others: they are independent backlogs, and a
// translation-specific query failure should not leave sentiment un-swept.
func (s *EnrichmentReconcileService) Sweep(ctx context.Context) (ReconcileResult, error) {
	result := ReconcileResult{Enqueued: map[string]int{}}

	// Refuse rather than report a successful sweep that enqueued nothing: an unset inserter is a
	// wiring mistake, and the symptom — coverage quietly never converging — is the exact failure
	// this service exists to prevent.
	if s.inserter == nil {
		return result, ErrReconcileInserterUnset
	}

	queues := make([]string, 0, len(s.enabled))
	for _, enrichment := range s.enabled {
		if queue := BackfillQueueFor(enrichment); queue != "" {
			queues = append(queues, queue)
		}
	}

	if len(queues) == 0 {
		return result, nil
	}

	depths, err := s.repo.CountRunnableByQueue(ctx, queues)
	if err != nil {
		return result, fmt.Errorf("read backfill queue depths: %w", err)
	}

	var errs []error

	for _, enrichment := range s.enabled {
		queue := BackfillQueueFor(enrichment)
		if queue == "" {
			continue
		}

		room := s.targetDepth - int(depths[queue])
		if room <= 0 {
			result.Skipped = append(result.Skipped, enrichment)

			continue
		}

		enqueued, sweepErr := s.sweepOne(ctx, enrichment, queue, room)
		if sweepErr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", enrichment, sweepErr))

			continue
		}

		result.Enqueued[enrichment] = enqueued
	}

	if len(errs) > 0 {
		return result, fmt.Errorf("reconcile sweep: %w", errors.Join(errs...))
	}

	return result, nil
}

// sweepOne fills one enrichment's queue up to room more jobs.
func (s *EnrichmentReconcileService) sweepOne(
	ctx context.Context, enrichment, queue string, room int,
) (int, error) {
	targets, err := s.repo.ListPendingEnrichment(ctx, enrichment, s.defaultLang, room)
	if err != nil {
		return 0, fmt.Errorf("list pending: %w", err)
	}

	if len(targets) == 0 {
		return 0, nil
	}

	params := make([]river.InsertManyParams, 0, len(targets))

	for _, target := range targets {
		args, argsErr := reconcileArgsFor(enrichment, target)
		if argsErr != nil {
			return 0, argsErr
		}

		params = append(params, river.InsertManyParams{
			Args: args,
			InsertOpts: &river.InsertOpts{
				Queue:       queue,
				MaxAttempts: s.maxAttempts[enrichment],
				// Uniqueness across the IN-FLIGHT states, so a record the event path already
				// queued is not enqueued a second time. `retryable` is in the set on purpose: a
				// job waiting out its backoff will run again by itself, and re-enqueueing it would
				// double the provider calls for a record that is already being handled.
				//
				// `completed` must stay OUT. River's default set includes it, and with no ByPeriod
				// the window is unbounded — the first sweep of a record would be the only one that
				// ever ran, so a record that failed after a successful enrichment could never be
				// picked up again.
				UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: InFlightUniqueStates()},
			},
		})
	}

	results, err := s.inserter.InsertMany(ctx, params)
	if err != nil {
		return 0, fmt.Errorf("enqueue: %w", err)
	}

	inserted := 0

	for _, res := range results {
		// A job River skipped as a duplicate is not new work. Counting it would make a sweep that
		// achieved nothing look busy, which is the opposite of what this number is for.
		if res != nil && !res.UniqueSkippedAsDuplicate {
			inserted++
		}
	}

	slog.InfoContext(ctx, "enrichment reconcile: enqueued",
		"enrichment", enrichment, "queue", queue, "found", len(targets), "inserted", inserted)

	return inserted, nil
}

// reconcileArgsFor builds the job args for one pending record. The args are deliberately identical
// to the event path's: the same worker handles both lanes, and a reconciled job that differed would
// be a second code path to keep correct.
func reconcileArgsFor(enrichment string, target repository.PendingEnrichmentTarget) (river.JobArgs, error) {
	switch enrichment {
	case models.EnrichmentNameSentiment:
		return FeedbackSentimentArgs{FeedbackRecordID: target.ID}, nil
	case models.EnrichmentNameEmotions:
		return FeedbackEmotionsArgs{FeedbackRecordID: target.ID}, nil
	case models.EnrichmentNameTranslation:
		return FeedbackTranslationArgs{FeedbackRecordID: target.ID, TargetLang: target.TargetLang}, nil
	default:
		return nil, fmt.Errorf("%w: %q", repository.ErrUnknownEnrichment, enrichment)
	}
}
