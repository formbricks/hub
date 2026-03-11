package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEmbeddingClient(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name               string
		cfg                EmbeddingClientConfig
		wantErr            bool
		errIs              error
		invokesFactoryOnly bool // true: just ensure factory runs (accept client or error)
	}{
		{
			name: "openai with api key succeeds",
			cfg: EmbeddingClientConfig{
				Provider:  EmbeddingProviderOpenAI,
				APIKey:    "sk-test",
				Model:     "text-embedding-3-small",
				Normalize: false,
			},
			wantErr: false,
		},
		{
			name: "openai without api key returns error",
			cfg: EmbeddingClientConfig{
				Provider: EmbeddingProviderOpenAI,
				APIKey:   "",
				Model:    "text-embedding-3-small",
			},
			wantErr: true,
			errIs:   ErrEmbeddingProviderAPIKey,
		},
		{
			name: "google with api key succeeds",
			cfg: EmbeddingClientConfig{
				Provider:  EmbeddingProviderGoogle,
				APIKey:    "test-api-key",
				Model:     "text-embedding-004",
				Normalize: true,
			},
			wantErr: false,
		},
		{
			name: "google without api key returns error",
			cfg: EmbeddingClientConfig{
				Provider: EmbeddingProviderGoogle,
				APIKey:   "",
				Model:    "text-embedding-004",
			},
			wantErr: true,
			errIs:   ErrEmbeddingProviderAPIKey,
		},
		{
			name: "google-vertex without project returns error",
			cfg: EmbeddingClientConfig{
				Provider:            EmbeddingProviderGoogleVertex,
				Model:               "text-embedding-004",
				GoogleCloudProject:  "",
				GoogleCloudLocation: "europe-west3",
			},
			wantErr: true,
			errIs:   ErrEmbeddingVertexConfig,
		},
		{
			name: "google-vertex without location returns error",
			cfg: EmbeddingClientConfig{
				Provider:            EmbeddingProviderGoogleVertex,
				Model:               "text-embedding-004",
				GoogleCloudProject:  "my-project",
				GoogleCloudLocation: "",
			},
			wantErr: true,
			errIs:   ErrEmbeddingVertexConfig,
		},
		{
			name: "google-vertex with project and location invokes factory",
			cfg: EmbeddingClientConfig{
				Provider:            EmbeddingProviderGoogleVertex,
				Model:               "text-embedding-004",
				GoogleCloudProject:  "test-project",
				GoogleCloudLocation: "europe-west3",
			},
			invokesFactoryOnly: true,
		},
		{
			name: "google-vertex with mixed-case and whitespace provider name normalizes and validates",
			cfg: EmbeddingClientConfig{
				Provider:            " Google-Vertex ",
				Model:               "text-embedding-004",
				GoogleCloudProject:  "p",
				GoogleCloudLocation: "",
			},
			wantErr: true,
			errIs:   ErrEmbeddingVertexConfig,
		},
		{
			name: "unsupported provider returns error",
			cfg: EmbeddingClientConfig{
				Provider: "unsupported",
				APIKey:   "key",
				Model:    "model",
			},
			wantErr: true,
			errIs:   ErrEmbeddingConfigInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewEmbeddingClient(ctx, tt.cfg)
			if tt.invokesFactoryOnly {
				if err != nil {
					return
				}

				require.NotNil(t, client)

				return
			}

			if tt.wantErr {
				require.Error(t, err)

				if tt.errIs != nil {
					require.ErrorIs(t, err, tt.errIs,
						"expected error to wrap %v, got %v", tt.errIs, err)
				}

				return
			}

			require.NoError(t, err)
			require.NotNil(t, client)
		})
	}
}

func TestProviderRequiresAPIKey(t *testing.T) {
	tests := []struct {
		provider string
		want     bool
	}{
		{EmbeddingProviderOpenAI, true},
		{EmbeddingProviderGoogle, true},
		{EmbeddingProviderGoogleVertex, false},
		{"unknown", false},
		{"OPENAI", true},
		{"Google", true},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := ProviderRequiresAPIKey(tt.provider)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestProviderRequiresVertexConfig(t *testing.T) {
	tests := []struct {
		provider string
		want     bool
	}{
		{EmbeddingProviderGoogleVertex, true},
		{EmbeddingProviderOpenAI, false},
		{EmbeddingProviderGoogle, false},
		{"unknown", false},
		{"google-vertex", true},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := ProviderRequiresVertexConfig(tt.provider)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSupportedEmbeddingProviders(t *testing.T) {
	providers := SupportedEmbeddingProviders()

	assert.Contains(t, providers, EmbeddingProviderOpenAI)
	assert.Contains(t, providers, EmbeddingProviderGoogle)
	assert.Contains(t, providers, EmbeddingProviderGoogleVertex)
	assert.Len(t, providers, 3)
}

func TestValidateEmbeddingConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     EmbeddingClientConfig
		wantErr bool
		errIs   error
	}{
		{"openai with key valid", EmbeddingClientConfig{Provider: EmbeddingProviderOpenAI, APIKey: "k", Model: "m"}, false, nil},
		{
			"openai without key invalid",
			EmbeddingClientConfig{Provider: EmbeddingProviderOpenAI, APIKey: "", Model: "m"},
			true, ErrEmbeddingProviderAPIKey,
		},
		{"google with key valid", EmbeddingClientConfig{Provider: EmbeddingProviderGoogle, APIKey: "k", Model: "m"}, false, nil},
		{
			"google without key invalid",
			EmbeddingClientConfig{Provider: EmbeddingProviderGoogle, APIKey: "", Model: "m"},
			true, ErrEmbeddingProviderAPIKey,
		},
		{
			"vertex with project and location valid",
			EmbeddingClientConfig{
				Provider: EmbeddingProviderGoogleVertex, Model: "m",
				GoogleCloudProject: "p", GoogleCloudLocation: "l",
			},
			false, nil,
		},
		{
			"vertex without project invalid",
			EmbeddingClientConfig{Provider: EmbeddingProviderGoogleVertex, Model: "m", GoogleCloudLocation: "l"},
			true, ErrEmbeddingVertexConfig,
		},
		{
			"vertex without location invalid",
			EmbeddingClientConfig{Provider: EmbeddingProviderGoogleVertex, Model: "m", GoogleCloudProject: "p"},
			true, ErrEmbeddingVertexConfig,
		},
		{"unsupported provider invalid", EmbeddingClientConfig{Provider: "unknown", APIKey: "k", Model: "m"}, true, ErrEmbeddingConfigInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEmbeddingConfig(tt.cfg)
			if tt.wantErr {
				require.Error(t, err)

				if tt.errIs != nil {
					require.ErrorIs(t, err, tt.errIs)
				}

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestNormalizeEmbeddingProvider(t *testing.T) {
	assert.Equal(t, "openai", NormalizeEmbeddingProvider("OpenAI"))
	assert.Equal(t, "openai", NormalizeEmbeddingProvider("  openai  "))
	assert.Equal(t, "google-vertex", NormalizeEmbeddingProvider(" Google-Vertex "))
}

func TestEmbeddingPrefixForProvider(t *testing.T) {
	assert.Empty(t, EmbeddingPrefixForProvider(EmbeddingProviderOpenAI))
	assert.Empty(t, EmbeddingPrefixForProvider(EmbeddingProviderGoogle))
	assert.Empty(t, EmbeddingPrefixForProvider(EmbeddingProviderGoogleVertex))
	assert.Empty(t, EmbeddingPrefixForProvider("unknown"))
}
