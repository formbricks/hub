// Package config provides application configuration loaded from environment variables.
package config

import (
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Sentinel errors for configuration validation (err113).
var (
	ErrAPIKeyRequired                  = errors.New("API_KEY environment variable is required but not set")
	ErrWebhookDeliveryMaxConcurrent    = errors.New("WEBHOOK_DELIVERY_MAX_CONCURRENT must be a positive integer")
	ErrWebhookDeliveryMaxAttempts      = errors.New("WEBHOOK_DELIVERY_MAX_ATTEMPTS must be a positive integer")
	ErrWebhookMaxFanOutPerEvent        = errors.New("WEBHOOK_MAX_FAN_OUT_PER_EVENT must be a positive integer")
	ErrMessagePublisherQueueMaxSize    = errors.New("MESSAGE_PUBLISHER_QUEUE_MAX_SIZE must be a positive integer")
	ErrMessagePublisherPerEventTimeout = errors.New("MESSAGE_PUBLISHER_PER_EVENT_TIMEOUT_SECONDS must be a positive integer")
	ErrShutdownTimeoutSeconds          = errors.New("SHUTDOWN_TIMEOUT_SECONDS must be a positive integer")
	ErrWebhookMaxCount                 = errors.New("WEBHOOK_MAX_COUNT must be a positive integer")
	ErrDatabaseMinConnsExceedsMax      = errors.New("DATABASE_MIN_CONNS must not exceed DATABASE_MAX_CONNS")
)

// Config holds all application configuration.
type Config struct {
	DatabaseURL string
	Port        string
	APIKey      string
	LogLevel    string

	// Webhook delivery concurrency cap (max concurrent outbound HTTP calls)
	WebhookDeliveryMaxConcurrent int

	// Webhook delivery max attempts per job (River retries); default 3
	WebhookDeliveryMaxAttempts int

	// Webhook max fan-out per event (max jobs enqueued per event); default 500
	WebhookMaxFanOutPerEvent int

	// Message publisher: event channel buffer size; default 1024
	MessagePublisherBufferSize int

	// Message publisher: per-event timeout (max time to process one event across all providers); default 10s
	MessagePublisherPerEventTimeout time.Duration

	// Graceful shutdown timeout for HTTP server and River; default 30s
	ShutdownTimeout time.Duration

	// Max total webhooks allowed (creation rejected when count >= this); default 500
	WebhookMaxCount int

	// Webhook delivery: HTTP client timeout for each POST; default 15s. Job timeout = this + 5s.
	WebhookHTTPTimeout time.Duration

	// Webhook enqueue: retries when InsertMany fails (transient River/DB errors). Defaults: 3, 100ms, 2s.
	WebhookEnqueueMaxRetries     int           // Number of retries after first attempt
	WebhookEnqueueInitialBackoff time.Duration // First backoff
	WebhookEnqueueMaxBackoff     time.Duration // Max backoff cap

	// Database connection pool: max connections; default 25
	DatabaseMaxConns int
	// Database connection pool: min connections to keep open; default 0
	DatabaseMinConns int
	// Database connection pool: max lifetime of a connection before it is closed; default 1h
	DatabaseMaxConnLifetime time.Duration
	// Database connection pool: max idle time before closing a connection; default 30m
	DatabaseMaxConnIdleTime time.Duration
	// Database connection pool: interval between health checks of idle connections; default 1m
	DatabaseHealthCheckPeriod time.Duration
	// Database connection: timeout when establishing a new connection; default 10s
	DatabaseConnectTimeout time.Duration

	// WebhookURLBlacklist: hosts (and IPs) that cannot be used as webhook endpoints (SSRF mitigation).
	// Loaded from WEBHOOK_BLACKLIST (comma-separated). Defaults: localhost, 127.0.0.1, ::1, 169.254.169.254.
	WebhookURLBlacklist map[string]struct{}

	// Embeddings: optional. Enabled only when both EMBEDDING_PROVIDER and EMBEDDING_MODEL are set and provider is supported.
	EmbeddingProviderAPIKey string
	// Embeddings: provider name (e.g. openai, google); env EMBEDDING_PROVIDER. Required (with EmbeddingModel) to enable embeddings.
	EmbeddingProvider string
	// Embeddings: model name; env EMBEDDING_MODEL. Required (with EmbeddingProvider) to enable embeddings; no default.
	EmbeddingModel string
	// Embeddings: max concurrent workers for the embeddings River queue; default 5
	EmbeddingMaxConcurrent int
	// Embeddings: max attempts per embedding job (River retries); default 3
	EmbeddingMaxAttempts int
	// Embeddings: if true, L2-normalize vectors before storing or caching (env EMBEDDING_NORMALIZE); default false
	EmbeddingNormalize bool

	// Embeddings: GCP project ID for Vertex AI (google-vertex provider). Env: EMBEDDING_GOOGLE_CLOUD_PROJECT or GOOGLE_CLOUD_PROJECT
	EmbeddingGoogleCloudProject string
	// Embeddings: GCP region for Vertex AI (e.g. europe-west3). Env: EMBEDDING_GOOGLE_CLOUD_LOCATION or GOOGLE_CLOUD_LOCATION
	EmbeddingGoogleCloudLocation string

	// OpenTelemetry: set to "otlp" to enable metrics (OTLP push); empty = metrics disabled
	OtelMetricsExporter string
	// OpenTelemetry: traces exporter (e.g. "otlp", "stdout"); empty = tracing disabled.
	// OTLP endpoint from OTEL_EXPORTER_OTLP_ENDPOINT (SDK reads env).
	OtelTracesExporter string
}

// getEnv retrieves an environment variable or returns a default value.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return defaultValue
}

// getEnvAsInt retrieves an environment variable as an integer or returns a default value.
func getEnvAsInt(key string, defaultValue int) int {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return defaultValue
	}

	return value
}

// GetEnvAsBool retrieves an environment variable as a boolean. "true", "1", "yes" (case-insensitive) => true; else false.
// Exported so other cmd packages (e.g. backfill-embeddings) can reuse it without duplicating logic.
func GetEnvAsBool(key string, defaultValue bool) bool {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	switch strings.ToLower(strings.TrimSpace(valueStr)) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return defaultValue
	}
}

// Load reads configuration from environment variables and returns a Config struct.
// It automatically loads .env file if it exists.
// Returns default values for any missing environment variables.
// API_KEY is required and the function will return an error if it's not set.
func Load() (*Config, error) {
	// Load .env file if it exists. Skip logging when absent (e.g. env from secrets/parameter store).
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("Failed to load .env file", "error", err)
	}

	webhookBlacklist := parseWebhookURLBlacklist(getEnv("WEBHOOK_BLACKLIST", "localhost,127.0.0.1,::1,169.254.169.254"))

	const (
		defaultWebhookDeliveryMaxConcurrent     = 100
		defaultWebhookDeliveryMaxAttempts       = 3
		defaultWebhookMaxFanOutPerEvent         = 500
		defaultMessagePublisherQueueMaxSize     = 16384
		defaultMessagePublisherPerEventTimeout  = 10
		defaultShutdownTimeoutSeconds           = 30
		defaultWebhookMaxCount                  = 500
		defaultWebhookHTTPTimeoutSeconds        = 15
		defaultWebhookEnqueueMaxRetries         = 3
		defaultWebhookEnqueueInitialBackoffMs   = 100
		defaultWebhookEnqueueMaxBackoffMs       = 2000
		defaultEmbeddingMaxConcurrent           = 5
		defaultEmbeddingMaxAttempts             = 3
		defaultDatabaseMaxConns                 = 25
		defaultDatabaseMinConns                 = 0
		defaultDatabaseMaxConnLifetimeSeconds   = 3600 // 1 hour
		defaultDatabaseMaxConnIdleTimeSeconds   = 1800 // 30 minutes
		defaultDatabaseHealthCheckPeriodSeconds = 60   // 1 minute
		defaultDatabaseConnectTimeoutSeconds    = 10
	)

	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		return nil, ErrAPIKeyRequired
	}

	webhookDeliveryMaxConcurrent := getEnvAsInt("WEBHOOK_DELIVERY_MAX_CONCURRENT", defaultWebhookDeliveryMaxConcurrent)
	if webhookDeliveryMaxConcurrent <= 0 {
		return nil, ErrWebhookDeliveryMaxConcurrent
	}

	webhookDeliveryMaxAttempts := getEnvAsInt("WEBHOOK_DELIVERY_MAX_ATTEMPTS", defaultWebhookDeliveryMaxAttempts)
	if webhookDeliveryMaxAttempts <= 0 {
		return nil, ErrWebhookDeliveryMaxAttempts
	}

	webhookMaxFanOutPerEvent := getEnvAsInt("WEBHOOK_MAX_FAN_OUT_PER_EVENT", defaultWebhookMaxFanOutPerEvent)
	if webhookMaxFanOutPerEvent <= 0 {
		return nil, ErrWebhookMaxFanOutPerEvent
	}

	messagePublisherBufferSize := getEnvAsInt("MESSAGE_PUBLISHER_QUEUE_MAX_SIZE", defaultMessagePublisherQueueMaxSize)
	if messagePublisherBufferSize <= 0 {
		return nil, ErrMessagePublisherQueueMaxSize
	}

	perEventTimeoutSecs := getEnvAsInt("MESSAGE_PUBLISHER_PER_EVENT_TIMEOUT_SECONDS", defaultMessagePublisherPerEventTimeout)
	if perEventTimeoutSecs <= 0 {
		return nil, ErrMessagePublisherPerEventTimeout
	}

	shutdownTimeoutSecs := getEnvAsInt("SHUTDOWN_TIMEOUT_SECONDS", defaultShutdownTimeoutSeconds)
	if shutdownTimeoutSecs <= 0 {
		return nil, ErrShutdownTimeoutSeconds
	}

	webhookMaxCount := getEnvAsInt("WEBHOOK_MAX_COUNT", defaultWebhookMaxCount)
	if webhookMaxCount <= 0 {
		return nil, ErrWebhookMaxCount
	}

	webhookHTTPTimeoutSecs := getEnvAsInt("WEBHOOK_HTTP_TIMEOUT_SECONDS", defaultWebhookHTTPTimeoutSeconds)
	if webhookHTTPTimeoutSecs <= 0 {
		webhookHTTPTimeoutSecs = defaultWebhookHTTPTimeoutSeconds
	}

	webhookHTTPTimeout := time.Duration(webhookHTTPTimeoutSecs) * time.Second

	webhookEnqueueMaxRetries := getEnvAsInt("WEBHOOK_ENQUEUE_MAX_RETRIES", defaultWebhookEnqueueMaxRetries)
	if webhookEnqueueMaxRetries < 0 {
		webhookEnqueueMaxRetries = defaultWebhookEnqueueMaxRetries
	}

	webhookEnqueueInitialBackoff := time.Duration(
		getEnvAsInt("WEBHOOK_ENQUEUE_INITIAL_BACKOFF_MS", defaultWebhookEnqueueInitialBackoffMs),
	) * time.Millisecond
	if webhookEnqueueInitialBackoff <= 0 {
		webhookEnqueueInitialBackoff = defaultWebhookEnqueueInitialBackoffMs * time.Millisecond
	}

	webhookEnqueueMaxBackoffMs := getEnvAsInt("WEBHOOK_ENQUEUE_MAX_BACKOFF_MS", defaultWebhookEnqueueMaxBackoffMs)
	if webhookEnqueueMaxBackoffMs <= 0 {
		webhookEnqueueMaxBackoffMs = defaultWebhookEnqueueMaxBackoffMs
	}

	webhookEnqueueMaxBackoff := max(time.Duration(webhookEnqueueMaxBackoffMs)*time.Millisecond, webhookEnqueueInitialBackoff)

	databaseMaxConns := getEnvAsInt("DATABASE_MAX_CONNS", defaultDatabaseMaxConns)
	if databaseMaxConns <= 0 {
		databaseMaxConns = defaultDatabaseMaxConns
	}

	databaseMinConns := getEnvAsInt("DATABASE_MIN_CONNS", defaultDatabaseMinConns)
	if databaseMinConns < 0 {
		databaseMinConns = defaultDatabaseMinConns
	}

	if databaseMinConns > databaseMaxConns {
		return nil, ErrDatabaseMinConnsExceedsMax
	}

	databaseMaxConnLifetimeSecs := getEnvAsInt("DATABASE_MAX_CONN_LIFETIME_SECONDS", defaultDatabaseMaxConnLifetimeSeconds)
	if databaseMaxConnLifetimeSecs <= 0 {
		databaseMaxConnLifetimeSecs = defaultDatabaseMaxConnLifetimeSeconds
	}

	databaseMaxConnLifetime := time.Duration(databaseMaxConnLifetimeSecs) * time.Second

	databaseMaxConnIdleTimeSecs := getEnvAsInt("DATABASE_MAX_CONN_IDLE_TIME_SECONDS", defaultDatabaseMaxConnIdleTimeSeconds)
	if databaseMaxConnIdleTimeSecs <= 0 {
		databaseMaxConnIdleTimeSecs = defaultDatabaseMaxConnIdleTimeSeconds
	}

	databaseMaxConnIdleTime := time.Duration(databaseMaxConnIdleTimeSecs) * time.Second

	databaseHealthCheckPeriodSecs := getEnvAsInt("DATABASE_HEALTH_CHECK_PERIOD_SECONDS", defaultDatabaseHealthCheckPeriodSeconds)
	if databaseHealthCheckPeriodSecs <= 0 {
		databaseHealthCheckPeriodSecs = defaultDatabaseHealthCheckPeriodSeconds
	}

	databaseHealthCheckPeriod := time.Duration(databaseHealthCheckPeriodSecs) * time.Second

	databaseConnectTimeoutSecs := getEnvAsInt("DATABASE_CONNECT_TIMEOUT_SECONDS", defaultDatabaseConnectTimeoutSeconds)
	if databaseConnectTimeoutSecs <= 0 {
		databaseConnectTimeoutSecs = defaultDatabaseConnectTimeoutSeconds
	}

	databaseConnectTimeout := time.Duration(databaseConnectTimeoutSecs) * time.Second

	embeddingMaxConcurrent := getEnvAsInt("EMBEDDING_MAX_CONCURRENT", defaultEmbeddingMaxConcurrent)
	if embeddingMaxConcurrent <= 0 {
		embeddingMaxConcurrent = defaultEmbeddingMaxConcurrent
	}

	embeddingMaxAttempts := getEnvAsInt("EMBEDDING_MAX_ATTEMPTS", defaultEmbeddingMaxAttempts)
	if embeddingMaxAttempts <= 0 {
		embeddingMaxAttempts = defaultEmbeddingMaxAttempts
	}

	cfg := &Config{
		DatabaseURL: getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/test_db?sslmode=disable"),
		Port:        getEnv("PORT", "8080"),
		APIKey:      apiKey,
		LogLevel:    getEnv("LOG_LEVEL", "info"),

		WebhookDeliveryMaxConcurrent:    webhookDeliveryMaxConcurrent,
		WebhookDeliveryMaxAttempts:      webhookDeliveryMaxAttempts,
		WebhookMaxFanOutPerEvent:        webhookMaxFanOutPerEvent,
		MessagePublisherBufferSize:      messagePublisherBufferSize,
		MessagePublisherPerEventTimeout: time.Duration(perEventTimeoutSecs) * time.Second,
		ShutdownTimeout:                 time.Duration(shutdownTimeoutSecs) * time.Second,
		WebhookMaxCount:                 webhookMaxCount,
		WebhookHTTPTimeout:              webhookHTTPTimeout,

		WebhookEnqueueMaxRetries:     webhookEnqueueMaxRetries,
		WebhookEnqueueInitialBackoff: webhookEnqueueInitialBackoff,
		WebhookEnqueueMaxBackoff:     webhookEnqueueMaxBackoff,

		DatabaseMaxConns:          databaseMaxConns,
		DatabaseMinConns:          databaseMinConns,
		DatabaseMaxConnLifetime:   databaseMaxConnLifetime,
		DatabaseMaxConnIdleTime:   databaseMaxConnIdleTime,
		DatabaseHealthCheckPeriod: databaseHealthCheckPeriod,
		DatabaseConnectTimeout:    databaseConnectTimeout,

		WebhookURLBlacklist: webhookBlacklist,

		EmbeddingProviderAPIKey:      getEnv("EMBEDDING_PROVIDER_API_KEY", ""),
		EmbeddingProvider:            getEnv("EMBEDDING_PROVIDER", ""),
		EmbeddingModel:               getEnv("EMBEDDING_MODEL", ""),
		EmbeddingMaxConcurrent:       embeddingMaxConcurrent,
		EmbeddingMaxAttempts:         embeddingMaxAttempts,
		EmbeddingNormalize:           GetEnvAsBool("EMBEDDING_NORMALIZE", false),
		EmbeddingGoogleCloudProject:  getEnv("EMBEDDING_GOOGLE_CLOUD_PROJECT", getEnv("GOOGLE_CLOUD_PROJECT", "")),
		EmbeddingGoogleCloudLocation: getEnv("EMBEDDING_GOOGLE_CLOUD_LOCATION", getEnv("GOOGLE_CLOUD_LOCATION", "")),

		OtelMetricsExporter: getEnv("OTEL_METRICS_EXPORTER", ""),
		OtelTracesExporter:  getEnv("OTEL_TRACES_EXPORTER", ""),
	}

	return cfg, nil
}

// parseWebhookURLBlacklist parses a comma-separated list of hosts into a map for O(1) lookups.
// Each host is normalized (trimmed, lowercased). Empty entries are ignored.
func parseWebhookURLBlacklist(s string) map[string]struct{} {
	out := make(map[string]struct{})

	parts := strings.SplitSeq(s, ",")
	for p := range parts {
		h := strings.TrimSpace(strings.ToLower(p))

		h = strings.TrimRight(h, ".")
		if h != "" {
			out[h] = struct{}{}
		}
	}

	return out
}
