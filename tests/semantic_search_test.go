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

const searchTestModel = "search-test-model"

// searchVec builds a deterministic unit-ish vector whose cosine distance to searchVec(0) grows
// with n, so ordering assertions are exact: dimension 0 carries the signal, dimension 1 the rest.
func searchVec(n float64) []float32 {
	vec := make([]float32, models.EmbeddingVectorDimensions)
	vec[0] = float32(1 - n/10)
	vec[1] = float32(n / 10)

	return vec
}

// TestSemanticSearch_NearestFeedbackRecords executes the real nearest-neighbor SQL end to end —
// the one read path every layer above mocks (the COALESCE regression shipped through four review
// passes precisely because nothing ran these queries): ordering, tenant isolation, excludeID,
// minScore filtering, NULL-field-label coalescing, and keyset continuation via the cursor variant.
func TestSemanticSearch_NearestFeedbackRecords(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL, database.WithPoolConfig(cfg.Database.PoolConfig()))
	require.NoError(t, err)

	defer db.Close()

	recordsRepo := repository.NewFeedbackRecordsRepository(db)
	embeddingsRepo := repository.NewEmbeddingsRepository(db)

	tenantA := testTenantID("search-a")
	tenantB := testTenantID("search-b")

	mkEmbedded := func(tenantID, text string, label *string, closeness float64) uuid.UUID {
		valueText := text
		rec, createErr := recordsRepo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:   "formbricks",
			FieldID:      "q1",
			FieldLabel:   label,
			FieldType:    models.FieldTypeText,
			ValueText:    &valueText,
			TenantID:     tenantID,
			SubmissionID: testTenantID("sub"),
		})
		require.NoError(t, createErr)
		require.NoError(t, embeddingsRepo.Upsert(ctx, rec.ID, searchTestModel, searchVec(closeness), nil))

		return rec.ID
	}

	label := "How was it?"
	nearest := mkEmbedded(tenantA, "closest text", &label, 0)
	middle := mkEmbedded(tenantA, "middle text", nil, 2) // NULL field_label -> COALESCE to ""
	far := mkEmbedded(tenantA, "far text", &label, 5)
	otherTenant := mkEmbedded(tenantB, "other tenant text", &label, 0)

	query := searchVec(0)

	t.Run("orders by distance, isolates tenants, fills labels", func(t *testing.T) {
		results, _, searchErr := embeddingsRepo.NearestFeedbackRecordsByEmbedding(
			ctx, searchTestModel, query, tenantA, 10, nil, 0)
		require.NoError(t, searchErr)
		require.GreaterOrEqual(t, len(results), 3)

		assert.Equal(t, nearest, results[0].FeedbackRecordID, "closest vector first")
		assert.Equal(t, "How was it?", results[0].FieldLabel)
		assert.Equal(t, middle, results[1].FeedbackRecordID)
		assert.Empty(t, results[1].FieldLabel, "NULL field_label coalesces to empty")
		assert.Equal(t, far, results[2].FeedbackRecordID)
		assert.NotEmpty(t, results[0].ValueText, "value_text is projected")

		for _, r := range results {
			assert.NotEqual(t, otherTenant, r.FeedbackRecordID, "tenant B rows never leak into tenant A search")
			assert.InDelta(t, 1-r.Distance, r.Score, 1e-9, "score is 1 - distance")
		}
	})

	t.Run("excludeID drops the anchor record", func(t *testing.T) {
		results, _, searchErr := embeddingsRepo.NearestFeedbackRecordsByEmbedding(
			ctx, searchTestModel, query, tenantA, 10, &nearest, 0)
		require.NoError(t, searchErr)

		for _, r := range results {
			assert.NotEqual(t, nearest, r.FeedbackRecordID)
		}
	})

	t.Run("minScore filters far rows", func(t *testing.T) {
		results, _, searchErr := embeddingsRepo.NearestFeedbackRecordsByEmbedding(
			ctx, searchTestModel, query, tenantA, 10, nil, 0.99)
		require.NoError(t, searchErr)

		ids := make(map[uuid.UUID]bool, len(results))
		for _, r := range results {
			ids[r.FeedbackRecordID] = true
			assert.GreaterOrEqual(t, r.Score, 0.99)
		}

		assert.True(t, ids[nearest], "the identical vector passes the threshold")
		assert.False(t, ids[far], "the far vector is filtered out")
	})

	t.Run("cursor page is a disjoint continuation", func(t *testing.T) {
		page1, hasMore, searchErr := embeddingsRepo.NearestFeedbackRecordsByEmbedding(
			ctx, searchTestModel, query, tenantA, 1, nil, 0)
		require.NoError(t, searchErr)
		require.Len(t, page1, 1)
		assert.True(t, hasMore, "more rows exist past a 1-row page")

		last := page1[0]
		page2, _, searchErr := embeddingsRepo.NearestFeedbackRecordsByEmbeddingAfterCursor(
			ctx, searchTestModel, query, tenantA, 10, last.Distance, last.FeedbackRecordID, nil, 0)
		require.NoError(t, searchErr)
		require.NotEmpty(t, page2)

		for _, r := range page2 {
			assert.NotEqual(t, last.FeedbackRecordID, r.FeedbackRecordID, "page 2 never repeats the cursor row")
			assert.GreaterOrEqual(t, r.Distance, last.Distance, "page 2 continues at or past the cursor distance")
		}

		assert.Equal(t, middle, page2[0].FeedbackRecordID, "page 2 starts at the next-nearest row")
	})
}
