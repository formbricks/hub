package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xernobyl/formbricks_worktrial/internal/config"
	"github.com/xernobyl/formbricks_worktrial/pkg/database"
)

func main() {
	ctx := context.Background()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Get Postgres pool
	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Run migrations
	if err := runMigrations(ctx, db); err != nil {
		slog.Error("Migration failed", "error", err)
		os.Exit(1)
	}

	slog.Info("All migrations completed successfully")
}

// runMigrations runs the migrations on the given pool
// Gets all .sql files on the migration folder and runs them
func runMigrations(ctx context.Context, db *pgxpool.Pool) error {
	// TODO Add DB versioning somewhere

	migrationsDir := "migrations"

	// Get all files on the migrations folder
	files, err := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	// Sort migrations alphanumerically
	sort.Strings(files)

	for _, file := range files {
		slog.Info("Running migration", "file", filepath.Base(file))

		// Get file content
		content, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", file, err)
		}

		// Run SQL
		if _, err := db.Exec(ctx, string(content)); err != nil {
			return fmt.Errorf("failed to execute migration %s: %w", file, err)
		}

		slog.Info("Completed migration", "file", filepath.Base(file))
	}

	return nil
}
