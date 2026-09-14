package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/models"
)

type stubEmbeddingReconcileRepository struct {
	depth       int
	depthErr    error
	ids         []uuid.UUID
	listErr     error
	listModel   string
	listLimit   int
	retryBefore time.Time
	depthQueue  string
}

func (r *stubEmbeddingReconcileRepository) ListPendingTaxonomyEmbeddingIDs(
	_ context.Context, model string, retryBefore time.Time, limit int,
) ([]uuid.UUID, error) {
	r.listModel = model
	r.listLimit = limit
	r.retryBefore = retryBefore

	if r.listErr != nil {
		return nil, r.listErr
	}

	if len(r.ids) > limit {
		return r.ids[:limit], nil
	}

	return r.ids, nil
}

func (r *stubEmbeddingReconcileRepository) CountRunnableEmbeddingJobs(
	_ context.Context, queue string,
) (int, error) {
	r.depthQueue = queue

	return r.depth, r.depthErr
}

type recordingBatchInserter struct {
	params  []river.InsertManyParams
	results []*rivertype.JobInsertResult
	err     error
}

func (i *recordingBatchInserter) InsertMany(
	_ context.Context, params []river.InsertManyParams,
) ([]*rivertype.JobInsertResult, error) {
	i.params = params

	if i.err != nil {
		return nil, i.err
	}

	if i.results != nil {
		return i.results, nil
	}

	results := make([]*rivertype.JobInsertResult, len(params))
	for index := range results {
		results[index] = &rivertype.JobInsertResult{}
	}

	return results, nil
}

func TestEmbeddingReconcileSweepTopsUpBoundedRepairQueue(t *testing.T) {
	ids := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	repo := &stubEmbeddingReconcileRepository{depth: 98, ids: ids}
	inserter := &recordingBatchInserter{}
	retryAfter := 15 * time.Minute
	reconciler := NewEmbeddingReconcileService(repo, "taxonomy:model", 100, 5, retryAfter)
	reconciler.SetInserter(inserter)

	started := time.Now()
	result, err := reconciler.Sweep(context.Background())
	require.NoError(t, err)
	require.Equal(t, EmbeddingsReconcileQueueName, repo.depthQueue)
	require.Equal(t, "taxonomy:model", repo.listModel)
	require.Equal(t, 2, repo.listLimit, "only the available target-depth room may be queried")
	assert.WithinDuration(t, started.Add(-retryAfter), repo.retryBefore, time.Second)
	require.Len(t, inserter.params, 2)
	assert.Equal(t, EmbeddingReconcileResult{Found: 2, Enqueued: 2, Depth: 98}, result)

	for index, param := range inserter.params {
		args, ok := param.Args.(FeedbackEmbeddingArgs)
		require.True(t, ok)
		assert.Equal(t, ids[index], args.FeedbackRecordID)
		assert.Equal(t, "taxonomy:model", args.Model)
		assert.Equal(t, models.EmbeddingInputKindTaxonomyTranslated, args.InputKind)
		assert.Equal(t, "reconcile", args.ValueTextHash)
		require.NotNil(t, param.InsertOpts)
		assert.Equal(t, EmbeddingsReconcileQueueName, param.InsertOpts.Queue)
		assert.Equal(t, 4, param.InsertOpts.Priority)
		assert.Equal(t, 5, param.InsertOpts.MaxAttempts)
		assert.True(t, param.InsertOpts.UniqueOpts.ByArgs)
		assert.ElementsMatch(t, []rivertype.JobState{
			rivertype.JobStateAvailable,
			rivertype.JobStatePending,
			rivertype.JobStateRetryable,
			rivertype.JobStateRunning,
			rivertype.JobStateScheduled,
		}, param.InsertOpts.UniqueOpts.ByState)
	}
}

func TestEmbeddingReconcileSweepStopsAtTarget(t *testing.T) {
	repo := &stubEmbeddingReconcileRepository{depth: 100}
	inserter := &recordingBatchInserter{}
	reconciler := NewEmbeddingReconcileService(repo, "taxonomy:model", 100, 5, 15*time.Minute)
	reconciler.SetInserter(inserter)

	result, err := reconciler.Sweep(context.Background())
	require.NoError(t, err)
	assert.True(t, result.AtTarget)
	assert.Equal(t, 100, result.Depth)
	assert.Zero(t, repo.listLimit, "a full lane must not scan the record backlog")
	assert.Empty(t, inserter.params)
}

func TestEmbeddingReconcileSweepCountsUniqueSkipsTruthfully(t *testing.T) {
	repo := &stubEmbeddingReconcileRepository{
		ids: []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())},
	}
	inserter := &recordingBatchInserter{results: []*rivertype.JobInsertResult{
		{},
		{UniqueSkippedAsDuplicate: true},
	}}
	reconciler := NewEmbeddingReconcileService(repo, "taxonomy:model", 2, 5, 15*time.Minute)
	reconciler.SetInserter(inserter)

	result, err := reconciler.Sweep(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, result.Found)
	assert.Equal(t, 1, result.Enqueued)
}

func TestEmbeddingReconcileSweepPropagatesBoundedFailures(t *testing.T) {
	t.Run("inserter is required", func(t *testing.T) {
		reconciler := NewEmbeddingReconcileService(
			&stubEmbeddingReconcileRepository{}, "taxonomy:model", 100, 5, 15*time.Minute)
		_, err := reconciler.Sweep(context.Background())
		require.ErrorIs(t, err, ErrEmbeddingReconcileInserterUnset)
	})

	t.Run("depth read", func(t *testing.T) {
		reconciler := NewEmbeddingReconcileService(
			&stubEmbeddingReconcileRepository{depthErr: errors.New("db unavailable")},
			"taxonomy:model", 100, 5, 15*time.Minute)
		reconciler.SetInserter(&recordingBatchInserter{})
		_, err := reconciler.Sweep(context.Background())
		require.ErrorContains(t, err, "read repair queue depth")
	})

	t.Run("record scan", func(t *testing.T) {
		reconciler := NewEmbeddingReconcileService(
			&stubEmbeddingReconcileRepository{listErr: errors.New("db unavailable")},
			"taxonomy:model", 100, 5, 15*time.Minute)
		reconciler.SetInserter(&recordingBatchInserter{})
		_, err := reconciler.Sweep(context.Background())
		require.ErrorContains(t, err, "list pending taxonomy embeddings")
	})

	t.Run("batch insert", func(t *testing.T) {
		reconciler := NewEmbeddingReconcileService(
			&stubEmbeddingReconcileRepository{ids: []uuid.UUID{uuid.Must(uuid.NewV7())}},
			"taxonomy:model", 100, 5, 15*time.Minute)
		reconciler.SetInserter(&recordingBatchInserter{err: errors.New("river unavailable")})
		_, err := reconciler.Sweep(context.Background())
		require.ErrorContains(t, err, "enqueue taxonomy embedding repairs")
	})
}
