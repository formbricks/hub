package tests

import (
	"context"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/internal/workers"
	"github.com/formbricks/hub/pkg/database"
)

// scriptedEmbeddingClient fails every call until healthy is set, then returns a fixed vector.
type scriptedEmbeddingClient struct {
	healthy bool
	err     error
}

func (c *scriptedEmbeddingClient) CreateEmbedding(context.Context, string) ([]float32, error) {
	if !c.healthy {
		return nil, c.err
	}

	vector := make([]float32, models.EmbeddingVectorDimensions)
	vector[0] = 1

	return vector, nil
}

func (c *scriptedEmbeddingClient) CreateEmbeddingForQuery(ctx context.Context, input string) ([]float32, error) {
	return c.CreateEmbedding(ctx, input)
}

// TestTaxonomyEmbeddingRecoversAfterTransientExhaustion reproduces the 22 October 2026 EU incident
// (ENG-2792): a record the live taxonomy-embedding path accepts exhausts its attempts on transient
// provider failures (connection resets), and must then be recovered by the reconciler alone — no
// record edit, no manual requeue. It drives the real live provider, the real worker on its final
// attempt, the real repair-selection query, and the real repair job, for every field type the live
// path accepts.
func TestTaxonomyEmbeddingRecoversAfterTransientExhaustion(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		fieldType models.FieldType
		valueText string
	}{
		{name: "text", fieldType: models.FieldTypeText, valueText: "The onboarding was confusing"},
		// Matrix/multi-choice expansion writes the chosen option label into value_text on
		// categorical rows; these are the 6,642 records stranded in the incident.
		{name: "categorical", fieldType: models.FieldTypeCategorical, valueText: "Very satisfied"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			testTaxonomyEmbeddingRecovery(t, testCase.fieldType, testCase.valueText)
		})
	}
}

func testTaxonomyEmbeddingRecovery(t *testing.T, fieldType models.FieldType, valueText string) {
	t.Helper()

	ctx := context.Background()
	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{})
	require.NoError(t, err)

	embeddingsRepo := repository.NewEmbeddingsRepository(db)
	failuresRepo := repository.NewEnrichmentFailuresRepository(db)
	taxonomyRepo := repository.NewTaxonomyRepository(db)
	recordsService := service.NewFeedbackRecordsService(
		repository.NewFeedbackRecordsRepository(db), embeddingsRepo, "", nil, nil,
		service.EmbeddingsQueueName, 5, "",
	)

	const maxAttempts = 5

	tenantID := "embedding-recovery-" + uuid.NewString()
	// A unique model isolates this test from every other embedding in the shared test database.
	model := "taxonomy:recovery-" + uuid.NewString()

	t.Cleanup(func() {
		_, _ = db.Exec(ctx, `DELETE FROM river_job WHERE args->>'model' = $1`, model)
		_, _ = db.Exec(ctx, `DELETE FROM feedback_records WHERE tenant_id = $1`, tenantID)
	})

	var recordID uuid.UUID

	// An epoch collected_at sorts the fixture ahead of anything else in the shared database, so the
	// deployment-wide, oldest-first sweep reaches it within its bound.
	err = db.QueryRow(ctx, `
		INSERT INTO feedback_records (
			source_type, source_id, field_id, field_label, field_type,
			value_text, tenant_id, submission_id, collected_at
		)
		VALUES ('formbricks', 'survey-1', 'q1', 'How satisfied are you?', $1::field_type_enum,
		        $2, $3, $4, '1970-01-01'::timestamptz)
		RETURNING id`,
		string(fieldType), valueText, tenantID, "submission-"+uuid.NewString(),
	).Scan(&recordID)
	require.NoError(t, err)

	record, err := recordsService.GetFeedbackRecord(ctx, recordID)
	require.NoError(t, err)

	// 1. Live delivery: the taxonomy provider enqueues for this record. This is the eligibility
	// rule repair has to match.
	liveProvider := service.NewEmbeddingProviderForInputKind(
		riverClient, model, service.EmbeddingsQueueName, maxAttempts, "", 0, nil,
		models.EmbeddingInputKindTaxonomyTranslated,
	)
	liveProvider.PublishEvent(ctx, service.Event{
		ID: uuid.Must(uuid.NewV7()), Type: datatypes.FeedbackRecordCreated, Timestamp: time.Now(), Data: record,
	})

	var liveJobID int64

	err = db.QueryRow(ctx, `
		SELECT id FROM river_job
		WHERE kind = 'feedback_embedding' AND queue = $1 AND args->>'feedback_record_id' = $2
		  AND args->>'model' = $3`,
		service.EmbeddingsQueueName, recordID.String(), model,
	).Scan(&liveJobID)
	require.NoError(t, err, "live taxonomy embedding path must enqueue a %s record", fieldType)

	// 2. The live job's final attempt fails on a transient provider error, as in the incident.
	provider := &scriptedEmbeddingClient{err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}}
	worker := workers.NewFeedbackEmbeddingWorkerWithOptions(
		recordsService, provider, "", nil, time.Minute, failuresRepo, nil, 0, nil,
	)
	liveArgs := service.FeedbackEmbeddingArgs{
		FeedbackRecordID: recordID,
		Model:            model,
		InputKind:        models.EmbeddingInputKindTaxonomyTranslated,
		ValueTextHash:    "live",
	}

	workErr := worker.Work(ctx, &river.Job[service.FeedbackEmbeddingArgs]{
		JobRow: &rivertype.JobRow{ID: liveJobID, Attempt: maxAttempts, MaxAttempts: maxAttempts, CreatedAt: time.Now()},
		Args:   liveArgs,
	})
	require.Error(t, workErr)

	var cancelErr *river.JobCancelError
	require.NotErrorAs(t, workErr, &cancelErr, "a connection reset is transient, not terminal")

	// River discards a job that fails its final attempt.
	_, err = db.Exec(ctx, `UPDATE river_job SET state = 'discarded', finalized_at = NOW() WHERE id = $1`, liveJobID)
	require.NoError(t, err)

	var terminal bool

	err = db.QueryRow(ctx, `
		SELECT terminal FROM feedback_record_enrichment_failures
		WHERE feedback_record_id = $1 AND enrichment = 'taxonomy_embedding' AND context_key = $2`,
		recordID, model,
	).Scan(&terminal)
	require.NoError(t, err, "the exhausted job must leave a durable failure marker")
	require.False(t, terminal)

	// 3. Wait out the transient-failure cooldown.
	const retryAfter = 15 * time.Minute

	_, err = db.Exec(ctx, `UPDATE feedback_record_enrichment_failures SET failed_at = NOW() - interval '1 hour'
		WHERE feedback_record_id = $1`, recordID)
	require.NoError(t, err)

	// 4. The provider has recovered. The periodic sweep must pick the record up on its own.
	provider.healthy = true

	reconciler := service.NewEmbeddingReconcileService(embeddingsRepo, model, 1_000, maxAttempts, retryAfter)
	reconciler.SetInserter(riverClient)

	_, err = reconciler.Sweep(ctx)
	require.NoError(t, err)

	var repairJobID int64

	err = db.QueryRow(ctx, `
		SELECT id FROM river_job
		WHERE kind = 'feedback_embedding' AND queue = $1 AND args->>'feedback_record_id' = $2
		  AND args->>'model' = $3 AND state = 'available'`,
		service.EmbeddingsReconcileQueueName, recordID.String(), model,
	).Scan(&repairJobID)
	require.NoError(t, err, "reconciler must enqueue a repair for the stranded %s record", fieldType)

	// 5. The repair job embeds the record; taxonomy readiness sees it as complete.
	require.NoError(t, worker.Work(ctx, &river.Job[service.FeedbackEmbeddingArgs]{
		JobRow: &rivertype.JobRow{ID: repairJobID, Attempt: 1, MaxAttempts: maxAttempts, CreatedAt: time.Now()},
		Args: service.FeedbackEmbeddingArgs{
			FeedbackRecordID: recordID,
			Model:            model,
			InputKind:        models.EmbeddingInputKindTaxonomyTranslated,
			ValueTextHash:    "reconcile",
		},
	}))

	recordCount, embeddingCount, _, err := taxonomyRepo.CountScopeInput(ctx, models.TaxonomyScope{
		ScopeType: models.TaxonomyScopeTypeDirectory, TenantID: tenantID,
	}, model)
	require.NoError(t, err)
	assert.Equal(t, 1, recordCount)
	assert.Equal(t, 1, embeddingCount, "directory must reach full taxonomy embedding coverage")
}
