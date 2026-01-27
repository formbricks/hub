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

			if cfg.DatabaseURL != tt.wantDatabaseURL {
				t.Errorf("Load() DatabaseURL = %v, want %v", cfg.DatabaseURL, tt.wantDatabaseURL)
			}

			if cfg.Port != tt.wantPort {
				t.Errorf("Load() Port = %v, want %v", cfg.Port, tt.wantPort)
			}
		})
	}
}

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

func TestLoadConnectorInstanceLimits(t *testing.T) {
	tests := []struct {
		name              string
		maxPolling        string
		maxWebhook        string
		maxOutput         string
		maxEnrichment     string
		wantMaxPolling    int
		wantMaxWebhook    int
		wantMaxOutput     int
		wantMaxEnrichment int
	}{
		{
			name:              "returns default values when not set",
			maxPolling:        "",
			maxWebhook:        "",
			maxOutput:         "",
			maxEnrichment:     "",
			wantMaxPolling:    10,
			wantMaxWebhook:    10,
			wantMaxOutput:     10,
			wantMaxEnrichment: 10,
		},
		{
			name:              "returns custom values when set",
			maxPolling:        "20",
			maxWebhook:        "15",
			maxOutput:         "5",
			maxEnrichment:     "25",
			wantMaxPolling:    20,
			wantMaxWebhook:    15,
			wantMaxOutput:     5,
			wantMaxEnrichment: 25,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("API_KEY", "test-api-key")
			if tt.maxPolling != "" {
				t.Setenv("MAX_POLLING_CONNECTOR_INSTANCES", tt.maxPolling)
			}
			if tt.maxWebhook != "" {
				t.Setenv("MAX_WEBHOOK_CONNECTOR_INSTANCES", tt.maxWebhook)
			}
			if tt.maxOutput != "" {
				t.Setenv("MAX_OUTPUT_CONNECTOR_INSTANCES", tt.maxOutput)
			}
			if tt.maxEnrichment != "" {
				t.Setenv("MAX_ENRICHMENT_CONNECTOR_INSTANCES", tt.maxEnrichment)
			}

			cfg, err := Load()
			if err != nil {
				t.Errorf("Load() error = %v, want nil", err)
				return
			}

			if cfg.MaxPollingConnectorInstances != tt.wantMaxPolling {
				t.Errorf("Load() MaxPollingConnectorInstances = %v, want %v", cfg.MaxPollingConnectorInstances, tt.wantMaxPolling)
			}
			if cfg.MaxWebhookConnectorInstances != tt.wantMaxWebhook {
				t.Errorf("Load() MaxWebhookConnectorInstances = %v, want %v", cfg.MaxWebhookConnectorInstances, tt.wantMaxWebhook)
			}
			if cfg.MaxOutputConnectorInstances != tt.wantMaxOutput {
				t.Errorf("Load() MaxOutputConnectorInstances = %v, want %v", cfg.MaxOutputConnectorInstances, tt.wantMaxOutput)
			}
			if cfg.MaxEnrichmentConnectorInstances != tt.wantMaxEnrichment {
				t.Errorf("Load() MaxEnrichmentConnectorInstances = %v, want %v", cfg.MaxEnrichmentConnectorInstances, tt.wantMaxEnrichment)
			}
		})
	}
}

func TestLoadRateLimiting(t *testing.T) {
	tests := []struct {
		name                string
		minDelay            string
		maxPollsPerHour     string
		wantMinDelay        string
		wantMaxPollsPerHour int
	}{
		{
			name:                "returns default values when not set",
			minDelay:            "",
			maxPollsPerHour:     "",
			wantMinDelay:        "1m0s",
			wantMaxPollsPerHour: 60,
		},
		{
			name:                "returns custom values when set",
			minDelay:            "5m",
			maxPollsPerHour:     "120",
			wantMinDelay:        "5m0s",
			wantMaxPollsPerHour: 120,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("API_KEY", "test-api-key")
			if tt.minDelay != "" {
				t.Setenv("POLLING_CONNECTOR_MIN_DELAY", tt.minDelay)
			}
			if tt.maxPollsPerHour != "" {
				t.Setenv("POLLING_CONNECTOR_MAX_POLLS_PER_HOUR", tt.maxPollsPerHour)
			}

			cfg, err := Load()
			if err != nil {
				t.Errorf("Load() error = %v, want nil", err)
				return
			}

			if cfg.PollingConnectorMinDelay.String() != tt.wantMinDelay {
				t.Errorf("Load() PollingConnectorMinDelay = %v, want %v", cfg.PollingConnectorMinDelay, tt.wantMinDelay)
			}
			if cfg.PollingConnectorMaxPollsPerHour != tt.wantMaxPollsPerHour {
				t.Errorf("Load() PollingConnectorMaxPollsPerHour = %v, want %v", cfg.PollingConnectorMaxPollsPerHour, tt.wantMaxPollsPerHour)
			}
		})
	}
}
