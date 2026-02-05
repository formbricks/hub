// Package config provides application configuration loaded from environment variables.
package config

import (
	"errors"
	"log/slog"
	"os"
	"strconv"

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

	cfg := &Config{
		DatabaseURL: getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/test_db?sslmode=disable"),
		Port:        getEnv("PORT", "8080"),
		APIKey:      apiKey,
		LogLevel:    getEnv("LOG_LEVEL", "info"),

		WebhookDeliveryMaxConcurrent: webhookDeliveryMaxConcurrent,
		WebhookDeliveryMaxAttempts:   webhookDeliveryMaxAttempts,
	}

	return cfg, nil
}
