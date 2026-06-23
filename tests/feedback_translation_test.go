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

	// Missing record: NotFound (resolved via the shared tenant write lock).
	err = repo.SetTranslation(ctx, uuid.New(), &translated, "de-DE")
	require.Error(t, err)
	assert.ErrorIs(t, err, huberrors.ErrNotFound, "a missing record returns NotFound")
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

	altTranslation := "alt"
	require.NoError(t, repo.SetTranslation(ctx, stale.ID, &altTranslation, "fr-FR")) // stale: target is de-DE

	currentTranslation := "Hallo"
	require.NoError(t, repo.SetTranslation(ctx, current.ID, &currentTranslation, "de-DE")) // matches target

	targets, err := repo.ListTranslationBackfillTargets(ctx)
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
	svc := service.NewFeedbackRecordsService(repo, nil, "", nil, nil, "", 0)

	createText := func(valueText, sourceLang string) *models.FeedbackRecord {
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

		return rec
	}

	t.Run("translates and persists", func(t *testing.T) {
		rec := createText("Bonjour le monde", "fr")
		fake := &fakeTranslationClient{out: "Hello world"}
		worker := workers.NewFeedbackTranslationWorker(svc, fake)

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
		rec := createText("Hello world", "en-US")
		fake := &fakeTranslationClient{out: "should-not-be-used"}
		worker := workers.NewFeedbackTranslationWorker(svc, fake)

		require.NoError(t, worker.Work(ctx, translationWorkerJob(rec.ID, "en-GB")))

		got, getErr := repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		require.NotNil(t, got.ValueTextTranslated)
		assert.Equal(t, "Hello world", *got.ValueTextTranslated, "source copied verbatim")
		assert.Equal(t, 0, fake.calls, "no provider call when source base+script == target")
	})

	t.Run("clears a stale translation when value_text is empty", func(t *testing.T) {
		rec := createText("", "fr")
		stale := "stale translation"
		require.NoError(t, repo.SetTranslation(ctx, rec.ID, &stale, "en-US"))

		worker := workers.NewFeedbackTranslationWorker(svc, &fakeTranslationClient{out: "unused"})
		require.NoError(t, worker.Work(ctx, translationWorkerJob(rec.ID, "en-US")))

		got, getErr := repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		assert.Nil(t, got.ValueTextTranslated, "stale translation cleared when value_text is empty")
	})
}
