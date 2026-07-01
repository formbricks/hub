package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/formbricks/hub/internal/googleai"
	"github.com/formbricks/hub/internal/openai"
)

// Embedding provider names for NewEmbeddingClient.
const (
	EmbeddingProviderOpenAI       = ProviderOpenAI
	EmbeddingProviderGoogle       = ProviderGoogle
	EmbeddingProviderGoogleGemini = ProviderGoogleGemini
)

var (
	// ErrEmbeddingConfigInvalid is returned when the embedding provider is unsupported.
	ErrEmbeddingConfigInvalid = errors.New("embedding config invalid")
	// ErrEmbeddingProviderAPIKey is returned when an API-key-based provider is configured without a key.
	ErrEmbeddingProviderAPIKey = errors.New("EMBEDDING_PROVIDER_API_KEY is required for this provider")
	// ErrEmbeddingBaseURLUnsupported is returned when a custom base URL is configured for a non-openai provider.
	ErrEmbeddingBaseURLUnsupported = errors.New("EMBEDDING_BASE_URL is only supported for openai")
	// ErrEmbeddingGoogleGeminiConfig is returned when google-gemini is configured without project or location.
	ErrEmbeddingGoogleGeminiConfig = errors.New(
		"google-gemini requires EMBEDDING_GOOGLE_CLOUD_PROJECT and EMBEDDING_GOOGLE_CLOUD_LOCATION")
)

// EmbeddingClientConfig holds configuration for creating an embedding client.
type EmbeddingClientConfig struct {
	Provider            string
	ProviderAPIKey      string // API key for openai/google providers; not logged or serialized
	Model               string
	BaseURL             string
	Normalize           bool
	GoogleCloudProject  string
	GoogleCloudLocation string
}

func (c EmbeddingClientConfig) clientProvider() string            { return c.Provider }
func (c EmbeddingClientConfig) clientAPIKey() string              { return c.ProviderAPIKey }
func (c EmbeddingClientConfig) clientBaseURL() string             { return c.BaseURL }
func (c EmbeddingClientConfig) clientGoogleCloudProject() string  { return c.GoogleCloudProject }
func (c EmbeddingClientConfig) clientGoogleCloudLocation() string { return c.GoogleCloudLocation }

// embeddingClientRegistry is the single source of truth for embedding provider capabilities and
// client creation, backed by the shared generic registry. Embedding accepts the legacy
// google-vertex alias.
var embeddingClientRegistry = clientRegistry[EmbeddingClientConfig, EmbeddingClient]{
	allowVertexAlias: true,
	errConfigInvalid: ErrEmbeddingConfigInvalid,
	errAPIKey:        ErrEmbeddingProviderAPIKey,
	errBaseURL:       ErrEmbeddingBaseURLUnsupported,
	errGoogleGemini:  ErrEmbeddingGoogleGeminiConfig,
	entries: map[string]providerFactory[EmbeddingClientConfig, EmbeddingClient]{
		EmbeddingProviderOpenAI:       {requiresAPIKey: true, build: openAIEmbeddingFactory},
		EmbeddingProviderGoogle:       {requiresAPIKey: true, build: googleEmbeddingFactory},
		EmbeddingProviderGoogleGemini: {requiresGoogleGeminiConfig: true, build: googleGeminiEmbeddingFactory},
	},
}

// embeddingDocPrefixes is a sparse override table mapping an embedding provider to a document
// prefix prepended to text before embedding, for the providers whose models require one (e.g.
// instruction-tuned embedders). A provider absent here — all of them today — uses no prefix, so
// this table never mirrors the provider registry and cannot drift out of sync with it.
var embeddingDocPrefixes = map[string]string{}

func openAIEmbeddingFactory(_ context.Context, cfg EmbeddingClientConfig) (EmbeddingClient, error) {
	return openai.NewClient(cfg.ProviderAPIKey,
		openai.WithModel(cfg.Model),
		openai.WithBaseURL(cfg.BaseURL),
		openai.WithNormalize(cfg.Normalize),
	), nil
}

func googleEmbeddingFactory(ctx context.Context, cfg EmbeddingClientConfig) (EmbeddingClient, error) {
	client, err := googleai.NewClient(ctx, cfg.ProviderAPIKey,
		googleai.WithModel(cfg.Model),
		googleai.WithNormalize(cfg.Normalize),
	)
	if err != nil {
		return nil, fmt.Errorf("create google embedding client: %w", err)
	}

	return client, nil
}

func googleGeminiEmbeddingFactory(ctx context.Context, cfg EmbeddingClientConfig) (EmbeddingClient, error) {
	client, err := googleai.NewGoogleGeminiClient(ctx, cfg.GoogleCloudProject, cfg.GoogleCloudLocation,
		googleai.WithModel(cfg.Model),
		googleai.WithNormalize(cfg.Normalize),
	)
	if err != nil {
		return nil, fmt.Errorf("create google-gemini embedding client: %w", err)
	}

	return client, nil
}

// NormalizeEmbeddingProvider returns the canonical provider name (lowercase, trimmed),
// mapping the legacy google-vertex alias to google-gemini.
func NormalizeEmbeddingProvider(provider string) string {
	return embeddingClientRegistry.normalize(provider)
}

// ValidateEmbeddingConfig checks provider support and provider-specific requirements (API key, Google Cloud project/location).
// Use before creating a client or at startup to fail fast with a clear error.
func ValidateEmbeddingConfig(cfg EmbeddingClientConfig) error {
	return embeddingClientRegistry.validate(cfg)
}

// NewEmbeddingClient creates an EmbeddingClient for the given config.
// Validates provider-specific requirements via the registry, then calls the registry factory.
func NewEmbeddingClient(ctx context.Context, cfg EmbeddingClientConfig) (EmbeddingClient, error) {
	return embeddingClientRegistry.newClient(ctx, cfg)
}

// ProviderRequiresAPIKey returns true for providers that require EMBEDDING_PROVIDER_API_KEY (from registry).
func ProviderRequiresAPIKey(provider string) bool {
	return embeddingClientRegistry.requiresAPIKey(provider)
}

// ProviderRequiresGoogleGeminiConfig returns true for providers that require Google Cloud project and location.
func ProviderRequiresGoogleGeminiConfig(provider string) bool {
	return embeddingClientRegistry.requiresGoogleGeminiConfig(provider)
}

// SupportedEmbeddingProviders returns the set of supported provider names (from registry).
func SupportedEmbeddingProviders() map[string]struct{} {
	return embeddingClientRegistry.supportedProviders()
}

// EmbeddingPrefixForProvider returns the document prefix for the given embedding provider.
// Returns "" for unknown providers (and for all providers today, as none set a prefix).
func EmbeddingPrefixForProvider(provider string) string {
	return embeddingDocPrefixes[NormalizeEmbeddingProvider(provider)]
}
