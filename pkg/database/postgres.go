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

// WithMaxConns sets the maximum number of connections in the pool.
func WithMaxConns(n int) PoolOption {
	return func(c *pgxpool.Config) {
		//nolint:gosec // G115: pgxpool requires int32; config validates n is in reasonable range
		c.MaxConns = int32(n)
	}
}

// WithMinConns sets the minimum number of connections to keep in the pool.
func WithMinConns(n int) PoolOption {
	return func(c *pgxpool.Config) {
		//nolint:gosec // G115: pgxpool requires int32; config validates n is in reasonable range
		c.MinConns = int32(n)
	}
}

// WithMaxConnLifetime sets the maximum lifetime of a connection before it is closed.
func WithMaxConnLifetime(d time.Duration) PoolOption {
	return func(c *pgxpool.Config) {
		c.MaxConnLifetime = d
	}
}

// WithMaxConnIdleTime sets the duration after which an idle connection is closed by the health check.
// Use ~30 minutes in production to release idle connections when traffic drops.
func WithMaxConnIdleTime(d time.Duration) PoolOption {
	return func(c *pgxpool.Config) {
		c.MaxConnIdleTime = d
	}
}

// WithHealthCheckPeriod sets the duration between health checks of idle connections.
// Use ~1 minute in production to detect dead connections (e.g. after DB restart or load balancer timeout).
func WithHealthCheckPeriod(d time.Duration) PoolOption {
	return func(c *pgxpool.Config) {
		c.HealthCheckPeriod = d
	}
}

// WithConnectTimeout sets the maximum time to wait when establishing a new connection.
// Prevents indefinite hangs when the database is unreachable.
func WithConnectTimeout(d time.Duration) PoolOption {
	return func(c *pgxpool.Config) {
		if c.ConnConfig != nil {
			c.ConnConfig.ConnectTimeout = d
		}
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
