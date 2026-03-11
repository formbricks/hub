package app

import (
	"log/slog"
	"strings"

	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/service"
)

// EmbeddingProviderAndModel returns (provider, model) when embeddings are enabled:
// both EMBEDDING_PROVIDER and EMBEDDING_MODEL must be set and the provider must be supported.
// Otherwise returns ("", "") so no embedding provider or jobs run. No default for model;
// embeddings are disabled if either is unset. Provider name is normalized via the
// embedding registry (consistent with backfill-embeddings and EmbeddingPrefixForProvider).
func EmbeddingProviderAndModel(cfg *config.Config) (provider, model string) {
	if cfg == nil {
		return "", ""
	}

	providerCanonical := service.NormalizeEmbeddingProvider(cfg.Embedding.Provider)

	modelCanonical := strings.TrimSpace(cfg.Embedding.Model)
	if providerCanonical == "" || modelCanonical == "" {
		return "", ""
	}

	if _, ok := service.SupportedEmbeddingProviders()[providerCanonical]; !ok {
		slog.Info("embeddings disabled: unsupported EMBEDDING_PROVIDER",
			"provider", cfg.Embedding.Provider, "model", cfg.Embedding.Model)

		return "", ""
	}

	return providerCanonical, modelCanonical
}
