package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateSentimentConfig(t *testing.T) {
	tests := map[string]struct {
		cfg     SentimentClientConfig
		wantErr error
	}{
		"openai with api key is valid": {
			cfg: SentimentClientConfig{Provider: "openai", ProviderAPIKey: "sk-x", Model: "gpt-4o-mini"},
		},
		"provider casing is normalized": {
			cfg: SentimentClientConfig{Provider: "  OpenAI ", ProviderAPIKey: "sk-x", Model: "gpt-4o-mini"},
		},
		"google-gemini with project and location is valid": {
			cfg: SentimentClientConfig{
				Provider: "google-gemini", Model: "gemini-2.5-flash",
				GoogleCloudProject: "proj", GoogleCloudLocation: "europe-west3",
			},
		},
		"unsupported provider": {
			cfg:     SentimentClientConfig{Provider: "anthropic", Model: "m"},
			wantErr: ErrSentimentConfigInvalid,
		},
		"openai without api key": {
			cfg:     SentimentClientConfig{Provider: "openai", Model: "gpt-4o-mini"},
			wantErr: ErrSentimentProviderAPIKey,
		},
		"base url on non-openai provider": {
			cfg: SentimentClientConfig{
				Provider: "google", ProviderAPIKey: "k", Model: "m", BaseURL: "https://x/v1",
			},
			wantErr: ErrSentimentBaseURLUnsupported,
		},
		"google-gemini without project": {
			cfg:     SentimentClientConfig{Provider: "google-gemini", Model: "m", GoogleCloudLocation: "europe-west3"},
			wantErr: ErrSentimentGoogleGeminiConfig,
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			err := sentimentClientRegistry.validate(testCase.cfg)
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestNewSentimentClient_OpenAI(t *testing.T) {
	client, err := NewSentimentClient(context.Background(), SentimentClientConfig{
		Provider: "openai", ProviderAPIKey: "sk-x", Model: "gpt-4o-mini",
	})
	require.NoError(t, err)
	assert.NotNil(t, client)
}

func TestNewSentimentClient_UnsupportedProvider(t *testing.T) {
	_, err := NewSentimentClient(context.Background(), SentimentClientConfig{Provider: "nope", Model: "m"})
	require.ErrorIs(t, err, ErrSentimentConfigInvalid)
}
