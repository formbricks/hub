// Package config provides application configuration loaded from environment variables
// and optional .env file via cleanenv.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
	"github.com/joho/godotenv"
	"golang.org/x/text/language"

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
	ErrInvalidPublicBaseURL            = errors.New("PUBLIC_BASE_URL must be an absolute http(s) URL without query or fragment")
	ErrInvalidEmbeddingBaseURL         = errors.New("EMBEDDING_BASE_URL must be an absolute http(s) URL without query or fragment")
	ErrInvalidTranslationBaseURL       = errors.New("TRANSLATION_BASE_URL must be an absolute http(s) URL without query or fragment")
	ErrInvalidSentimentBaseURL         = errors.New("SENTIMENT_BASE_URL must be an absolute http(s) URL without query or fragment")
	ErrInvalidEmotionsBaseURL          = errors.New("EMOTIONS_BASE_URL must be an absolute http(s) URL without query or fragment")
	// ErrDotEnvMalformed deliberately withholds the parser's own message: godotenv error strings
	// echo raw file content (up to the whole remainder of the file), which for a .env means
	// secrets — API keys, the database password — straight into startup logs.
	ErrDotEnvMalformed = errors.New(
		".env file is malformed (fix quoting/characters; parse detail withheld to avoid logging secrets)")
	ErrInvalidTranslationDefaultLanguage = errors.New("TRANSLATION_DEFAULT_LANGUAGE must be a valid BCP-47 locale (e.g. en-US)")
	ErrInvalidTaxonomyServiceURL         = errors.New("TAXONOMY_SERVICE_URL must be an absolute http(s) URL without query or fragment")
)

// DefaultDatabaseURL is the default connection URL when DATABASE_URL is unset (local/test only).
// Runtime binaries (hub-worker, backfill-embeddings) should reject this and require an explicit URL.
//
//nolint:gosec // test default URL, not a production secret
const DefaultDatabaseURL = "postgres://postgres:postgres@localhost:5432/test_db?sslmode=disable"

// Config holds all application configuration in nested groups.
type Config struct {
	Server              ServerConfig
	Database            DatabaseConfig
	River               RiverConfig
	Webhook             WebhookConfig
	MessagePublisher    MessagePublisherConfig
	Embedding           EmbeddingConfig
	Translation         TranslationConfig
	Sentiment           SentimentConfig
	Emotions            EmotionsConfig
	TenantSettingsCache TenantSettingsCacheConfig
	Taxonomy            TaxonomyConfig
	TenantData          TenantDataConfig
	Observability       ObservabilityConfig
}

// ServerConfig holds HTTP server and process settings.
type ServerConfig struct {
	Port            string      `env:"PORT"                     env-default:"8080"`
	HubAPIKey       string      `env:"API_KEY"`
	PublicBaseURL   string      `env:"PUBLIC_BASE_URL"`
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

// RiverConfig holds River client settings (worker process). Zero values mean use River defaults.
// See https://pkg.go.dev/github.com/riverqueue/river#Config.
type RiverConfig struct {
	// JobTimeoutSec is the max time a job may run before its context is cancelled. 0 = River default (1m).
	JobTimeoutSec DurationSec `env:"RIVER_JOB_TIMEOUT_SECONDS" env-default:"0"`
	// RescueStuckJobsAfterSec: how long a "running" job is considered stuck (then retried/discarded). 0 = River default.
	RescueStuckJobsAfterSec DurationSec `env:"RIVER_RESCUE_STUCK_JOBS_AFTER_SECONDS" env-default:"0"`
	// CompletedJobRetentionSec is how long to keep completed jobs before cleanup. -1 = disable deletion.
	CompletedJobRetentionSec int `env:"RIVER_COMPLETED_JOB_RETENTION_SECONDS" env-default:"86400"`
	// ClientID identifies this client instance (logs, leader election). Empty = auto-generated.
	ClientID string `env:"RIVER_CLIENT_ID" env-default:""`
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
	ProviderAPIKey      string `env:"EMBEDDING_PROVIDER_API_KEY"`
	Provider            string `env:"EMBEDDING_PROVIDER"`
	Model               string `env:"EMBEDDING_MODEL"`
	BaseURL             string `env:"EMBEDDING_BASE_URL"`
	MaxConcurrent       int    `env:"EMBEDDING_MAX_CONCURRENT"        env-default:"5"`
	MaxAttempts         int    `env:"EMBEDDING_MAX_ATTEMPTS"          env-default:"3"`
	Normalize           bool   `env:"EMBEDDING_NORMALIZE"             env-default:"false"`
	GoogleCloudProject  string `env:"EMBEDDING_GOOGLE_CLOUD_PROJECT"`
	GoogleCloudLocation string `env:"EMBEDDING_GOOGLE_CLOUD_LOCATION"`
}

// TranslationConfig holds the feedback open-text translation enrichment settings
// (ENG-1255). Translation is disabled unless Provider and Model are both set.
type TranslationConfig struct {
	ProviderAPIKey string `env:"TRANSLATION_PROVIDER_API_KEY"`
	Provider       string `env:"TRANSLATION_PROVIDER"`
	Model          string `env:"TRANSLATION_MODEL"`
	BaseURL        string `env:"TRANSLATION_BASE_URL"`
	// DefaultLanguage is the fallback target language (BCP-47) applied when a tenant has no
	// target_language of its own. Empty means no fallback — translation is then per-tenant
	// opt-in (a tenant is translated only once it sets its own target). Normalized to
	// canonical form at load.
	DefaultLanguage     string `env:"TRANSLATION_DEFAULT_LANGUAGE"`
	MaxConcurrent       int    `env:"TRANSLATION_MAX_CONCURRENT"        env-default:"5"`
	MaxAttempts         int    `env:"TRANSLATION_MAX_ATTEMPTS"          env-default:"3"`
	GoogleCloudProject  string `env:"TRANSLATION_GOOGLE_CLOUD_PROJECT"`
	GoogleCloudLocation string `env:"TRANSLATION_GOOGLE_CLOUD_LOCATION"`
}

// SentimentConfig holds the feedback sentiment-enrichment provider settings (ENG-1529).
// Sentiment enrichment is disabled unless Provider and Model are both set — the same
// provider+model gate embeddings and translation use (there is no separate enable flag).
type SentimentConfig struct {
	ProviderAPIKey      string `env:"SENTIMENT_PROVIDER_API_KEY"`
	Provider            string `env:"SENTIMENT_PROVIDER"`
	Model               string `env:"SENTIMENT_MODEL"`
	BaseURL             string `env:"SENTIMENT_BASE_URL"`
	MaxConcurrent       int    `env:"SENTIMENT_MAX_CONCURRENT"        env-default:"5"`
	MaxAttempts         int    `env:"SENTIMENT_MAX_ATTEMPTS"          env-default:"3"`
	GoogleCloudProject  string `env:"SENTIMENT_GOOGLE_CLOUD_PROJECT"`
	GoogleCloudLocation string `env:"SENTIMENT_GOOGLE_CLOUD_LOCATION"`
}

// Enabled reports whether sentiment enrichment is configured (provider and model both set).
func (c SentimentConfig) Enabled() bool {
	return c.Provider != "" && c.Model != ""
}

// EmotionsConfig holds the feedback emotion-enrichment provider settings (ENG-1573).
// Emotion enrichment is disabled unless Provider and Model are both set — the same
// provider+model gate the other enrichments use (there is no separate enable flag).
type EmotionsConfig struct {
	ProviderAPIKey      string `env:"EMOTIONS_PROVIDER_API_KEY"`
	Provider            string `env:"EMOTIONS_PROVIDER"`
	Model               string `env:"EMOTIONS_MODEL"`
	BaseURL             string `env:"EMOTIONS_BASE_URL"`
	MaxConcurrent       int    `env:"EMOTIONS_MAX_CONCURRENT"        env-default:"5"`
	MaxAttempts         int    `env:"EMOTIONS_MAX_ATTEMPTS"          env-default:"3"`
	GoogleCloudProject  string `env:"EMOTIONS_GOOGLE_CLOUD_PROJECT"`
	GoogleCloudLocation string `env:"EMOTIONS_GOOGLE_CLOUD_LOCATION"`
}

// Enabled reports whether emotion enrichment is configured (provider and model both set).
func (c EmotionsConfig) Enabled() bool {
	return c.Provider != "" && c.Model != ""
}

// TenantSettingsCacheConfig configures the per-process tenant-settings cache that
// the translation enqueue gate and worker use to resolve a tenant's target
// language without hitting the database on every feedback event. A short TTL
// bounds cross-replica staleness; the worker records the target it actually used,
// so a changed target self-corrects on the next write.
type TenantSettingsCacheConfig struct {
	Size int         `env:"TENANT_SETTINGS_CACHE_SIZE"        env-default:"2048"`
	TTL  DurationSec `env:"TENANT_SETTINGS_CACHE_TTL_SECONDS" env-default:"60"`
}

// TaxonomyConfig holds Hub-to-taxonomy service settings.
type TaxonomyConfig struct {
	ServiceURL             string `env:"TAXONOMY_SERVICE_URL"`
	ServiceToken           string `env:"TAXONOMY_SERVICE_TOKEN"`
	HubInternalAPIToken    string `env:"HUB_INTERNAL_API_TOKEN"`
	EmbeddingModel         string `env:"TAXONOMY_EMBEDDING_MODEL"`
	MinimumEmbeddedRecords int    `env:"TAXONOMY_MIN_EMBEDDED_RECORDS" env-default:"20"`
}

// TenantDataConfig holds tenant data purge settings.
type TenantDataConfig struct {
	// PurgeLockTimeout bounds how long a tenant data purge waits for in-flight
	// tenant-owned writes to drain before giving up with a retryable conflict.
	// Must be positive: 0 would mean "wait forever" at the database level.
	PurgeLockTimeout DurationSec `env:"TENANT_PURGE_LOCK_TIMEOUT_SECONDS" env-default:"5"`
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

	// cleanenv.ReadConfig(".env", cfg) is split here into its two phases on purpose, to
	// discriminate the error SOURCE:
	//   - A .env *parse* failure (godotenv) is formatted with the offending file content (e.g.
	//     `unexpected character %q in variable name near %q`, the second value being the rest of
	//     the file), and main logs the returned error — surfacing it would put API keys and the
	//     database password into stdout and the log aggregator. It is masked as the static
	//     ErrDotEnvMalformed.
	//   - A struct *coercion* failure from ReadEnv (e.g. a non-numeric SENTIMENT_MAX_ATTEMPTS)
	//     names only the field and env var plus the type error; it carries no file content, so it
	//     is surfaced to point the operator at the actual problem instead of a misleading
	//     "malformed .env".
	// godotenv.Overload mirrors cleanenv's .env handling (parse + unconditional os.Setenv), so a
	// value in .env still overrides a pre-existing environment variable, and a missing file is a
	// no-op (env-only configuration).
	if err := godotenv.Overload(".env"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, ErrDotEnvMalformed
	}

	if err := cleanenv.ReadEnv(cfg); err != nil {
		return nil, fmt.Errorf("read env: %w", err)
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
		cfg.Database.URL = DefaultDatabaseURL
	}

	if cfg.Database.MaxConns <= 0 {
		cfg.Database.MaxConns = 25
	}

	if len(cfg.Webhook.URLBlacklist) == 0 {
		cfg.Webhook.URLBlacklist = BlacklistSet(parseBlacklist("localhost,127.0.0.1,::1,169.254.169.254"))
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

	// Google Cloud fallbacks: EMBEDDING_* can fall back to GOOGLE_CLOUD_*
	// The global GOOGLE_CLOUD_PROJECT/LOCATION pair backs every enrichment's per-type override
	// (as .env.example documents): one gcloud project setting serves all four pipelines unless a
	// type overrides it.
	for _, pair := range []struct{ project, location *string }{
		{&cfg.Embedding.GoogleCloudProject, &cfg.Embedding.GoogleCloudLocation},
		{&cfg.Translation.GoogleCloudProject, &cfg.Translation.GoogleCloudLocation},
		{&cfg.Sentiment.GoogleCloudProject, &cfg.Sentiment.GoogleCloudLocation},
		{&cfg.Emotions.GoogleCloudProject, &cfg.Emotions.GoogleCloudLocation},
	} {
		if *pair.project == "" {
			*pair.project = os.Getenv("GOOGLE_CLOUD_PROJECT")
		}

		if *pair.location == "" {
			*pair.location = os.Getenv("GOOGLE_CLOUD_LOCATION")
		}
	}

	// Coerce unset/nonsensical worker tunables back to their defaults for every enrichment
	// type. An explicit 0 would otherwise fail hub-worker startup (river rejects MaxWorkers 0)
	// or, worse, flow into InsertOpts where River substitutes its default of 25 attempts — 25
	// LLM calls per failing job instead of the intended 3.
	for _, tunables := range []struct{ maxConcurrent, maxAttempts *int }{
		{&cfg.Embedding.MaxConcurrent, &cfg.Embedding.MaxAttempts},
		{&cfg.Translation.MaxConcurrent, &cfg.Translation.MaxAttempts},
		{&cfg.Sentiment.MaxConcurrent, &cfg.Sentiment.MaxAttempts},
		{&cfg.Emotions.MaxConcurrent, &cfg.Emotions.MaxAttempts},
	} {
		if *tunables.maxConcurrent <= 0 {
			*tunables.maxConcurrent = 5
		}

		if *tunables.maxAttempts <= 0 {
			*tunables.maxAttempts = 3
		}
	}

	// Default the cache size only when the operator did not set it. An explicit 0 (or
	// negative) disables the cache: NewCachedTenantSettings treats size <= 0 as "no
	// caching". cleanenv does not reliably apply env-default to nested-struct fields, so
	// we cannot distinguish "unset" from an explicit "0" without consulting the env.
	const defaultTenantSettingsCacheSize = 2048
	if _, ok := os.LookupEnv("TENANT_SETTINGS_CACHE_SIZE"); !ok {
		cfg.TenantSettingsCache.Size = defaultTenantSettingsCacheSize
	}

	// Mirror the other nested DurationSec defaults: re-assert in applyDefaults because
	// cleanenv does not reliably apply env-default to nested-struct fields, and a zero TTL
	// would silently disable the cache rather than use 60s.
	const defaultTenantSettingsCacheTTLSec = 60
	if cfg.TenantSettingsCache.TTL.Duration() <= 0 {
		cfg.TenantSettingsCache.TTL = DurationSec(time.Duration(defaultTenantSettingsCacheTTLSec) * time.Second)
	}

	if cfg.Taxonomy.MinimumEmbeddedRecords <= 0 {
		cfg.Taxonomy.MinimumEmbeddedRecords = 20
	}

	const defaultPurgeLockTimeoutSec = 5
	if cfg.TenantData.PurgeLockTimeout.Duration() <= 0 {
		cfg.TenantData.PurgeLockTimeout = DurationSec(time.Duration(defaultPurgeLockTimeoutSec) * time.Second)
	}
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

	if cfg.Database.MinConns > cfg.Database.MaxConns {
		return ErrDatabaseMinConnsExceedsMax
	}

	if cfg.Server.PublicBaseURL != "" {
		normalized, err := normalizeHTTPBaseURL(cfg.Server.PublicBaseURL, ErrInvalidPublicBaseURL)
		if err != nil {
			return err
		}

		cfg.Server.PublicBaseURL = normalized
	}

	if cfg.Embedding.BaseURL != "" {
		normalized, err := normalizeHTTPBaseURL(cfg.Embedding.BaseURL, ErrInvalidEmbeddingBaseURL)
		if err != nil {
			return err
		}

		cfg.Embedding.BaseURL = normalized
	}

	if cfg.Translation.BaseURL != "" {
		normalized, err := normalizeHTTPBaseURL(cfg.Translation.BaseURL, ErrInvalidTranslationBaseURL)
		if err != nil {
			return err
		}

		cfg.Translation.BaseURL = normalized
	}

	if cfg.Sentiment.BaseURL != "" {
		normalized, err := normalizeHTTPBaseURL(cfg.Sentiment.BaseURL, ErrInvalidSentimentBaseURL)
		if err != nil {
			return err
		}

		cfg.Sentiment.BaseURL = normalized
	}

	if cfg.Emotions.BaseURL != "" {
		normalized, err := normalizeHTTPBaseURL(cfg.Emotions.BaseURL, ErrInvalidEmotionsBaseURL)
		if err != nil {
			return err
		}

		cfg.Emotions.BaseURL = normalized
	}

	// Canonicalize the fallback target language so it compares equal to tenant targets (which
	// the settings service normalizes on write). Empty is allowed (no fallback).
	if cfg.Translation.DefaultLanguage != "" {
		tag, err := language.Parse(strings.TrimSpace(cfg.Translation.DefaultLanguage))
		if err != nil {
			return ErrInvalidTranslationDefaultLanguage
		}

		cfg.Translation.DefaultLanguage = tag.String()
	}

	if cfg.Taxonomy.ServiceURL != "" {
		normalized, err := normalizeHTTPBaseURL(cfg.Taxonomy.ServiceURL, ErrInvalidTaxonomyServiceURL)
		if err != nil {
			return err
		}

		cfg.Taxonomy.ServiceURL = normalized
	}

	return nil
}

func normalizeHTTPBaseURL(raw string, sentinel error) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", sentinel
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: %w", sentinel, err)
	}

	if !parsed.IsAbs() || parsed.Host == "" {
		return "", sentinel
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", sentinel
	}

	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", sentinel
	}

	if parsed.User != nil {
		return "", sentinel
	}

	if parsed.Path == "/" {
		parsed.Path = ""
		parsed.RawPath = ""
	} else {
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")
	}

	return parsed.String(), nil
}
