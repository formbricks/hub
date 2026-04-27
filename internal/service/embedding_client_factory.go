package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/formbricks/hub/internal/googleai"
	"github.com/formbricks/hub/internal/openai"
)

// Embedding provider names for NewEmbeddingClient.
const (
	EmbeddingProviderOpenAI       = "openai"
	EmbeddingProviderGoogle       = "google"
	EmbeddingProviderGoogleGemini = "google-gemini"

	embeddingProviderGoogleVertexLegacy = "google-vertex"
)

var (
	// ErrEmbeddingConfigInvalid is returned when the embedding provider is unsupported.
	ErrEmbeddingConfigInvalid = errors.New("embedding config invalid")
	// ErrEmbeddingProviderAPIKey is returned when an API-key-based provider is configured without a key.
	ErrEmbeddingProviderAPIKey = errors.New("EMBEDDING_PROVIDER_API_KEY is required for this provider")
	// ErrEmbeddingGoogleGeminiConfig is returned when google-gemini is configured without project or location.
	ErrEmbeddingGoogleGeminiConfig = errors.New(
		"google-gemini requires EMBEDDING_GOOGLE_CLOUD_PROJECT and EMBEDDING_GOOGLE_CLOUD_LOCATION")
)

// providerEntry describes capabilities and construction for one embedding provider (single source of truth).
type providerEntry struct {
	RequiresAPIKey             bool
	RequiresGoogleGeminiConfig bool
	DocPrefix                  string
	Factory                    func(context.Context, EmbeddingClientConfig) (EmbeddingClient, error)
}

// embeddingProviderRegistry is the single source of truth for provider capabilities and client creation.
var embeddingProviderRegistry = map[string]providerEntry{
	EmbeddingProviderOpenAI: {
		RequiresAPIKey: true,
		DocPrefix:      "",
		Factory:        openAIEmbeddingFactory,
	},
	EmbeddingProviderGoogle: {
		RequiresAPIKey: true,
		DocPrefix:      "",
		Factory:        googleEmbeddingFactory,
	},
	EmbeddingProviderGoogleGemini: {
		RequiresGoogleGeminiConfig: true,
		DocPrefix:                  "",
		Factory:                    googleGeminiEmbeddingFactory,
	},
}

func openAIEmbeddingFactory(_ context.Context, cfg EmbeddingClientConfig) (EmbeddingClient, error) {
	return openai.NewClient(cfg.ProviderAPIKey,
		openai.WithModel(cfg.Model),
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

// NormalizeEmbeddingProvider returns the canonical provider name (lowercase, trimmed).
func NormalizeEmbeddingProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == embeddingProviderGoogleVertexLegacy {
		return EmbeddingProviderGoogleGemini
	}

	return provider
}

// EmbeddingClientConfig holds configuration for creating an embedding client.
type EmbeddingClientConfig struct {
	Provider            string
	ProviderAPIKey      string // API key for openai/google providers; not logged or serialized
	Model               string
	Normalize           bool
	GoogleCloudProject  string
	GoogleCloudLocation string
}

// ValidateEmbeddingConfig checks provider support and provider-specific requirements (API key, Google Cloud project/location).
// Use before creating a client or at startup to fail fast with a clear error.
func ValidateEmbeddingConfig(cfg EmbeddingClientConfig) error {
	provider := NormalizeEmbeddingProvider(cfg.Provider)

	entry, ok := embeddingProviderRegistry[provider]
	if !ok {
		return fmt.Errorf("%w: unsupported provider %q", ErrEmbeddingConfigInvalid, provider)
	}

	if entry.RequiresAPIKey && cfg.ProviderAPIKey == "" {
		return fmt.Errorf("%w: %s", ErrEmbeddingProviderAPIKey, provider)
	}

	if entry.RequiresGoogleGeminiConfig && (cfg.GoogleCloudProject == "" || cfg.GoogleCloudLocation == "") {
		return ErrEmbeddingGoogleGeminiConfig
	}

	return nil
}

// NewEmbeddingClient creates an EmbeddingClient for the given config.
// Validates provider-specific requirements via the registry, then calls the registry factory.
func NewEmbeddingClient(ctx context.Context, cfg EmbeddingClientConfig) (EmbeddingClient, error) {
	provider := NormalizeEmbeddingProvider(cfg.Provider)

	entry, ok := embeddingProviderRegistry[provider]
	if !ok {
		return nil, fmt.Errorf("%w: unsupported provider %q", ErrEmbeddingConfigInvalid, provider)
	}

	if err := ValidateEmbeddingConfig(cfg); err != nil {
		return nil, err
	}

	return entry.Factory(ctx, cfg)
}

// ProviderRequiresAPIKey returns true for providers that require EMBEDDING_PROVIDER_API_KEY (from registry).
func ProviderRequiresAPIKey(provider string) bool {
	entry, ok := embeddingProviderRegistry[NormalizeEmbeddingProvider(provider)]

	return ok && entry.RequiresAPIKey
}

// ProviderRequiresGoogleGeminiConfig returns true for providers that require Google Cloud project and location.
func ProviderRequiresGoogleGeminiConfig(provider string) bool {
	entry, ok := embeddingProviderRegistry[NormalizeEmbeddingProvider(provider)]

	return ok && entry.RequiresGoogleGeminiConfig
}

// SupportedEmbeddingProviders returns the set of supported provider names (from registry).
func SupportedEmbeddingProviders() map[string]struct{} {
	out := make(map[string]struct{}, len(embeddingProviderRegistry))
	for k := range embeddingProviderRegistry {
		out[k] = struct{}{}
	}

	return out
}

// EmbeddingPrefixForProvider returns the document prefix for the given embedding provider (from registry).
// Returns "" for unknown providers.
func EmbeddingPrefixForProvider(provider string) string {
	entry, ok := embeddingProviderRegistry[NormalizeEmbeddingProvider(provider)]
	if !ok {
		return ""
	}

	return entry.DocPrefix
}
