package tests

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
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
