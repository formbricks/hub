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
		vt := valueText
		rec, createErr := frepo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			FieldID:      "q1",
			FieldType:    fieldType,
			ValueText:    &vt,
			TenantID:     tenant,
			SubmissionID: testTenantID("sub"),
		})
		require.NoError(t, createErr)

		return rec
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
		mkRecord(tenant, models.FieldTypeText, "")                   // empty content

		counts, err := statusRepo.CountEnrichmentStatus(ctx, tenant, "")
		require.NoError(t, err)

		assert.Equal(t, int64(3), counts.SentimentEligible, "3 text records with content are eligible")
		assert.Equal(t, int64(2), counts.SentimentDone, "doneAll + staleTrans have sentiment")
		assert.Equal(t, int64(3), counts.EmotionsEligible)
		assert.Equal(t, int64(1), counts.EmotionsDone, "only doneAll has emotions")
		assert.Equal(t, int64(3), counts.TranslationEligible, "all 3 have the effective target de-DE")
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
