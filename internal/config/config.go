// Package config handles application configuration from environment variables and CLI arguments.
// Configuration is automatically loaded by Huma CLI with SERVICE_ prefix.
package config

import (
	"fmt"
	"strings"
)

// Config holds the application configuration
// Huma CLI automatically reads from environment variables with SERVICE_ prefix
// or command-line arguments
type Config struct {
	// Database configuration
	DatabaseURL       string `help:"PostgreSQL connection string" env:"DATABASE_URL" required:"true"`
	DBMaxOpenConns    int    `help:"Maximum number of open database connections" default:"25"`
	DBMaxIdleConns    int    `help:"Maximum number of idle database connections" default:"5"`
	DBConnMaxLifetime int    `help:"Maximum connection lifetime in minutes" default:"5"`
	DBConnMaxIdleTime int    `help:"Maximum connection idle time in minutes" default:"5"`

	// Server configuration
	Host string `help:"Host to bind to" default:"0.0.0.0"`
	Port int    `help:"Port to listen on" short:"p" default:"8080"`

	// Webhook configuration
	WebhookUrls string `help:"Comma-separated webhook URLs"`

	// Environment
	Environment string `help:"Environment (development/production)" default:"development"`

	// Security
	APIKey string `help:"Optional API key for authentication" env:"API_KEY"`

	// AI Enrichment configuration
	OpenAIKey              string `help:"OpenAI API key for AI features (optional)"`
	OpenAIEnrichmentModel  string `help:"OpenAI model for sentiment/topic enrichment" default:"gpt-4o-mini"`
	OpenAIEmbeddingModel   string `help:"OpenAI model for embeddings (e.g., text-embedding-3-small)"`
	EnrichmentTimeout      int    `help:"Enrichment timeout in seconds" default:"10"`
	EnrichmentWorkers      int    `help:"Number of concurrent enrichment workers" default:"3"`
	EnrichmentPollInterval int    `help:"Worker poll interval in seconds" default:"1"`

	// Logging
	LogLevel string `help:"Log level (debug/info/warn/error)" default:"info" enum:"debug,info,warn,error"`

	// Rate Limiting
	RateLimitPerIP       int `help:"Max requests per second per IP address" default:"100"`
	RateLimitBurst       int `help:"Burst size for rate limiter (allows temporary spikes)" default:"200"`
	RateLimitGlobal      int `help:"Max requests per second globally (all IPs combined)" default:"1000"`
	RateLimitGlobalBurst int `help:"Global burst size" default:"2000"`
}

// Address returns the server address in host:port format
func (c *Config) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// IsDevelopment returns true if the environment is development
func (c *Config) IsDevelopment() bool {
	return c.Environment == "development"
}

// IsEnrichmentEnabled returns true if OpenAI enrichment is configured
func (c *Config) IsEnrichmentEnabled() bool {
	return c.OpenAIKey != "" && c.OpenAIEnrichmentModel != ""
}

// IsEmbeddingEnabled returns true if OpenAI embeddings are configured
func (c *Config) IsEmbeddingEnabled() bool {
	return c.OpenAIKey != "" && c.OpenAIEmbeddingModel != ""
}

// GetWebhookURLs parses and returns the webhook URLs as a slice
func (c *Config) GetWebhookURLs() []string {
	if c.WebhookUrls == "" {
		return []string{}
	}

	urls := strings.Split(c.WebhookUrls, ",")
	result := make([]string, 0, len(urls))
	for _, url := range urls {
		trimmed := strings.TrimSpace(url)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
