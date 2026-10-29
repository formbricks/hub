package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// EmbeddingEnqueuer inserts the embedding jobs a feedback-record write owes inside that write's
// transaction, so the jobs commit with the write or not at all (repository.RecordWriteHook).
//
// Embeddings are the one enrichment with no other way to notice a lost job for a vector that
// already exists: a lost re-embed leaves the old vector in place, describing text the record no
// longer has. That is why they leave the best-effort event channel, which the webhook and classify
// providers still use.
type EmbeddingEnqueuer struct {
	inserter  RiverTxBatchInserter
	providers []*EmbeddingProvider
}

// NewEmbeddingEnqueuer creates an enqueuer for the given models; typically the raw search model
// and, when taxonomy is configured, the translated taxonomy model.
func NewEmbeddingEnqueuer(inserter RiverTxBatchInserter, providers ...*EmbeddingProvider) *EmbeddingEnqueuer {
	return &EmbeddingEnqueuer{inserter: inserter, providers: providers}
}

// EnqueueTx inserts, in tx, every embedding job event owes. An error must abort the write.
func (e *EmbeddingEnqueuer) EnqueueTx(ctx context.Context, dbTx pgx.Tx, event Event) error {
	params := make([]river.InsertManyParams, 0, len(e.providers))
	owners := make([]*EmbeddingProvider, 0, len(e.providers))

	for _, provider := range e.providers {
		if job, ok := provider.jobFor(event); ok {
			params = append(params, job)
			owners = append(owners, provider)
		}
	}

	return e.insert(ctx, dbTx, params, owners, event.ID, recordIDFromEventData(event.Data))
}

// EnqueueTranslationTx inserts, in tx, the taxonomy re-embed a translation write owes: the
// taxonomy input prefers translated text, so a new (or cleared) translation changes it.
func (e *EmbeddingEnqueuer) EnqueueTranslationTx(ctx context.Context, dbTx pgx.Tx, feedbackRecordID uuid.UUID) error {
	params := make([]river.InsertManyParams, 0, 1)
	owners := make([]*EmbeddingProvider, 0, 1)

	for _, provider := range e.providers {
		if job, ok := provider.translationJob(feedbackRecordID); ok {
			params = append(params, job)
			owners = append(owners, provider)
		}
	}

	return e.insert(ctx, dbTx, params, owners, uuid.Nil, feedbackRecordID)
}

// insert writes params in one statement. Counters are recorded at insert time: a transaction that
// later fails to commit overcounts by its jobs, which is rarer and cheaper than threading commit
// outcomes back here.
func (e *EmbeddingEnqueuer) insert(
	ctx context.Context, dbTx pgx.Tx, params []river.InsertManyParams, owners []*EmbeddingProvider,
	eventID, feedbackRecordID uuid.UUID,
) error {
	if len(params) == 0 {
		return nil
	}

	if _, err := e.inserter.InsertManyTx(ctx, dbTx, params); err != nil {
		for _, owner := range owners {
			if owner.metrics != nil {
				owner.metrics.RecordProviderError(ctx, "enqueue_failed")
			}
		}

		slog.ErrorContext(ctx, "embedding: enqueue failed, write rolled back",
			"event_id", eventID,
			"feedback_record_id", feedbackRecordID,
			"error", err,
		)

		return fmt.Errorf("enqueue embedding jobs: %w", err)
	}

	for _, owner := range owners {
		if owner.metrics != nil {
			owner.metrics.RecordJobsEnqueued(ctx, 1)
		}
	}

	slog.InfoContext(ctx, "embedding: jobs enqueued",
		"event_id", eventID,
		"feedback_record_id", feedbackRecordID,
		"jobs", len(params),
	)

	return nil
}
