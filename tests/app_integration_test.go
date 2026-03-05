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

	// Minimal config: no OTLP, no embeddings, random port
	t.Setenv("API_KEY", testAPIKey)
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("PORT", "0")
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

	// Allow server and River to start
	time.Sleep(100 * time.Millisecond)

	cancel()

	err = <-runDone
	require.NoError(t, err)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	err = application.Shutdown(shutdownCtx)
	require.NoError(t, err)
}
