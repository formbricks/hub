package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"strconv"

	_ "github.com/lib/pq"

	"github.com/formbricks/hub/internal/api"
	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/ent"
	"github.com/formbricks/hub/internal/queue"
	"github.com/formbricks/hub/internal/webhook"
)

func main() {
	yamlFormat := flag.Bool("yaml", false, "Output in YAML format instead of JSON")
	flag.Parse()

	// Load configuration from environment with SERVICE_ prefix
	cfg := &config.Config{
		DatabaseURL:            getEnv("SERVICE_DATABASE_URL", os.Getenv("DATABASE_URL")),
		Host:                   getEnv("SERVICE_HOST", "0.0.0.0"),
		Port:                   getEnvInt("SERVICE_PORT", 8080),
		WebhookUrls:            getEnv("SERVICE_WEBHOOK_URLS", ""),
		Environment:            getEnv("SERVICE_ENVIRONMENT", "development"),
		APIKey:                 getEnv("SERVICE_API_KEY", ""),
		OpenAIKey:              getEnv("SERVICE_OPEN_AI_KEY", ""),
		OpenAIEnrichmentModel:  getEnv("SERVICE_OPENAI_ENRICHMENT_MODEL", "gpt-4o-mini"),
		OpenAIEmbeddingModel:   getEnv("SERVICE_OPENAI_EMBEDDING_MODEL", "text-embedding-3-small"),
		EnrichmentTimeout:      getEnvInt("SERVICE_ENRICHMENT_TIMEOUT", 10),
		EnrichmentWorkers:      getEnvInt("SERVICE_ENRICHMENT_WORKERS", 3),
		EnrichmentPollInterval: getEnvInt("SERVICE_ENRICHMENT_POLL_INTERVAL", 1),
		LogLevel:               getEnv("SERVICE_LOG_LEVEL", "info"),
		RateLimitPerIP:         getEnvInt("SERVICE_RATE_LIMIT_PER_IP", 100),
		RateLimitBurst:         getEnvInt("SERVICE_RATE_LIMIT_BURST", 200),
		RateLimitGlobal:        getEnvInt("SERVICE_RATE_LIMIT_GLOBAL", 1000),
		RateLimitGlobalBurst:   getEnvInt("SERVICE_RATE_LIMIT_GLOBAL_BURST", 2000),
	}

	// Setup logger (write to stderr so stdout is reserved for JSON spec)
	logLevel := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	}))

	// Validate database URL
	if cfg.DatabaseURL == "" {
		logger.Error("DATABASE_URL or SERVICE_DATABASE_URL environment variable not set")
		os.Exit(1)
	}

	// Connect to database
	client, err := ent.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := client.Close(); err != nil {
			logger.Error("failed to close database connection", "error", err)
		}
	}()

	// Run migrations
	if err := client.Schema.Create(context.Background()); err != nil {
		logger.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	logger.Info("database connected")

	// Create webhook dispatcher
	webhookURLs := cfg.GetWebhookURLs()
	dispatcher := webhook.NewDispatcher(webhookURLs, logger)

	// Create a queue instance for spec generation (routes need it even though spec generation doesn't use it)
	enrichmentQueue := queue.NewPostgresQueue(client)

	// Generate and export the OpenAPI spec
	logger.Info("generating OpenAPI specification...")
	var errExport error
	if *yamlFormat {
		errExport = api.ExportOpenAPISpecYAML(cfg, client, dispatcher, enrichmentQueue, logger, os.Stdout)
	} else {
		errExport = api.ExportOpenAPISpecJSON(cfg, client, dispatcher, enrichmentQueue, logger, os.Stdout)
	}

	if errExport != nil {
		logger.Error("failed to generate OpenAPI spec", "error", errExport)
		os.Exit(1)
	}

	logger.Info("OpenAPI specification generated successfully")
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if value, ok := os.LookupEnv(key); ok {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return fallback
}
