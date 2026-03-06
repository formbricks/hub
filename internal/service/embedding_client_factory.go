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
	EmbeddingProviderGoogleVertex = "google-vertex"
)

var (
	// ErrEmbeddingConfigInvalid is returned when the embedding provider is unsupported.
	ErrEmbeddingConfigInvalid = errors.New("embedding config invalid")
	// ErrEmbeddingProviderAPIKey is returned when an API-key-based provider is configured without a key.
	ErrEmbeddingProviderAPIKey = errors.New("EMBEDDING_PROVIDER_API_KEY is required for this provider")
	// ErrEmbeddingVertexConfig is returned when google-vertex is configured without project or location.
	ErrEmbeddingVertexConfig = errors.New("google-vertex requires EMBEDDING_GOOGLE_CLOUD_PROJECT and EMBEDDING_GOOGLE_CLOUD_LOCATION")
)

// EmbeddingClientConfig holds configuration for creating an embedding client.
type EmbeddingClientConfig struct {
	Provider            string
	APIKey              string //nolint:gosec // API key for openai/google providers; not logged or serialized
	Model               string
	Normalize           bool
	GoogleCloudProject  string
	GoogleCloudLocation string
}

// NewEmbeddingClient creates an EmbeddingClient for the given config.
// Validates provider-specific requirements before creating the client.
func NewEmbeddingClient(ctx context.Context, cfg EmbeddingClientConfig) (EmbeddingClient, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))

	switch provider {
	case EmbeddingProviderOpenAI:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("%w: %s", ErrEmbeddingProviderAPIKey, provider)
		}

		return openai.NewClient(cfg.APIKey,
			openai.WithModel(cfg.Model),
			openai.WithNormalize(cfg.Normalize),
		), nil
	case EmbeddingProviderGoogle:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("%w: %s", ErrEmbeddingProviderAPIKey, provider)
		}

		client, err := googleai.NewClient(ctx, cfg.APIKey,
			googleai.WithModel(cfg.Model),
			googleai.WithNormalize(cfg.Normalize),
		)
		if err != nil {
			return nil, fmt.Errorf("create google embedding client: %w", err)
		}

		return client, nil
	case EmbeddingProviderGoogleVertex:
		if cfg.GoogleCloudProject == "" || cfg.GoogleCloudLocation == "" {
			return nil, ErrEmbeddingVertexConfig
		}

		client, err := googleai.NewVertexClient(ctx, cfg.GoogleCloudProject, cfg.GoogleCloudLocation,
			googleai.WithModel(cfg.Model),
			googleai.WithNormalize(cfg.Normalize),
		)
		if err != nil {
			return nil, fmt.Errorf("create google-vertex embedding client: %w", err)
		}

		return client, nil
	default:
		return nil, fmt.Errorf("%w: unsupported provider %q", ErrEmbeddingConfigInvalid, provider)
	}
}

// ProviderRequiresAPIKey returns true for providers that require EMBEDDING_PROVIDER_API_KEY.
func ProviderRequiresAPIKey(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case EmbeddingProviderOpenAI, EmbeddingProviderGoogle:
		return true
	default:
		return false
	}
}

// ProviderRequiresVertexConfig returns true for providers that require project and location.
func ProviderRequiresVertexConfig(provider string) bool {
	return strings.ToLower(strings.TrimSpace(provider)) == EmbeddingProviderGoogleVertex
}

// SupportedEmbeddingProviders returns the set of supported provider names.
func SupportedEmbeddingProviders() map[string]struct{} {
	return map[string]struct{}{
		EmbeddingProviderOpenAI:       {},
		EmbeddingProviderGoogle:       {},
		EmbeddingProviderGoogleVertex: {},
	}
}
