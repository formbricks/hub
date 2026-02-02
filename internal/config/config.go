package config

import (
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all application configuration
type Config struct {
	DatabaseURL string
	Port        string
	APIKey      string
	LogLevel    string

	// Webhook delivery cache
	WebhookCacheEnabled bool
	WebhookCacheSize    int
	WebhookCacheTTL     time.Duration
}

// getEnv retrieves an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvAsInt retrieves an environment variable as an integer or returns a default value
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

// getEnvAsDuration retrieves an environment variable as a duration or returns a default value
func getEnvAsDuration(key string, defaultValue time.Duration) time.Duration {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	value, err := time.ParseDuration(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}

// getEnvAsBool retrieves an environment variable as a boolean or returns a default value
func getEnvAsBool(key string, defaultValue bool) bool {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	value, err := strconv.ParseBool(valueStr)
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
	// Load .env file if it exists (ignore error if file doesn't exist)
	_ = godotenv.Load()

	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		return nil, errors.New("API_KEY environment variable is required but not set")
	}

	webhookCacheEnabled := getEnvAsBool("WEBHOOK_CACHE_ENABLED", false)
	webhookCacheSize := getEnvAsInt("WEBHOOK_CACHE_SIZE", 100)
	webhookCacheTTL := getEnvAsDuration("WEBHOOK_CACHE_TTL", 5*time.Minute)

	if webhookCacheSize <= 0 {
		return nil, errors.New("WEBHOOK_CACHE_SIZE must be a positive integer")
	}
	if webhookCacheTTL <= 0 {
		return nil, errors.New("WEBHOOK_CACHE_TTL must be a positive duration")
	}

	cfg := &Config{
		DatabaseURL: getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/test_db?sslmode=disable"),
		Port:        getEnv("PORT", "8080"),
		APIKey:      apiKey,
		LogLevel:    getEnv("LOG_LEVEL", "info"),

		WebhookCacheEnabled: webhookCacheEnabled,
		WebhookCacheSize:    webhookCacheSize,
		WebhookCacheTTL:     webhookCacheTTL,
	}

	return cfg, nil
}
