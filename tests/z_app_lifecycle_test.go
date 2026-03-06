package tests

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/joho/godotenv"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/app"
	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/pkg/database"
)

// TestApp_Lifecycle runs last (file prefix z_) so it doesn't block other tests.
// Starting the full app modifies global state (slog, otel) and River; tests running after
// it can hang waiting for connections or cleanup. Running last avoids that.
func TestApp_Lifecycle(t *testing.T) {
	ctx := context.Background()

	_ = godotenv.Load()
	if os.Getenv("DATABASE_URL") == "" {
		_ = godotenv.Load("../.env")
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultTestDatabaseURL
	}

	// Minimal config: no OTLP, no embeddings, random port.
	// Use 2 workers to avoid connection pool exhaustion (default 100 would spike DB connections).
	t.Setenv("API_KEY", testAPIKey)
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("PORT", "0")
	t.Setenv("WEBHOOK_DELIVERY_MAX_CONCURRENT", "2")
	t.Setenv("OTEL_METRICS_EXPORTER", "")
	t.Setenv("OTEL_TRACES_EXPORTER", "")
	t.Setenv("EMBEDDING_PROVIDER", "")
	t.Setenv("EMBEDDING_MODEL", "")

	cfg, err := config.Load()
	require.NoError(t, err)

	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL,
		database.WithAfterConnect(pgxvec.RegisterTypes),
		database.WithMaxConns(cfg.DatabaseMaxConns),
		database.WithMinConns(cfg.DatabaseMinConns),
		database.WithMaxConnLifetime(cfg.DatabaseMaxConnLifetime),
		database.WithMaxConnIdleTime(cfg.DatabaseMaxConnIdleTime),
		database.WithHealthCheckPeriod(cfg.DatabaseHealthCheckPeriod),
		database.WithConnectTimeout(cfg.DatabaseConnectTimeout),
	)
	require.NoError(t, err)

	defer db.Close()

	application, err := app.NewApp(cfg, db)
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(ctx)
	runDone := make(chan error, 1)

	go func() {
		runDone <- application.Run(runCtx)
	}()

	// Ensure app doesn't exit immediately before cancellation.
	select {
	case err = <-runDone:
		require.NoError(t, err, "application exited before cancellation")
	case <-time.After(500 * time.Millisecond):
	}

	cancel()

	// Bound wait to avoid hanging the suite on shutdown regressions.
	select {
	case err = <-runDone:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("application.Run did not return after cancellation")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	err = application.Shutdown(shutdownCtx)
	require.NoError(t, err)
}
