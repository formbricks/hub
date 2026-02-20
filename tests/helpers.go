// Package tests provides integration test helpers and utilities.
package tests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/pkg/database"
)

const testAPIKey = "test-api-key-12345"

// CleanupTestData removes test data from the database.
func CleanupTestData(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL, &database.PoolConfig{
		MaxConns: cfg.DatabaseMaxConns, MinConns: cfg.DatabaseMinConns,
		MaxConnLifetime: cfg.DatabaseMaxConnLifetime,
	})
	require.NoError(t, err)

	defer db.Close()

	// Delete all feedback records created during tests
	// Be careful with this in production!
	_, err = db.Exec(ctx, "DELETE FROM feedback_records WHERE source_type = 'formbricks'")
	require.NoError(t, err)
}
