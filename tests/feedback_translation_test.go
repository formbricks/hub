package tests

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
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

// TestFeedbackRecords_SetTranslation locks the SetTranslation write contract: it
// persists the translated text + target locale (round-tripping through GetByID and
// the shared scanFeedbackRecord), clears them when the translation is nil, leaves
// the source value_text untouched, and returns NotFound for a missing record. The
// async worker exercises this end-to-end; this covers the repo paths directly,
// including the not-found path the worker test would not reach.
func TestFeedbackRecords_SetTranslation(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	repo := repository.NewFeedbackRecordsRepository(db)

	tenantID := testTenantID("set-translation")
	valueText := "Hello, world"

	// A translation write lands only while it matches the tenant's current target_language,
	// so seed that target before exercising the write contract.
	settingsRepo := repository.NewTenantSettingsRepository(db)
	_, err = settingsRepo.Upsert(ctx, tenantID, models.EnrichmentSettings{TargetLanguage: "de-DE"})
	require.NoError(t, err)

	created, err := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
		SourceType:   "formbricks",
		FieldID:      "q1",
		FieldType:    models.FieldTypeText,
		ValueText:    &valueText,
		TenantID:     tenantID,
		SubmissionID: testTenantID("submission"),
	})
	require.NoError(t, err)

	// A fresh record has no translation yet.
	require.Nil(t, created.ValueTextTranslated)
	require.Nil(t, created.TranslationLangKey)

	// Success: translated text + target locale persist and round-trip via GetByID.
	translated := "Hallo, Welt"
	require.NoError(t, repo.SetTranslation(ctx, created.ID, &translated, "de-DE"))

	got, err := repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ValueTextTranslated)
	assert.Equal(t, "Hallo, Welt", *got.ValueTextTranslated)
	require.NotNil(t, got.TranslationLangKey)
	assert.Equal(t, "de-DE", *got.TranslationLangKey)
	require.NotNil(t, got.ValueText)
	assert.Equal(t, "Hello, world", *got.ValueText, "source value_text must be preserved")

	// Clearing: a nil translation nulls the column.
	require.NoError(t, repo.SetTranslation(ctx, created.ID, nil, "de-DE"))

	cleared, err := repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	assert.Nil(t, cleared.ValueTextTranslated, "nil translation clears value_text_translated")
	assert.Nil(t, cleared.TranslationLangKey, "clearing also nulls translation_lang_key")

	// Missing record: NotFound (resolved via the shared tenant write lock).
	err = repo.SetTranslation(ctx, uuid.New(), &translated, "de-DE")
	require.Error(t, err)
	assert.ErrorIs(t, err, huberrors.ErrNotFound, "a missing record returns NotFound")
}

// TestFeedbackRecords_SetTranslation_StaleTargetIsNoOp locks the out-of-order-write guard: a
// translation whose target no longer matches the tenant's current target_language (an older
// job finishing after a target change / stale-cache enqueue) is a no-op that returns
// ErrTranslationSuperseded and never clobbers the current translation.
func TestFeedbackRecords_SetTranslation_StaleTargetIsNoOp(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	repo := repository.NewFeedbackRecordsRepository(db)
	settingsRepo := repository.NewTenantSettingsRepository(db)

	tenantID := testTenantID("stale-translation")
	valueText := "Hello, world"

	// Tenant's current target is de-DE.
	_, err = settingsRepo.Upsert(ctx, tenantID, models.EnrichmentSettings{TargetLanguage: "de-DE"})
	require.NoError(t, err)

	created, err := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
		SourceType:   "formbricks",
		FieldID:      "q1",
		FieldType:    models.FieldTypeText,
		ValueText:    &valueText,
		TenantID:     tenantID,
		SubmissionID: testTenantID("submission"),
	})
	require.NoError(t, err)

	// A stale-target write (fr-FR — an older job or a stale-cache enqueue) must not land.
	stale := "Bonjour le monde"
	err = repo.SetTranslation(ctx, created.ID, &stale, "fr-FR")
	require.ErrorIs(t, err, huberrors.ErrTranslationSuperseded, "stale-target write is superseded")

	got, err := repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	assert.Nil(t, got.ValueTextTranslated, "stale-target write must not set a translation")
	assert.Nil(t, got.TranslationLangKey, "stale-target write must not set a lang key")

	// A current-target write (de-DE) lands normally.
	current := "Hallo, Welt"
	require.NoError(t, repo.SetTranslation(ctx, created.ID, &current, "de-DE"))

	got, err = repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, got.TranslationLangKey)
	assert.Equal(t, "de-DE", *got.TranslationLangKey)
	require.NotNil(t, got.ValueTextTranslated)
	assert.Equal(t, "Hallo, Welt", *got.ValueTextTranslated)

	// After the tenant switches its target to fr-FR, the previously-stale fr-FR write now
	// lands — proving the guard tracks the current target rather than a fixed value.
	_, err = settingsRepo.Upsert(ctx, tenantID, models.EnrichmentSettings{TargetLanguage: "fr-FR"})
	require.NoError(t, err)

	require.NoError(t, repo.SetTranslation(ctx, created.ID, &stale, "fr-FR"))

	got, err = repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, got.TranslationLangKey)
	assert.Equal(t, "fr-FR", *got.TranslationLangKey)
	require.NotNil(t, got.ValueTextTranslated)
	assert.Equal(t, "Bonjour le monde", *got.ValueTextTranslated)
}

// TestFeedbackRecords_ListTranslationBackfillTargets verifies the backfill query: it
// returns text records whose tenant has a target language and whose stored
// translation_lang_key differs from it (never translated, or stale), and excludes
// records already current and tenants with no target.
func TestFeedbackRecords_ListTranslationBackfillTargets(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	repo := repository.NewFeedbackRecordsRepository(db)
	settingsRepo := repository.NewTenantSettingsRepository(db)

	tenantWithTarget := testTenantID("backfill-target")
	tenantNoTarget := testTenantID("backfill-notarget")

	_, err = settingsRepo.Upsert(ctx, tenantWithTarget, models.EnrichmentSettings{TargetLanguage: "de-DE"})
	require.NoError(t, err)

	makeTextRecord := func(tenantID, valueText string) *models.FeedbackRecord {
		rec, createErr := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			FieldID:      "q1",
			FieldType:    models.FieldTypeText,
			ValueText:    &valueText,
			TenantID:     tenantID,
			SubmissionID: testTenantID("sub"),
		})
		require.NoError(t, createErr)

		return rec
	}

	untranslated := makeTextRecord(tenantWithTarget, "needs translation")
	stale := makeTextRecord(tenantWithTarget, "stale translation")
	current := makeTextRecord(tenantWithTarget, "already current")
	noTarget := makeTextRecord(tenantNoTarget, "tenant has no target")

	// Seed a stale translation: SetTranslation only persists a write matching the tenant's
	// current target, so briefly switch the target to fr-FR, write, then restore de-DE — the
	// stored fr-FR is now stale relative to the de-DE target the backfill query reads.
	altTranslation := "alt"
	_, err = settingsRepo.Upsert(ctx, tenantWithTarget, models.EnrichmentSettings{TargetLanguage: "fr-FR"})
	require.NoError(t, err)
	require.NoError(t, repo.SetTranslation(ctx, stale.ID, &altTranslation, "fr-FR"))
	_, err = settingsRepo.Upsert(ctx, tenantWithTarget, models.EnrichmentSettings{TargetLanguage: "de-DE"})
	require.NoError(t, err)

	currentTranslation := "Hallo"
	require.NoError(t, repo.SetTranslation(ctx, current.ID, &currentTranslation, "de-DE")) // matches target

	targets, err := repo.ListTranslationBackfillTargets(ctx, uuid.Nil, 100)
	require.NoError(t, err)

	byID := make(map[uuid.UUID]string, len(targets))
	for _, target := range targets {
		byID[target.FeedbackRecordID] = target.TargetLang
	}

	assert.Equal(t, "de-DE", byID[untranslated.ID], "untranslated record is a backfill target for de-DE")
	assert.Equal(t, "de-DE", byID[stale.ID], "stale translation (fr-FR != de-DE) is a backfill target")

	_, currentPresent := byID[current.ID]
	assert.False(t, currentPresent, "record already translated to the current target must be excluded")

	_, noTargetPresent := byID[noTarget.ID]
	assert.False(t, noTargetPresent, "record whose tenant has no target must be excluded")
}

type fakeTranslationClient struct {
	out   string
	calls int
}

func (f *fakeTranslationClient) Translate(_ context.Context, _ service.TranslateRequest) (string, error) {
	f.calls++

	return f.out, nil
}

func translationWorkerJob(recordID uuid.UUID, targetLang string) *river.Job[service.FeedbackTranslationArgs] {
	return &river.Job[service.FeedbackTranslationArgs]{
		JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 3},
		Args: service.FeedbackTranslationArgs{
			FeedbackRecordID: recordID,
			TargetLang:       targetLang,
			ValueTextHash:    "h",
		},
	}
}

// TestFeedbackTranslation_WorkerPipeline drives FeedbackTranslationWorker end to end
// against Postgres (with a fake translation client): it translates and persists,
// copies verbatim when the source already matches the target (no provider call), and
// clears a stale translation when value_text is empty.
func TestFeedbackTranslation_WorkerPipeline(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	repo := repository.NewFeedbackRecordsRepository(db)
	settingsRepo := repository.NewTenantSettingsRepository(db)
	svc := service.NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0)

	// Each record gets a fresh tenant; seed that tenant's target so the translation write
	// (which now only lands while it matches the current target) succeeds.
	createText := func(valueText, sourceLang, target string) *models.FeedbackRecord {
		req := &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			FieldID:      "q1",
			FieldType:    models.FieldTypeText,
			ValueText:    &valueText,
			TenantID:     testTenantID("worker-pipeline"),
			SubmissionID: testTenantID("sub"),
		}
		if sourceLang != "" {
			req.Language = &sourceLang
		}

		rec, createErr := repo.Create(ctx, req)
		require.NoError(t, createErr)

		if target != "" {
			_, seedErr := settingsRepo.Upsert(ctx, rec.TenantID, models.EnrichmentSettings{TargetLanguage: target})
			require.NoError(t, seedErr)
		}

		return rec
	}

	t.Run("translates and persists", func(t *testing.T) {
		rec := createText("Bonjour le monde", "fr", "en-US")
		fake := &fakeTranslationClient{out: "Hello world"}
		worker := workers.NewFeedbackTranslationWorker(svc, fake, nil)

		require.NoError(t, worker.Work(ctx, translationWorkerJob(rec.ID, "en-US")))

		got, getErr := repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		require.NotNil(t, got.ValueTextTranslated)
		assert.Equal(t, "Hello world", *got.ValueTextTranslated)
		require.NotNil(t, got.TranslationLangKey)
		assert.Equal(t, "en-US", *got.TranslationLangKey)
		assert.Equal(t, 1, fake.calls)
		require.NotNil(t, got.ValueText)
		assert.Equal(t, "Bonjour le monde", *got.ValueText, "source value_text preserved")
	})

	t.Run("copies when source matches target without calling the provider", func(t *testing.T) {
		rec := createText("Hello world", "en-US", "en-GB")
		fake := &fakeTranslationClient{out: "should-not-be-used"}
		worker := workers.NewFeedbackTranslationWorker(svc, fake, nil)

		require.NoError(t, worker.Work(ctx, translationWorkerJob(rec.ID, "en-GB")))

		got, getErr := repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		require.NotNil(t, got.ValueTextTranslated)
		assert.Equal(t, "Hello world", *got.ValueTextTranslated, "source copied verbatim")
		assert.Equal(t, 0, fake.calls, "no provider call when source base+script == target")
	})

	t.Run("clears a stale translation when value_text is empty", func(t *testing.T) {
		rec := createText("", "fr", "en-US")
		stale := "stale translation"
		require.NoError(t, repo.SetTranslation(ctx, rec.ID, &stale, "en-US"))

		worker := workers.NewFeedbackTranslationWorker(svc, &fakeTranslationClient{out: "unused"}, nil)
		require.NoError(t, worker.Work(ctx, translationWorkerJob(rec.ID, "en-US")))

		got, getErr := repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		assert.Nil(t, got.ValueTextTranslated, "stale translation cleared when value_text is empty")
		assert.Nil(t, got.TranslationLangKey, "clearing path must also null translation_lang_key")
	})

	t.Run("stale-target write is superseded without persisting", func(t *testing.T) {
		rec := createText("Hallo Welt", "de", "de-DE") // tenant's current target is de-DE

		worker := workers.NewFeedbackTranslationWorker(svc, &fakeTranslationClient{out: "Bonjour le monde"}, nil)

		// The job's target (fr-FR) no longer matches the tenant's current target (de-DE) — an
		// older job or a stale-cache enqueue. The write must no-op and the job must complete
		// (not fail or retry), leaving the record untranslated.
		require.NoError(t, worker.Work(ctx, translationWorkerJob(rec.ID, "fr-FR")))

		got, getErr := repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		assert.Nil(t, got.ValueTextTranslated, "stale-target translation must not be persisted")
		assert.Nil(t, got.TranslationLangKey, "stale-target lang key must not be persisted")
	})
}

// TestFeedbackRecords_UpdateClearsTranslationOnlyOnContentChange locks the update-clears-stale
// behavior: re-sending the SAME value_text keeps the existing translation (so a deduped
// re-translation can't strand the record), while an actual value_text change clears it so the
// UI falls back to the original and the row becomes a backfill target.
func TestFeedbackRecords_UpdateClearsTranslationOnlyOnContentChange(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	repo := repository.NewFeedbackRecordsRepository(db)
	settingsRepo := repository.NewTenantSettingsRepository(db)

	tenantID := testTenantID("update-clears-translation")
	original := "Hello, world"

	// Tenant target de-DE so the translation write lands.
	_, err = settingsRepo.Upsert(ctx, tenantID, models.EnrichmentSettings{TargetLanguage: "de-DE"})
	require.NoError(t, err)

	created, err := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
		SourceType:   "formbricks",
		FieldID:      "q1",
		FieldType:    models.FieldTypeText,
		ValueText:    &original,
		TenantID:     tenantID,
		SubmissionID: testTenantID("submission"),
	})
	require.NoError(t, err)

	translated := "Hallo, Welt"
	require.NoError(t, repo.SetTranslation(ctx, created.ID, &translated, "de-DE"))

	// Re-sending the SAME value_text must NOT clear the translation — otherwise a deduped
	// re-translation (identical content hash) would strand the record with no translation.
	same := original
	if _, err = repo.Update(ctx, created.ID, &models.UpdateFeedbackRecordRequest{ValueText: &same}); err != nil {
		t.Fatalf("Update(same value_text) error = %v", err)
	}

	got, err := repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ValueTextTranslated, "unchanged value_text must keep the translation")
	assert.Equal(t, "Hallo, Welt", *got.ValueTextTranslated)
	require.NotNil(t, got.TranslationLangKey)
	assert.Equal(t, "de-DE", *got.TranslationLangKey)

	// An actual value_text change clears the now-stale translation.
	changed := "Goodbye, world"
	if _, err = repo.Update(ctx, created.ID, &models.UpdateFeedbackRecordRequest{ValueText: &changed}); err != nil {
		t.Fatalf("Update(changed value_text) error = %v", err)
	}

	got, err = repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	assert.Nil(t, got.ValueTextTranslated, "changed value_text must clear the stale translation")
	assert.Nil(t, got.TranslationLangKey, "changed value_text must clear the translation lang key")
}
