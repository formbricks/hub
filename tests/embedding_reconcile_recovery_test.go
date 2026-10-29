package tests

import (
	"context"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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

// CreateEmbedding returns the scripted error while unhealthy, and a fixed vector once healthy.
func (c *scriptedEmbeddingClient) CreateEmbedding(context.Context, string) ([]float32, error) {
	if !c.healthy {
		return nil, c.err
	}

	vector := make([]float32, models.EmbeddingVectorDimensions)
	vector[0] = 1

	return vector, nil
}

// CreateEmbeddingForQuery behaves exactly like CreateEmbedding.
func (c *scriptedEmbeddingClient) CreateEmbeddingForQuery(ctx context.Context, input string) ([]float32, error) {
	return c.CreateEmbedding(ctx, input)
}

// enqueueLiveEmbeddings runs the live embedding enqueue the way a record write does: inside a
// transaction that commits.
func enqueueLiveEmbeddings(
	ctx context.Context, t *testing.T, db *pgxpool.Pool, enqueuer *service.EmbeddingEnqueuer, event service.Event,
) {
	t.Helper()

	tx, err := db.Begin(ctx)
	require.NoError(t, err)

	require.NoError(t, enqueuer.EnqueueTx(ctx, tx, event))
	require.NoError(t, tx.Commit(ctx))
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

// testTaxonomyEmbeddingRecovery drives one record of fieldType through live enqueue, a final failed
// attempt, the cooldown, the repair sweep and the repair job, and asserts readiness reaches full coverage.
func testTaxonomyEmbeddingRecovery(t *testing.T, fieldType models.FieldType, valueText string) {
	t.Helper()

	ctx := context.Background()
	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	// Registered before the data cleanup below: cleanups run last-in-first-out, so the
	// pool closes only after the fixtures are deleted. A deferred Close would run first.
	t.Cleanup(db.Close)

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
	liveEnqueuer := service.NewEmbeddingEnqueuer(riverClient, service.NewEmbeddingJobBuilderForInputKind(
		model, service.EmbeddingsQueueName, maxAttempts, "", 0, nil,
		models.EmbeddingInputKindTaxonomyTranslated,
	))
	enqueueLiveEmbeddings(ctx, t, db, liveEnqueuer, service.Event{
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

	reconciler := service.NewEmbeddingReconcileService(embeddingsRepo, []service.EmbeddingReconcileTarget{
		{Model: model, InputKind: models.EmbeddingInputKindTaxonomyTranslated},
	}, 1_000, maxAttempts, retryAfter)
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

// TestTaxonomyEmbeddingEligibilityAgreesAcrossPaths pins the rule whose drift caused ENG-2792: for
// every field type the schema allows, the live provider, the repair sweep, readiness, the pending
// and failed-records gauges, and the backfill command must agree on which records need a taxonomy
// embedding. Field types are read from the database enum, so a type added later is covered without
// editing this test.
func TestTaxonomyEmbeddingEligibilityAgreesAcrossPaths(t *testing.T) {
	ctx := context.Background()
	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	// Registered before the data cleanup below: cleanups run last-in-first-out, so the
	// pool closes only after the fixtures are deleted. A deferred Close would run first.
	t.Cleanup(db.Close)

	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{})
	require.NoError(t, err)

	embeddingsRepo := repository.NewEmbeddingsRepository(db)
	taxonomyRepo := repository.NewTaxonomyRepository(db)
	failuresRepo := repository.NewEnrichmentFailuresRepository(db)
	statusRepo := repository.NewEnrichmentStatusRepository(db)
	recordsService := service.NewFeedbackRecordsService(
		repository.NewFeedbackRecordsRepository(db), embeddingsRepo, "", nil, nil,
		service.EmbeddingsQueueName, 5, "",
	)

	// The pending gauge is only computed by the elected backlog leader. Closed before the pool:
	// this cleanup is registered after t.Cleanup(db.Close), so it runs first.
	leader := repository.NewEnrichmentBacklogLeader(db)

	t.Cleanup(func() { leader.Close(ctx) })

	runID := uuid.NewString()
	model := "taxonomy:parity-" + runID
	tenantPrefix := "embedding-parity-" + runID + "-"

	t.Cleanup(func() {
		_, _ = db.Exec(ctx, `DELETE FROM river_job WHERE args->>'model' = $1`, model)
		_, _ = db.Exec(ctx, `DELETE FROM feedback_records WHERE starts_with(tenant_id, $1)`, tenantPrefix)
	})

	rows, err := db.Query(ctx, `SELECT unnest(enum_range(NULL::field_type_enum))::text`)
	require.NoError(t, err)

	var fieldTypes []string

	for rows.Next() {
		var fieldType string
		require.NoError(t, rows.Scan(&fieldType))

		fieldTypes = append(fieldTypes, fieldType)
	}

	require.NoError(t, rows.Err())
	require.NotEmpty(t, fieldTypes)

	// Deployment-wide gauges are compared as deltas around the fixtures. The unique model makes every
	// pre-existing record count as missing in both readings, so only the fixtures move the counts.
	pendingBefore, isLeader, err := leader.CountIfLeader(ctx, "", model)
	require.NoError(t, err)
	require.True(t, isLeader, "the test must hold backlog leadership to read the pending gauge")

	failedBefore, err := statusRepo.CountFailedRecordsAggregate(ctx, model)
	require.NoError(t, err)

	liveEnqueuer := service.NewEmbeddingEnqueuer(riverClient, service.NewEmbeddingJobBuilderForInputKind(
		model, service.EmbeddingsQueueName, 5, "", 0, nil,
		models.EmbeddingInputKindTaxonomyTranslated,
	))

	type fixture struct {
		id       uuid.UUID
		hasText  bool
		tenantID string
	}

	fixtures := make([]fixture, 0, 2*len(fieldTypes))
	records := make(map[uuid.UUID]*models.FeedbackRecord, 2*len(fieldTypes))

	for _, fieldType := range fieldTypes {
		tenantID := tenantPrefix + fieldType

		for _, valueText := range []string{"Option label " + fieldType, " \t "} {
			var id uuid.UUID

			err := db.QueryRow(ctx, `
				INSERT INTO feedback_records (
					source_type, source_id, field_id, field_label, field_type,
					value_text, tenant_id, submission_id, collected_at
				)
				VALUES ('formbricks', 'survey-1', 'q1', 'Question', $1::field_type_enum,
				        $2, $3, $4, '1970-01-01'::timestamptz)
				RETURNING id`,
				fieldType, valueText, tenantID, "submission-"+uuid.NewString(),
			).Scan(&id)
			require.NoError(t, err)

			record, err := recordsService.GetFeedbackRecord(ctx, id)
			require.NoError(t, err)

			records[id] = record

			enqueueLiveEmbeddings(ctx, t, db, liveEnqueuer, service.Event{
				ID: uuid.Must(uuid.NewV7()), Type: datatypes.FeedbackRecordCreated, Timestamp: time.Now(), Data: record,
			})

			fixtures = append(fixtures, fixture{id: id, hasText: valueText != " \t ", tenantID: tenantID})
		}
	}

	// Live enqueue first, then clear those jobs: an active job would (correctly) hide a record from
	// the repair sweep, and the question here is eligibility alone.
	liveQueued := map[uuid.UUID]bool{}

	jobRows, err := db.Query(ctx, `SELECT (args->>'feedback_record_id')::uuid FROM river_job WHERE args->>'model' = $1`, model)
	require.NoError(t, err)

	for jobRows.Next() {
		var id uuid.UUID
		require.NoError(t, jobRows.Scan(&id))

		liveQueued[id] = true
	}

	require.NoError(t, jobRows.Err())

	_, err = db.Exec(ctx, `DELETE FROM river_job WHERE args->>'model' = $1`, model)
	require.NoError(t, err)

	pending, err := embeddingsRepo.ListPendingEmbeddingIDs(
		ctx, model, models.EmbeddingInputKindTaxonomyTranslated, time.Now(), 10_000)
	require.NoError(t, err)

	repairSelected := make(map[uuid.UUID]bool, len(pending))
	for _, id := range pending {
		repairSelected[id] = true
	}

	readinessWant := map[string]int{}
	wantTotal := 0

	for _, seeded := range fixtures {
		assert.Equal(t, seeded.hasText, liveQueued[seeded.id], "live enqueue for %s (has text: %v)", seeded.tenantID, seeded.hasText)
		assert.Equal(t, liveQueued[seeded.id], repairSelected[seeded.id],
			"repair must select exactly what live delivery embeds (%s, has text: %v)", seeded.tenantID, seeded.hasText)

		if seeded.hasText {
			readinessWant[seeded.tenantID]++
			wantTotal++
		}
	}

	for _, fieldType := range fieldTypes {
		tenantID := tenantPrefix + fieldType

		recordCount, _, _, err := taxonomyRepo.CountScopeInput(ctx, models.TaxonomyScope{
			ScopeType: models.TaxonomyScopeTypeDirectory, TenantID: tenantID,
		}, model)
		require.NoError(t, err)
		assert.Equal(t, readinessWant[tenantID], recordCount, "readiness counts what live delivery embeds (%s)", fieldType)

		// The operator backfill must select exactly the same records, and count what it would select.
		backfillFilter := repository.PendingEmbeddingsFilter{
			Model: model, InputKind: models.EmbeddingInputKindTaxonomyTranslated, RetryBefore: time.Now(),
		}
		backfillPage, err := embeddingsRepo.ListTenantPendingEmbeddings(ctx, tenantID, backfillFilter, 100)
		require.NoError(t, err)
		assert.Len(t, backfillPage, readinessWant[tenantID], "backfill selects what live delivery embeds (%s)", fieldType)

		backfillCount, err := embeddingsRepo.CountTenantPendingEmbeddings(ctx, tenantID, backfillFilter)
		require.NoError(t, err)
		assert.Equal(t, readinessWant[tenantID], backfillCount, "backfill counts what it selects (%s)", fieldType)
	}

	pendingAfter, isLeader, err := leader.CountIfLeader(ctx, "", model)
	require.NoError(t, err)
	require.True(t, isLeader)
	assert.Equal(t, int64(wantTotal), pendingAfter.TaxonomyEmbeddingPending-pendingBefore.TaxonomyEmbeddingPending,
		"the pending gauge counts what live delivery embeds")

	// Every fixture exhausts its attempts on a transient error. Only the ones owed an embedding are
	// failures; a blank record's marker is history, as for the text enrichments.
	for _, seeded := range fixtures {
		record := records[seeded.id]
		require.NoError(t, failuresRepo.RecordFailure(ctx, models.EnrichmentFailure{
			FeedbackRecordID: record.ID,
			TenantID:         record.TenantID,
			Enrichment:       models.EnrichmentNameTaxonomyEmbedding,
			Reason:           models.EnrichmentFailureReasonProviderError,
			Attempts:         5,
			ContextKey:       model,
			SourceUpdatedAt:  &record.UpdatedAt,
		}))
	}

	failedAfter, err := statusRepo.CountFailedRecordsAggregate(ctx, model)
	require.NoError(t, err)
	assert.Equal(t, int64(wantTotal), transientTaxonomyFailures(failedAfter)-transientTaxonomyFailures(failedBefore),
		"the failed-records gauge counts every record owed a taxonomy embedding, whatever its field type")
}

// transientTaxonomyFailures returns the non-terminal taxonomy_embedding bucket of the failed-records gauge.
func transientTaxonomyFailures(counts []repository.FailedRecordCount) int64 {
	for _, count := range counts {
		if count.Enrichment == models.EnrichmentNameTaxonomyEmbedding && !count.Terminal {
			return count.Count
		}
	}

	return 0
}
