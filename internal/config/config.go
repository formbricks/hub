package config

import (
	"errors"
	"log/slog"
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

	// Connector instance limits
	MaxPollingConnectorInstances    int
	MaxWebhookConnectorInstances    int
	MaxOutputConnectorInstances     int
	MaxEnrichmentConnectorInstances int

	// Rate limiting
	PollingConnectorMinDelay        time.Duration
	PollingConnectorMaxPollsPerHour int

	// Webhook cache configuration
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

	// Load connector instance limits (default: 10)
	maxPolling := getEnvAsInt("MAX_POLLING_CONNECTOR_INSTANCES", 10)
	maxWebhook := getEnvAsInt("MAX_WEBHOOK_CONNECTOR_INSTANCES", 10)
	maxOutput := getEnvAsInt("MAX_OUTPUT_CONNECTOR_INSTANCES", 10)
	maxEnrichment := getEnvAsInt("MAX_ENRICHMENT_CONNECTOR_INSTANCES", 10)

	// Validate limits are positive
	if maxPolling < 0 || maxWebhook < 0 || maxOutput < 0 || maxEnrichment < 0 {
		return nil, errors.New("connector instance limits must be non-negative integers")
	}

	// Load rate limiting configuration
	minDelay := getEnvAsDuration("POLLING_CONNECTOR_MIN_DELAY", 1*time.Minute)
	maxPollsPerHour := getEnvAsInt("POLLING_CONNECTOR_MAX_POLLS_PER_HOUR", 60)

	// Validate rate limiting values
	if minDelay < 0 {
		return nil, errors.New("POLLING_CONNECTOR_MIN_DELAY must be a positive duration")
	}
	if maxPollsPerHour < 0 {
		return nil, errors.New("POLLING_CONNECTOR_MAX_POLLS_PER_HOUR must be a non-negative integer")
	}

	// Load webhook cache configuration
	webhookCacheEnabled := getEnvAsBool("WEBHOOK_CACHE_ENABLED", false)
	webhookCacheSize := getEnvAsInt("WEBHOOK_CACHE_SIZE", 100)
	webhookCacheTTL := getEnvAsDuration("WEBHOOK_CACHE_TTL", 5*time.Minute)

	// Validate cache configuration
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

		MaxPollingConnectorInstances:    maxPolling,
		MaxWebhookConnectorInstances:    maxWebhook,
		MaxOutputConnectorInstances:     maxOutput,
		MaxEnrichmentConnectorInstances: maxEnrichment,

		PollingConnectorMinDelay:        minDelay,
		PollingConnectorMaxPollsPerHour: maxPollsPerHour,

		WebhookCacheEnabled: webhookCacheEnabled,
		WebhookCacheSize:    webhookCacheSize,
		WebhookCacheTTL:     webhookCacheTTL,
	}

	slog.Info("Loaded connector instance limits",
		"polling", maxPolling,
		"webhook", maxWebhook,
		"output", maxOutput,
		"enrichment", maxEnrichment,
	)
	slog.Info("Loaded rate limiting configuration",
		"min_delay", minDelay,
		"max_polls_per_hour", maxPollsPerHour,
	)
	slog.Info("Loaded webhook cache configuration",
		"enabled", webhookCacheEnabled,
		"size", webhookCacheSize,
		"ttl", webhookCacheTTL,
	)

	return cfg, nil
}
