package config

import (
	"testing"
)

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
			if tt.setDatabaseURL {
				t.Setenv("DATABASE_URL", tt.databaseURL)
			}

			if tt.setPort {
				t.Setenv("PORT", tt.port)
			}

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
		})
	}
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
	t.Setenv("EMBEDDING_GOOGLE_CLOUD_PROJECT", "my-vertex-project")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Embedding.GoogleCloudProject != "my-vertex-project" {
		t.Errorf("Embedding.GoogleCloudProject = %q, want my-vertex-project", cfg.Embedding.GoogleCloudProject)
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
