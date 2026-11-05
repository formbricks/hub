package service

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// RiverJobInserter inserts a single River job. It is the shared seam the enrichment
// providers (embedding, translation) and their backfill flows use to enqueue work;
// satisfied by the River client.
type RiverJobInserter interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// RiverBatchInserter inserts many River jobs in one round trip. Shared by the webhook fan-out and
// the enrichment reconcile sweep, both of which enqueue in batches; the concrete River client
// satisfies it, and keeping the seam this small is what lets their tests run without a
// database-backed queue.
//
// InsertMany, never InsertManyFast. The fast variant skips the unique-options machinery and fails
// the entire batch on a conflict, and callers here depend on uniqueness to avoid enqueueing work
// that is already in flight.
type RiverBatchInserter interface {
	InsertMany(ctx context.Context, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error)
}

// RiverTxBatchInserter inserts River jobs inside a caller's transaction, so they commit or roll back
// with the write that owes them (see repository.RecordWriteHook). The River client satisfies it.
type RiverTxBatchInserter interface {
	InsertManyTx(ctx context.Context, tx pgx.Tx, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error)
}
