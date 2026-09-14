package tests

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/pkg/database"
)

// TestCountEnrichmentStatus exercises the data-derived enrichment-status counts against Postgres,
// locking the gap-analysis edge cases: only text records with real content are eligible
// (field_type gate + whitespace trim), translation "done" means the stored lang key equals the
// effective target (stale != done), the sentiment/emotions per-tenant switch gates eligibility,
// the translation default-language fallback, and strict per-tenant isolation.
func TestCountEnrichmentStatus(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	statusRepo := repository.NewEnrichmentStatusRepository(db)
	frepo := repository.NewFeedbackRecordsRepository(db)
	tsRepo := repository.NewTenantSettingsRepository(db)

	// mkRecord inserts one feedback record for a tenant with an explicit field type + value_text.
	mkRecord := func(tenant string, fieldType models.FieldType, valueText string) *models.FeedbackRecord {
		return seedEnrichmentRecord(t, frepo, tenant, fieldType, valueText)
	}

	setSentiment := func(id uuid.UUID) {
		label := models.SentimentPositive
		score := 1.0
		require.NoError(t, frepo.SetSentiment(ctx, id, &label, &score, nil))
	}
	setEmotions := func(id uuid.UUID) {
		require.NoError(t, frepo.SetEmotions(ctx, id, []models.EmotionValue{models.EmotionJoy}, nil))
	}
	// setTranslationLang writes the translation columns directly to a given lang key, so a test can
	// stage both "done" (key == effective target) and "stale" (key != target) states precisely.
	setTranslationLang := func(id uuid.UUID, langKey string) {
		translated := "translated text"
		_, execErr := db.Exec(ctx,
			`UPDATE feedback_records SET value_text_translated = $1, translation_lang_key = $2 WHERE id = $3`,
			translated, langKey, id)
		require.NoError(t, execErr)
	}

	t.Run("eligibility, done buckets, and translation staleness", func(t *testing.T) {
		tenant := testTenantID("enrich-status-main")
		_, err := tsRepo.Upsert(ctx, tenant, models.EnrichmentSettings{TargetLanguage: "de-DE"})
		require.NoError(t, err)

		// Three eligible text records with content. The first stays fully un-enriched (pending in
		// every bucket); the other two are enriched below.
		mkRecord(tenant, models.FieldTypeText, "great product, would recommend")
		doneAll := mkRecord(tenant, models.FieldTypeText, "fully enriched record")
		staleTrans := mkRecord(tenant, models.FieldTypeText, "sentiment set, translation stale")

		// doneAll: sentiment + emotions set, translated to the effective target.
		setSentiment(doneAll.ID)
		setEmotions(doneAll.ID)
		setTranslationLang(doneAll.ID, "de-DE")

		// staleTrans: sentiment set (done), emotions NULL (pending), translated to a DIFFERENT
		// language than the tenant's target — so translation is NOT done.
		setSentiment(staleTrans.ID)
		setTranslationLang(staleTrans.ID, "fr-FR")

		// Ineligible rows that must NOT be counted:
		mkRecord(tenant, models.FieldTypeCategorical, "some choice") // non-text carries value_text
		mkRecord(tenant, models.FieldTypeText, "\t\n ")              // whitespace-only content
		mkRecord(tenant, models.FieldTypeText, "\v")                 // vertical tab is whitespace on every supported PG
		mkRecord(tenant, models.FieldTypeText, "\u00a0\u3000")       // Unicode whitespace mirrors Go TrimSpace
		mkRecord(tenant, models.FieldTypeText, "")                   // empty content
		mkRecord(tenant, models.FieldTypeText, "v")                  // PG16 must not treat E'\v' as this literal letter

		counts, err := statusRepo.CountEnrichmentStatus(ctx, tenant, "")
		require.NoError(t, err)

		assert.Equal(t, int64(4), counts.SentimentEligible, "4 text records with content are eligible")
		assert.Equal(t, int64(2), counts.SentimentDone, "doneAll + staleTrans have sentiment")
		assert.Equal(t, int64(4), counts.EmotionsEligible)
		assert.Equal(t, int64(1), counts.EmotionsDone, "only doneAll has emotions")
		assert.Equal(t, int64(4), counts.TranslationEligible, "all 4 have the effective target de-DE")
		assert.Equal(t, int64(1), counts.TranslationDone, "only doneAll's lang key matches; fr-FR is stale")
	})

	t.Run("sentiment switch off zeroes sentiment eligibility, emotions unaffected", func(t *testing.T) {
		tenant := testTenantID("enrich-status-switch")
		off := false
		_, err := tsRepo.Upsert(ctx, tenant, models.EnrichmentSettings{SentimentEnabled: &off})
		require.NoError(t, err)

		mkRecord(tenant, models.FieldTypeText, "a")
		mkRecord(tenant, models.FieldTypeText, "b")

		counts, err := statusRepo.CountEnrichmentStatus(ctx, tenant, "")
		require.NoError(t, err)

		assert.Equal(t, int64(0), counts.SentimentEligible, "sentiment disabled → not eligible")
		assert.Equal(t, int64(2), counts.EmotionsEligible, "emotions default-enabled → still eligible")
	})

	t.Run("emotions switch off zeroes emotions eligibility, sentiment unaffected", func(t *testing.T) {
		tenant := testTenantID("enrich-status-emotions-off")
		off := false
		_, err := tsRepo.Upsert(ctx, tenant, models.EnrichmentSettings{EmotionsEnabled: &off})
		require.NoError(t, err)

		mkRecord(tenant, models.FieldTypeText, "a")
		mkRecord(tenant, models.FieldTypeText, "b")

		counts, err := statusRepo.CountEnrichmentStatus(ctx, tenant, "")
		require.NoError(t, err)

		assert.Equal(t, int64(0), counts.EmotionsEligible, "emotions disabled → not eligible")
		assert.Equal(t, int64(2), counts.SentimentEligible, "sentiment default-enabled → still eligible")
	})

	t.Run("emotions completion is the marker, not the labels", func(t *testing.T) {
		tenant := testTenantID("enrich-status-emotions-done")

		// A successful classification that detects NO emotion stores NULL labels (the 015 CHECK
		// rejects an empty array). Without the completion marker it is indistinguishable from
		// "never classified" and would count as pending forever, so the backlog would never drain.
		classifiedEmpty := mkRecord(tenant, models.FieldTypeText, "the export ran at 3pm")
		require.NoError(t, frepo.SetEmotions(ctx, classifiedEmpty.ID, nil, nil))

		classifiedWithLabels := mkRecord(tenant, models.FieldTypeText, "I am thrilled")
		setEmotions(classifiedWithLabels.ID)

		mkRecord(tenant, models.FieldTypeText, "not processed yet")

		// A pre-020 row: labels present but no marker (the column did not exist when it was
		// enriched). Non-NULL labels are themselves proof of completion, which is why the migration
		// needs no bulk backfill.
		legacy := mkRecord(tenant, models.FieldTypeText, "enriched before the marker existed")
		setEmotions(legacy.ID)
		_, err := db.Exec(ctx,
			`UPDATE feedback_records SET emotions_classified_at = NULL WHERE id = $1`, legacy.ID)
		require.NoError(t, err)

		counts, err := statusRepo.CountEnrichmentStatus(ctx, tenant, "")
		require.NoError(t, err)

		assert.Equal(t, int64(4), counts.EmotionsEligible, "all four text records are eligible")
		assert.Equal(t, int64(3), counts.EmotionsDone,
			"classified-empty, classified-with-labels and the legacy row are all done; only the unprocessed one is pending")

		// The eager-clear on a content edit must drop the marker with the labels, or an edited
		// record would keep counting as classified while its emotions are gone.
		newText := "completely different text now"
		_, _, err = frepo.Update(ctx, classifiedWithLabels.ID,
			&models.UpdateFeedbackRecordRequest{ValueText: &newText})
		require.NoError(t, err)

		afterEdit, err := statusRepo.CountEnrichmentStatus(ctx, tenant, "")
		require.NoError(t, err)
		assert.Equal(t, int64(2), afterEdit.EmotionsDone, "editing the text returns that record to pending")
	})

	t.Run("translation eligibility follows the effective target", func(t *testing.T) {
		tenant := testTenantID("enrich-status-trans")
		// No tenant settings row at all → no own target language.
		mkRecord(tenant, models.FieldTypeText, "x")
		mkRecord(tenant, models.FieldTypeText, "y")

		noDefault, err := statusRepo.CountEnrichmentStatus(ctx, tenant, "")
		require.NoError(t, err)
		assert.Equal(t, int64(0), noDefault.TranslationEligible, "no target + no default → not eligible")

		withDefault, err := statusRepo.CountEnrichmentStatus(ctx, tenant, "en-US")
		require.NoError(t, err)
		assert.Equal(t, int64(2), withDefault.TranslationEligible, "default-language fallback makes records eligible")
	})

	t.Run("counts are strictly per-tenant", func(t *testing.T) {
		tenantA := testTenantID("enrich-status-iso-a")
		tenantB := testTenantID("enrich-status-iso-b")

		mkRecord(tenantA, models.FieldTypeText, "a1")

		for range 5 {
			mkRecord(tenantB, models.FieldTypeText, "b")
		}

		counts, err := statusRepo.CountEnrichmentStatus(ctx, tenantA, "")
		require.NoError(t, err)
		assert.Equal(t, int64(1), counts.SentimentEligible, "tenant A sees only its own record, not tenant B's")
	})
}

// seedEnrichmentRecord inserts one feedback record and returns it. Shared by the per-tenant and
// aggregate enrichment-status tests.
func seedEnrichmentRecord(
	t *testing.T, frepo *repository.FeedbackRecordsRepository, tenant string, fieldType models.FieldType, valueText string,
) *models.FeedbackRecord {
	t.Helper()

	vt := valueText
	rec, err := frepo.Create(context.Background(), &models.CreateFeedbackRecordRequest{
		SourceType:   "formbricks",
		FieldID:      "q1",
		FieldType:    fieldType,
		ValueText:    &vt,
		TenantID:     tenant,
		SubmissionID: testTenantID("sub"),
	})
	require.NoError(t, err)

	return rec
}

// TestCountEnrichmentBacklogAggregate covers the cross-tenant aggregate query that feeds the
// backlog gauge. The shared test DB holds records from other tests, so it asserts on the DELTA
// around a fresh seed rather than absolute totals — and verifies the per-tenant enable gate still
// applies (a sentiment-off tenant contributes to emotions but not sentiment).
func TestCountEnrichmentBacklogAggregate(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	statusRepo := repository.NewEnrichmentStatusRepository(db)
	frepo := repository.NewFeedbackRecordsRepository(db)
	tsRepo := repository.NewTenantSettingsRepository(db)

	const defaultLang = "en-US"

	pending := func(c repository.EnrichmentStatusCounts) (sentiment, emotions, translation int64) {
		return c.SentimentEligible - c.SentimentDone,
			c.EmotionsEligible - c.EmotionsDone,
			c.TranslationEligible - c.TranslationDone
	}

	before, err := statusRepo.CountEnrichmentBacklogAggregate(ctx, defaultLang)
	require.NoError(t, err)

	beforeSent, beforeEmo, beforeTrans := pending(before)

	// Tenant with everything enabled (no settings row → sentiment/emotions default-on; translation
	// via the en-US default): 3 un-enriched text records.
	enabled := testTenantID("agg-enabled")
	for range 3 {
		seedEnrichmentRecord(t, frepo, enabled, models.FieldTypeText, "pending record")
	}

	// Tenant with sentiment switched OFF: 2 un-enriched text records. These must add to emotions and
	// translation backlog but NOT sentiment.
	sentimentOff := testTenantID("agg-sentiment-off")
	off := false
	_, err = tsRepo.Upsert(ctx, sentimentOff, models.EnrichmentSettings{SentimentEnabled: &off})
	require.NoError(t, err)

	for range 2 {
		seedEnrichmentRecord(t, frepo, sentimentOff, models.FieldTypeText, "pending record")
	}

	after, err := statusRepo.CountEnrichmentBacklogAggregate(ctx, defaultLang)
	require.NoError(t, err)

	afterSent, afterEmo, afterTrans := pending(after)

	assert.Equal(t, int64(3), afterSent-beforeSent, "only the sentiment-enabled tenant's 3 records add to sentiment backlog")
	assert.Equal(t, int64(5), afterEmo-beforeEmo, "emotions default-enabled for both tenants → 3+2")
	assert.Equal(t, int64(5), afterTrans-beforeTrans, "en-US default makes all 5 records translation-pending")
}

// TestCountEnrichmentBacklogAggregateIfLeader covers the single-flight advisory lock that stops
// every API replica from repeating the same cross-tenant scan: one caller wins and gets the counts,
// a concurrent caller is denied (not an error) and skips its tick, and the lock is released
// afterwards so the next tick can win again.
func TestCountEnrichmentBacklogAggregateIfLeader(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	// Two independent pools stand in for two API replicas: the advisory lock is session-scoped and
	// held on one connection, so contention is only observable across separate connections.
	dbLeader, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer dbLeader.Close()

	dbRival, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer dbRival.Close()

	leaderOne := repository.NewEnrichmentBacklogLeader(dbLeader)
	leaderTwo := repository.NewEnrichmentBacklogLeader(dbRival)
	fallbackRepo := repository.NewFeedbackRecordsRepository(dbLeader)

	taxonomyModel := "taxonomy:leader-test:translated-v1"

	// Release leadership before the pools close. Close is idempotent, and without this an early
	// require failure would unwind into pgxpool.Close with the leader's connection still checked
	// out, wedging the whole suite until the test timeout instead of reporting the failure.
	defer leaderOne.Close(ctx)
	defer leaderTwo.Close(ctx)

	// First replica wins leadership and establishes the existing taxonomy backlog baseline.
	before, isLeader, err := leaderOne.CountIfLeader(ctx, "", taxonomyModel)
	require.NoError(t, err)
	require.True(t, isLeader, "the first replica to poll becomes the leader")

	seedEnrichmentRecord(t, fallbackRepo, testTenantID("taxonomy-backlog-text"), models.FieldTypeText, "taxonomy pending")
	seedEnrichmentRecord(t, fallbackRepo, testTenantID("taxonomy-backlog-ascii-space"), models.FieldTypeText, "\t\r\n")
	seedEnrichmentRecord(t, fallbackRepo, testTenantID("taxonomy-backlog-unicode-space"), models.FieldTypeText, "\u00a0\u3000")
	seedEnrichmentRecord(
		t, fallbackRepo, testTenantID("taxonomy-backlog-categorical"), models.FieldTypeCategorical, "not enrichable")

	counts, isLeader, err := leaderOne.CountIfLeader(ctx, "", taxonomyModel)
	require.NoError(t, err)
	require.True(t, isLeader)
	assert.Equal(t, before.TaxonomyEmbeddingPending+1, counts.TaxonomyEmbeddingPending,
		"only the non-blank text record enters the live translation-to-taxonomy backlog")

	// Prove the leader returns the real aggregate, not a zero value.
	want, err := repository.NewEnrichmentStatusRepository(dbLeader).CountEnrichmentBacklogAggregate(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, want.SentimentEligible, counts.SentimentEligible, "leader returns the true aggregate")

	// The second replica is denied — a normal skip, not an error — and must NOT publish counts.
	zero, isLeader, err := leaderTwo.CountIfLeader(ctx, "", "")
	require.NoError(t, err, "losing the leader election is not an error")
	assert.False(t, isLeader, "a second replica must not scan or export the global gauge")
	assert.Equal(t, repository.EnrichmentStatusCounts{}, zero, "non-leader returns no counts to publish")

	// Leadership is STICKY: unlike a scan-scoped lock, it persists across polls, so the same
	// replica keeps exporting the series instead of it flapping between replicas.
	_, stillLeader, err := leaderOne.CountIfLeader(ctx, "", "")
	require.NoError(t, err)
	assert.True(t, stillLeader, "leadership is held for the process lifetime, not per scan")

	_, stillDenied, err := leaderTwo.CountIfLeader(ctx, "", "")
	require.NoError(t, err)
	assert.False(t, stillDenied, "the follower stays a follower while the leader lives")

	// Releasing hands leadership over promptly rather than waiting for a session timeout.
	leaderOne.Close(ctx)

	_, promoted, err := leaderTwo.CountIfLeader(ctx, "", "")
	require.NoError(t, err)
	assert.True(t, promoted, "a follower is promoted once the leader releases")

	leaderTwo.Close(ctx)
}

// TestEnrichmentBacklogLeaderLeavesNoSessionState pins the release path: the leader sets a
// session-scoped idle_session_timeout on its connection, and pgxpool hands that same backend to
// unrelated callers afterwards. If release() did not RESET it, Postgres would eventually terminate
// some other component's pooled connection after 30 idle minutes. MaxConns=1 guarantees the
// connection reused below is the very one leadership was held on.
func TestEnrichmentBacklogLeaderLeavesNoSessionState(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL,
		database.WithPoolConfig(database.PoolConfig{MaxConns: 1, MinConns: 1}))
	require.NoError(t, err)

	defer db.Close()

	// Record the value the connection starts with, and prove this server/role actually honours the
	// SET/RESET pair. tryAcquire applies the timeout best-effort and still grants leadership if the
	// SET fails, so without this the assertion below could pass against an already-default value
	// without ever exercising RESET.
	var original string
	require.NoError(t, db.QueryRow(ctx, `SELECT current_setting('idle_session_timeout')`).Scan(&original))

	// set_config applies the value and returns it in one statement: pgx's extended protocol rejects
	// a multi-statement "SET ...; SELECT ...", and going through the pool (rather than holding an
	// acquired connection) means a failed assertion here cannot leave a connection checked out and
	// wedge db.Close().
	var probeSet string
	require.NoError(t, db.QueryRow(ctx,
		`SELECT set_config('idle_session_timeout', '30min', false)`).Scan(&probeSet))
	require.Equal(t, "30min", probeSet, "server must honour idle_session_timeout for this test to mean anything")

	_, err = db.Exec(ctx, `RESET idle_session_timeout`)
	require.NoError(t, err)

	leader := repository.NewEnrichmentBacklogLeader(db)

	// Registered before leadership is taken: if a require below fails, cleanup must still return the
	// connection or db.Close() would block on it and wedge the suite. Close is idempotent.
	defer leader.Close(ctx)

	_, isLeader, err := leader.CountIfLeader(ctx, "", "")
	require.NoError(t, err)
	require.True(t, isLeader, "single-connection pool must win leadership")

	leader.Close(ctx)

	// Same backend, borrowed as any other caller would: it must be back to what it started as, not
	// carrying the leader's 30min timeout.
	var afterRelease string
	require.NoError(t, db.QueryRow(ctx, `SELECT current_setting('idle_session_timeout')`).Scan(&afterRelease))
	assert.Equal(t, original, afterRelease, "the released connection must carry no leader-only session state")
}
