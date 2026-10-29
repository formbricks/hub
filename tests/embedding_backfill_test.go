package tests

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/database"
)

// countingEmbeddingInserter records the FeedbackEmbeddingArgs jobs enqueued.
type countingEmbeddingInserter struct {
	ids  []uuid.UUID
	args []service.FeedbackEmbeddingArgs
}

func (c *countingEmbeddingInserter) InsertMany(
	_ context.Context, params []river.InsertManyParams,
) ([]*rivertype.JobInsertResult, error) {
	results := make([]*rivertype.JobInsertResult, 0, len(params))

	for _, param := range params {
		if a, ok := param.Args.(service.FeedbackEmbeddingArgs); ok {
			c.ids = append(c.ids, a.FeedbackRecordID)
			c.args = append(c.args, a)
		}

		results = append(results, &rivertype.JobInsertResult{})
	}

	return results, nil
}

func embeddingBackfillRepos(t *testing.T) (*repository.FeedbackRecordsRepository, *repository.EmbeddingsRepository) {
	t.Helper()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(
		context.Background(), cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	t.Cleanup(db.Close)

	return repository.NewFeedbackRecordsRepository(db), repository.NewEmbeddingsRepository(db)
}

// TestListPendingEmbeddings_KeysetPagination locks the single-pass paging the backfill relies on:
// each page is bounded by the limit, ids come strictly ascending with none on two pages, and paging
// from uuid.Nil returns every record that needs an embedding.
// A fresh model is used so the created records are all eligible.
func TestListPendingEmbeddings_KeysetPagination(t *testing.T) {
	ctx := context.Background()
	feedbackRepo, embeddingsRepo := embeddingBackfillRepos(t)

	model := "backfill-keyset-" + uuid.NewString()
	tenant := uuid.NewString()

	makeText := func(value string) uuid.UUID {
		rec, err := feedbackRepo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			SubmissionID: uuid.NewString(),
			TenantID:     tenant,
			FieldID:      "q1",
			FieldType:    models.FieldTypeText,
			ValueText:    &value,
		}, nil)
		require.NoError(t, err)

		return rec.ID
	}

	mine := map[uuid.UUID]bool{}
	for _, value := range []string{"one", "two", "three", "four", "five"} {
		mine[makeText(value)] = true
	}

	seen := map[uuid.UUID]bool{}
	filter := repository.PendingEmbeddingsFilter{
		Model: model, InputKind: models.EmbeddingInputKindRaw, RetryBefore: time.Now(),
	}

	var prev uuid.UUID

	for {
		page, err := embeddingsRepo.ListTenantPendingEmbeddings(ctx, tenant, filter, 2)
		require.NoError(t, err)
		require.LessOrEqual(t, len(page), 2, "LIMIT bounds the page size")

		if len(page) == 0 {
			break
		}

		for _, id := range page {
			require.False(t, seen[id], "a record must not appear on two pages")
			seen[id] = true

			require.Negativef(t, bytes.Compare(prev[:], id[:]), "ids are returned strictly ascending")
			prev = id
		}

		filter.AfterID = prev

		if len(page) < 2 {
			break
		}
	}

	for id := range mine {
		assert.Truef(t, seen[id], "record %s needing an embedding must be returned across pages", id)
	}
}

// TestBackfillEmbeddings_StreamsAllEligible drives the service backfill against the real DB
// with a recording inserter and asserts it enqueues an embedding job for every record that
// needs one (a fresh model makes the created records eligible).
func TestBackfillEmbeddings_StreamsAllEligible(t *testing.T) {
	ctx := context.Background()
	feedbackRepo, embeddingsRepo := embeddingBackfillRepos(t)

	model := "backfill-stream-" + uuid.NewString()
	tenant := uuid.NewString()

	makeText := func(value string) uuid.UUID {
		rec, err := feedbackRepo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			SubmissionID: uuid.NewString(),
			TenantID:     tenant,
			FieldID:      "q1",
			FieldType:    models.FieldTypeText,
			ValueText:    &value,
		}, nil)
		require.NoError(t, err)

		return rec.ID
	}

	mine := []uuid.UUID{makeText("one"), makeText("two"), makeText("three")}

	inserter := &countingEmbeddingInserter{}
	svc := service.NewFeedbackRecordsService(feedbackRepo, embeddingsRepo, model, nil, inserter, "embeddings", 3, "")

	result, err := svc.BackfillEmbeddings(ctx, service.EmbeddingBackfillParams{
		Model: model, InputKind: models.EmbeddingInputKindRaw,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, result.Enqueued, len(mine))

	got := map[uuid.UUID]bool{}
	for _, id := range inserter.ids {
		got[id] = true
	}

	for _, id := range mine {
		assert.Truef(t, got[id], "record %s needing an embedding must be enqueued", id)
	}
}

func TestBackfillEmbeddings_TaxonomyInputKind(t *testing.T) {
	ctx := context.Background()
	feedbackRepo, embeddingsRepo := embeddingBackfillRepos(t)

	model := "taxonomy-backfill-stream-" + uuid.NewString()
	tenant := uuid.NewString()
	value := "Bonjour"

	rec, err := feedbackRepo.Create(ctx, &models.CreateFeedbackRecordRequest{
		SourceType:   "formbricks",
		SubmissionID: uuid.NewString(),
		TenantID:     tenant,
		FieldID:      "q1",
		FieldType:    models.FieldTypeText,
		ValueText:    &value,
	}, nil)
	require.NoError(t, err)

	inserter := &countingEmbeddingInserter{}
	svc := service.NewFeedbackRecordsService(feedbackRepo, embeddingsRepo, model, nil, inserter, "embeddings", 3, "")

	result, err := svc.BackfillEmbeddings(ctx, service.EmbeddingBackfillParams{
		Model: model, InputKind: models.EmbeddingInputKindTaxonomyTranslated, TenantID: tenant,
	})
	require.NoError(t, err)
	require.Positive(t, result.Enqueued)

	found := false

	for _, args := range inserter.args {
		if args.FeedbackRecordID == rec.ID {
			found = true

			assert.Equal(t, models.EmbeddingInputKindTaxonomyTranslated, args.InputKind)
			assert.Equal(t, model, args.Model)
		}
	}

	assert.True(t, found, "taxonomy backfill should enqueue the created record")
}

// TestTenantEmbeddingBackfillStaysInsideTenant verifies the operator-facing tenant scope at both
// repository and enqueue boundaries. A global backfill may see both tenants, but the tenant path
// must never return or enqueue the other tenant's records.
func TestTenantEmbeddingBackfillStaysInsideTenant(t *testing.T) {
	ctx := context.Background()
	feedbackRepo, embeddingsRepo := embeddingBackfillRepos(t)

	model := "tenant-backfill-" + uuid.NewString()
	tenantA := uuid.NewString()
	tenantB := uuid.NewString()

	create := func(tenantID, value string) uuid.UUID {
		record, err := feedbackRepo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			SubmissionID: uuid.NewString(),
			TenantID:     tenantID,
			FieldID:      "q1",
			FieldType:    models.FieldTypeText,
			ValueText:    &value,
		}, nil)
		require.NoError(t, err)

		return record.ID
	}

	aIDs := map[uuid.UUID]bool{
		create(tenantA, "one"): true,
		create(tenantA, "two"): true,
	}
	bID := create(tenantB, "other tenant")

	exists, err := embeddingsRepo.TenantExistsForEmbeddingBackfill(ctx, tenantA)
	require.NoError(t, err)
	assert.True(t, exists, "a tenant with feedback records is known to the backfill command")

	exists, err = embeddingsRepo.TenantExistsForEmbeddingBackfill(ctx, uuid.NewString())
	require.NoError(t, err)
	assert.False(t, exists, "an unknown tenant ID is distinguishable from a fully backfilled tenant")

	filter := repository.PendingEmbeddingsFilter{
		Model: model, InputKind: models.EmbeddingInputKindTaxonomyTranslated, RetryBefore: time.Now(),
	}

	count, err := embeddingsRepo.CountTenantPendingEmbeddings(ctx, tenantA, filter)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	_, err = embeddingsRepo.ListTenantPendingEmbeddings(ctx, "", filter, 10)
	require.Error(t, err, "the tenant entry point refuses an empty tenant rather than widening to all")

	page, err := embeddingsRepo.ListTenantPendingEmbeddings(ctx, tenantA, filter, 10)
	require.NoError(t, err)
	require.Len(t, page, 2)

	for _, id := range page {
		assert.Truef(t, aIDs[id], "tenant-scoped repository returned foreign id %s", id)
		assert.NotEqual(t, bID, id)
	}

	inserter := &countingEmbeddingInserter{}
	svc := service.NewFeedbackRecordsService(feedbackRepo, embeddingsRepo, model, nil, inserter, "embeddings", 3, "")
	result, err := svc.BackfillEmbeddings(ctx, service.EmbeddingBackfillParams{
		Model: model, InputKind: models.EmbeddingInputKindTaxonomyTranslated, TenantID: tenantA, MaxRecords: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Enqueued, "maxRecords must bound the canary")
	require.Len(t, inserter.ids, 1)
	assert.True(t, aIDs[inserter.ids[0]], "tenant-scoped service must enqueue only tenant A")
	assert.NotEqual(t, bID, inserter.ids[0])

	_, err = svc.BackfillEmbeddings(ctx, service.EmbeddingBackfillParams{
		Model: model, InputKind: models.EmbeddingInputKindTaxonomyTranslated, IncludeLegacy: true,
	})
	require.ErrorIs(t, err, service.ErrMissingTenantID, "re-verifying legacy vectors is tenant-scoped only")
}

// TestEmbeddingsUpsert_StaleWriteGuard locks the concurrent-jobs guard: an upsert (or clear)
// whose stillCurrent check fails against the record's current content is skipped with
// ErrEmbeddingSuperseded, so a slower job that read older content can never clobber the vector
// a newer job wrote — while a matching check writes normally.
func TestEmbeddingsUpsert_StaleWriteGuard(t *testing.T) {
	ctx := context.Background()
	feedbackRepo, embeddingsRepo := embeddingBackfillRepos(t)

	model := "stale-guard-" + uuid.NewString()
	text := "current text"

	rec, err := feedbackRepo.Create(ctx, &models.CreateFeedbackRecordRequest{
		SourceType:   "formbricks",
		SubmissionID: uuid.NewString(),
		TenantID:     uuid.NewString(),
		FieldID:      "q1",
		FieldType:    models.FieldTypeText,
		ValueText:    &text,
	}, nil)
	require.NoError(t, err)

	embedding := make([]float32, models.EmbeddingVectorDimensions)
	embedding[0] = 0.5

	// A stale job (its content no longer matches the row) is skipped and writes nothing.
	staleErr := embeddingsRepo.Upsert(ctx, rec.ID, model, models.EmbeddingInputKindRaw, embedding,
		func(_, valueText, _ *string) bool { return valueText != nil && *valueText == "older text" })
	require.ErrorIs(t, staleErr, huberrors.ErrEmbeddingSuperseded)

	_, getErr := embeddingsRepo.GetEmbeddingByFeedbackRecordAndModel(ctx, rec.ID, model)
	require.ErrorIs(t, getErr, repository.ErrEmbeddingNotFound, "the stale upsert must not write")

	// The job holding the current content writes normally.
	require.NoError(t, embeddingsRepo.Upsert(ctx, rec.ID, model, models.EmbeddingInputKindRaw, embedding,
		func(_, valueText, _ *string) bool { return valueText != nil && *valueText == text }))

	stored, err := embeddingsRepo.GetEmbeddingByFeedbackRecordAndModel(ctx, rec.ID, model)
	require.NoError(t, err)
	assert.InDelta(t, 0.5, stored[0], 1e-6)

	// A stale clear must not delete the current vector either.
	require.ErrorIs(t,
		embeddingsRepo.DeleteByFeedbackRecordAndModel(ctx, rec.ID, model,
			func(_, valueText, _ *string) bool { return valueText == nil }),
		huberrors.ErrEmbeddingSuperseded)

	_, err = embeddingsRepo.GetEmbeddingByFeedbackRecordAndModel(ctx, rec.ID, model)
	require.NoError(t, err, "the guarded stale clear must leave the row")
}

// TestDeleteEmbeddingsForOtherModels locks the stale-model prune: rows for other models are
// batch-deleted, rows for the current models survive.
func TestDeleteEmbeddingsForOtherModels(t *testing.T) {
	ctx := context.Background()
	feedbackRepo, embeddingsRepo := embeddingBackfillRepos(t)

	// Unique per-run model names so the assertion is immune to leftover rows from other tests.
	currentModel := "prune-current-" + uuid.NewString()
	currentTaxonomyModel := "taxonomy:" + currentModel + ":translated-v1"
	staleModel := "prune-stale-" + uuid.NewString()
	text := "prune me"

	embedding := make([]float32, models.EmbeddingVectorDimensions)

	keep := make([]uuid.UUID, 0, 3)
	drop := make([]uuid.UUID, 0, 3)

	for range 3 {
		rec, err := feedbackRepo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			SubmissionID: uuid.NewString(),
			TenantID:     uuid.NewString(),
			FieldID:      "q1",
			FieldType:    models.FieldTypeText,
			ValueText:    &text,
		}, nil)
		require.NoError(t, err)

		require.NoError(t, embeddingsRepo.Upsert(ctx, rec.ID, currentModel, models.EmbeddingInputKindRaw, embedding, nil))
		require.NoError(t, embeddingsRepo.Upsert(ctx, rec.ID, currentTaxonomyModel, models.EmbeddingInputKindTaxonomyTranslated, embedding, nil))
		require.NoError(t, embeddingsRepo.Upsert(ctx, rec.ID, staleModel, models.EmbeddingInputKindRaw, embedding, nil))

		keep = append(keep, rec.ID)
		drop = append(drop, rec.ID)
	}

	// batchSize 2 forces multiple delete rounds over the 3 stale rows.
	deleted, err := embeddingsRepo.DeleteEmbeddingsForOtherModels(ctx, currentModel, 2, currentTaxonomyModel)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, deleted, int64(3), "at least this test's stale rows are pruned")

	for _, id := range keep {
		_, err := embeddingsRepo.GetEmbeddingByFeedbackRecordAndModel(ctx, id, currentModel)
		require.NoError(t, err, "current-model rows must survive the prune")

		_, err = embeddingsRepo.GetEmbeddingByFeedbackRecordAndModel(ctx, id, currentTaxonomyModel)
		require.NoError(t, err, "current taxonomy-model rows must survive the prune")
	}

	for _, id := range drop {
		_, err := embeddingsRepo.GetEmbeddingByFeedbackRecordAndModel(ctx, id, staleModel)
		require.ErrorIs(t, err, repository.ErrEmbeddingNotFound, "stale-model rows must be gone")
	}
}

// TestEmbeddingBackfillSelectsWhatTheSweepWould drives the operator backfill against real River
// (ENG-2792). Before, it queued a second job for a record whose live job was still in flight,
// retried records the provider had rejected outright, never saw stale vectors, and a re-run within
// 24 hours silently skipped records whose earlier backfill job had completed. Now it selects
// through the same query as the repair sweep, and an identical re-run queues nothing.
func TestEmbeddingBackfillSelectsWhatTheSweepWould(t *testing.T) {
	ctx := context.Background()
	fixtures := newStaleRepairFixtures(t)
	db := fixtures.db
	embeddingsRepo := repository.NewEmbeddingsRepository(db)
	failuresRepo := repository.NewEnrichmentFailuresRepository(db)
	model := "backfill-dedupe-" + uuid.NewString()

	// By model as well as by tenant: a regression that widened the backfill beyond its tenant would
	// otherwise leave jobs for the whole shared database behind.
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM river_job WHERE args->>'model' = $1`, model)
	})

	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{})
	require.NoError(t, err)

	svc := service.NewFeedbackRecordsService(
		repository.NewFeedbackRecordsRepository(db), embeddingsRepo, model, nil, riverClient,
		service.EmbeddingsQueueName, 5, "")

	text := func(value string) *string { return &value }
	missing := fixtures.seed(ctx, t, models.FieldTypeCategorical, text("Missing"), nil)

	inFlight := fixtures.seed(ctx, t, models.FieldTypeCategorical, text("Live job queued"), nil)
	_, err = riverClient.Insert(ctx, service.FeedbackEmbeddingArgs{
		FeedbackRecordID: inFlight.ID, Model: model, InputKind: models.EmbeddingInputKindRaw, ValueTextHash: "live",
	}, &river.InsertOpts{Queue: service.EmbeddingsQueueName})
	require.NoError(t, err)

	rejected := fixtures.seed(ctx, t, models.FieldTypeCategorical, text("Rejected"), nil)
	require.NoError(t, failuresRepo.RecordFailure(ctx, models.EnrichmentFailure{
		FeedbackRecordID: rejected.ID, TenantID: fixtures.tenantID, Enrichment: models.EnrichmentNameEmbedding,
		Terminal: true, Reason: string(huberrors.TerminalReasonLength), Attempts: 1, ContextKey: model,
		SourceUpdatedAt: &rejected.UpdatedAt,
	}))

	stale := fixtures.seed(ctx, t, models.FieldTypeCategorical, text("Before"), nil)
	require.NoError(t, embeddingsRepo.Upsert(ctx, stale.ID, model, models.EmbeddingInputKindRaw, unitVector(), nil))
	fixtures.exec(t, `UPDATE feedback_records SET value_text = 'After' WHERE id = $1`, stale.ID)

	legacy := fixtures.seed(ctx, t, models.FieldTypeCategorical, text("Legacy"), nil)
	require.NoError(t, embeddingsRepo.Upsert(ctx, legacy.ID, model, models.EmbeddingInputKindRaw, unitVector(), nil))
	fixtures.exec(t, `UPDATE embeddings SET source_hash = NULL WHERE feedback_record_id = $1`, legacy.ID)

	// Its attempts ran out on a transient error a moment ago, as in an incident the operator is
	// recovering from: the sweep waits out the cooldown, the backfill must not.
	cooling := fixtures.seed(ctx, t, models.FieldTypeCategorical, text("Just failed"), nil)
	require.NoError(t, failuresRepo.RecordFailure(ctx, models.EnrichmentFailure{
		FeedbackRecordID: cooling.ID, TenantID: fixtures.tenantID, Enrichment: models.EnrichmentNameEmbedding,
		Terminal: false, Reason: models.EnrichmentFailureReasonProviderError, Attempts: 5, ContextKey: model,
		SourceUpdatedAt: &cooling.UpdatedAt,
	}))

	sweepIDs, err := embeddingsRepo.ListPendingEmbeddingIDs(
		ctx, model, models.EmbeddingInputKindRaw, time.Now().Add(-15*time.Minute), 10_000)
	require.NoError(t, err)
	require.NotContains(t, sweepIDs, cooling.ID, "precondition: the sweep is still cooling it down")

	params := service.EmbeddingBackfillParams{
		Model: model, InputKind: models.EmbeddingInputKindRaw, TenantID: fixtures.tenantID,
	}

	queued := func() []uuid.UUID {
		t.Helper()

		var ids []uuid.UUID
		require.NoError(t, db.QueryRow(ctx, `
			SELECT COALESCE(array_agg((args->>'feedback_record_id')::uuid), '{}') FROM river_job
			WHERE args->>'model' = $1 AND args->>'value_text_hash' = $2`, model, service.ValueTextHashRepair).Scan(&ids))

		return ids
	}

	result, err := svc.BackfillEmbeddings(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, service.EmbeddingBackfillResult{Selected: 3, Enqueued: 3}, result)
	assert.ElementsMatch(t, []uuid.UUID{missing.ID, stale.ID, cooling.ID}, queued(),
		"not the record with a live job in flight, not the rejected one, not the trusted legacy vector")

	count, err := svc.CountEmbeddingBackfill(ctx, params)
	require.NoError(t, err)
	assert.Zero(t, count, "-count-only agrees: everything owed is now in flight")

	// The sweep's repair for the same record is the same River unique key, so even a sweep that
	// selected it before the backfill's insert landed could not queue it twice.
	sweepRepair, err := riverClient.Insert(ctx, service.FeedbackEmbeddingArgs{
		FeedbackRecordID: missing.ID, Model: model, InputKind: models.EmbeddingInputKindRaw,
		ValueTextHash: service.ValueTextHashRepair,
	}, &river.InsertOpts{
		Queue: service.EmbeddingsReconcileQueueName,
		UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: []rivertype.JobState{
			rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable,
			rivertype.JobStateRunning, rivertype.JobStateScheduled,
		}},
	})
	require.NoError(t, err)
	assert.True(t, sweepRepair.UniqueSkippedAsDuplicate)

	result, err = svc.BackfillEmbeddings(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, service.EmbeddingBackfillResult{}, result, "an identical re-run queues nothing")

	// Finish the backfill jobs without fixing the records: a later run must not be swallowed by
	// River's completed-state uniqueness.
	_, err = db.Exec(ctx, `UPDATE river_job SET state = 'completed', finalized_at = NOW()
		WHERE args->>'model' = $1 AND args->>'value_text_hash' = $2`, model, service.ValueTextHashRepair)
	require.NoError(t, err)

	params.IncludeLegacy = true
	result, err = svc.BackfillEmbeddings(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, 4, result.Enqueued, "-stale re-verifies the legacy vector; completed jobs block nothing")
	assert.Contains(t, queued(), legacy.ID)
	assert.NotContains(t, queued(), rejected.ID)

	// -retry-terminal, for after the cause of a rejection was fixed.
	params.IncludeLegacy = false
	params.IncludeTerminal = true
	result, err = svc.BackfillEmbeddings(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, service.EmbeddingBackfillResult{Selected: 1, Enqueued: 1}, result)
	assert.Contains(t, queued(), rejected.ID)

	_, err = svc.BackfillEmbeddings(ctx, service.EmbeddingBackfillParams{
		Model: model, InputKind: models.EmbeddingInputKindRaw, IncludeTerminal: true,
	})
	require.ErrorIs(t, err, service.ErrMissingTenantID, "retrying rejected records is tenant-scoped only")
}
