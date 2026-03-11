package config

import (
	"testing"
)

func TestGetEnv(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		defaultValue string
		envValue     string
		shouldSet    bool
		want         string
	}{
		{
			name:         "returns environment variable when set",
			key:          "TEST_VAR",
			defaultValue: "default",
			envValue:     "custom",
			shouldSet:    true,
			want:         "custom",
		},
		{
			name:         "returns default when environment variable not set",
			key:          "TEST_VAR_MISSING",
			defaultValue: "default",
			envValue:     "",
			shouldSet:    false,
			want:         "default",
		},
		{
			name:         "returns default when environment variable is empty string",
			key:          "TEST_VAR_EMPTY",
			defaultValue: "default",
			envValue:     "",
			shouldSet:    true,
			want:         "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.shouldSet {
				t.Setenv(tt.key, tt.envValue)
			}

			got := getEnv(tt.key, tt.defaultValue)
			if got != tt.want {
				t.Errorf("getEnv() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetEnvAsInt(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		defaultValue int
		envValue     string
		shouldSet    bool
		want         int
	}{
		{
			name:         "returns environment variable as int when set with valid integer",
			key:          "TEST_INT_VAR",
			defaultValue: 100,
			envValue:     "200",
			shouldSet:    true,
			want:         200,
		},
		{
			name:         "returns default when environment variable not set",
			key:          "TEST_INT_VAR_MISSING",
			defaultValue: 100,
			envValue:     "",
			shouldSet:    false,
			want:         100,
		},
		{
			name:         "returns default when environment variable is empty string",
			key:          "TEST_INT_VAR_EMPTY",
			defaultValue: 100,
			envValue:     "",
			shouldSet:    true,
			want:         100,
		},
		{
			name:         "returns default when environment variable is not a valid integer",
			key:          "TEST_INT_VAR_INVALID",
			defaultValue: 100,
			envValue:     "not_a_number",
			shouldSet:    true,
			want:         100,
		},
		{
			name:         "handles negative integers",
			key:          "TEST_INT_VAR_NEGATIVE",
			defaultValue: 100,
			envValue:     "-50",
			shouldSet:    true,
			want:         -50,
		},
		{
			name:         "handles zero",
			key:          "TEST_INT_VAR_ZERO",
			defaultValue: 100,
			envValue:     "0",
			shouldSet:    true,
			want:         0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.shouldSet {
				t.Setenv(tt.key, tt.envValue)
			}

			got := getEnvAsInt(tt.key, tt.defaultValue)
			if got != tt.want {
				t.Errorf("getEnvAsInt() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetEnvAsBool(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		defaultValue bool
		envValue     string
		shouldSet    bool
		want         bool
	}{
		{"unset returns default true", "TEST_BOOL", true, "", false, true},
		{"unset returns default false", "TEST_BOOL", false, "", false, false},
		{"empty string returns default", "TEST_BOOL", true, "", true, true},
		{"true (lowercase) returns true", "TEST_BOOL", false, "true", true, true},
		{"TRUE returns true", "TEST_BOOL", false, "TRUE", true, true},
		{"1 returns true", "TEST_BOOL", false, "1", true, true},
		{"yes returns true", "TEST_BOOL", false, "yes", true, true},
		{"YES returns true", "TEST_BOOL", false, "YES", true, true},
		{"false (lowercase) returns false", "TEST_BOOL", true, "false", true, false},
		{"FALSE returns false", "TEST_BOOL", true, "FALSE", true, false},
		{"0 returns false", "TEST_BOOL", true, "0", true, false},
		{"no returns false", "TEST_BOOL", true, "no", true, false},
		{"NO returns false", "TEST_BOOL", true, "NO", true, false},
		{"unknown value returns default true", "TEST_BOOL", true, "other", true, true},
		{"unknown value returns default false", "TEST_BOOL", false, "other", true, false},
		{"whitespace trimmed", "TEST_BOOL", false, "  true  ", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.shouldSet {
				t.Setenv(tt.key, tt.envValue)
			}

			got := GetEnvAsBool(tt.key, tt.defaultValue)
			if got != tt.want {
				t.Errorf("GetEnvAsBool(%q, %v) = %v, want %v", tt.key, tt.defaultValue, got, tt.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name            string
		databaseURL     string
		port            string
		setDatabaseURL  bool
		setPort         bool
		wantDatabaseURL string
		wantPort        string
	}{
		{
			name:            "returns default values when no environment variables set",
			databaseURL:     "",
			port:            "",
			setDatabaseURL:  false,
			setPort:         false,
			wantDatabaseURL: "postgres://postgres:postgres@localhost:5432/test_db?sslmode=disable",
			wantPort:        "8080",
		},
		{
			name:            "returns custom DATABASE_URL when set",
			databaseURL:     "postgres://custom:password@localhost:5432/custom_db",
			port:            "",
			setDatabaseURL:  true,
			setPort:         false,
			wantDatabaseURL: "postgres://custom:password@localhost:5432/custom_db",
			wantPort:        "8080",
		},
		{
			name:            "returns custom PORT when set",
			databaseURL:     "",
			port:            "3000",
			setDatabaseURL:  false,
			setPort:         true,
			wantDatabaseURL: "postgres://postgres:postgres@localhost:5432/test_db?sslmode=disable",
			wantPort:        "3000",
		},
		{
			name:            "returns custom values for both when set",
			databaseURL:     "postgres://custom:password@localhost:5432/custom_db",
			port:            "3000",
			setDatabaseURL:  true,
			setPort:         true,
			wantDatabaseURL: "postgres://custom:password@localhost:5432/custom_db",
			wantPort:        "3000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// API_KEY is required for Load() to succeed
			t.Setenv("API_KEY", "test-api-key")

			// Explicitly set or clear so parent env (e.g. .env) does not affect expectations
			if tt.setDatabaseURL {
				t.Setenv("DATABASE_URL", tt.databaseURL)
			} else {
				t.Setenv("DATABASE_URL", "")
			}

			if tt.setPort {
				t.Setenv("PORT", tt.port)
			} else {
				t.Setenv("PORT", "")
			}

			cfg, err := Load()
			if err != nil {
				t.Errorf("Load() error = %v, want nil", err)

				return
			}

			if cfg.DatabaseURL != tt.wantDatabaseURL {
				t.Errorf("Load() DatabaseURL = %v, want %v", cfg.DatabaseURL, tt.wantDatabaseURL)
			}

			if cfg.Port != tt.wantPort {
				t.Errorf("Load() Port = %v, want %v", cfg.Port, tt.wantPort)
			}
		})
	}
}

// TestLoadAlwaysReturnsNilError cannot use t.Parallel() because it uses t.Setenv (Go restriction).
func TestLoadAlwaysReturnsNilError(t *testing.T) {
	t.Setenv("API_KEY", "test-api-key")

	cfg, err := Load()
	if err != nil {
		t.Errorf("Load() error = %v, want nil", err)
	}

	if cfg == nil {
		t.Error("Load() config = nil, want non-nil config")
	}
}

func TestLoad_WebhookDeliveryMaxAttempts(t *testing.T) {
	t.Setenv("API_KEY", "test-api-key")

	t.Run("default is 3 when unset", func(t *testing.T) {
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if cfg.WebhookDeliveryMaxAttempts != 3 {
			t.Errorf("WebhookDeliveryMaxAttempts = %d, want 3", cfg.WebhookDeliveryMaxAttempts)
		}
	})

	t.Run("override via WEBHOOK_DELIVERY_MAX_ATTEMPTS", func(t *testing.T) {
		t.Setenv("WEBHOOK_DELIVERY_MAX_ATTEMPTS", "5")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if cfg.WebhookDeliveryMaxAttempts != 5 {
			t.Errorf("WebhookDeliveryMaxAttempts = %d, want 5", cfg.WebhookDeliveryMaxAttempts)
		}
	})

	t.Run("validation error when <= 0", func(t *testing.T) {
		t.Setenv("WEBHOOK_DELIVERY_MAX_ATTEMPTS", "0")

		_, err := Load()
		if err == nil {
			t.Error("Load() error = nil, want error for WEBHOOK_DELIVERY_MAX_ATTEMPTS <= 0")
		}
	})

	t.Run("non-numeric falls back to default", func(t *testing.T) {
		t.Setenv("WEBHOOK_DELIVERY_MAX_ATTEMPTS", "x")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if cfg.WebhookDeliveryMaxAttempts != 3 {
			t.Errorf("WebhookDeliveryMaxAttempts = %d, want default 3", cfg.WebhookDeliveryMaxAttempts)
		}
	})
}

func TestLoad_EmbeddingGoogleCloudProject(t *testing.T) {
	t.Setenv("API_KEY", "test-api-key")
	t.Setenv("EMBEDDING_GOOGLE_CLOUD_PROJECT", "my-vertex-project")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.EmbeddingGoogleCloudProject != "my-vertex-project" {
		t.Errorf("EmbeddingGoogleCloudProject = %q, want my-vertex-project", cfg.EmbeddingGoogleCloudProject)
	}
}

func TestLoad_EmbeddingGoogleCloudProject_fallback(t *testing.T) {
	t.Setenv("API_KEY", "test-api-key")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "fallback-project")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.EmbeddingGoogleCloudProject != "fallback-project" {
		t.Errorf("EmbeddingGoogleCloudProject = %q, want fallback-project", cfg.EmbeddingGoogleCloudProject)
	}
}

func TestLoad_EmbeddingGoogleCloudLocation(t *testing.T) {
	t.Setenv("API_KEY", "test-api-key")
	t.Setenv("EMBEDDING_GOOGLE_CLOUD_LOCATION", "europe-west3")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.EmbeddingGoogleCloudLocation != "europe-west3" {
		t.Errorf("EmbeddingGoogleCloudLocation = %q, want europe-west3", cfg.EmbeddingGoogleCloudLocation)
	}
}

func TestLoad_EmbeddingGoogleCloudLocation_fallback(t *testing.T) {
	t.Setenv("API_KEY", "test-api-key")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "europe-west1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.EmbeddingGoogleCloudLocation != "europe-west1" {
		t.Errorf("EmbeddingGoogleCloudLocation = %q, want europe-west1", cfg.EmbeddingGoogleCloudLocation)
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

	if cfg.EmbeddingGoogleCloudProject != "explicit-project" {
		t.Errorf("EmbeddingGoogleCloudProject = %q, want explicit-project (EMBEDDING_* must override GOOGLE_*)", cfg.EmbeddingGoogleCloudProject)
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

	if cfg.EmbeddingGoogleCloudLocation != "explicit-location" {
		t.Errorf("EmbeddingGoogleCloudLocation = %q, want explicit-location (EMBEDDING_* overrides GOOGLE_*)",
			cfg.EmbeddingGoogleCloudLocation)
	}
}
