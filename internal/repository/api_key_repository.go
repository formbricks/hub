package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/formbricks/hub/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBPool is an interface for database operations used by the repository
type DBPool interface {
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
	Exec(ctx context.Context, sql string, arguments ...interface{}) (pgconn.CommandTag, error)
}

// APIKeyRepository handles data access for API keys
type APIKeyRepository struct {
	db DBPool
}

// NewAPIKeyRepository creates a new API key repository
func NewAPIKeyRepository(db *pgxpool.Pool) *APIKeyRepository {
	return &APIKeyRepository{db: db}
}

// HashAPIKey creates a SHA-256 hash of the API key
func HashAPIKey(apiKey string) string {
	hash := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(hash[:])
}

// ValidateAPIKey checks if an API key exists and is active
// Returns the API key record if valid, error otherwise
func (r *APIKeyRepository) ValidateAPIKey(ctx context.Context, apiKey string) (*models.APIKey, error) {
	keyHash := HashAPIKey(apiKey)

	query := `
		SELECT id, key_hash, name, is_active, created_at, updated_at, last_used_at
		FROM api_keys
		WHERE key_hash = $1 AND is_active = true
	`

	var key models.APIKey
	err := r.db.QueryRow(ctx, query, keyHash).Scan(
		&key.ID, &key.KeyHash, &key.Name, &key.IsActive,
		&key.CreatedAt, &key.UpdatedAt, &key.LastUsedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("invalid or inactive API key")
		}
		return nil, fmt.Errorf("failed to validate API key: %w", err)
	}

	return &key, nil
}

// UpdateLastUsedAt updates the last_used_at timestamp for an API key
func (r *APIKeyRepository) UpdateLastUsedAt(ctx context.Context, keyHash string) error {
	query := `
		UPDATE api_keys
		SET last_used_at = $1, updated_at = $1
		WHERE key_hash = $2
	`

	_, err := r.db.Exec(ctx, query, time.Now(), keyHash)
	if err != nil {
		return fmt.Errorf("failed to update last used timestamp: %w", err)
	}

	return nil
}
