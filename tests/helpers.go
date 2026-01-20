package tests

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/pkg/database"
	"github.com/stretchr/testify/require"
)

const testAPIKey = "test-api-key-12345"

// EnsureTestAPIKey ensures the test API key exists in the database
func EnsureTestAPIKey(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	require.NoError(t, err)
	defer db.Close()

	// Hash the test API key
	hash := sha256.Sum256([]byte(testAPIKey))
	keyHash := hex.EncodeToString(hash[:])

	// Insert or update the API key
	query := `
		INSERT INTO api_keys (key_hash, name, is_active)
		VALUES ($1, $2, $3)
		ON CONFLICT (key_hash) DO UPDATE SET is_active = true
	`

	_, err = db.Exec(ctx, query, keyHash, "Test API Key", true)
	require.NoError(t, err)
}

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
}
