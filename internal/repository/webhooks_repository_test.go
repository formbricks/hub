package repository

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/pkg/database"
)

const repositoryTestDatabaseURL = "postgres://postgres:postgres@localhost:5432/test_db?sslmode=disable"

func TestWebhooksRepository_ListEnabledForEventTypeAndTenant(t *testing.T) {
	ctx := context.Background()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = repositoryTestDatabaseURL
	}

	t.Setenv("DATABASE_URL", databaseURL)

	cfg, err := config.Load()
	require.NoError(t, err)
	db, err := database.NewPostgresPool(ctx, cfg.Database.URL,
		database.WithPoolConfig(cfg.Database.PoolConfig()),
	)
	require.NoError(t, err)

	defer db.Close()

	_, err = db.Exec(ctx, "DELETE FROM webhooks WHERE url LIKE 'https://tenant-scope.test/%'")
	require.NoError(t, err)

	defer func() {
		_, cleanupErr := db.Exec(ctx, "DELETE FROM webhooks WHERE url LIKE 'https://tenant-scope.test/%'")
		require.NoError(t, cleanupErr)
	}()

	repo := NewWebhooksRepository(db)
	tenantA := "repo-scope-tenant-a"
	tenantB := "repo-scope-tenant-b"
	disabled := false
	feedbackCreated := []datatypes.EventType{datatypes.FeedbackRecordCreated}
	feedbackUpdated := []datatypes.EventType{datatypes.FeedbackRecordUpdated}

	globalWebhook := createWebhookForRepositoryTest(ctx, t, repo, "https://tenant-scope.test/global", nil, nil)
	tenantAWebhook := createWebhookForRepositoryTest(ctx, t, repo, "https://tenant-scope.test/tenant-a", &tenantA, feedbackCreated)
	tenantBWebhook := createWebhookForRepositoryTest(ctx, t, repo, "https://tenant-scope.test/tenant-b", &tenantB, feedbackCreated)
	disabledTenantAWebhook := createWebhookForRepositoryTest(ctx, t, repo, "https://tenant-scope.test/disabled-a", &tenantA, feedbackCreated)
	_, err = repo.Update(ctx, disabledTenantAWebhook.ID, &models.UpdateWebhookRequest{Enabled: &disabled})
	require.NoError(t, err)
	createWebhookForRepositoryTest(ctx, t, repo, "https://tenant-scope.test/updated-only-a", &tenantA, feedbackUpdated)

	tenantAWebhooks, err := repo.ListEnabledForEventTypeAndTenant(ctx, datatypes.FeedbackRecordCreated.String(), &tenantA)
	require.NoError(t, err)
	assertRepositoryWebhookIDs(t, tenantAWebhooks, map[uuid.UUID]bool{
		globalWebhook.ID:  true,
		tenantAWebhook.ID: true,
	})

	tenantBWebhooks, err := repo.ListEnabledForEventTypeAndTenant(ctx, datatypes.FeedbackRecordCreated.String(), &tenantB)
	require.NoError(t, err)
	assertRepositoryWebhookIDs(t, tenantBWebhooks, map[uuid.UUID]bool{
		globalWebhook.ID:  true,
		tenantBWebhook.ID: true,
	})

	globalOnlyWebhooks, err := repo.ListEnabledForEventTypeAndTenant(ctx, datatypes.FeedbackRecordCreated.String(), nil)
	require.NoError(t, err)
	assertRepositoryWebhookIDs(t, globalOnlyWebhooks, map[uuid.UUID]bool{
		globalWebhook.ID: true,
	})
}

func createWebhookForRepositoryTest(
	ctx context.Context,
	t *testing.T,
	repo *WebhooksRepository,
	url string,
	tenantID *string,
	eventTypes []datatypes.EventType,
) *models.Webhook {
	t.Helper()

	webhook, err := repo.Create(ctx, &models.CreateWebhookRequest{
		URL:        url,
		SigningKey: "whsec_abcdefghijklmnopqrstuvwxyz123456",
		TenantID:   tenantID,
		EventTypes: eventTypes,
	})
	require.NoError(t, err)

	return webhook
}

func assertRepositoryWebhookIDs(t *testing.T, webhooks []models.Webhook, wantIDs map[uuid.UUID]bool) {
	t.Helper()

	gotIDs := make(map[uuid.UUID]bool, len(webhooks))
	for _, webhook := range webhooks {
		if !strings.HasPrefix(webhook.URL, "https://tenant-scope.test/") {
			continue
		}

		if !wantIDs[webhook.ID] {
			t.Fatalf("unexpected scoped test webhook returned: %+v", webhook)
		}

		gotIDs[webhook.ID] = true
	}

	assert.Len(t, gotIDs, len(wantIDs), "webhooks = %+v", webhooks)

	for id := range wantIDs {
		assert.True(t, gotIDs[id], "missing webhook %s in %+v", id, webhooks)
	}
}
