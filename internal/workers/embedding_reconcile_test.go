package workers

import (
	"context"
	"errors"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/service"
)

type stubEmbeddingReconcileWorkerSweeper struct {
	result service.EmbeddingReconcileResult
	err    error
}

func (s stubEmbeddingReconcileWorkerSweeper) Sweep(
	context.Context,
) (service.EmbeddingReconcileResult, error) {
	return s.result, s.err
}

func embeddingReconcileJob() *river.Job[service.EmbeddingReconcileArgs] {
	return &river.Job[service.EmbeddingReconcileArgs]{
		JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 1},
	}
}

func TestEmbeddingReconcileWorkerRecordsEnqueuedJobs(t *testing.T) {
	metrics := newCountingEmbeddingMetrics()
	worker := NewEmbeddingReconcileWorker(stubEmbeddingReconcileWorkerSweeper{
		result: service.EmbeddingReconcileResult{Found: 5, Enqueued: 3},
	}, metrics)

	require.NoError(t, worker.Work(context.Background(), embeddingReconcileJob()))
	assert.Equal(t, int64(3), metrics.jobsEnqueued)
	assert.Zero(t, metrics.workerErr["reconcile_failed"])
}

func TestEmbeddingReconcileWorkerRecordsSweepFailure(t *testing.T) {
	metrics := newCountingEmbeddingMetrics()
	worker := NewEmbeddingReconcileWorker(stubEmbeddingReconcileWorkerSweeper{
		err: errors.New("database unavailable"),
	}, metrics)

	err := worker.Work(context.Background(), embeddingReconcileJob())
	require.ErrorContains(t, err, "embedding reconcile")
	assert.Equal(t, 1, metrics.workerErr["reconcile_failed"])
	assert.Zero(t, metrics.jobsEnqueued)
}
