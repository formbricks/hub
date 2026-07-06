package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/formbricks/hub/internal/googleai"
	"github.com/formbricks/hub/internal/openai"
)

var (
	// ErrEmotionsConfigInvalid is returned when the emotions provider is unsupported.
	ErrEmotionsConfigInvalid = errors.New("emotions config invalid")
	// ErrEmotionsProviderAPIKey is returned when an API-key-based provider is configured without a key.
	ErrEmotionsProviderAPIKey = errors.New("EMOTIONS_PROVIDER_API_KEY is required for this provider")
	// ErrEmotionsBaseURLUnsupported is returned when a custom base URL is configured for a non-openai provider.
	ErrEmotionsBaseURLUnsupported = errors.New("EMOTIONS_BASE_URL is only supported for openai")
	// ErrEmotionsGoogleGeminiConfig is returned when google-gemini is configured without project or location.
	ErrEmotionsGoogleGeminiConfig = errors.New(
		"google-gemini requires EMOTIONS_GOOGLE_CLOUD_PROJECT and EMOTIONS_GOOGLE_CLOUD_LOCATION")
)

// EmotionsClientConfig aliases the shared classify client config (see EnrichmentClientConfig).
type EmotionsClientConfig = EnrichmentClientConfig

// emotionsClientRegistry is the single source of truth for emotions provider capabilities and
// client creation, backed by the shared generic registry. Like sentiment it does not accept the
// legacy google-vertex alias (a newer surface).
var emotionsClientRegistry = clientRegistry[EmotionsClientConfig, EmotionsClient]{
	allowVertexAlias: false,
	errConfigInvalid: ErrEmotionsConfigInvalid,
	errAPIKey:        ErrEmotionsProviderAPIKey,
	errBaseURL:       ErrEmotionsBaseURLUnsupported,
	errGoogleGemini:  ErrEmotionsGoogleGeminiConfig,
	entries: map[string]providerFactory[EmotionsClientConfig, EmotionsClient]{
		ProviderOpenAI:       {requiresAPIKey: true, build: openAIEmotionsFactory},
		ProviderGoogle:       {requiresAPIKey: true, build: googleEmotionsFactory},
		ProviderGoogleGemini: {requiresGoogleGeminiConfig: true, build: googleGeminiEmotionsFactory},
	},
}

func openAIEmotionsFactory(_ context.Context, cfg EmotionsClientConfig) (EmotionsClient, error) {
	raw := openai.NewClient(cfg.ProviderAPIKey,
		openai.WithModel(cfg.Model),
		openai.WithBaseURL(cfg.BaseURL),
	)

	return promptEmotionsClient{raw: raw}, nil
}

func googleEmotionsFactory(ctx context.Context, cfg EmotionsClientConfig) (EmotionsClient, error) {
	raw, err := googleai.NewClient(ctx, cfg.ProviderAPIKey, googleai.WithModel(cfg.Model))
	if err != nil {
		return nil, fmt.Errorf("create google emotions client: %w", err)
	}

	return promptEmotionsClient{raw: raw}, nil
}

func googleGeminiEmotionsFactory(ctx context.Context, cfg EmotionsClientConfig) (EmotionsClient, error) {
	raw, err := googleai.NewGoogleGeminiClient(ctx, cfg.GoogleCloudProject, cfg.GoogleCloudLocation,
		googleai.WithModel(cfg.Model))
	if err != nil {
		return nil, fmt.Errorf("create google-gemini emotions client: %w", err)
	}

	return promptEmotionsClient{raw: raw}, nil
}

// NewEmotionsClient creates an EmotionsClient for the given config. It validates provider-specific
// requirements via the registry, then calls the registry factory.
func NewEmotionsClient(ctx context.Context, cfg EmotionsClientConfig) (EmotionsClient, error) {
	return emotionsClientRegistry.newClient(ctx, cfg)
}
