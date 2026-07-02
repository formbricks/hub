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

// TestListClassifyBackfillTargets covers the ENG-1623 backfill target queries against Postgres:
// a text record whose sentiment/emotions column is NULL is a target, and one already enriched is
// excluded. A large limit + membership lookup (rather than an exact count) keeps the assertion
// robust to other records already present in the shared test DB.
func TestListClassifyBackfillTargets(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	repo := repository.NewFeedbackRecordsRepository(db)
	tenant := testTenantID("classify-backfill")

	mkText := func(valueText string) *models.FeedbackRecord {
		vt := valueText
		rec, createErr := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			FieldID:      "q1",
			FieldType:    models.FieldTypeText,
			ValueText:    &vt,
			TenantID:     tenant,
			SubmissionID: testTenantID("sub"),
		})
		require.NoError(t, createErr)

		return rec
	}

	const bigLimit = 100000

	membership := func(ids []uuid.UUID) map[uuid.UUID]bool {
		m := make(map[uuid.UUID]bool, len(ids))
		for _, id := range ids {
			m[id] = true
		}

		return m
	}

	t.Run("sentiment IS NULL is a target; set is excluded", func(t *testing.T) {
		needsBackfill := mkText("great product, would recommend")
		alreadySet := mkText("already classified")

		label := models.SentimentPositive
		score := 1.0
		require.NoError(t, repo.SetSentiment(ctx, alreadySet.ID, &label, &score))

		ids, listErr := repo.ListSentimentBackfillTargets(ctx, uuid.Nil, bigLimit)
		require.NoError(t, listErr)

		targets := membership(ids)
		assert.True(t, targets[needsBackfill.ID], "text record with NULL sentiment is a backfill target")
		assert.False(t, targets[alreadySet.ID], "record whose sentiment is set is excluded")
	})

	t.Run("emotions IS NULL is a target; set is excluded", func(t *testing.T) {
		needsBackfill := mkText("I am thrilled and a little scared")
		alreadySet := mkText("already classified")

		require.NoError(t, repo.SetEmotions(ctx, alreadySet.ID, []models.EmotionValue{models.EmotionJoy}))

		ids, listErr := repo.ListEmotionsBackfillTargets(ctx, uuid.Nil, bigLimit)
		require.NoError(t, listErr)

		targets := membership(ids)
		assert.True(t, targets[needsBackfill.ID], "text record with NULL emotions is a backfill target")
		assert.False(t, targets[alreadySet.ID], "record whose emotions are set is excluded")
	})
}
