package tests

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/pkg/cursor"
	"github.com/formbricks/hub/pkg/database"
)

// setupFeedbackRecordsRepo creates a DB pool and feedback records repository for direct repository tests.
func setupFeedbackRecordsRepo(t *testing.T) (ctx context.Context, repo *repository.DBFeedbackRecordsRepository, cleanup func()) {
	ctx = context.Background()

	t.Helper()

	_ = godotenv.Load()
	if os.Getenv("DATABASE_URL") == "" {
		_ = godotenv.Load("../.env")
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultTestDatabaseURL
	}

	t.Setenv("API_KEY", testAPIKey)
	t.Setenv("DATABASE_URL", databaseURL)

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	require.NoError(t, err)

	repo = repository.NewDBFeedbackRecordsRepository(db)
	cleanup = func() { db.Close() }

	return ctx, repo, cleanup
}

func TestFeedbackRecordsRepository_List(t *testing.T) {
	ctx, repo, cleanup := setupFeedbackRecordsRepo(t)
	defer cleanup()

	tenantID := "feedback-repo-list-" + uuid.New().String()
	sourceType := "formbricks"
	userID := "feedback-list-user"

	t.Run("empty result", func(t *testing.T) {
		filters := &models.ListFeedbackRecordsFilters{
			TenantID: &tenantID,
			Limit:    10,
		}

		records, hasMore, err := repo.List(ctx, filters)
		require.NoError(t, err)
		assert.Empty(t, records)
		assert.False(t, hasMore)
	})

	t.Run("with data returns ordered by collected_at desc", func(t *testing.T) {
		subID := uuid.New().String()

		req1 := &models.CreateFeedbackRecordRequest{
			SourceType:     sourceType,
			SubmissionID:   subID,
			TenantID:       tenantID,
			FieldID:        "f1",
			FieldType:      models.FieldTypeNumber,
			ValueNumber:    ptrFloat64(1),
			UserIdentifier: &userID,
		}
		record1, err := repo.Create(ctx, req1)
		require.NoError(t, err)

		req2 := &models.CreateFeedbackRecordRequest{
			SourceType:     sourceType,
			SubmissionID:   subID,
			TenantID:       tenantID,
			FieldID:        "f2",
			FieldType:      models.FieldTypeNumber,
			ValueNumber:    ptrFloat64(2),
			UserIdentifier: &userID,
		}
		record2, err := repo.Create(ctx, req2)
		require.NoError(t, err)

		filters := &models.ListFeedbackRecordsFilters{
			TenantID: &tenantID,
			Limit:    10,
		}

		records, hasMore, err := repo.List(ctx, filters)
		require.NoError(t, err)
		require.Len(t, records, 2)
		assert.False(t, hasMore)
		assert.Equal(t, record2.ID, records[0].ID)
		assert.Equal(t, record1.ID, records[1].ID)
	})

	t.Run("limit and hasMore", func(t *testing.T) {
		tenantLimit := "feedback-repo-limit-" + uuid.New().String()
		subID := uuid.New().String()

		for i := range 5 {
			fieldID := fmt.Sprintf("field-%c", 'a'+i)
			_, err := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
				SourceType:     sourceType,
				SubmissionID:   subID,
				TenantID:       tenantLimit,
				FieldID:        fieldID,
				FieldType:      models.FieldTypeNumber,
				ValueNumber:    ptrFloat64(float64(i)),
				UserIdentifier: &userID,
			})
			require.NoError(t, err)
		}

		filters := &models.ListFeedbackRecordsFilters{
			TenantID: &tenantLimit,
			Limit:    2,
		}

		records, hasMore, err := repo.List(ctx, filters)
		require.NoError(t, err)
		require.Len(t, records, 2)
		assert.True(t, hasMore)
	})

	t.Run("filter by submission_id", func(t *testing.T) {
		tenantSub := "feedback-repo-sub-" + uuid.New().String()
		subID := "sub-filter-" + uuid.New().String()

		_, err := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:     sourceType,
			SubmissionID:   subID,
			TenantID:       tenantSub,
			FieldID:        "x",
			FieldType:      models.FieldTypeText,
			ValueText:      new("a"),
			UserIdentifier: &userID,
		})
		require.NoError(t, err)

		filters := &models.ListFeedbackRecordsFilters{
			TenantID:     &tenantSub,
			SubmissionID: &subID,
			Limit:        10,
		}

		records, _, err := repo.List(ctx, filters)
		require.NoError(t, err)
		require.Len(t, records, 1)
		assert.Equal(t, subID, records[0].SubmissionID)
	})

	t.Run("filter by source_type", func(t *testing.T) {
		tenantSrc := "feedback-repo-source-" + uuid.New().String()
		subID := uuid.New().String()
		customSource := "custom-source"

		_, err := repo.Create(ctx, &models.CreateFeedbackRecordRequest{
			SourceType:     customSource,
			SubmissionID:   subID,
			TenantID:       tenantSrc,
			FieldID:        "y",
			FieldType:      models.FieldTypeText,
			ValueText:      new("b"),
			UserIdentifier: &userID,
		})
		require.NoError(t, err)

		filters := &models.ListFeedbackRecordsFilters{
			TenantID:   &tenantSrc,
			SourceType: &customSource,
			Limit:      10,
		}

		records, _, err := repo.List(ctx, filters)
		require.NoError(t, err)
		require.Len(t, records, 1)
		assert.Equal(t, customSource, records[0].SourceType)
	})

	t.Run("limit <= 0 defaults to 100", func(t *testing.T) {
		tenantDefault := "feedback-repo-default-" + uuid.New().String()
		filters := &models.ListFeedbackRecordsFilters{
			TenantID: &tenantDefault,
			Limit:    0,
		}

		records, _, err := repo.List(ctx, filters)
		require.NoError(t, err)
		assert.NotNil(t, records)
	})
}

func TestFeedbackRecordsRepository_ListAfterCursor(t *testing.T) {
	ctx, repo, cleanup := setupFeedbackRecordsRepo(t)
	defer cleanup()

	tenantID := "feedback-repo-cursor-" + uuid.New().String()
	sourceType := "formbricks"
	userID := "feedback-cursor-user"
	subID := uuid.New().String()

	// Create 5 records (created[4] newest, created[0] oldest)
	created := make([]*models.FeedbackRecord, 0, 5)

	for i := range 5 {
		req := &models.CreateFeedbackRecordRequest{
			SourceType:     sourceType,
			SubmissionID:   subID,
			TenantID:       tenantID,
			FieldID:        fmt.Sprintf("cursor-%c", 'a'+i),
			FieldType:      models.FieldTypeNumber,
			ValueNumber:    ptrFloat64(float64(i)),
			UserIdentifier: &userID,
		}
		r, err := repo.Create(ctx, req)
		require.NoError(t, err)

		created = append(created, r)
	}

	filters := &models.ListFeedbackRecordsFilters{TenantID: &tenantID, Limit: 2}

	t.Run("first page", func(t *testing.T) {
		records, hasMore, err := repo.List(ctx, filters)
		require.NoError(t, err)
		require.Len(t, records, 2)
		assert.True(t, hasMore)
		assert.Equal(t, created[4].ID, records[0].ID)
		assert.Equal(t, created[3].ID, records[1].ID)
	})

	t.Run("second page via ListAfterCursor", func(t *testing.T) {
		last := created[3]
		records, hasMore, err := repo.ListAfterCursor(ctx, filters, last.CollectedAt, last.ID)
		require.NoError(t, err)
		require.Len(t, records, 2)
		assert.True(t, hasMore) // 5 total, limit 2 → page 2 has more
		assert.Equal(t, created[2].ID, records[0].ID)
		assert.Equal(t, created[1].ID, records[1].ID)
	})

	t.Run("third page (last)", func(t *testing.T) {
		last := created[1]
		records, hasMore, err := repo.ListAfterCursor(ctx, filters, last.CollectedAt, last.ID)
		require.NoError(t, err)
		require.Len(t, records, 1)
		assert.Equal(t, created[0].ID, records[0].ID)
		assert.False(t, hasMore)
	})

	t.Run("cursor round-trip with cursor package", func(t *testing.T) {
		page1, hasMore1, err := repo.List(ctx, filters)
		require.NoError(t, err)
		require.True(t, hasMore1)
		require.Len(t, page1, 2)

		last := page1[len(page1)-1]
		encoded, err := cursor.Encode(last.CollectedAt, last.ID)
		require.NoError(t, err)

		decodedAt, decodedID, err := cursor.Decode(encoded)
		require.NoError(t, err)

		page2, hasMore2, err := repo.ListAfterCursor(ctx, filters, decodedAt, decodedID)
		require.NoError(t, err)
		require.Len(t, page2, 2)
		assert.True(t, hasMore2)
	})
}
