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
	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/pkg/cursor"
	"github.com/formbricks/hub/pkg/database"
)

// setupWebhooksRepo creates a DB pool and webhooks repository for direct repository tests.
func setupWebhooksRepo(t *testing.T) (ctx context.Context, repo *repository.DBWebhooksRepository, cleanup func()) {
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

	repo = repository.NewDBWebhooksRepository(db)
	cleanup = func() { db.Close() }

	return ctx, repo, cleanup
}

func TestWebhooksRepository_List(t *testing.T) {
	ctx, repo, cleanup := setupWebhooksRepo(t)
	defer cleanup()

	tenantID := "webhooks-repo-list-" + uuid.New().String()

	t.Run("empty result", func(t *testing.T) {
		filters := &models.ListWebhooksFilters{
			TenantID: &tenantID,
			Limit:    10,
		}

		webhooks, hasMore, err := repo.List(ctx, filters)
		require.NoError(t, err)
		assert.Empty(t, webhooks)
		assert.False(t, hasMore)
	})

	t.Run("with data returns ordered by created_at desc", func(t *testing.T) {
		// Create webhooks
		req1 := &models.CreateWebhookRequest{
			URL:        "https://example.com/first",
			TenantID:   &tenantID,
			EventTypes: []datatypes.EventType{datatypes.FeedbackRecordCreated},
		}
		webhook1, err := repo.Create(ctx, req1)
		require.NoError(t, err)

		req2 := &models.CreateWebhookRequest{
			URL:        "https://example.com/second",
			TenantID:   &tenantID,
			EventTypes: []datatypes.EventType{datatypes.FeedbackRecordUpdated},
		}
		webhook2, err := repo.Create(ctx, req2)
		require.NoError(t, err)

		filters := &models.ListWebhooksFilters{
			TenantID: &tenantID,
			Limit:    10,
		}

		webhooks, hasMore, err := repo.List(ctx, filters)
		require.NoError(t, err)
		require.Len(t, webhooks, 2)
		assert.False(t, hasMore)
		// Newest first (webhook2 created after webhook1)
		assert.Equal(t, webhook2.ID, webhooks[0].ID)
		assert.Equal(t, webhook1.ID, webhooks[1].ID)
	})

	t.Run("limit and hasMore", func(t *testing.T) {
		tenantLimit := "webhooks-repo-limit-" + uuid.New().String()

		for i := range 5 {
			url := fmt.Sprintf("https://example.com/webhook-%c", 'a'+i)
			_, err := repo.Create(ctx, &models.CreateWebhookRequest{
				URL:      url,
				TenantID: &tenantLimit,
			})
			require.NoError(t, err)
		}

		filters := &models.ListWebhooksFilters{
			TenantID: &tenantLimit,
			Limit:    2,
		}

		webhooks, hasMore, err := repo.List(ctx, filters)
		require.NoError(t, err)
		require.Len(t, webhooks, 2)
		assert.True(t, hasMore)
	})

	t.Run("filter by enabled", func(t *testing.T) {
		tenantEnabled := "webhooks-repo-enabled-" + uuid.New().String()
		enabledVal := true
		disabled := false

		_, err := repo.Create(ctx, &models.CreateWebhookRequest{
			URL:      "https://example.com/enabled",
			TenantID: &tenantEnabled,
			Enabled:  &enabledVal,
		})
		require.NoError(t, err)

		_, err = repo.Create(ctx, &models.CreateWebhookRequest{
			URL:      "https://example.com/disabled",
			TenantID: &tenantEnabled,
			Enabled:  &disabled,
		})
		require.NoError(t, err)

		filters := &models.ListWebhooksFilters{
			TenantID: &tenantEnabled,
			Enabled:  &enabledVal,
			Limit:    10,
		}

		webhooks, _, err := repo.List(ctx, filters)
		require.NoError(t, err)
		require.Len(t, webhooks, 1)
		assert.True(t, webhooks[0].Enabled)
	})

	t.Run("limit <= 0 defaults to 100", func(t *testing.T) {
		tenantDefault := "webhooks-repo-default-" + uuid.New().String()
		filters := &models.ListWebhooksFilters{
			TenantID: &tenantDefault,
			Limit:    0,
		}

		webhooks, _, err := repo.List(ctx, filters)
		require.NoError(t, err)
		assert.NotNil(t, webhooks)
	})
}

func TestWebhooksRepository_ListAfterCursor(t *testing.T) {
	ctx, repo, cleanup := setupWebhooksRepo(t)
	defer cleanup()

	tenantID := "webhooks-repo-cursor-" + uuid.New().String()

	// Create 5 webhooks (created[4] newest, created[0] oldest)
	created := make([]*models.Webhook, 0, 5)

	for i := range 5 {
		w, err := repo.Create(ctx, &models.CreateWebhookRequest{
			URL:      fmt.Sprintf("https://example.com/cursor-%c", 'a'+i),
			TenantID: &tenantID,
		})
		require.NoError(t, err)

		created = append(created, w)
	}

	filters := &models.ListWebhooksFilters{TenantID: &tenantID, Limit: 2}

	t.Run("first page", func(t *testing.T) {
		webhooks, hasMore, err := repo.List(ctx, filters)
		require.NoError(t, err)
		require.Len(t, webhooks, 2)
		assert.True(t, hasMore)
		assert.Equal(t, created[4].ID, webhooks[0].ID)
		assert.Equal(t, created[3].ID, webhooks[1].ID)
	})

	t.Run("second page via ListAfterCursor", func(t *testing.T) {
		last := created[3]
		webhooks, hasMore, err := repo.ListAfterCursor(ctx, filters, last.CreatedAt, last.ID)
		require.NoError(t, err)
		require.Len(t, webhooks, 2)
		assert.True(t, hasMore) // 5 total, limit 2 → page 2 has more
		assert.Equal(t, created[2].ID, webhooks[0].ID)
		assert.Equal(t, created[1].ID, webhooks[1].ID)
	})

	t.Run("third page (last)", func(t *testing.T) {
		last := created[1]
		webhooks, hasMore, err := repo.ListAfterCursor(ctx, filters, last.CreatedAt, last.ID)
		require.NoError(t, err)
		require.Len(t, webhooks, 1)
		assert.Equal(t, created[0].ID, webhooks[0].ID)
		assert.False(t, hasMore)
	})

	t.Run("cursor round-trip with filters", func(t *testing.T) {
		page1, hasMore1, err := repo.List(ctx, filters)
		require.NoError(t, err)
		require.True(t, hasMore1)
		require.Len(t, page1, 2)

		last := page1[len(page1)-1]
		page2, hasMore2, err := repo.ListAfterCursor(ctx, filters, last.CreatedAt, last.ID)
		require.NoError(t, err)
		require.Len(t, page2, 2)
		assert.True(t, hasMore2)

		last2 := page2[len(page2)-1]
		page3, hasMore3, err := repo.ListAfterCursor(ctx, filters, last2.CreatedAt, last2.ID)
		require.NoError(t, err)
		require.Len(t, page3, 1)
		assert.False(t, hasMore3)
	})
}

func TestWebhooksRepository_List_verifyCursorEncoding(t *testing.T) {
	ctx, repo, cleanup := setupWebhooksRepo(t)
	defer cleanup()

	tenantID := "webhooks-repo-encode-" + uuid.New().String()

	w, err := repo.Create(ctx, &models.CreateWebhookRequest{
		URL:      "https://example.com/encode",
		TenantID: &tenantID,
	})
	require.NoError(t, err)

	_, hasMore, err := repo.List(ctx, &models.ListWebhooksFilters{
		TenantID: &tenantID,
		Limit:    1,
	})
	require.NoError(t, err)
	assert.False(t, hasMore)

	encoded, err := cursor.Encode(w.CreatedAt, w.ID)
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	decodedAt, decodedID, err := cursor.Decode(encoded)
	require.NoError(t, err)
	assert.True(t, decodedAt.Equal(w.CreatedAt))
	assert.Equal(t, w.ID, decodedID)
}
