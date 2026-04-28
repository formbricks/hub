package tests

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
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/pkg/database"
)

const repositoryWebhookScopeURLPrefix = "https://tenant-scope.test/"

func TestWebhooksRepository_ListEnabledForEventTypeAndTenant(t *testing.T) {
	ctx := context.Background()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultTestDatabaseURL
	}

	t.Setenv("API_KEY", testAPIKey)
	t.Setenv("DATABASE_URL", databaseURL)

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL,
		database.WithPoolConfig(cfg.Database.PoolConfig()),
	)
	require.NoError(t, err)

	defer db.Close()

	cleanupRepositoryWebhookScopeTestRows := func() {
		_, cleanupErr := db.Exec(ctx, "DELETE FROM webhooks WHERE url LIKE $1", repositoryWebhookScopeURLPrefix+"%")
		require.NoError(t, cleanupErr)
	}

	cleanupRepositoryWebhookScopeTestRows()
	defer cleanupRepositoryWebhookScopeTestRows()

	repo := repository.NewWebhooksRepository(db)
	tenantA := "repo-scope-tenant-a"
	tenantB := "repo-scope-tenant-b"
	disabled := false
	feedbackCreated := []datatypes.EventType{datatypes.FeedbackRecordCreated}
	feedbackUpdated := []datatypes.EventType{datatypes.FeedbackRecordUpdated}

	globalWebhook := createWebhookForRepositoryScopeTest(ctx, t, repo, "global", nil, nil)
	tenantAWebhook := createWebhookForRepositoryScopeTest(ctx, t, repo, "tenant-a", &tenantA, feedbackCreated)
	tenantBWebhook := createWebhookForRepositoryScopeTest(ctx, t, repo, "tenant-b", &tenantB, feedbackCreated)
	disabledTenantAWebhook := createWebhookForRepositoryScopeTest(ctx, t, repo, "disabled-a", &tenantA, feedbackCreated)
	_, err = repo.Update(ctx, disabledTenantAWebhook.ID, &models.UpdateWebhookRequest{Enabled: &disabled})
	require.NoError(t, err)

	createWebhookForRepositoryScopeTest(ctx, t, repo, "updated-only-a", &tenantA, feedbackUpdated)

	tenantAWebhooks, err := repo.ListEnabledForEventTypeAndTenant(ctx, datatypes.FeedbackRecordCreated.String(), &tenantA)
	require.NoError(t, err)
	assertRepositoryScopeWebhookIDs(t, tenantAWebhooks, map[uuid.UUID]bool{
		globalWebhook.ID:  true,
		tenantAWebhook.ID: true,
	})

	tenantBWebhooks, err := repo.ListEnabledForEventTypeAndTenant(ctx, datatypes.FeedbackRecordCreated.String(), &tenantB)
	require.NoError(t, err)
	assertRepositoryScopeWebhookIDs(t, tenantBWebhooks, map[uuid.UUID]bool{
		globalWebhook.ID:  true,
		tenantBWebhook.ID: true,
	})

	globalOnlyWebhooks, err := repo.ListEnabledForEventTypeAndTenant(ctx, datatypes.FeedbackRecordCreated.String(), nil)
	require.NoError(t, err)
	assertRepositoryScopeWebhookIDs(t, globalOnlyWebhooks, map[uuid.UUID]bool{
		globalWebhook.ID: true,
	})
}

func createWebhookForRepositoryScopeTest(
	ctx context.Context,
	t *testing.T,
	repo *repository.WebhooksRepository,
	path string,
	tenantID *string,
	eventTypes []datatypes.EventType,
) *models.Webhook {
	t.Helper()

	webhook, err := repo.Create(ctx, &models.CreateWebhookRequest{
		URL:        repositoryWebhookScopeURLPrefix + path,
		SigningKey: "whsec_abcdefghijklmnopqrstuvwxyz123456",
		TenantID:   tenantID,
		EventTypes: eventTypes,
	})
	require.NoError(t, err)

	return webhook
}

func assertRepositoryScopeWebhookIDs(t *testing.T, webhooks []models.Webhook, wantIDs map[uuid.UUID]bool) {
	t.Helper()

	gotIDs := make(map[uuid.UUID]bool, len(webhooks))
	for _, webhook := range webhooks {
		if !strings.HasPrefix(webhook.URL, repositoryWebhookScopeURLPrefix) {
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
