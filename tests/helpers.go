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

// pendingPageLimit bounds every read of the reconciler's pending set in this package.
//
// ListPendingEnrichment is deliberately cross-tenant, so this is a GLOBAL limit and callers filter
// by tenant in Go afterwards. That makes their assertions depend on the seeded records landing
// inside the page: enough unrelated un-enriched text records in the shared database and the seeds
// fall off the end, the sets come back empty, and the failure blames the query rather than the
// page. Set far above what this suite creates, and every caller pairs it with a require.Less guard
// so a full page names itself.
//
// One constant rather than one per file: the limit and the guard only work as a pair, and two
// copies drift.
const pendingPageLimit = 20000

// CleanupTestData removes test data from the database.
func CleanupTestData(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.Database.URL,
		database.WithPoolConfig(cfg.Database.PoolConfig()),
	)
	require.NoError(t, err)

	defer db.Close()

	// Delete all feedback records created during tests
	// Be careful with this in production!
	_, err = db.Exec(ctx, "DELETE FROM feedback_records WHERE source_type = 'formbricks'")
	require.NoError(t, err)
}
