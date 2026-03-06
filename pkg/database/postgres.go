// Package database provides database connection utilities.
package database

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolConfig holds connection pool settings (from env/config).
type PoolConfig struct {
	MaxConns          int
	MinConns          int
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
	ConnectTimeout    time.Duration
}

// PoolOption configures the connection pool.
type PoolOption func(*pgxpool.Config)

// clampInt32 returns v capped to int32 range to avoid overflow in pgxpool.
func clampInt32(v int) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	// Safe: v <= math.MaxInt32 at this point
	return int32(v) // #nosec G115 -- v is bounded above
}

// WithPoolConfig applies pool settings from config.
func WithPoolConfig(cfg PoolConfig) PoolOption {
	return func(poolCfg *pgxpool.Config) {
		if cfg.MaxConns > 0 {
			poolCfg.MaxConns = clampInt32(cfg.MaxConns)
		}

		if cfg.MinConns >= 0 {
			poolCfg.MinConns = clampInt32(cfg.MinConns)
		}

		if cfg.MaxConnLifetime > 0 {
			poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
		}

		if cfg.MaxConnIdleTime > 0 {
			poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
		}

		if cfg.HealthCheckPeriod > 0 {
			poolCfg.HealthCheckPeriod = cfg.HealthCheckPeriod
		}

		if cfg.ConnectTimeout > 0 && poolCfg.ConnConfig != nil {
			poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
		}
	}
}

// WithAfterConnect sets a callback run on each new connection (e.g. for type registration).
func WithAfterConnect(fn func(context.Context, *pgx.Conn) error) PoolOption {
	return func(c *pgxpool.Config) {
		c.AfterConnect = fn
	}
}

// NewPostgresPool creates a new PostgreSQL connection pool.
func NewPostgresPool(ctx context.Context, databaseURL string, opts ...PoolOption) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	for _, opt := range opts {
		opt(config)
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
