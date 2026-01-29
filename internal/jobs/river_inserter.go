package jobs

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// RiverJobInserter implements JobInserter using the River client.
type RiverJobInserter struct {
	client *river.Client[pgx.Tx]
}

// NewRiverJobInserter creates a new River-based job inserter.
func NewRiverJobInserter(client *river.Client[pgx.Tx]) *RiverJobInserter {
	return &RiverJobInserter{client: client}
}

// InsertEmbeddingJob enqueues an embedding generation job with uniqueness constraints.
func (r *RiverJobInserter) InsertEmbeddingJob(ctx context.Context, args EmbeddingJobArgs) error {
	_, err := r.client.Insert(ctx, args, &river.InsertOpts{
		UniqueOpts: river.UniqueOpts{
			// Only one pending job per record (by args)
			ByArgs: true,
			// Consider jobs in these states for deduplication
			// Note: JobStatePending is required by River when using ByState
			ByState: []rivertype.JobState{
				rivertype.JobStatePending,
				rivertype.JobStateAvailable,
				rivertype.JobStateRunning,
				rivertype.JobStateRetryable,
				rivertype.JobStateScheduled,
			},
		},
	})
	return err
}
