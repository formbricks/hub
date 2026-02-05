// Package config provides application configuration loaded from environment variables.
package config

import (
	"errors"
	"log/slog"
	"os"
	"strconv"
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
	}

	return cfg, nil
}
