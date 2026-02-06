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

	// Prometheus metrics: when true, enable MeterProvider and metrics server on PROMETHEUS_EXPORTER_PORT
	PrometheusEnabled bool

	// Prometheus metrics server listen address (e.g. ":9464"); only used when PrometheusEnabled is true
	PrometheusExporterPort string
}

// getEnv retrieves an environment variable or returns a default value.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvAsBool interprets an environment variable as a boolean (case-insensitive).
// True: "1", "true", "yes", "on". False: "", "0", "false", "no", "off", or numeric zero (e.g. "000", "0.0").
// Any other value is treated as false.
func getEnvAsBool(key string) bool {
	s := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if s == "" {
		return false
	}
	switch s {
	case "false", "no", "off":
		return false
	case "true", "yes", "on", "1":
		return true
	}
	// Numeric zero (0, 000, 0.0, etc.) -> false
	if n, err := strconv.ParseFloat(s, 64); err == nil && n == 0 {
		return false
	}
	return false
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

// Load reads configuration from environment variables and returns a Config struct.
// It automatically loads .env file if it exists.
// Returns default values for any missing environment variables.
// API_KEY is required and the function will return an error if it's not set.
func Load() (*Config, error) {
	// Load .env file if it exists. Skip logging when absent (e.g. env from secrets/parameter store).
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("Failed to load .env file", "error", err)
	}

	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		return nil, errors.New("API_KEY environment variable is required but not set")
	}

	webhookDeliveryMaxConcurrent := getEnvAsInt("WEBHOOK_DELIVERY_MAX_CONCURRENT", 100)
	if webhookDeliveryMaxConcurrent <= 0 {
		return nil, errors.New("WEBHOOK_DELIVERY_MAX_CONCURRENT must be a positive integer")
	}

	webhookDeliveryMaxAttempts := getEnvAsInt("WEBHOOK_DELIVERY_MAX_ATTEMPTS", 3)
	if webhookDeliveryMaxAttempts <= 0 {
		return nil, errors.New("WEBHOOK_DELIVERY_MAX_ATTEMPTS must be a positive integer")
	}

	webhookMaxFanOutPerEvent := getEnvAsInt("WEBHOOK_MAX_FAN_OUT_PER_EVENT", 500)
	if webhookMaxFanOutPerEvent <= 0 {
		return nil, errors.New("WEBHOOK_MAX_FAN_OUT_PER_EVENT must be a positive integer")
	}

	messagePublisherBufferSize := getEnvAsInt("MESSAGE_PUBLISHER_BUFFER_SIZE", 1024)
	if messagePublisherBufferSize <= 0 {
		return nil, errors.New("MESSAGE_PUBLISHER_BUFFER_SIZE must be a positive integer")
	}

	perEventTimeoutSecs := getEnvAsInt("MESSAGE_PUBLISHER_PER_EVENT_TIMEOUT_SECONDS", 10)
	if perEventTimeoutSecs <= 0 {
		return nil, errors.New("MESSAGE_PUBLISHER_PER_EVENT_TIMEOUT_SECONDS must be a positive integer")
	}

	shutdownTimeoutSecs := getEnvAsInt("SHUTDOWN_TIMEOUT_SECONDS", 30)
	if shutdownTimeoutSecs <= 0 {
		return nil, errors.New("SHUTDOWN_TIMEOUT_SECONDS must be a positive integer")
	}

	prometheusEnabled := getEnvAsBool("PROMETHEUS_ENABLED")
	prometheusPort := getEnv("PROMETHEUS_EXPORTER_PORT", "9464")
	if prometheusEnabled && prometheusPort == "" {
		prometheusPort = "9464"
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
		PrometheusEnabled:               prometheusEnabled,
		PrometheusExporterPort:          prometheusPort,
	}

	return cfg, nil
}
