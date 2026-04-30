package config

import (
	"errors"
	"os"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name              string
		databaseURL       string
		port              string
		publicBaseURL     string
		setDatabaseURL    bool
		setPort           bool
		setPublicBaseURL  bool
		wantDatabaseURL   string
		wantPort          string
		wantPublicBaseURL string
	}{
		{
			name:              "returns default values when no environment variables set",
			databaseURL:       "",
			port:              "",
			publicBaseURL:     "",
			setDatabaseURL:    false,
			setPort:           false,
			setPublicBaseURL:  false,
			wantDatabaseURL:   "postgres://postgres:postgres@localhost:5432/test_db?sslmode=disable",
			wantPort:          "8080",
			wantPublicBaseURL: "",
		},
		{
			name:              "returns custom DATABASE_URL when set",
			databaseURL:       "postgres://custom:password@localhost:5432/custom_db",
			port:              "",
			publicBaseURL:     "",
			setDatabaseURL:    true,
			setPort:           false,
			setPublicBaseURL:  false,
			wantDatabaseURL:   "postgres://custom:password@localhost:5432/custom_db",
			wantPort:          "8080",
			wantPublicBaseURL: "",
		},
		{
			name:              "returns custom PORT when set",
			databaseURL:       "",
			port:              "3000",
			publicBaseURL:     "",
			setDatabaseURL:    false,
			setPort:           true,
			setPublicBaseURL:  false,
			wantDatabaseURL:   "postgres://postgres:postgres@localhost:5432/test_db?sslmode=disable",
			wantPort:          "3000",
			wantPublicBaseURL: "",
		},
		{
			name:              "returns custom values for both when set",
			databaseURL:       "postgres://custom:password@localhost:5432/custom_db",
			port:              "3000",
			publicBaseURL:     "",
			setDatabaseURL:    true,
			setPort:           true,
			setPublicBaseURL:  false,
			wantDatabaseURL:   "postgres://custom:password@localhost:5432/custom_db",
			wantPort:          "3000",
			wantPublicBaseURL: "",
		},
		{
			name:              "normalizes PUBLIC_BASE_URL when set",
			databaseURL:       "",
			port:              "",
			publicBaseURL:     "https://hub.example.com/root/",
			setDatabaseURL:    false,
			setPort:           false,
			setPublicBaseURL:  true,
			wantDatabaseURL:   "postgres://postgres:postgres@localhost:5432/test_db?sslmode=disable",
			wantPort:          "8080",
			wantPublicBaseURL: "https://hub.example.com/root",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setOrUnsetEnv(t, "DATABASE_URL", tt.databaseURL, tt.setDatabaseURL)
			setOrUnsetEnv(t, "PORT", tt.port, tt.setPort)
			setOrUnsetEnv(t, "PUBLIC_BASE_URL", tt.publicBaseURL, tt.setPublicBaseURL)

			cfg, err := Load()
			if err != nil {
				t.Errorf("Load() error = %v, want nil", err)

				return
			}

			if cfg.Database.URL != tt.wantDatabaseURL {
				t.Errorf("Load() Database.URL = %v, want %v", cfg.Database.URL, tt.wantDatabaseURL)
			}

			if cfg.Server.Port != tt.wantPort {
				t.Errorf("Load() Server.Port = %v, want %v", cfg.Server.Port, tt.wantPort)
			}

			if cfg.Server.PublicBaseURL != tt.wantPublicBaseURL {
				t.Errorf("Load() Server.PublicBaseURL = %v, want %v", cfg.Server.PublicBaseURL, tt.wantPublicBaseURL)
			}
		})
	}
}

//nolint:usetesting // These table tests intentionally exercise truly unset env vars.
func setOrUnsetEnv(t *testing.T, key, value string, set bool) {
	t.Helper()

	originalValue, hadOriginalValue := os.LookupEnv(key)
	if set {
		if err := os.Setenv(key, value); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	} else if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}

	t.Cleanup(func() {
		if hadOriginalValue {
			if err := os.Setenv(key, originalValue); err != nil {
				t.Fatalf("restore %s: %v", key, err)
			}

			return
		}

		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("restore unset %s: %v", key, err)
		}
	})
}

// TestLoadAlwaysReturnsNilError cannot use t.Parallel() because it uses t.Setenv (Go restriction).
func TestLoadAlwaysReturnsNilError(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Errorf("Load() error = %v, want nil", err)
	}

	if cfg == nil {
		t.Error("Load() config = nil, want non-nil config")
	}
}

func TestLoad_WebhookDeliveryMaxAttempts(t *testing.T) {
	t.Run("default is 3 when unset", func(t *testing.T) {
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if cfg.Webhook.DeliveryMaxAttempts != 3 {
			t.Errorf("Webhook.DeliveryMaxAttempts = %d, want 3", cfg.Webhook.DeliveryMaxAttempts)
		}
	})

	t.Run("override via WEBHOOK_DELIVERY_MAX_ATTEMPTS", func(t *testing.T) {
		t.Setenv("WEBHOOK_DELIVERY_MAX_ATTEMPTS", "5")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if cfg.Webhook.DeliveryMaxAttempts != 5 {
			t.Errorf("Webhook.DeliveryMaxAttempts = %d, want 5", cfg.Webhook.DeliveryMaxAttempts)
		}
	})

	t.Run("validation error when <= 0", func(t *testing.T) {
		t.Setenv("WEBHOOK_DELIVERY_MAX_ATTEMPTS", "0")

		_, err := Load()
		if err == nil {
			t.Error("Load() error = nil, want error for WEBHOOK_DELIVERY_MAX_ATTEMPTS <= 0")
		}
	})

	t.Run("non-numeric returns error", func(t *testing.T) {
		t.Setenv("WEBHOOK_DELIVERY_MAX_ATTEMPTS", "x")

		_, err := Load()
		if err == nil {
			t.Error("Load() error = nil, want error for invalid WEBHOOK_DELIVERY_MAX_ATTEMPTS")
		}
	})
}

func TestLoad_EmbeddingGoogleCloudProject(t *testing.T) {
	t.Setenv("API_KEY", "test-api-key")
	t.Setenv("EMBEDDING_GOOGLE_CLOUD_PROJECT", "my-google-cloud-project")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Embedding.GoogleCloudProject != "my-google-cloud-project" {
		t.Errorf("Embedding.GoogleCloudProject = %q, want my-google-cloud-project", cfg.Embedding.GoogleCloudProject)
	}
}

func TestLoad_EmbeddingGoogleCloudProject_fallback(t *testing.T) {
	t.Setenv("API_KEY", "test-api-key")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "fallback-project")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Embedding.GoogleCloudProject != "fallback-project" {
		t.Errorf("Embedding.GoogleCloudProject = %q, want fallback-project", cfg.Embedding.GoogleCloudProject)
	}
}

func TestLoad_EmbeddingGoogleCloudLocation(t *testing.T) {
	t.Setenv("API_KEY", "test-api-key")
	t.Setenv("EMBEDDING_GOOGLE_CLOUD_LOCATION", "europe-west3")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Embedding.GoogleCloudLocation != "europe-west3" {
		t.Errorf("Embedding.GoogleCloudLocation = %q, want europe-west3", cfg.Embedding.GoogleCloudLocation)
	}
}

func TestLoad_EmbeddingGoogleCloudLocation_fallback(t *testing.T) {
	t.Setenv("API_KEY", "test-api-key")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "europe-west1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Embedding.GoogleCloudLocation != "europe-west1" {
		t.Errorf("Embedding.GoogleCloudLocation = %q, want europe-west1", cfg.Embedding.GoogleCloudLocation)
	}
}

func TestLoad_EmbeddingGoogleCloudProject_precedence(t *testing.T) {
	t.Setenv("API_KEY", "test-api-key")
	t.Setenv("EMBEDDING_GOOGLE_CLOUD_PROJECT", "explicit-project")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "fallback-project")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Embedding.GoogleCloudProject != "explicit-project" {
		t.Errorf("Embedding.GoogleCloudProject = %q, want explicit-project", cfg.Embedding.GoogleCloudProject)
	}
}

func TestLoad_EmbeddingGoogleCloudLocation_precedence(t *testing.T) {
	t.Setenv("API_KEY", "test-api-key")
	t.Setenv("EMBEDDING_GOOGLE_CLOUD_LOCATION", "explicit-location")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "fallback-location")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Embedding.GoogleCloudLocation != "explicit-location" {
		t.Errorf("Embedding.GoogleCloudLocation = %q, want explicit-location (EMBEDDING_* overrides GOOGLE_*)",
			cfg.Embedding.GoogleCloudLocation)
	}
}

func TestLoad_PublicBaseURLValidation(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "rejects relative url", value: "/hub"},
		{name: "rejects unsupported scheme", value: "ftp://hub.example.com"},
		{name: "rejects query", value: "https://hub.example.com?x=1"},
		{name: "rejects fragment", value: "https://hub.example.com#frag"},
		{name: "rejects user info", value: "https://user:pass@hub.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PUBLIC_BASE_URL", tt.value)

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() error = nil, want error")
			}

			if !errors.Is(err, ErrInvalidPublicBaseURL) {
				t.Fatalf("Load() error = %v, want %v", err, ErrInvalidPublicBaseURL)
			}
		})
	}
}

func TestDatabasePoolConfig(t *testing.T) {
	cfg := &DatabaseConfig{
		MaxConns:          25,
		MinConns:          2,
		MaxConnLifetime:   DurationSec(30 * time.Second),
		MaxConnIdleTime:   DurationSec(15 * time.Second),
		HealthCheckPeriod: DurationSec(10 * time.Second),
		ConnectTimeout:    DurationSec(5 * time.Second),
	}

	got := cfg.PoolConfig()

	if got.MaxConns != cfg.MaxConns {
		t.Errorf("PoolConfig().MaxConns = %d, want %d", got.MaxConns, cfg.MaxConns)
	}

	if got.MinConns != cfg.MinConns {
		t.Errorf("PoolConfig().MinConns = %d, want %d", got.MinConns, cfg.MinConns)
	}

	if got.MaxConnLifetime != cfg.MaxConnLifetime.Duration() {
		t.Errorf("PoolConfig().MaxConnLifetime = %v, want %v", got.MaxConnLifetime, cfg.MaxConnLifetime.Duration())
	}

	if got.MaxConnIdleTime != cfg.MaxConnIdleTime.Duration() {
		t.Errorf("PoolConfig().MaxConnIdleTime = %v, want %v", got.MaxConnIdleTime, cfg.MaxConnIdleTime.Duration())
	}

	if got.HealthCheckPeriod != cfg.HealthCheckPeriod.Duration() {
		t.Errorf("PoolConfig().HealthCheckPeriod = %v, want %v", got.HealthCheckPeriod, cfg.HealthCheckPeriod.Duration())
	}

	if got.ConnectTimeout != cfg.ConnectTimeout.Duration() {
		t.Errorf("PoolConfig().ConnectTimeout = %v, want %v", got.ConnectTimeout, cfg.ConnectTimeout.Duration())
	}
}

func TestDurationSecSetValue(t *testing.T) {
	var duration DurationSec

	if err := duration.SetValue(" 45 "); err != nil {
		t.Fatalf("SetValue() error = %v, want nil", err)
	}

	if duration.Duration() != 45*time.Second {
		t.Fatalf("Duration() = %v, want 45s", duration.Duration())
	}

	if err := duration.SetValue(""); err != nil {
		t.Fatalf("SetValue(\"\") error = %v, want nil", err)
	}

	if duration.Duration() != 45*time.Second {
		t.Fatalf("Duration() after empty value = %v, want 45s", duration.Duration())
	}

	if err := duration.SetValue("not-a-number"); err == nil {
		t.Fatal("SetValue() error = nil, want error")
	}
}

func TestApplyDefaults(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "fallback-project")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "europe-west1")

	cfg := &Config{
		Webhook: WebhookConfig{
			EnqueueMaxRetries: -1,
		},
	}

	applyDefaults(cfg)

	if cfg.Server.Port != "8080" {
		t.Errorf("Server.Port = %q, want 8080", cfg.Server.Port)
	}

	if cfg.Server.LogLevel != "info" {
		t.Errorf("Server.LogLevel = %q, want info", cfg.Server.LogLevel)
	}

	if cfg.Server.ShutdownTimeout.Duration() != 30*time.Second {
		t.Errorf("Server.ShutdownTimeout = %v, want 30s", cfg.Server.ShutdownTimeout.Duration())
	}

	if cfg.Database.URL != DefaultDatabaseURL {
		t.Errorf("Database.URL = %q, want %q", cfg.Database.URL, DefaultDatabaseURL)
	}

	if cfg.Database.MaxConns != 25 {
		t.Errorf("Database.MaxConns = %d, want 25", cfg.Database.MaxConns)
	}

	if _, ok := cfg.Webhook.URLBlacklist["localhost"]; !ok {
		t.Error("Webhook.URLBlacklist missing localhost default")
	}

	if cfg.Webhook.HTTPTimeout.Duration() != 15*time.Second {
		t.Errorf("Webhook.HTTPTimeout = %v, want 15s", cfg.Webhook.HTTPTimeout.Duration())
	}

	if cfg.Webhook.EnqueueMaxRetries != 3 {
		t.Errorf("Webhook.EnqueueMaxRetries = %d, want 3", cfg.Webhook.EnqueueMaxRetries)
	}

	if cfg.Webhook.EnqueueInitialBackoffMs != 100 {
		t.Errorf("Webhook.EnqueueInitialBackoffMs = %d, want 100", cfg.Webhook.EnqueueInitialBackoffMs)
	}

	if cfg.Webhook.EnqueueMaxBackoffMs != 2000 {
		t.Errorf("Webhook.EnqueueMaxBackoffMs = %d, want 2000", cfg.Webhook.EnqueueMaxBackoffMs)
	}

	if cfg.Embedding.GoogleCloudProject != "fallback-project" {
		t.Errorf("Embedding.GoogleCloudProject = %q, want fallback-project", cfg.Embedding.GoogleCloudProject)
	}

	if cfg.Embedding.GoogleCloudLocation != "europe-west1" {
		t.Errorf("Embedding.GoogleCloudLocation = %q, want europe-west1", cfg.Embedding.GoogleCloudLocation)
	}

	if cfg.Embedding.MaxConcurrent != 5 {
		t.Errorf("Embedding.MaxConcurrent = %d, want 5", cfg.Embedding.MaxConcurrent)
	}

	if cfg.Embedding.MaxAttempts != 3 {
		t.Errorf("Embedding.MaxAttempts = %d, want 3", cfg.Embedding.MaxAttempts)
	}
}

func TestIsNotExist(t *testing.T) {
	missingFileErr := &os.PathError{
		Op:   "open",
		Path: ".env",
		Err:  os.ErrNotExist,
	}

	if !isNotExist(missingFileErr) {
		t.Fatal("isNotExist(os.ErrNotExist path error) = false, want true")
	}

	permissionErr := &os.PathError{
		Op:   "open",
		Path: ".env",
		Err:  os.ErrPermission,
	}

	if isNotExist(permissionErr) {
		t.Fatal("isNotExist(permission path error) = true, want false")
	}

	if isNotExist(errors.New("boom")) {
		t.Fatal("isNotExist(non-path error) = true, want false")
	}
}

func TestValidateRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr error
	}{
		{
			name: "webhook delivery max concurrent",
			mutate: func(cfg *Config) {
				cfg.Webhook.DeliveryMaxConcurrent = 0
			},
			wantErr: ErrWebhookDeliveryMaxConcurrent,
		},
		{
			name: "webhook delivery max attempts",
			mutate: func(cfg *Config) {
				cfg.Webhook.DeliveryMaxAttempts = 0
			},
			wantErr: ErrWebhookDeliveryMaxAttempts,
		},
		{
			name: "webhook fanout",
			mutate: func(cfg *Config) {
				cfg.Webhook.MaxFanOutPerEvent = 0
			},
			wantErr: ErrWebhookMaxFanOutPerEvent,
		},
		{
			name: "publisher buffer",
			mutate: func(cfg *Config) {
				cfg.MessagePublisher.BufferSize = 0
			},
			wantErr: ErrMessagePublisherQueueMaxSize,
		},
		{
			name: "publisher timeout",
			mutate: func(cfg *Config) {
				cfg.MessagePublisher.PerEventTimeoutSec = 0
			},
			wantErr: ErrMessagePublisherPerEventTimeout,
		},
		{
			name: "shutdown timeout",
			mutate: func(cfg *Config) {
				cfg.Server.ShutdownTimeout = 0
			},
			wantErr: ErrShutdownTimeoutSeconds,
		},
		{
			name: "webhook max count",
			mutate: func(cfg *Config) {
				cfg.Webhook.MaxCount = 0
			},
			wantErr: ErrWebhookMaxCount,
		},
		{
			name: "database min exceeds max",
			mutate: func(cfg *Config) {
				cfg.Database.MinConns = 3
			},
			wantErr: ErrDatabaseMinConnsExceedsMax,
		},
		{
			name: "invalid public base url",
			mutate: func(cfg *Config) {
				cfg.Server.PublicBaseURL = "https://hub.example.com?cache-poison=true"
			},
			wantErr: ErrInvalidPublicBaseURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validValidationConfig()
			tt.mutate(cfg)

			err := validate(cfg)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateNormalizesPublicBaseURL(t *testing.T) {
	cfg := validValidationConfig()
	cfg.Server.PublicBaseURL = " https://hub.example.com/root/ "

	if err := validate(cfg); err != nil {
		t.Fatalf("validate() error = %v, want nil", err)
	}

	if cfg.Server.PublicBaseURL != "https://hub.example.com/root" {
		t.Fatalf("Server.PublicBaseURL = %q, want https://hub.example.com/root", cfg.Server.PublicBaseURL)
	}
}

func TestNormalizePublicBaseURL(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "trims spaces and root path",
			value: " https://hub.example.com/ ",
			want:  "https://hub.example.com",
		},
		{
			name:  "preserves configured path prefix",
			value: "http://hub.example.com/prefix///",
			want:  "http://hub.example.com/prefix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizePublicBaseURL(tt.value)
			if err != nil {
				t.Fatalf("normalizePublicBaseURL() error = %v, want nil", err)
			}

			if got != tt.want {
				t.Fatalf("normalizePublicBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizePublicBaseURLRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{" ", "http://[::1", "hub.example.com"} {
		t.Run(value, func(t *testing.T) {
			_, err := normalizePublicBaseURL(value)
			if !errors.Is(err, ErrInvalidPublicBaseURL) {
				t.Fatalf("normalizePublicBaseURL() error = %v, want %v", err, ErrInvalidPublicBaseURL)
			}
		})
	}
}

func validValidationConfig() *Config {
	return &Config{
		Server: ServerConfig{
			ShutdownTimeout: DurationSec(time.Second),
			PublicBaseURL:   "https://hub.example.com",
		},
		Database: DatabaseConfig{
			MaxConns: 2,
			MinConns: 1,
		},
		Webhook: WebhookConfig{
			DeliveryMaxConcurrent: 1,
			DeliveryMaxAttempts:   1,
			MaxFanOutPerEvent:     1,
			MaxCount:              1,
		},
		MessagePublisher: MessagePublisherConfig{
			BufferSize:         1,
			PerEventTimeoutSec: 1,
		},
	}
}
