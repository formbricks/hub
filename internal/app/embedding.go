package app

import (
	"log/slog"
	"strings"

	"github.com/formbricks/hub/internal/config"
)

const (
	embeddingProviderOpenAI = "openai"
	embeddingProviderGoogle = "google"
)

var supportedEmbeddingProviders = map[string]struct{}{
	embeddingProviderOpenAI: {},
	embeddingProviderGoogle: {},
}

// EmbeddingProviderAndModel returns (provider, model) when embeddings are enabled:
// both EMBEDDING_PROVIDER and EMBEDDING_MODEL must be set and the provider must be supported.
// Otherwise returns ("", "") so no embedding provider or jobs run. No default for model;
// embeddings are disabled if either is unset. Provider name is normalized to lowercase
// so that "OpenAI", "openai", and "OPENAI" behave the same (consistent with backfill-embeddings
// and EmbeddingPrefixForProvider).
func EmbeddingProviderAndModel(cfg *config.Config) (provider, model string) {
	if cfg == nil || cfg.EmbeddingProvider == "" || cfg.EmbeddingModel == "" {
		return "", ""
	}

	providerCanonical := strings.ToLower(strings.TrimSpace(cfg.EmbeddingProvider))
	if _, ok := supportedEmbeddingProviders[providerCanonical]; !ok {
		slog.Info("embeddings disabled: unsupported EMBEDDING_PROVIDER",
			"provider", cfg.EmbeddingProvider, "model", cfg.EmbeddingModel)

		return "", ""
	}

	return providerCanonical, cfg.EmbeddingModel
}
