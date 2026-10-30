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
	idsByModel  map[string][]uuid.UUID
	listCalls   []listCall
}

func (r *stubEmbeddingReconcileRepository) ListPendingEmbeddingIDs(
	_ context.Context, model string, inputKind models.EmbeddingInputKind, retryBefore time.Time, limit int,
) ([]uuid.UUID, error) {
	r.listModel = model
	r.listLimit = limit
	r.retryBefore = retryBefore
	r.listCalls = append(r.listCalls, listCall{model: model, inputKind: inputKind, limit: limit})

	if r.listErr != nil {
		return nil, r.listErr
	}

	ids := r.ids
	if byModel, ok := r.idsByModel[model]; ok {
		ids = byModel
	}

	if len(ids) > limit {
		return ids[:limit], nil
	}

	return ids, nil
}

type listCall struct {
	model     string
	inputKind models.EmbeddingInputKind
	limit     int
}

// taxonomyOnly is the single-target configuration the original sweep tests were written against.
var taxonomyOnly = []EmbeddingReconcileTarget{
	{Model: "taxonomy:model", InputKind: models.EmbeddingInputKindTaxonomyTranslated},
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
	reconciler := NewEmbeddingReconcileService(repo, taxonomyOnly, 100, 5, retryAfter)
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
		assert.Equal(t, ValueTextHashRepair, args.ValueTextHash)
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
	reconciler := NewEmbeddingReconcileService(repo, taxonomyOnly, 100, 5, 15*time.Minute)
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
	reconciler := NewEmbeddingReconcileService(repo, taxonomyOnly, 2, 5, 15*time.Minute)
	reconciler.SetInserter(inserter)

	result, err := reconciler.Sweep(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, result.Found)
	assert.Equal(t, 1, result.Enqueued)
}

func TestEmbeddingReconcileSweepPropagatesBoundedFailures(t *testing.T) {
	t.Run("inserter is required", func(t *testing.T) {
		reconciler := NewEmbeddingReconcileService(
			&stubEmbeddingReconcileRepository{}, taxonomyOnly, 100, 5, 15*time.Minute)
		_, err := reconciler.Sweep(context.Background())
		require.ErrorIs(t, err, ErrEmbeddingReconcileInserterUnset)
	})

	t.Run("depth read", func(t *testing.T) {
		reconciler := NewEmbeddingReconcileService(
			&stubEmbeddingReconcileRepository{depthErr: errors.New("db unavailable")},
			taxonomyOnly, 100, 5, 15*time.Minute)
		reconciler.SetInserter(&recordingBatchInserter{})
		_, err := reconciler.Sweep(context.Background())
		require.ErrorContains(t, err, "read repair queue depth")
	})

	t.Run("record scan", func(t *testing.T) {
		reconciler := NewEmbeddingReconcileService(
			&stubEmbeddingReconcileRepository{listErr: errors.New("db unavailable")},
			taxonomyOnly, 100, 5, 15*time.Minute)
		reconciler.SetInserter(&recordingBatchInserter{})
		_, err := reconciler.Sweep(context.Background())
		require.ErrorContains(t, err, "list pending embeddings")
	})

	t.Run("batch insert", func(t *testing.T) {
		reconciler := NewEmbeddingReconcileService(
			&stubEmbeddingReconcileRepository{ids: []uuid.UUID{uuid.Must(uuid.NewV7())}},
			taxonomyOnly, 100, 5, 15*time.Minute)
		reconciler.SetInserter(&recordingBatchInserter{err: errors.New("river unavailable")})
		_, err := reconciler.Sweep(context.Background())
		require.ErrorContains(t, err, "enqueue embedding repairs")
	})
}

// TestEmbeddingReconcileSweepSharesRoomAcrossTargets keeps a big backlog on one embedding from
// starving repair of the other: each target may claim an equal share of what is left, and a target
// with less backlog than its share leaves the rest to the next.
func TestEmbeddingReconcileSweepSharesRoomAcrossTargets(t *testing.T) {
	newIDs := func(n int) []uuid.UUID {
		ids := make([]uuid.UUID, n)
		for i := range ids {
			ids[i] = uuid.Must(uuid.NewV7())
		}

		return ids
	}

	targets := []EmbeddingReconcileTarget{
		{Model: "raw", InputKind: models.EmbeddingInputKindRaw},
		{Model: "taxonomy:model", InputKind: models.EmbeddingInputKindTaxonomyTranslated},
	}

	t.Run("both backlogged: half each", func(t *testing.T) {
		repo := &stubEmbeddingReconcileRepository{idsByModel: map[string][]uuid.UUID{
			"raw": newIDs(500), "taxonomy:model": newIDs(500),
		}}
		inserter := &recordingBatchInserter{}
		reconciler := NewEmbeddingReconcileService(repo, targets, 100, 5, 15*time.Minute)
		reconciler.SetInserter(inserter)

		result, err := reconciler.Sweep(context.Background())
		require.NoError(t, err)
		assert.Equal(t, []listCall{
			{model: "raw", inputKind: models.EmbeddingInputKindRaw, limit: 50},
			{model: "taxonomy:model", inputKind: models.EmbeddingInputKindTaxonomyTranslated, limit: 50},
		}, repo.listCalls)
		assert.Equal(t, 100, result.Enqueued)
	})

	t.Run("a single free slot alternates between targets", func(t *testing.T) {
		repo := &stubEmbeddingReconcileRepository{depth: 99, idsByModel: map[string][]uuid.UUID{
			"raw": newIDs(5), "taxonomy:model": newIDs(5),
		}}
		reconciler := NewEmbeddingReconcileService(repo, targets, 100, 5, 15*time.Minute)
		reconciler.SetInserter(&recordingBatchInserter{})

		served := []string{}

		for range 2 {
			repo.listCalls = nil
			_, err := reconciler.Sweep(context.Background())
			require.NoError(t, err)

			for _, call := range repo.listCalls {
				if call.limit > 0 {
					served = append(served, call.model)
				}
			}
		}

		assert.ElementsMatch(t, []string{"raw", "taxonomy:model"}, served)
	})

	t.Run("unused share flows to the next target", func(t *testing.T) {
		repo := &stubEmbeddingReconcileRepository{idsByModel: map[string][]uuid.UUID{
			"raw": newIDs(10), "taxonomy:model": newIDs(500),
		}}
		inserter := &recordingBatchInserter{}
		reconciler := NewEmbeddingReconcileService(repo, targets, 100, 5, 15*time.Minute)
		reconciler.SetInserter(inserter)

		result, err := reconciler.Sweep(context.Background())
		require.NoError(t, err)
		require.Len(t, repo.listCalls, 2)
		assert.Equal(t, 90, repo.listCalls[1].limit)
		assert.Equal(t, 100, result.Enqueued)

		kinds := map[models.EmbeddingInputKind]int{}

		for _, param := range inserter.params {
			args, ok := param.Args.(FeedbackEmbeddingArgs)
			require.True(t, ok)

			kinds[args.InputKind]++
		}

		assert.Equal(t, map[models.EmbeddingInputKind]int{
			models.EmbeddingInputKindRaw:                10,
			models.EmbeddingInputKindTaxonomyTranslated: 90,
		}, kinds)
	})
}
