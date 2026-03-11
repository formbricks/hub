package app

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/formbricks/hub/internal/config"
)

func TestEmbeddingProviderAndModel(t *testing.T) {
	t.Run("nil config returns empty", func(t *testing.T) {
		provider, model := EmbeddingProviderAndModel(nil)
		assert.Empty(t, provider)
		assert.Empty(t, model)
	})

	t.Run("empty provider returns empty", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Embedding.Model = "text-embedding-3-small"
		provider, model := EmbeddingProviderAndModel(cfg)
		assert.Empty(t, provider)
		assert.Empty(t, model)
	})

	t.Run("empty model returns empty", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Embedding.Provider = "openai"
		provider, model := EmbeddingProviderAndModel(cfg)
		assert.Empty(t, provider)
		assert.Empty(t, model)
	})

	t.Run("openai normalized to lowercase", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Embedding.Provider = "OpenAI"
		cfg.Embedding.Model = "text-embedding-3-small"
		provider, model := EmbeddingProviderAndModel(cfg)
		assert.Equal(t, "openai", provider)
		assert.Equal(t, "text-embedding-3-small", model)
	})

	t.Run("google normalized to lowercase", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Embedding.Provider = "GOOGLE"
		cfg.Embedding.Model = "embedding-001"
		provider, model := EmbeddingProviderAndModel(cfg)
		assert.Equal(t, "google", provider)
		assert.Equal(t, "embedding-001", model)
	})

	t.Run("provider trimmed of whitespace", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Embedding.Provider = "  openai  "
		cfg.Embedding.Model = "model"
		provider, model := EmbeddingProviderAndModel(cfg)
		assert.Equal(t, "openai", provider)
		assert.Equal(t, "model", model)
	})

	t.Run("unsupported provider returns empty", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Embedding.Provider = "anthropic"
		cfg.Embedding.Model = "embed-v3"
		provider, model := EmbeddingProviderAndModel(cfg)
		assert.Empty(t, provider)
		assert.Empty(t, model)
	})
}
