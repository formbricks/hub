package tests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/pkg/database"
)

// TestFeedbackRecords_SentimentFields covers the ENG-1529 data-model step: sentiment /
// sentiment_score are NULL on a fresh record, round-trip through the read path once set
// (confirming the typed-enum scan), and the CHECK constraints reject an out-of-range score
// and an unknown label.
func TestFeedbackRecords_SentimentFields(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	repo := repository.NewFeedbackRecordsRepository(db)

	valueText := "Great product"
	created, err := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
		SourceType:   "formbricks",
		FieldID:      "q1",
		FieldType:    models.FieldTypeText,
		ValueText:    &valueText,
		TenantID:     testTenantID("sentiment"),
		SubmissionID: testTenantID("submission"),
	})
	require.NoError(t, err)

	// Fresh record: both enrichment fields are NULL until a record is enriched.
	require.Nil(t, created.Sentiment)
	require.Nil(t, created.SentimentScore)

	// A valid label + score round-trips through the read path (scan into the typed enum).
	_, err = db.Exec(ctx,
		`UPDATE feedback_records SET sentiment = $2, sentiment_score = $3 WHERE id = $1`,
		created.ID, models.SentimentPositive, 1.5)
	require.NoError(t, err)

	got, err := repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Sentiment)
	assert.Equal(t, models.SentimentPositive, *got.Sentiment)
	require.NotNil(t, got.SentimentScore)
	assert.InDelta(t, 1.5, *got.SentimentScore, 1e-9)

	// CHECK constraints reject an out-of-range score and an unknown label.
	_, err = db.Exec(ctx, `UPDATE feedback_records SET sentiment_score = 3.0 WHERE id = $1`, created.ID)
	require.Error(t, err, "score > 2 must violate feedback_records_sentiment_score_range")

	_, err = db.Exec(ctx, `UPDATE feedback_records SET sentiment = 'bogus' WHERE id = $1`, created.ID)
	require.Error(t, err, "unknown label must violate feedback_records_sentiment_valid")
}
