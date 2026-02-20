// Package database provides database connection utilities.
package database

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolConfig holds optional connection pool settings. Nil means use pgx defaults.
type PoolConfig struct {
	MaxConns        int           // Max connections in the pool (default from pgx if 0 when applied)
	MinConns        int           // Min connections to keep open (0 = no minimum)
	MaxConnLifetime time.Duration // Max time a connection may be reused (0 = no limit)
}

// NewPostgresPool creates a new PostgreSQL connection pool.
// If opts is non-nil, MaxConns, MinConns, and MaxConnLifetime are applied; otherwise pgx defaults are used.
func NewPostgresPool(ctx context.Context, databaseURL string, opts *PoolConfig) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	if opts != nil {
		if opts.MaxConns > 0 {
			maxConns := min(opts.MaxConns, math.MaxInt32)

			config.MaxConns = int32(maxConns) //nolint:gosec // G115: maxConns clamped to math.MaxInt32 above
		}

		if opts.MinConns > 0 {
			minConns := min(opts.MinConns, math.MaxInt32)

			config.MinConns = int32(minConns) //nolint:gosec // G115: minConns clamped to math.MaxInt32 above
		}

		if opts.MaxConnLifetime > 0 {
			config.MaxConnLifetime = opts.MaxConnLifetime
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	slog.Info("Successfully connected to PostgreSQL")

	return pool, nil
}
