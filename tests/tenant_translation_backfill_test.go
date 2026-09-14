package tests

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/internal/workers"
	"github.com/formbricks/hub/pkg/database"
)

// countingTranslationInserter records the FeedbackTranslationArgs jobs enqueued.
type countingTranslationInserter struct {
	args []service.FeedbackTranslationArgs
}

func (c *countingTranslationInserter) Insert(
	_ context.Context, args river.JobArgs, _ *river.InsertOpts,
) (*rivertype.JobInsertResult, error) {
	if a, ok := args.(service.FeedbackTranslationArgs); ok {
		c.args = append(c.args, a)
	}

	return &rivertype.JobInsertResult{}, nil
}

// backfillTestEnv opens the test DB and seeds two tenants, each with a target language.
func backfillTestEnv(t *testing.T) (*pgxpool.Pool, *repository.FeedbackRecordsRepository, string, string) {
	t.Helper()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(
		context.Background(), cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	t.Cleanup(db.Close)

	repo := repository.NewFeedbackRecordsRepository(db)
	settingsRepo := repository.NewTenantSettingsRepository(db)

	tenantA := testTenantID("bf-a")
	tenantB := testTenantID("bf-b")

	_, err = settingsRepo.Upsert(context.Background(), tenantA, models.EnrichmentSettings{TargetLanguage: "de-DE"})
	require.NoError(t, err)

	_, err = settingsRepo.Upsert(context.Background(), tenantB, models.EnrichmentSettings{TargetLanguage: "fr-FR"})
	require.NoError(t, err)

	return db, repo, tenantA, tenantB
}

// TestListTranslationBackfillTargetsForTenant locks the new keyset query: it returns only
// the named tenant's stale text records (tenant isolation — the global query had none),
// excludes records already at the current target (idempotency), and tiles a >limit set
// across keyset pages with no gaps or overlaps.
func TestListTranslationBackfillTargetsForTenant(t *testing.T) {
	ctx := context.Background()
	_, repo, tenantA, tenantB := backfillTestEnv(t)

	makeText := func(tenantID, valueText string) *models.FeedbackRecord {
		rec, err := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			FieldID:      "q1",
			FieldType:    models.FieldTypeText,
			ValueText:    &valueText,
			TenantID:     tenantID,
			SubmissionID: testTenantID("sub"),
		})
		require.NoError(t, err)

		return rec
	}

	// Tenant A: three untranslated text records (created in order → ascending UUIDv7 ids).
	recA1 := makeText(tenantA, "one")
	recA2 := makeText(tenantA, "two")
	recA3 := makeText(tenantA, "three")

	// Tenant A: one already translated to the current target (de-DE) → must be excluded.
	recCurrent := makeText(tenantA, "current")
	currentTranslation := "Aktuell"
	require.NoError(t, repo.SetTranslation(ctx, recCurrent.ID, &currentTranslation, "de-DE", "", nil))

	// Tenant B: one untranslated record → must never appear for tenant A.
	recB := makeText(tenantB, "bee")

	page1, err := repo.ListTranslationBackfillTargetsForTenant(ctx, tenantA, uuid.Nil, 2, "")
	require.NoError(t, err)
	require.Len(t, page1, 2, "first keyset page should be full")

	page2, err := repo.ListTranslationBackfillTargetsForTenant(ctx, tenantA, page1[1].FeedbackRecordID, 2, "")
	require.NoError(t, err)
	require.Len(t, page2, 1, "second page holds the remaining stale record")

	page3, err := repo.ListTranslationBackfillTargetsForTenant(ctx, tenantA, page2[0].FeedbackRecordID, 2, "")
	require.NoError(t, err)
	assert.Empty(t, page3, "no more pages")

	got := map[uuid.UUID]string{}
	for _, target := range page1 {
		got[target.FeedbackRecordID] = target.TargetLang
	}

	for _, target := range page2 {
		got[target.FeedbackRecordID] = target.TargetLang
	}

	assert.Equal(t, map[uuid.UUID]string{
		recA1.ID: "de-DE", recA2.ID: "de-DE", recA3.ID: "de-DE",
	}, got, "exactly tenant A's three stale records, all targeting de-DE")

	assert.NotContains(t, got, recCurrent.ID, "record already at the current target is excluded (idempotency)")
	assert.NotContains(t, got, recB.ID, "another tenant's record never appears (tenant isolation)")
}

// TestListTranslationBackfillTargetsForTenant_UsesDefaultLanguage locks the default-aware
// per-tenant backfill: a tenant with no target_language of its own inherits the deployment
// default, so its stale/untranslated records become backfill targets under that default (this is
// what lets clearing a target re-translate existing rows to the default instead of no-opping).
func TestListTranslationBackfillTargetsForTenant_UsesDefaultLanguage(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	repo := repository.NewFeedbackRecordsRepository(db)

	// A tenant with NO settings row → no target_language of its own (relies on the default).
	tenant := testTenantID("bf-no-target")
	makeText := func(valueText string) *models.FeedbackRecord {
		rec, err := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			FieldID:      "q1",
			FieldType:    models.FieldTypeText,
			ValueText:    &valueText,
			TenantID:     tenant,
			SubmissionID: testTenantID("sub"),
		})
		require.NoError(t, err)

		return rec
	}

	// One record was translated to a now-former target (de-DE), another never translated.
	stale := makeText("eins")
	never := makeText("zwei")

	// Seed the stale record's translation to its former target — de-DE is the effective target
	// for this write because we pass de-DE as the default.
	oldTranslation := "Eins (alt)"
	require.NoError(t, repo.SetTranslation(ctx, stale.ID, &oldTranslation, "de-DE", "de-DE", nil))

	// With no default configured, a tenant with no target has no effective target → no targets.
	none, err := repo.ListTranslationBackfillTargetsForTenant(ctx, tenant, uuid.Nil, 100, "")
	require.NoError(t, err)
	assert.Empty(t, none, "without a default, a no-target tenant yields no backfill targets")

	// With a default (en-US), both the stale (de-DE != en-US) and the never-translated record
	// must be (re)translated to the default.
	targets, err := repo.ListTranslationBackfillTargetsForTenant(ctx, tenant, uuid.Nil, 100, "en-US")
	require.NoError(t, err)

	got := map[uuid.UUID]string{}
	for _, target := range targets {
		got[target.FeedbackRecordID] = target.TargetLang
	}

	assert.Equal(t, map[uuid.UUID]string{stale.ID: "en-US", never.ID: "en-US"}, got,
		"under the default, the stale-target and untranslated records both target the default")
}

// TestBackfillTranslationsForTenant_Isolation drives the service against the real DB with a
// recording inserter: a per-tenant backfill enqueues a translation job for exactly that
// tenant's stale records and nothing for any other tenant.
func TestBackfillTranslationsForTenant_Isolation(t *testing.T) {
	ctx := context.Background()
	_, repo, tenantA, tenantB := backfillTestEnv(t)

	makeText := func(tenantID, valueText string) *models.FeedbackRecord {
		rec, err := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			FieldID:      "q1",
			FieldType:    models.FieldTypeText,
			ValueText:    &valueText,
			TenantID:     tenantID,
			SubmissionID: testTenantID("sub"),
		})
		require.NoError(t, err)

		return rec
	}

	recA1 := makeText(tenantA, "one")
	recA2 := makeText(tenantA, "two")
	makeText(tenantB, "bee") // other tenant, must be untouched

	inserter := &countingTranslationInserter{}
	svc := service.NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")

	enqueued, err := svc.BackfillTranslationsForTenant(ctx, inserter, service.TranslationsQueueName, 3, tenantA, "run-1")
	require.NoError(t, err)
	assert.Equal(t, 2, enqueued)

	enqueuedIDs := map[uuid.UUID]string{}
	for _, job := range inserter.args {
		enqueuedIDs[job.FeedbackRecordID] = job.TargetLang

		assert.Equal(t, "backfill:run-1", job.ValueTextHash, "backfill jobs carry the per-run marker")
	}

	assert.Equal(t, map[uuid.UUID]string{recA1.ID: "de-DE", recA2.ID: "de-DE"}, enqueuedIDs,
		"only tenant A's stale records are enqueued, targeting de-DE")
}

// TestTenantTranslationBackfillWorker_Work drives the worker end to end: given a
// TenantTranslationBackfillArgs and a River client on the context (the seam the worker uses
// to enqueue), it inserts a feedback_translation job for each of the tenant's stale records.
func TestTenantTranslationBackfillWorker_Work(t *testing.T) {
	ctx := context.Background()
	pool, repo, tenantA, _ := backfillTestEnv(t)

	makeText := func(tenantID, valueText string) *models.FeedbackRecord {
		rec, err := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			FieldID:      "q1",
			FieldType:    models.FieldTypeText,
			ValueText:    &valueText,
			TenantID:     tenantID,
			SubmissionID: testTenantID("sub"),
		})
		require.NoError(t, err)

		return rec
	}

	recA1 := makeText(tenantA, "one")
	recA2 := makeText(tenantA, "two")

	// Insert-only River client (no queues/workers) just to satisfy the worker's
	// ClientFromContext seam; the per-record inserts skip the unknown-kind check.
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	require.NoError(t, err)

	svc := service.NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")
	worker := workers.NewTenantTranslationBackfillWorker(svc, 3)

	job := &river.Job[service.TenantTranslationBackfillArgs]{
		JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 1},
		Args:   service.TenantTranslationBackfillArgs{TenantID: tenantA},
	}
	require.NoError(t, worker.Work(rivertest.WorkContext(ctx, riverClient), job))

	var count int

	err = pool.QueryRow(ctx, `
		SELECT count(*) FROM river_job
		WHERE kind = 'feedback_translation'
			AND args->>'feedback_record_id' = ANY($1)`,
		[]string{recA1.ID.String(), recA2.ID.String()}).Scan(&count)
	require.NoError(t, err)

	assert.Equal(t, 2, count, "the worker enqueues one feedback_translation job per stale record")
}

func TestTenantTranslationBackfillWorker_Timeout(t *testing.T) {
	worker := workers.NewTenantTranslationBackfillWorker(nil, 3)

	assert.Equal(t, 5*time.Minute, worker.Timeout(nil), "per-tenant fan-out timeout")
}

// TestListTranslationBackfillTargetsSkipsTerminalRecords binds the exclusion that keeps a
// settings change from undoing the reconciler's work.
//
// A terminal marker means the provider refused this text structurally and will refuse it again.
// The reconciler already skips such records; before this exclusion the settings-triggered
// backfill re-enqueued them anyway, so every target-language change spent one provider call per
// permanently-failing record — 1517 wasted jobs in a measured 3000-record run. Both the
// per-tenant and the global query are pinned, because the one-off command shares the predicate
// and a fix applied to only one of them would leave the other paying the bill.
//
// A NON-terminal marker must still be a target: those records failed for reasons a retry can
// fix, which is the whole distinction the terminal taxonomy exists to draw.
func TestListTranslationBackfillTargetsSkipsTerminalRecords(t *testing.T) {
	ctx := context.Background()
	db, repo, tenantA, _ := backfillTestEnv(t)

	makeText := func(valueText string) *models.FeedbackRecord {
		rec, err := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			FieldID:      "q1",
			FieldType:    models.FieldTypeText,
			ValueText:    &valueText,
			TenantID:     tenantA,
			SubmissionID: testTenantID("sub"),
		})
		require.NoError(t, err)

		return rec
	}

	markFailure := func(recordID uuid.UUID, terminal bool, reason string) {
		_, err := db.Exec(ctx, `INSERT INTO feedback_record_enrichment_failures
			(feedback_record_id, enrichment, tenant_id, terminal, reason) VALUES ($1,$2,$3,$4,$5)`,
			recordID, models.EnrichmentNameTranslation, tenantA, terminal, reason)
		require.NoError(t, err)
	}

	plain := makeText("translate me")
	terminal := makeText("refused forever")
	transient := makeText("failed but retryable")

	markFailure(terminal.ID, true, string(huberrors.TerminalReasonContentFilter))
	markFailure(transient.ID, false, models.EnrichmentFailureReasonProviderError)

	// A terminal marker for a DIFFERENT enrichment must not exclude the record from translation:
	// the join is per (record, enrichment), and a record the sentiment provider refuses is still
	// perfectly translatable.
	otherEnrichment := makeText("sentiment refused, translation fine")
	_, err := db.Exec(ctx, `INSERT INTO feedback_record_enrichment_failures
		(feedback_record_id, enrichment, tenant_id, terminal, reason) VALUES ($1,'sentiment',$2,true,'content_filter')`,
		otherEnrichment.ID, tenantA)
	require.NoError(t, err)

	collect := func(targets []models.TranslationBackfillTarget) map[uuid.UUID]bool {
		got := map[uuid.UUID]bool{}
		for _, target := range targets {
			got[target.FeedbackRecordID] = true
		}

		return got
	}

	perTenant, err := repo.ListTranslationBackfillTargetsForTenant(ctx, tenantA, uuid.Nil, 100, "")
	require.NoError(t, err)

	global, err := repo.ListTranslationBackfillTargets(ctx, uuid.Nil, 1000, "")
	require.NoError(t, err)

	for name, got := range map[string]map[uuid.UUID]bool{
		"per-tenant": collect(perTenant),
		"global":     collect(global),
	} {
		assert.True(t, got[plain.ID], "%s: an untranslated record with no marker is a target", name)
		assert.True(t, got[transient.ID],
			"%s: a non-terminal failure is retryable, so it stays a target", name)
		assert.True(t, got[otherEnrichment.ID],
			"%s: another enrichment's terminal marker does not exclude translation", name)
		assert.False(t, got[terminal.ID],
			"%s: a terminally-failed record is excluded — re-enqueueing it buys a guaranteed refusal", name)
	}
}

// TestTenantTranslationBackfillWorkerUsesTheReconcileLane binds the queue the settings-triggered
// fan-out enqueues onto. A target-language change can fan out a tenant's whole history, and on
// the live `translations` queue that backlog sits in front of records arriving right now — the
// starvation the reconciler's separate lanes exist to prevent. Asserting the queue by name is
// the only thing that keeps a later refactor from quietly moving it back.
func TestTenantTranslationBackfillWorkerUsesTheReconcileLane(t *testing.T) {
	ctx := context.Background()
	pool, repo, tenantA, _ := backfillTestEnv(t)

	valueText := "translate me"
	rec, err := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
		SourceType:   "formbricks",
		FieldID:      "q1",
		FieldType:    models.FieldTypeText,
		ValueText:    &valueText,
		TenantID:     tenantA,
		SubmissionID: testTenantID("sub"),
	})
	require.NoError(t, err)

	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	require.NoError(t, err)

	svc := service.NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0, "")
	worker := workers.NewTenantTranslationBackfillWorker(svc, 3)

	job := &river.Job[service.TenantTranslationBackfillArgs]{
		JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 1},
		Args:   service.TenantTranslationBackfillArgs{TenantID: tenantA},
	}
	require.NoError(t, worker.Work(rivertest.WorkContext(ctx, riverClient), job))

	var queue string

	err = pool.QueryRow(ctx, `
		SELECT queue FROM river_job
		WHERE kind = 'feedback_translation' AND args->>'feedback_record_id' = $1`,
		rec.ID.String()).Scan(&queue)
	require.NoError(t, err)

	assert.Equal(t, service.TranslationsReconcileQueueName, queue,
		"bulk catch-up work belongs in the reconcile lane, not in front of live enrichment")
}
