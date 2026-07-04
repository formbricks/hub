package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateEmotionsConfig(t *testing.T) {
	tests := map[string]struct {
		cfg     EmotionsClientConfig
		wantErr error
	}{
		"openai with api key is valid": {
			cfg: EmotionsClientConfig{Provider: "openai", ProviderAPIKey: "sk-x", Model: "gpt-4o-mini"},
		},
		"provider casing is normalized": {
			cfg: EmotionsClientConfig{Provider: "  OpenAI ", ProviderAPIKey: "sk-x", Model: "gpt-4o-mini"},
		},
		"google-gemini with project and location is valid": {
			cfg: EmotionsClientConfig{
				Provider: "google-gemini", Model: "gemini-2.5-flash",
				GoogleCloudProject: "proj", GoogleCloudLocation: "europe-west3",
			},
		},
		"unsupported provider": {
			cfg:     EmotionsClientConfig{Provider: "anthropic", Model: "m"},
			wantErr: ErrEmotionsConfigInvalid,
		},
		"openai without api key": {
			cfg:     EmotionsClientConfig{Provider: "openai", Model: "gpt-4o-mini"},
			wantErr: ErrEmotionsProviderAPIKey,
		},
		"base url on non-openai provider": {
			cfg: EmotionsClientConfig{
				Provider: "google", ProviderAPIKey: "k", Model: "m", BaseURL: "https://x/v1",
			},
			wantErr: ErrEmotionsBaseURLUnsupported,
		},
		"google-gemini without project": {
			cfg:     EmotionsClientConfig{Provider: "google-gemini", Model: "m", GoogleCloudLocation: "europe-west3"},
			wantErr: ErrEmotionsGoogleGeminiConfig,
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			err := emotionsClientRegistry.validate(testCase.cfg)
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestNewEmotionsClient_OpenAI(t *testing.T) {
	client, err := NewEmotionsClient(context.Background(), EmotionsClientConfig{
		Provider: "openai", ProviderAPIKey: "sk-x", Model: "gpt-4o-mini",
	})
	require.NoError(t, err)
	assert.NotNil(t, client)
}

func TestNewEmotionsClient_UnsupportedProvider(t *testing.T) {
	_, err := NewEmotionsClient(context.Background(), EmotionsClientConfig{Provider: "nope", Model: "m"})
	require.ErrorIs(t, err, ErrEmotionsConfigInvalid)
}
