package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestAPIKeyRepository creates a repository with a mock DB for testing
func newTestAPIKeyRepository(mock pgxmock.PgxPoolIface) *APIKeyRepository {
	return &APIKeyRepository{db: mock}
}

func TestNewAPIKeyRepository(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := newTestAPIKeyRepository(mock)

	assert.NotNil(t, repo, "Repository should not be nil")
	assert.Equal(t, mock, repo.db, "Repository should store the database pool")
}

func TestHashAPIKey(t *testing.T) {
	tests := []struct {
		name   string
		apiKey string
		want   string
	}{
		{
			name:   "hashes simple API key",
			apiKey: "test-api-key-123",
			want: func() string {
				hash := sha256.Sum256([]byte("test-api-key-123"))
				return hex.EncodeToString(hash[:])
			}(),
		},
		{
			name:   "hashes empty string",
			apiKey: "",
			want: func() string {
				hash := sha256.Sum256([]byte(""))
				return hex.EncodeToString(hash[:])
			}(),
		},
		{
			name:   "hashes long API key",
			apiKey: "very-long-api-key-with-many-characters-1234567890abcdefghijklmnopqrstuvwxyz",
			want: func() string {
				hash := sha256.Sum256([]byte("very-long-api-key-with-many-characters-1234567890abcdefghijklmnopqrstuvwxyz"))
				return hex.EncodeToString(hash[:])
			}(),
		},
		{
			name:   "produces consistent hash",
			apiKey: "consistent-key",
			want: func() string {
				hash := sha256.Sum256([]byte("consistent-key"))
				return hex.EncodeToString(hash[:])
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HashAPIKey(tt.apiKey)
			assert.Equal(t, tt.want, got, "Hash should match expected value")
			assert.Equal(t, 64, len(got), "SHA-256 hash should be 64 characters (32 bytes in hex)")

			// Test consistency - hashing the same key twice should produce the same result
			got2 := HashAPIKey(tt.apiKey)
			assert.Equal(t, got, got2, "Hash should be consistent across multiple calls")
		})
	}
}

func TestHashAPIKey_Uniqueness(t *testing.T) {
	// Test that different keys produce different hashes
	key1 := "api-key-1"
	key2 := "api-key-2"

	hash1 := HashAPIKey(key1)
	hash2 := HashAPIKey(key2)

	assert.NotEqual(t, hash1, hash2, "Different API keys should produce different hashes")
}

func TestValidateAPIKey_ValidKey(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := newTestAPIKeyRepository(mock)
	ctx := context.Background()

	testKey := "test-valid-key-123"
	keyHash := HashAPIKey(testKey)
	testID := uuid.New()
	testName := "Test Key"
	now := time.Now()

	query := `
		SELECT id, key_hash, name, is_active, created_at, updated_at, last_used_at
		FROM api_keys
		WHERE key_hash = \$1 AND is_active = true
	`

	rows := pgxmock.NewRows([]string{"id", "key_hash", "name", "is_active", "created_at", "updated_at", "last_used_at"}).
		AddRow(testID, keyHash, &testName, true, now, now, nil)

	mock.ExpectQuery(query).WithArgs(keyHash).WillReturnRows(rows)

	result, err := repo.ValidateAPIKey(ctx, testKey)

	require.NoError(t, err, "Should not return error for valid API key")
	require.NotNil(t, result, "Should return API key record")
	assert.Equal(t, testID, result.ID, "Should return correct ID")
	assert.Equal(t, keyHash, result.KeyHash, "Should return correct key hash")
	assert.Equal(t, testName, *result.Name, "Should return correct name")
	assert.True(t, result.IsActive, "Should return correct active status")
	assert.NotZero(t, result.CreatedAt, "Should have created_at timestamp")
	assert.NotZero(t, result.UpdatedAt, "Should have updated_at timestamp")
	assert.Nil(t, result.LastUsedAt, "Should have nil last_used_at")

	assert.NoError(t, mock.ExpectationsWereMet(), "All expectations should be met")
}

func TestValidateAPIKey_InvalidKey(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := newTestAPIKeyRepository(mock)
	ctx := context.Background()

	wrongKey := "wrong-api-key"
	keyHash := HashAPIKey(wrongKey)

	query := `
		SELECT id, key_hash, name, is_active, created_at, updated_at, last_used_at
		FROM api_keys
		WHERE key_hash = \$1 AND is_active = true
	`

	mock.ExpectQuery(query).WithArgs(keyHash).WillReturnError(pgx.ErrNoRows)

	result, err := repo.ValidateAPIKey(ctx, wrongKey)

	assert.Error(t, err, "Should return error for invalid API key")
	assert.Nil(t, result, "Should not return API key record")
	assert.Contains(t, err.Error(), "invalid or inactive API key", "Error message should indicate invalid key")

	assert.NoError(t, mock.ExpectationsWereMet(), "All expectations should be met")
}

func TestValidateAPIKey_InactiveKey(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := newTestAPIKeyRepository(mock)
	ctx := context.Background()

	testKey := "test-inactive-key-123"
	keyHash := HashAPIKey(testKey)

	query := `
		SELECT id, key_hash, name, is_active, created_at, updated_at, last_used_at
		FROM api_keys
		WHERE key_hash = \$1 AND is_active = true
	`

	// No rows returned because the key is inactive (filtered by WHERE clause)
	mock.ExpectQuery(query).WithArgs(keyHash).WillReturnError(pgx.ErrNoRows)

	result, err := repo.ValidateAPIKey(ctx, testKey)

	assert.Error(t, err, "Should return error for inactive API key")
	assert.Nil(t, result, "Should not return API key record")
	assert.Contains(t, err.Error(), "invalid or inactive API key", "Error message should indicate invalid key")

	assert.NoError(t, mock.ExpectationsWereMet(), "All expectations should be met")
}

func TestValidateAPIKey_DatabaseError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := newTestAPIKeyRepository(mock)
	ctx := context.Background()

	testKey := "test-key"
	keyHash := HashAPIKey(testKey)

	query := `
		SELECT id, key_hash, name, is_active, created_at, updated_at, last_used_at
		FROM api_keys
		WHERE key_hash = \$1 AND is_active = true
	`

	dbError := errors.New("database connection error")
	mock.ExpectQuery(query).WithArgs(keyHash).WillReturnError(dbError)

	result, err := repo.ValidateAPIKey(ctx, testKey)

	assert.Error(t, err, "Should return error when database fails")
	assert.Nil(t, result, "Should not return API key record")
	assert.Contains(t, err.Error(), "failed to validate API key", "Error should be wrapped")

	assert.NoError(t, mock.ExpectationsWereMet(), "All expectations should be met")
}

func TestValidateAPIKey_NullName(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := newTestAPIKeyRepository(mock)
	ctx := context.Background()

	testKey := "test-null-name-key"
	keyHash := HashAPIKey(testKey)
	testID := uuid.New()
	now := time.Now()

	query := `
		SELECT id, key_hash, name, is_active, created_at, updated_at, last_used_at
		FROM api_keys
		WHERE key_hash = \$1 AND is_active = true
	`

	rows := pgxmock.NewRows([]string{"id", "key_hash", "name", "is_active", "created_at", "updated_at", "last_used_at"}).
		AddRow(testID, keyHash, nil, true, now, now, nil)

	mock.ExpectQuery(query).WithArgs(keyHash).WillReturnRows(rows)

	result, err := repo.ValidateAPIKey(ctx, testKey)

	require.NoError(t, err, "Should not return error for valid API key")
	require.NotNil(t, result, "Should return API key record")
	assert.Equal(t, testID, result.ID, "Should return correct ID")
	assert.Nil(t, result.Name, "Name should be nil")

	assert.NoError(t, mock.ExpectationsWereMet(), "All expectations should be met")
}

func TestValidateAPIKey_WithLastUsedAt(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := newTestAPIKeyRepository(mock)
	ctx := context.Background()

	testKey := "test-key-with-last-used"
	keyHash := HashAPIKey(testKey)
	testID := uuid.New()
	testName := "Test Key"
	now := time.Now()
	lastUsed := now.Add(-1 * time.Hour)

	query := `
		SELECT id, key_hash, name, is_active, created_at, updated_at, last_used_at
		FROM api_keys
		WHERE key_hash = \$1 AND is_active = true
	`

	rows := pgxmock.NewRows([]string{"id", "key_hash", "name", "is_active", "created_at", "updated_at", "last_used_at"}).
		AddRow(testID, keyHash, &testName, true, now, now, &lastUsed)

	mock.ExpectQuery(query).WithArgs(keyHash).WillReturnRows(rows)

	result, err := repo.ValidateAPIKey(ctx, testKey)

	require.NoError(t, err, "Should not return error for valid API key")
	require.NotNil(t, result, "Should return API key record")
	assert.NotNil(t, result.LastUsedAt, "Should have last_used_at timestamp")
	assert.Equal(t, lastUsed, *result.LastUsedAt, "Should return correct last_used_at")

	assert.NoError(t, mock.ExpectationsWereMet(), "All expectations should be met")
}

func TestUpdateLastUsedAt_Success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := newTestAPIKeyRepository(mock)
	ctx := context.Background()

	keyHash := "test-hash"

	query := `
		UPDATE api_keys
		SET last_used_at = \$1, updated_at = \$1
		WHERE key_hash = \$2
	`

	mock.ExpectExec(query).WithArgs(pgxmock.AnyArg(), keyHash).WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	err = repo.UpdateLastUsedAt(ctx, keyHash)

	require.NoError(t, err, "Should not return error when updating")
	assert.NoError(t, mock.ExpectationsWereMet(), "All expectations should be met")
}

func TestUpdateLastUsedAt_DatabaseError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := newTestAPIKeyRepository(mock)
	ctx := context.Background()

	keyHash := "test-hash"

	query := `
		UPDATE api_keys
		SET last_used_at = \$1, updated_at = \$1
		WHERE key_hash = \$2
	`

	dbError := errors.New("database connection error")
	mock.ExpectExec(query).WithArgs(pgxmock.AnyArg(), keyHash).WillReturnError(dbError)

	err = repo.UpdateLastUsedAt(ctx, keyHash)

	assert.Error(t, err, "Should return error when database fails")
	assert.Contains(t, err.Error(), "failed to update last used timestamp", "Error should be wrapped")

	assert.NoError(t, mock.ExpectationsWereMet(), "All expectations should be met")
}

func TestUpdateLastUsedAt_NonExistentKey(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := newTestAPIKeyRepository(mock)
	ctx := context.Background()

	keyHash := "non-existent-hash"

	query := `
		UPDATE api_keys
		SET last_used_at = \$1, updated_at = \$1
		WHERE key_hash = \$2
	`

	// pgx doesn't return an error for UPDATE with no matching rows
	mock.ExpectExec(query).WithArgs(pgxmock.AnyArg(), keyHash).WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	err = repo.UpdateLastUsedAt(ctx, keyHash)

	assert.NoError(t, err, "Should not return error for non-existent key")
	assert.NoError(t, mock.ExpectationsWereMet(), "All expectations should be met")
}

func TestValidateAPIKey_Integration(t *testing.T) {
	// Integration test simulating the full workflow
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := newTestAPIKeyRepository(mock)
	ctx := context.Background()

	testKey := "integration-test-key"
	keyHash := HashAPIKey(testKey)
	testID := uuid.New()
	testName := "Integration Test"
	now := time.Now()

	// Step 1: First validation (no last_used_at)
	query := `
		SELECT id, key_hash, name, is_active, created_at, updated_at, last_used_at
		FROM api_keys
		WHERE key_hash = \$1 AND is_active = true
	`

	rows1 := pgxmock.NewRows([]string{"id", "key_hash", "name", "is_active", "created_at", "updated_at", "last_used_at"}).
		AddRow(testID, keyHash, &testName, true, now, now, nil)

	mock.ExpectQuery(query).WithArgs(keyHash).WillReturnRows(rows1)

	apiKey, err := repo.ValidateAPIKey(ctx, testKey)
	require.NoError(t, err, "First validation should succeed")
	require.NotNil(t, apiKey, "Should return API key")
	assert.Nil(t, apiKey.LastUsedAt, "Initially last_used_at should be nil")

	// Step 2: Update last used timestamp
	updateQuery := `
		UPDATE api_keys
		SET last_used_at = \$1, updated_at = \$1
		WHERE key_hash = \$2
	`

	mock.ExpectExec(updateQuery).WithArgs(pgxmock.AnyArg(), keyHash).WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	err = repo.UpdateLastUsedAt(ctx, keyHash)
	require.NoError(t, err, "Update should succeed")

	// Step 3: Second validation (with last_used_at)
	lastUsed := now.Add(1 * time.Minute)
	rows2 := pgxmock.NewRows([]string{"id", "key_hash", "name", "is_active", "created_at", "updated_at", "last_used_at"}).
		AddRow(testID, keyHash, &testName, true, now, now, &lastUsed)

	mock.ExpectQuery(query).WithArgs(keyHash).WillReturnRows(rows2)

	apiKey, err = repo.ValidateAPIKey(ctx, testKey)
	require.NoError(t, err, "Second validation should succeed")
	require.NotNil(t, apiKey, "Should return API key")
	assert.NotNil(t, apiKey.LastUsedAt, "last_used_at should now be set")

	assert.NoError(t, mock.ExpectationsWereMet(), "All expectations should be met")
}

func TestValidateAPIKey_ConcurrentCalls(t *testing.T) {
	// Test that the repository can handle concurrent validation calls
	// Note: This doesn't test actual database concurrency, but ensures the repository
	// doesn't have any concurrency issues in its own code
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := newTestAPIKeyRepository(mock)
	ctx := context.Background()

	testKey := "concurrent-test-key"
	keyHash := HashAPIKey(testKey)
	testID := uuid.New()
	testName := "Concurrent Test"
	now := time.Now()

	query := `
		SELECT id, key_hash, name, is_active, created_at, updated_at, last_used_at
		FROM api_keys
		WHERE key_hash = \$1 AND is_active = true
	`

	// Expect 5 calls
	for i := 0; i < 5; i++ {
		rows := pgxmock.NewRows([]string{"id", "key_hash", "name", "is_active", "created_at", "updated_at", "last_used_at"}).
			AddRow(testID, keyHash, &testName, true, now, now, nil)
		mock.ExpectQuery(query).WithArgs(keyHash).WillReturnRows(rows)
	}

	done := make(chan bool, 5)
	errChan := make(chan error, 5)

	// Run 5 concurrent validations
	for i := 0; i < 5; i++ {
		go func() {
			_, err := repo.ValidateAPIKey(ctx, testKey)
			if err != nil {
				errChan <- err
			}
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 5; i++ {
		<-done
	}
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("Concurrent access error: %v", err)
	}

	assert.NoError(t, mock.ExpectationsWereMet(), "All expectations should be met")
}
