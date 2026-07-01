package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/formbricks/hub/internal/googleai"
	"github.com/formbricks/hub/internal/openai"
)

// Sentiment provider names for NewSentimentClient (same identifiers as the embedding and
// translation providers; sentiment reuses the OpenAI and Google SDK wrappers).
const (
	SentimentProviderOpenAI       = ProviderOpenAI
	SentimentProviderGoogle       = ProviderGoogle
	SentimentProviderGoogleGemini = ProviderGoogleGemini
)

var (
	// ErrSentimentConfigInvalid is returned when the sentiment provider is unsupported.
	ErrSentimentConfigInvalid = errors.New("sentiment config invalid")
	// ErrSentimentProviderAPIKey is returned when an API-key-based provider is configured without a key.
	ErrSentimentProviderAPIKey = errors.New("SENTIMENT_PROVIDER_API_KEY is required for this provider")
	// ErrSentimentBaseURLUnsupported is returned when a custom base URL is configured for a non-openai provider.
	ErrSentimentBaseURLUnsupported = errors.New("SENTIMENT_BASE_URL is only supported for openai")
	// ErrSentimentGoogleGeminiConfig is returned when google-gemini is configured without project or location.
	ErrSentimentGoogleGeminiConfig = errors.New(
		"google-gemini requires SENTIMENT_GOOGLE_CLOUD_PROJECT and SENTIMENT_GOOGLE_CLOUD_LOCATION")
)

// SentimentClientConfig holds configuration for creating a sentiment client.
type SentimentClientConfig struct {
	Provider            string
	ProviderAPIKey      string // API key for openai/google providers; not logged or serialized
	Model               string
	BaseURL             string
	GoogleCloudProject  string
	GoogleCloudLocation string
}

func (c SentimentClientConfig) clientProvider() string            { return c.Provider }
func (c SentimentClientConfig) clientAPIKey() string              { return c.ProviderAPIKey }
func (c SentimentClientConfig) clientBaseURL() string             { return c.BaseURL }
func (c SentimentClientConfig) clientGoogleCloudProject() string  { return c.GoogleCloudProject }
func (c SentimentClientConfig) clientGoogleCloudLocation() string { return c.GoogleCloudLocation }

// sentimentClientRegistry is the single source of truth for sentiment provider capabilities and
// client creation, backed by the shared generic registry. Sentiment does not accept the legacy
// google-vertex alias (it is a newer surface).
var sentimentClientRegistry = clientRegistry[SentimentClientConfig, SentimentClient]{
	allowVertexAlias: false,
	errConfigInvalid: ErrSentimentConfigInvalid,
	errAPIKey:        ErrSentimentProviderAPIKey,
	errBaseURL:       ErrSentimentBaseURLUnsupported,
	errGoogleGemini:  ErrSentimentGoogleGeminiConfig,
	entries: map[string]providerFactory[SentimentClientConfig, SentimentClient]{
		SentimentProviderOpenAI:       {requiresAPIKey: true, build: openAISentimentFactory},
		SentimentProviderGoogle:       {requiresAPIKey: true, build: googleSentimentFactory},
		SentimentProviderGoogleGemini: {requiresGoogleGeminiConfig: true, build: googleGeminiSentimentFactory},
	},
}

func openAISentimentFactory(_ context.Context, cfg SentimentClientConfig) (SentimentClient, error) {
	raw := openai.NewClient(cfg.ProviderAPIKey,
		openai.WithModel(cfg.Model),
		openai.WithBaseURL(cfg.BaseURL),
	)

	return promptSentimentClient{raw: raw}, nil
}

func googleSentimentFactory(ctx context.Context, cfg SentimentClientConfig) (SentimentClient, error) {
	raw, err := googleai.NewClient(ctx, cfg.ProviderAPIKey, googleai.WithModel(cfg.Model))
	if err != nil {
		return nil, fmt.Errorf("create google sentiment client: %w", err)
	}

	return promptSentimentClient{raw: raw}, nil
}

func googleGeminiSentimentFactory(ctx context.Context, cfg SentimentClientConfig) (SentimentClient, error) {
	raw, err := googleai.NewGoogleGeminiClient(ctx, cfg.GoogleCloudProject, cfg.GoogleCloudLocation,
		googleai.WithModel(cfg.Model))
	if err != nil {
		return nil, fmt.Errorf("create google-gemini sentiment client: %w", err)
	}

	return promptSentimentClient{raw: raw}, nil
}

// NormalizeSentimentProvider returns the canonical provider name (lowercase, trimmed).
func NormalizeSentimentProvider(provider string) string {
	return sentimentClientRegistry.normalize(provider)
}

// ValidateSentimentConfig checks provider support and provider-specific requirements.
// Use before creating a client or at startup to fail fast with a clear error.
func ValidateSentimentConfig(cfg SentimentClientConfig) error {
	return sentimentClientRegistry.validate(cfg)
}

// NewSentimentClient creates a SentimentClient for the given config. It validates
// provider-specific requirements via the registry, then calls the registry factory.
func NewSentimentClient(ctx context.Context, cfg SentimentClientConfig) (SentimentClient, error) {
	return sentimentClientRegistry.newClient(ctx, cfg)
}
