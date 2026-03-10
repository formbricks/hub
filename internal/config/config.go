// Package config provides application configuration loaded from environment variables
// and optional .env file via cleanenv.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ilyakaznacheev/cleanenv"

	"github.com/formbricks/hub/pkg/database"
)

// Sentinel errors for configuration validation (err113).
var (
	ErrWebhookDeliveryMaxConcurrent    = errors.New("WEBHOOK_DELIVERY_MAX_CONCURRENT must be a positive integer")
	ErrWebhookDeliveryMaxAttempts      = errors.New("WEBHOOK_DELIVERY_MAX_ATTEMPTS must be a positive integer")
	ErrWebhookMaxFanOutPerEvent        = errors.New("WEBHOOK_MAX_FAN_OUT_PER_EVENT must be a positive integer")
	ErrMessagePublisherQueueMaxSize    = errors.New("MESSAGE_PUBLISHER_QUEUE_MAX_SIZE must be a positive integer")
	ErrMessagePublisherPerEventTimeout = errors.New("MESSAGE_PUBLISHER_PER_EVENT_TIMEOUT_SECONDS must be a positive integer")
	ErrShutdownTimeoutSeconds          = errors.New("SHUTDOWN_TIMEOUT_SECONDS must be a positive integer")
	ErrWebhookMaxCount                 = errors.New("WEBHOOK_MAX_COUNT must be a positive integer")
	ErrDatabaseMinConnsExceedsMax      = errors.New("DATABASE_MIN_CONNS must not exceed DATABASE_MAX_CONNS")
)

// Config holds all application configuration in nested groups.
type Config struct {
	Server           ServerConfig
	Database         DatabaseConfig
	Webhook          WebhookConfig
	MessagePublisher MessagePublisherConfig
	Embedding        EmbeddingConfig
	Observability    ObservabilityConfig
}

// ServerConfig holds HTTP server and process settings.
type ServerConfig struct {
	Port            string      `env:"PORT"                     env-default:"8080"`
	APIKey          string      `env:"API_KEY"`
	LogLevel        string      `env:"LOG_LEVEL"                env-default:"info"`
	ShutdownTimeout DurationSec `env:"SHUTDOWN_TIMEOUT_SECONDS" env-default:"30"`
}

// DatabaseConfig holds database connection settings.
type DatabaseConfig struct {
	URL               string      `env:"DATABASE_URL"                         env-default:"postgres://postgres:postgres@localhost:5432/test_db?sslmode=disable"` //nolint:lll // default connection URL
	MaxConns          int         `env:"DATABASE_MAX_CONNS"                   env-default:"25"`
	MinConns          int         `env:"DATABASE_MIN_CONNS"                   env-default:"0"`
	MaxConnLifetime   DurationSec `env:"DATABASE_MAX_CONN_LIFETIME_SECONDS"   env-default:"3600"`
	MaxConnIdleTime   DurationSec `env:"DATABASE_MAX_CONN_IDLE_TIME_SECONDS"  env-default:"1800"`
	HealthCheckPeriod DurationSec `env:"DATABASE_HEALTH_CHECK_PERIOD_SECONDS" env-default:"60"`
	ConnectTimeout    DurationSec `env:"DATABASE_CONNECT_TIMEOUT_SECONDS"     env-default:"10"`
}

// PoolConfig returns database pool options for this config (for use with database.NewPostgresPool).
func (d *DatabaseConfig) PoolConfig() database.PoolConfig {
	return database.PoolConfig{
		MaxConns:          d.MaxConns,
		MinConns:          d.MinConns,
		MaxConnLifetime:   d.MaxConnLifetime.Duration(),
		MaxConnIdleTime:   d.MaxConnIdleTime.Duration(),
		HealthCheckPeriod: d.HealthCheckPeriod.Duration(),
		ConnectTimeout:    d.ConnectTimeout.Duration(),
	}
}

// WebhookConfig holds webhook delivery and enqueue settings.
type WebhookConfig struct {
	DeliveryMaxConcurrent   int          `env:"WEBHOOK_DELIVERY_MAX_CONCURRENT"    env-default:"100"`
	DeliveryMaxAttempts     int          `env:"WEBHOOK_DELIVERY_MAX_ATTEMPTS"      env-default:"3"`
	MaxFanOutPerEvent       int          `env:"WEBHOOK_MAX_FAN_OUT_PER_EVENT"      env-default:"500"`
	MaxCount                int          `env:"WEBHOOK_MAX_COUNT"                  env-default:"500"`
	HTTPTimeout             DurationSec  `env:"WEBHOOK_HTTP_TIMEOUT_SECONDS"       env-default:"15"`
	EnqueueMaxRetries       int          `env:"WEBHOOK_ENQUEUE_MAX_RETRIES"        env-default:"3"`
	EnqueueInitialBackoffMs int          `env:"WEBHOOK_ENQUEUE_INITIAL_BACKOFF_MS" env-default:"100"`
	EnqueueMaxBackoffMs     int          `env:"WEBHOOK_ENQUEUE_MAX_BACKOFF_MS"     env-default:"2000"`
	URLBlacklist            BlacklistSet `env:"WEBHOOK_BLACKLIST"                  env-default:"localhost,127.0.0.1,::1,169.254.169.254"`
}

// MessagePublisherConfig holds event channel and timeout settings.
type MessagePublisherConfig struct {
	BufferSize         int `env:"MESSAGE_PUBLISHER_QUEUE_MAX_SIZE"            env-default:"16384"`
	PerEventTimeoutSec int `env:"MESSAGE_PUBLISHER_PER_EVENT_TIMEOUT_SECONDS" env-default:"10"`
}

// EmbeddingConfig holds embedding provider and queue settings.
type EmbeddingConfig struct {
	ProviderAPIKey string `env:"EMBEDDING_PROVIDER_API_KEY"`
	Provider       string `env:"EMBEDDING_PROVIDER"`
	Model          string `env:"EMBEDDING_MODEL"`
	MaxConcurrent  int    `env:"EMBEDDING_MAX_CONCURRENT"   env-default:"5"`
	MaxAttempts    int    `env:"EMBEDDING_MAX_ATTEMPTS"     env-default:"3"`
	Normalize      bool   `env:"EMBEDDING_NORMALIZE"        env-default:"false"`
}

// ObservabilityConfig holds OpenTelemetry settings.
type ObservabilityConfig struct {
	MetricsExporter string `env:"OTEL_METRICS_EXPORTER"`
	TracesExporter  string `env:"OTEL_TRACES_EXPORTER"`
}

// DurationSec parses integer seconds from env and stores as time.Duration.
// It implements cleanenv.Setter for use in config structs.
type DurationSec time.Duration

// SetValue implements cleanenv.Setter. s is the raw env value (e.g. "30" for seconds).
func (d *DurationSec) SetValue(s string) error {
	if s == "" {
		return nil
	}

	sec, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("parse duration seconds: %w", err)
	}

	*d = DurationSec(time.Duration(sec) * time.Second)

	return nil
}

// Duration returns the value as time.Duration.
func (d *DurationSec) Duration() time.Duration {
	return time.Duration(*d)
}

// BlacklistSet is a set of normalized hostnames (e.g. for SSRF mitigation).
// It implements cleanenv.Setter by parsing a comma-separated list.
type BlacklistSet map[string]struct{}

// SetValue implements cleanenv.Setter.
func (b *BlacklistSet) SetValue(s string) error {
	*b = parseBlacklist(s)

	return nil
}

func parseBlacklist(s string) map[string]struct{} {
	out := make(map[string]struct{})

	parts := strings.SplitSeq(s, ",")
	for part := range parts {
		h := strings.TrimSpace(strings.ToLower(part))

		h = strings.TrimSuffix(h, ".")
		if h != "" {
			out[h] = struct{}{}
		}
	}

	return out
}

// Load reads configuration from .env (if present) and environment variables.
// cleanenv supports .env in ReadConfig (see https://github.com/ilyakaznacheev/cleanenv).
// If .env is missing, ReadEnv is used so config comes from the process environment only.
// API_KEY is not required by Load (worker can run without it); validate in API main if needed.
func Load() (*Config, error) {
	cfg := &Config{}

	err := cleanenv.ReadConfig(".env", cfg)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || isNotExist(err) {
			if readErr := cleanenv.ReadEnv(cfg); readErr != nil {
				return nil, fmt.Errorf("read env: %w", readErr)
			}
		} else {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	applyDefaults(cfg)

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// applyDefaults fills in default values for empty fields (cleanenv may leave nested struct defaults unset).
func applyDefaults(cfg *Config) {
	if cfg.Server.Port == "" {
		cfg.Server.Port = "8080"
	}

	if cfg.Server.LogLevel == "" {
		cfg.Server.LogLevel = "info"
	}

	const defaultShutdownSec = 30
	if cfg.Server.ShutdownTimeout.Duration() == 0 {
		cfg.Server.ShutdownTimeout = DurationSec(time.Duration(defaultShutdownSec) * time.Second)
	}

	if cfg.Database.URL == "" {
		cfg.Database.URL = "postgres://postgres:postgres@localhost:5432/test_db?sslmode=disable"
	}

	if cfg.Database.MaxConns <= 0 {
		cfg.Database.MaxConns = 25
	}

	if len(cfg.Webhook.URLBlacklist) == 0 {
		cfg.Webhook.URLBlacklist = BlacklistSet(parseBlacklist("localhost,127.0.0.1,::1,169.254.169.254"))
	}
}

func isNotExist(err error) bool {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return errors.Is(pathErr.Err, os.ErrNotExist)
	}

	return false
}

func validate(cfg *Config) error {
	if cfg.Webhook.DeliveryMaxConcurrent <= 0 {
		return ErrWebhookDeliveryMaxConcurrent
	}

	if cfg.Webhook.DeliveryMaxAttempts <= 0 {
		return ErrWebhookDeliveryMaxAttempts
	}

	if cfg.Webhook.MaxFanOutPerEvent <= 0 {
		return ErrWebhookMaxFanOutPerEvent
	}

	if cfg.MessagePublisher.BufferSize <= 0 {
		return ErrMessagePublisherQueueMaxSize
	}

	if cfg.MessagePublisher.PerEventTimeoutSec <= 0 {
		return ErrMessagePublisherPerEventTimeout
	}

	if cfg.Server.ShutdownTimeout.Duration() <= 0 {
		return ErrShutdownTimeoutSeconds
	}

	if cfg.Webhook.MaxCount <= 0 {
		return ErrWebhookMaxCount
	}

	const defaultWebhookHTTPTimeoutSec = 15
	if cfg.Webhook.HTTPTimeout.Duration() <= 0 {
		cfg.Webhook.HTTPTimeout = DurationSec(time.Duration(defaultWebhookHTTPTimeoutSec) * time.Second)
	}

	if cfg.Webhook.EnqueueMaxRetries < 0 {
		cfg.Webhook.EnqueueMaxRetries = 3
	}

	if cfg.Webhook.EnqueueInitialBackoffMs <= 0 {
		cfg.Webhook.EnqueueInitialBackoffMs = 100
	}

	if cfg.Webhook.EnqueueMaxBackoffMs <= 0 {
		cfg.Webhook.EnqueueMaxBackoffMs = 2000
	}

	if cfg.Database.MinConns > cfg.Database.MaxConns {
		return ErrDatabaseMinConnsExceedsMax
	}

	if cfg.Database.MaxConns <= 0 {
		cfg.Database.MaxConns = 25
	}

	if cfg.Embedding.MaxConcurrent <= 0 {
		cfg.Embedding.MaxConcurrent = 5
	}

	if cfg.Embedding.MaxAttempts <= 0 {
		cfg.Embedding.MaxAttempts = 3
	}

	return nil
}
