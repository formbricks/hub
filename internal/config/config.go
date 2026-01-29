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
	DatabaseURL  string
	Port         string
	APIKey       string
	LogLevel     string
	OpenAIAPIKey string // Optional: for AI enrichment features

	// River job queue settings
	RiverEnabled       bool    // RIVER_ENABLED - enable River job queue (default: true if OpenAI key set)
	RiverWorkers       int     // RIVER_WORKERS - concurrent embedding workers (default: 10)
	RiverMaxRetries    int     // RIVER_MAX_RETRIES - max retry attempts (default: 5)
	EmbeddingRateLimit float64 // EMBEDDING_RATE_LIMIT - OpenAI requests per second (default: 50)

	// Taxonomy service settings
	TaxonomyServiceURL       string        // URL of the taxonomy-generator Python microservice
	TaxonomySchedulerEnabled bool          // Enable periodic taxonomy scheduler
	TaxonomyPollInterval     time.Duration // How often to check for due jobs (default: 1m)
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

// getEnvAsFloat retrieves an environment variable as a float64 or returns a default value
func getEnvAsFloat(key string, defaultValue float64) float64 {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return defaultValue
	}
	return value
}

// getEnvAsBool retrieves an environment variable as a bool or returns a default value
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

	openAIKey := os.Getenv("OPENAI_API_KEY")

	// River is enabled by default when OpenAI key is set, unless explicitly disabled
	riverEnabledDefault := openAIKey != ""
	riverEnabled := getEnvAsBool("RIVER_ENABLED", riverEnabledDefault)

	cfg := &Config{
		DatabaseURL:  getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/test_db?sslmode=disable"),
		Port:         getEnv("PORT", "8080"),
		APIKey:       apiKey,
		LogLevel:     getEnv("LOG_LEVEL", "info"),
		OpenAIAPIKey: openAIKey,

		// River job queue configuration
		RiverEnabled:       riverEnabled,
		RiverWorkers:       getEnvAsInt("RIVER_WORKERS", 10),
		RiverMaxRetries:    getEnvAsInt("RIVER_MAX_RETRIES", 5),
		EmbeddingRateLimit: getEnvAsFloat("EMBEDDING_RATE_LIMIT", 50),

		// Taxonomy service settings
		TaxonomyServiceURL:       getEnv("TAXONOMY_SERVICE_URL", "http://localhost:8001"),
		TaxonomySchedulerEnabled: getEnv("TAXONOMY_SCHEDULER_ENABLED", "false") == "true",
		TaxonomyPollInterval:     getEnvAsDuration("TAXONOMY_POLL_INTERVAL", 1*time.Minute),
	}

	return cfg, nil
}
