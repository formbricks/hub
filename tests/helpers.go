package tests

import (
	"context"
	"testing"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/pkg/database"
	"github.com/stretchr/testify/require"
)

const testAPIKey = "test-api-key-12345"

// CleanupTestData removes test data from the database
func CleanupTestData(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	require.NoError(t, err)
	defer db.Close()

	// Delete all feedback records created during tests
	// Be careful with this in production!
	_, err = db.Exec(ctx, "DELETE FROM feedback_records WHERE source_type = 'formbricks'")
	require.NoError(t, err)

	// Delete all knowledge records created during tests
	_, err = db.Exec(ctx, "DELETE FROM knowledge_records")
	require.NoError(t, err)

	// Delete all topics created during tests
	_, err = db.Exec(ctx, "DELETE FROM topics")
	require.NoError(t, err)
}
