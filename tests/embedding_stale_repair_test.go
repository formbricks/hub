package tests

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/internal/workers"
)

// staleRepairFixtures seeds records in one tenant and cleans them, and their jobs, up afterwards.
type staleRepairFixtures struct {
	db       *pgxpool.Pool
	tenantID string
}

func newStaleRepairFixtures(t *testing.T) *staleRepairFixtures {
	t.Helper()

	ctx := context.Background()
	fixtures := &staleRepairFixtures{
		db:       newTenantLockDB(ctx, t),
		tenantID: "embedding-stale-repair-" + uuid.NewString(),
	}

	t.Cleanup(func() {
		_, _ = fixtures.db.Exec(ctx, `DELETE FROM river_job WHERE args->>'feedback_record_id' IN (
			SELECT id::text FROM feedback_records WHERE tenant_id = $1
		)`, fixtures.tenantID)
		_, _ = fixtures.db.Exec(ctx, `DELETE FROM feedback_records WHERE tenant_id = $1`, fixtures.tenantID)
	})

	return fixtures
}

// seed inserts a record directly, as a write whose event (or job) was lost would leave it. An epoch
// collected_at sorts it ahead of anything else in the shared database, so the deployment-wide,
// oldest-first sweep reaches it within its bound.
func (f *staleRepairFixtures) seed(
	t *testing.T, fieldType models.FieldType, valueText, valueTextTranslated *string,
) *models.FeedbackRecord {
	t.Helper()

	var record models.FeedbackRecord

	err := f.db.QueryRow(context.Background(), `
		INSERT INTO feedback_records (
			source_type, field_id, field_label, field_type,
			value_text, value_text_translated, tenant_id, submission_id, collected_at
		)
		VALUES ('formbricks', 'q1', 'How was it?', $1::field_type_enum, $2, $3, $4, $5, '1970-01-01'::timestamptz)
		RETURNING id, tenant_id, updated_at`,
		string(fieldType), valueText, valueTextTranslated, f.tenantID, "submission-"+uuid.NewString(),
	).Scan(&record.ID, &record.TenantID, &record.UpdatedAt)
	require.NoError(t, err)

	return &record
}

func (f *staleRepairFixtures) exec(t *testing.T, sql string, args ...any) {
	t.Helper()

	_, err := f.db.Exec(context.Background(), sql, args...)
	require.NoError(t, err)
}

// tenantPendingIDs lists what the repair selection returns for filter, the way the tenant-scoped
// operator backfill sees it.
func tenantPendingIDs(
	ctx context.Context, t *testing.T, repo *repository.EmbeddingsRepository, filter repository.PendingEmbeddingsFilter,
) []uuid.UUID {
	t.Helper()

	ids, err := repo.ListPendingEmbeddings(ctx, filter, 1_000)
	require.NoError(t, err)

	return ids
}

func unitVector() []float32 {
	vector := make([]float32, models.EmbeddingVectorDimensions)
	vector[0] = 1

	return vector
}

// TestListPendingEmbeddingIDsFindsStaleAndOrphanedVectors pins the repair selection for both the
// search (raw) and the taxonomy embedding, for a text and a categorical record (ENG-2792): a vector
// is owed repair when it is missing, when the content its input reads has changed since it was
// written, or when the record no longer has any text. Writes that leave its input alone (an
// enrichment write, a re-import) must not make it look stale.
func TestListPendingEmbeddingIDsFindsStaleAndOrphanedVectors(t *testing.T) {
	for _, kind := range []models.EmbeddingInputKind{
		models.EmbeddingInputKindRaw, models.EmbeddingInputKindTaxonomyTranslated,
	} {
		for _, fieldType := range []models.FieldType{models.FieldTypeText, models.FieldTypeCategorical} {
			t.Run(string(kind)+"/"+string(fieldType), func(t *testing.T) {
				testListPendingEmbeddingIDsFindsStaleAndOrphanedVectors(t, kind, fieldType)
			})
		}
	}
}

func testListPendingEmbeddingIDsFindsStaleAndOrphanedVectors(
	t *testing.T, kind models.EmbeddingInputKind, fieldType models.FieldType,
) {
	t.Helper()

	ctx := context.Background()
	fixtures := newStaleRepairFixtures(t)
	embeddingsRepo := repository.NewEmbeddingsRepository(fixtures.db)
	failuresRepo := repository.NewEnrichmentFailuresRepository(fixtures.db)
	model := string(kind) + "-" + uuid.NewString()

	text := func(value string) *string { return &value }
	embed := func(record *models.FeedbackRecord) {
		t.Helper()
		require.NoError(t, embeddingsRepo.Upsert(ctx, record.ID, model, kind, unitVector(), nil))
	}

	missing := fixtures.seed(t, fieldType, text("Missing"), nil)

	current := fixtures.seed(t, fieldType, text("Current"), text("Current, translated"))
	embed(current)

	editedText := fixtures.seed(t, fieldType, text("Before"), nil)
	embed(editedText)
	fixtures.exec(t, `UPDATE feedback_records SET value_text = 'After', updated_at = NOW() WHERE id = $1`, editedText.ID)

	editedLabel := fixtures.seed(t, fieldType, text("Same answer"), nil)
	embed(editedLabel)
	fixtures.exec(t, `UPDATE feedback_records SET field_label = 'A new question', updated_at = NOW() WHERE id = $1`,
		editedLabel.ID)

	retranslated := fixtures.seed(t, fieldType, text("Bonjour"), text("Hello"))
	embed(retranslated)
	fixtures.exec(t, `UPDATE feedback_records SET value_text_translated = 'Good day', updated_at = NOW() WHERE id = $1`,
		retranslated.ID)

	enrichedOnly := fixtures.seed(t, fieldType, text("Unchanged"), nil)
	embed(enrichedOnly)
	fixtures.exec(t, `UPDATE feedback_records SET sentiment = 'positive', sentiment_score = 0.9, updated_at = NOW()
		WHERE id = $1`, enrichedOnly.ID)

	emptied := fixtures.seed(t, fieldType, text("Soon gone"), nil)
	embed(emptied)
	fixtures.exec(t, `UPDATE feedback_records SET value_text = NULL, updated_at = NOW() WHERE id = $1`, emptied.ID)

	// A vector written before source_hash existed is trusted as current, even if it is not.
	legacy := fixtures.seed(t, fieldType, text("Legacy"), nil)
	embed(legacy)
	fixtures.exec(t, `UPDATE embeddings SET source_hash = NULL WHERE feedback_record_id = $1`, legacy.ID)
	fixtures.exec(t, `UPDATE feedback_records SET value_text = 'Legacy, edited', updated_at = NOW() WHERE id = $1`,
		legacy.ID)

	// A terminal failure for the current revision is excluded for this kind, and only this kind.
	terminal := fixtures.seed(t, fieldType, text("Rejected"), nil)
	enrichment := models.EnrichmentNameEmbedding

	otherEnrichment := models.EnrichmentNameTaxonomyEmbedding
	if kind == models.EmbeddingInputKindTaxonomyTranslated {
		enrichment, otherEnrichment = otherEnrichment, enrichment
	}

	require.NoError(t, failuresRepo.RecordFailure(ctx, models.EnrichmentFailure{
		FeedbackRecordID: terminal.ID, TenantID: fixtures.tenantID, Enrichment: enrichment, Terminal: true,
		Reason: string(huberrors.TerminalReasonLength), Attempts: 1, ContextKey: model, SourceUpdatedAt: &terminal.UpdatedAt,
	}))

	otherKindTerminal := fixtures.seed(t, fieldType, text("Rejected elsewhere"), nil)
	require.NoError(t, failuresRepo.RecordFailure(ctx, models.EnrichmentFailure{
		FeedbackRecordID: otherKindTerminal.ID, TenantID: fixtures.tenantID, Enrichment: otherEnrichment,
		Terminal: true, Reason: string(huberrors.TerminalReasonLength), Attempts: 1, ContextKey: model,
		SourceUpdatedAt: &otherKindTerminal.UpdatedAt,
	}))

	ids := tenantPendingIDs(ctx, t, embeddingsRepo, repository.PendingEmbeddingsFilter{
		Model: model, InputKind: kind, TenantID: fixtures.tenantID, RetryBefore: time.Now().Add(-15 * time.Minute),
	})

	want := []uuid.UUID{missing.ID, editedText.ID, editedLabel.ID, emptied.ID, otherKindTerminal.ID}
	if kind == models.EmbeddingInputKindTaxonomyTranslated {
		// Only the taxonomy input reads the translation.
		want = append(want, retranslated.ID)
	}

	assert.ElementsMatch(t, want, ids)

	// The deployment-wide sweep agrees with the tenant-scoped operator path on this tenant.
	all, err := embeddingsRepo.ListPendingEmbeddingIDs(ctx, model, kind, time.Now().Add(-15*time.Minute), 10_000)
	require.NoError(t, err)
	assert.Subset(t, all, want)
	assert.NotContains(t, all, current.ID)
	assert.NotContains(t, all, legacy.ID)
	assert.NotContains(t, all, enrichedOnly.ID)
	assert.NotContains(t, all, terminal.ID)
}

// TestEmbeddingRepairHealsStaleAndOrphanedVectors drives the whole repair loop for a categorical
// record (ENG-2792): its content changes while its re-embed is lost, and a second record's text is
// removed while its clear is lost. Nothing but the sweep and the worker may fix either: the stale
// vectors are re-embedded for both models, the orphaned ones are deleted without a provider call,
// and a second sweep finds nothing left to do.
func TestEmbeddingRepairHealsStaleAndOrphanedVectors(t *testing.T) {
	ctx := context.Background()
	fixtures := newStaleRepairFixtures(t)
	db := fixtures.db
	embeddingsRepo := repository.NewEmbeddingsRepository(db)
	recordsService := service.NewFeedbackRecordsService(
		repository.NewFeedbackRecordsRepository(db), embeddingsRepo, "", nil, nil, service.EmbeddingsQueueName, 5, "")
	provider := &countingEmbeddingClient{}
	worker := workers.NewFeedbackEmbeddingWorkerWithOptions(
		recordsService, provider, "", nil, time.Minute, repository.NewEnrichmentFailuresRepository(db), nil, 0, nil)

	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{})
	require.NoError(t, err)

	targets := []service.EmbeddingReconcileTarget{
		{Model: "raw-" + uuid.NewString(), InputKind: models.EmbeddingInputKindRaw},
		{Model: "taxonomy:" + uuid.NewString(), InputKind: models.EmbeddingInputKindTaxonomyTranslated},
	}

	// The sweep is deployment-wide, so with these fresh models it queues a repair for every record in
	// the shared test database. Remove all of them, or they fill the repair lane for later tests.
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM river_job WHERE args->>'model' IN ($1, $2)`,
			targets[0].Model, targets[1].Model)
	})

	answer := "Very satisfied"
	edited := fixtures.seed(t, models.FieldTypeCategorical, &answer, nil)
	emptied := fixtures.seed(t, models.FieldTypeCategorical, &answer, nil)

	for _, target := range targets {
		for _, record := range []*models.FeedbackRecord{edited, emptied} {
			require.NoError(t, embeddingsRepo.Upsert(ctx, record.ID, target.Model, target.InputKind, unitVector(), nil))
		}
	}

	fixtures.exec(t, `UPDATE feedback_records SET value_text = 'Not satisfied', updated_at = NOW() WHERE id = $1`,
		edited.ID)
	fixtures.exec(t, `UPDATE feedback_records SET value_text = NULL, updated_at = NOW() WHERE id = $1`, emptied.ID)

	reconciler := service.NewEmbeddingReconcileService(embeddingsRepo, targets, 10_000, 5, 15*time.Minute)
	reconciler.SetInserter(riverClient)

	_, err = reconciler.Sweep(ctx)
	require.NoError(t, err)

	worked := runRepairJobs(ctx, t, db, worker, fixtures.tenantID)
	assert.Equal(t, 4, worked, "one repair per model for each of the two records")
	assert.Equal(t, 2, provider.calls, "only the edited record is re-embedded; clearing needs no provider call")

	var vectors int
	require.NoError(t, db.QueryRow(ctx,
		`SELECT COUNT(*) FROM embeddings WHERE feedback_record_id = $1`, emptied.ID).Scan(&vectors))
	assert.Zero(t, vectors, "a record with no text keeps no vector of the text it had")

	for _, target := range targets {
		ids := tenantPendingIDs(ctx, t, embeddingsRepo, repository.PendingEmbeddingsFilter{
			Model: target.Model, InputKind: target.InputKind, TenantID: fixtures.tenantID, RetryBefore: time.Now(),
		})
		assert.Empty(t, ids, "nothing is left to repair for %s", target.InputKind)
	}

	_, err = reconciler.Sweep(ctx)
	require.NoError(t, err)
	assert.Zero(t, runRepairJobs(ctx, t, db, worker, fixtures.tenantID), "a second sweep repeats nothing")
}

// countingEmbeddingClient returns a fixed vector and counts calls.
type countingEmbeddingClient struct{ calls int }

func (c *countingEmbeddingClient) CreateEmbedding(context.Context, string) ([]float32, error) {
	c.calls++

	return unitVector(), nil
}

func (c *countingEmbeddingClient) CreateEmbeddingForQuery(ctx context.Context, input string) ([]float32, error) {
	return c.CreateEmbedding(ctx, input)
}

// runRepairJobs works every available repair job for tenantID's records through worker, the way
// hub-worker would, and returns how many it ran.
func runRepairJobs(
	ctx context.Context, t *testing.T, db *pgxpool.Pool, worker *workers.FeedbackEmbeddingWorker, tenantID string,
) int {
	t.Helper()

	rows, err := db.Query(ctx, `
		SELECT id, args FROM river_job
		WHERE kind = 'feedback_embedding' AND queue = $1 AND state = 'available'
		  AND args->>'feedback_record_id' IN (SELECT id::text FROM feedback_records WHERE tenant_id = $2)
		ORDER BY id`, service.EmbeddingsReconcileQueueName, tenantID)
	require.NoError(t, err)

	type queued struct {
		id   int64
		args []byte
	}

	jobs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (queued, error) {
		var job queued

		return job, row.Scan(&job.id, &job.args)
	})
	require.NoError(t, err)

	for _, job := range jobs {
		var args service.FeedbackEmbeddingArgs
		require.NoError(t, json.Unmarshal(job.args, &args))

		require.NoError(t, worker.Work(ctx, &river.Job[service.FeedbackEmbeddingArgs]{
			JobRow: &rivertype.JobRow{ID: job.id, Attempt: 1, MaxAttempts: 5, CreatedAt: time.Now()},
			Args:   args,
		}))

		_, err := db.Exec(ctx, `UPDATE river_job SET state = 'completed', finalized_at = NOW() WHERE id = $1`, job.id)
		require.NoError(t, err)
	}

	return len(jobs)
}
