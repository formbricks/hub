package service

import (
	"context"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// RiverJobInserter inserts a single River job. It is the shared seam the enrichment
// providers (embedding, translation) and their backfill flows use to enqueue work;
// satisfied by the River client.
type RiverJobInserter interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// RiverBatchInserter inserts a bounded group of River jobs. The concrete River client satisfies
// both inserter interfaces; keeping this seam small makes reconciliation tests independent of a
// database-backed queue.
type RiverBatchInserter interface {
	InsertMany(ctx context.Context, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error)
}
