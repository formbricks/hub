package tests

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/pkg/database"
)

// TestFeedbackRecords_EnrichmentWritesSupersededOnContentChange locks the content-supersession
// guard on the classify persists (the write-side half of the concurrent-jobs race the embedding
// pipeline already guards): a job that read OLDER value_text must not land its result LAST over a
// newer job's write — a stale non-NULL label would escape the NULL-rows-only backfills forever.
// stillCurrent is the worker's Work-time snapshot comparison; here it is driven directly against
// the repository with a snapshot that does / does not match the row's current text.
func TestFeedbackRecords_EnrichmentWritesSupersededOnContentChange(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	repo := repository.NewFeedbackRecordsRepository(db)
	settingsRepo := repository.NewTenantSettingsRepository(db)

	const currentText = "The product is great"

	// guardFor mirrors the worker's valueTextStillCurrent: a TrimSpace-level comparison against
	// the value_text snapshot the (simulated) job classified.
	guardFor := func(snapshot string) func(valueText *string) bool {
		return func(valueText *string) bool {
			if valueText == nil {
				return snapshot == ""
			}

			return strings.TrimSpace(*valueText) == snapshot
		}
	}
	staleGuard := guardFor("older text a slow job read")
	currentGuard := guardFor(currentText)

	mkRecord := func(t *testing.T, slug string) *models.FeedbackRecord {
		t.Helper()

		tenantID := testTenantID(slug)
		// Seed a target so translation's own target guard passes and only the content guard trips.
		_, upsertErr := settingsRepo.Upsert(ctx, tenantID, models.EnrichmentSettings{TargetLanguage: "de-DE"})
		require.NoError(t, upsertErr)

		vt := currentText
		rec, createErr := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			FieldID:      "q1",
			FieldType:    models.FieldTypeText,
			ValueText:    &vt,
			TenantID:     tenantID,
			SubmissionID: testTenantID("submission"),
		})
		require.NoError(t, createErr)

		return rec
	}

	t.Run("sentiment: stale-content write is superseded, current-content write lands", func(t *testing.T) {
		rec := mkRecord(t, "supersede-sentiment")
		label := models.SentimentPositive
		score := 1.0

		err := repo.SetSentiment(ctx, rec.ID, &label, &score, staleGuard)
		require.ErrorIs(t, err, huberrors.ErrClassificationSuperseded)

		got, getErr := repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		assert.Nil(t, got.Sentiment, "a superseded write must not set sentiment")
		assert.Nil(t, got.SentimentScore, "a superseded write must not set sentiment_score")

		require.NoError(t, repo.SetSentiment(ctx, rec.ID, &label, &score, currentGuard))

		got, getErr = repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		require.NotNil(t, got.Sentiment)
		assert.Equal(t, models.SentimentPositive, *got.Sentiment)
	})

	t.Run("sentiment: stale-content clear is superseded and keeps the newer label", func(t *testing.T) {
		rec := mkRecord(t, "supersede-sentiment-clear")
		label := models.SentimentNegative
		score := -1.0
		require.NoError(t, repo.SetSentiment(ctx, rec.ID, &label, &score, currentGuard))

		// A clear enqueued for since-refilled text (the job read empty content) must not drop the
		// label a newer job wrote.
		err := repo.SetSentiment(ctx, rec.ID, nil, nil, guardFor(""))
		require.ErrorIs(t, err, huberrors.ErrClassificationSuperseded)

		got, getErr := repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		require.NotNil(t, got.Sentiment, "a superseded clear must keep the current label")
	})

	t.Run("emotions: stale-content write is superseded, current-content write lands", func(t *testing.T) {
		rec := mkRecord(t, "supersede-emotions")
		labels := []models.EmotionValue{models.EmotionJoy}

		err := repo.SetEmotions(ctx, rec.ID, labels, staleGuard)
		require.ErrorIs(t, err, huberrors.ErrClassificationSuperseded)

		got, getErr := repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		assert.Nil(t, got.Emotions, "a superseded write must not set emotions")

		require.NoError(t, repo.SetEmotions(ctx, rec.ID, labels, currentGuard))

		got, getErr = repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		require.NotNil(t, got.Emotions)
		assert.Equal(t, []models.EmotionValue{models.EmotionJoy}, *got.Emotions)
	})

	t.Run("translation: stale-content write is superseded, current-content write lands", func(t *testing.T) {
		rec := mkRecord(t, "supersede-translation")
		translated := "Das Produkt ist großartig"

		err := repo.SetTranslation(ctx, rec.ID, &translated, "de-DE", "", staleGuard)
		require.ErrorIs(t, err, huberrors.ErrTranslationSuperseded)

		got, getErr := repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		assert.Nil(t, got.ValueTextTranslated, "a superseded write must not set a translation")

		require.NoError(t, repo.SetTranslation(ctx, rec.ID, &translated, "de-DE", "", currentGuard))

		got, getErr = repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		require.NotNil(t, got.ValueTextTranslated)
		assert.Equal(t, translated, *got.ValueTextTranslated)
	})

	t.Run("nil guard stays unconditional", func(t *testing.T) {
		rec := mkRecord(t, "supersede-nil-guard")
		label := models.SentimentNeutral
		score := 0.0

		require.NoError(t, repo.SetSentiment(ctx, rec.ID, &label, &score, nil))

		got, getErr := repo.GetByID(ctx, rec.ID)
		require.NoError(t, getErr)
		require.NotNil(t, got.Sentiment, "a nil guard writes unconditionally")
	})
}
