package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// classifyBackfillPageSize bounds how many records a classify backfill lists and enqueues per
// keyset page, so neither the global scan nor the enqueue loop materializes the full result set.
const classifyBackfillPageSize = 500

// classifyBackfillUniquePeriod dedupes a classify backfill's jobs by (record, run) within this
// window, so a rescued or retried fan-out cannot double-enqueue pages it already inserted. The
// event-driven classify path deliberately carries no UniqueOpts (River's completed state would
// swallow legitimate re-enrichment), but a backfill enumerates a bounded set once per run, where
// the per-run guard is safe and wanted.
const classifyBackfillUniquePeriod = 24 * time.Hour

// classifyBackfillInsertOpts is the shared River insert config for classify backfill jobs:
// per-record dedup by (record, run) within the window, so a rescued or retried fan-out cannot
// double-enqueue the pages it already inserted.
func classifyBackfillInsertOpts(queueName string, maxAttempts int) *river.InsertOpts {
	return &river.InsertOpts{
		Queue:       queueName,
		MaxAttempts: maxAttempts,
		UniqueOpts:  river.UniqueOpts{ByArgs: true, ByPeriod: classifyBackfillUniquePeriod},
	}
}

// BackfillSentiment enqueues a sentiment job for every eligible feedback record (across all
// tenants) whose sentiment is not yet set, streaming the targets in keyset pages. Used by the
// one-off backfill command. runID discriminates this run's jobs from earlier runs' (the worker
// re-checks the per-tenant gate, so a since-disabled tenant is skipped there rather than here).
// Returns the number of jobs enqueued.
func (s *FeedbackRecordsService) BackfillSentiment(
	ctx context.Context, inserter RiverJobInserter, queueName string, maxAttempts int, runID string,
) (int, error) {
	return s.backfillClassifyPaged(ctx, inserter, classifyBackfillInsertOpts(queueName, maxAttempts),
		"sentiment", runID,
		func(afterID uuid.UUID) ([]uuid.UUID, error) {
			ids, err := s.repo.ListSentimentBackfillTargets(ctx, afterID, classifyBackfillPageSize)
			if err != nil {
				return nil, fmt.Errorf("list sentiment backfill targets: %w", err)
			}

			return ids, nil
		},
		func(recordID uuid.UUID, hash string) river.JobArgs {
			return FeedbackSentimentArgs{FeedbackRecordID: recordID, ValueTextHash: hash}
		})
}

// BackfillEmotions enqueues an emotions job for every eligible feedback record (across all tenants)
// whose emotions are not yet set, streaming the targets in keyset pages. See BackfillSentiment.
func (s *FeedbackRecordsService) BackfillEmotions(
	ctx context.Context, inserter RiverJobInserter, queueName string, maxAttempts int, runID string,
) (int, error) {
	return s.backfillClassifyPaged(ctx, inserter, classifyBackfillInsertOpts(queueName, maxAttempts),
		"emotions", runID,
		func(afterID uuid.UUID) ([]uuid.UUID, error) {
			ids, err := s.repo.ListEmotionsBackfillTargets(ctx, afterID, classifyBackfillPageSize)
			if err != nil {
				return nil, fmt.Errorf("list emotions backfill targets: %w", err)
			}

			return ids, nil
		},
		func(recordID uuid.UUID, hash string) river.JobArgs {
			return FeedbackEmotionsArgs{FeedbackRecordID: recordID, ValueTextHash: hash}
		})
}

// backfillClassifyPaged enqueues a classify job for every record id produced by fetchPage,
// streaming in keyset pages (so the full set is never materialized) and stopping on the first
// short page. buildArgs turns a record id + the run-scoped dedupe hash ("backfill:<runID>") into
// the per-type job args. Advancing the cursor by the last id seen means even a fully-deduped page
// cannot livelock the loop. Unique-skipped duplicates are counted separately, never as enqueued;
// the loop stops on the first insert error, returning what was enqueued so far.
func (s *FeedbackRecordsService) backfillClassifyPaged(
	ctx context.Context,
	inserter RiverJobInserter,
	opts *river.InsertOpts,
	name, runID string,
	fetchPage func(afterID uuid.UUID) ([]uuid.UUID, error),
	buildArgs func(recordID uuid.UUID, hash string) river.JobArgs,
) (int, error) {
	hash := "backfill:" + runID

	return backfillPaged(name, classifyBackfillPageSize, fetchPage,
		func(id uuid.UUID) uuid.UUID { return id },
		func(ids []uuid.UUID) (int, int, error) {
			inserted, skipped := 0, 0

			for _, id := range ids {
				res, insErr := inserter.Insert(ctx, buildArgs(id, hash), opts)
				if insErr != nil {
					return inserted, skipped, fmt.Errorf("enqueue %s backfill job for %s: %w", name, id, insErr)
				}

				if res != nil && res.UniqueSkippedAsDuplicate {
					skipped++

					continue
				}

				inserted++
			}

			return inserted, skipped, nil
		})
}

// backfillPaged is the one keyset-paged backfill loop shared by the classify and translation
// backfills: stream pages, insert each, count duplicate-skips, advance the cursor by the last
// item seen (so a fully-deduped page cannot livelock), and stop on the first short page.
func backfillPaged[T any](
	name string,
	pageSize int,
	fetchPage func(afterID uuid.UUID) ([]T, error),
	cursorID func(item T) uuid.UUID,
	insertPage func(items []T) (inserted, skipped int, err error),
) (int, error) {
	enqueued := 0
	skipped := 0
	afterID := uuid.Nil

	for {
		items, err := fetchPage(afterID)
		if err != nil {
			return enqueued, err
		}

		if len(items) == 0 {
			break
		}

		inserted, duplicates, err := insertPage(items)
		enqueued += inserted
		skipped += duplicates

		if err != nil {
			return enqueued, err
		}

		afterID = cursorID(items[len(items)-1])

		if len(items) < pageSize {
			break
		}
	}

	if skipped > 0 {
		slog.Info(name+" backfill: duplicate jobs skipped by unique insert",
			"skipped", skipped, "enqueued", enqueued)
	}

	return enqueued, nil
}
