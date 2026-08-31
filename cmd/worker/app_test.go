package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/formbricks/hub/internal/config"
)

func TestEmbeddingReconcileConfigured(t *testing.T) {
	base := func() *config.Config {
		return &config.Config{
			Embedding: config.EmbeddingConfig{
				ReconcileEnabled: true,
				Provider:         "openai",
				Model:            "embedding-model",
			},
			Taxonomy: config.TaxonomyConfig{ServiceURL: "https://taxonomy.example.com"},
		}
	}

	t.Run("translation is not required because taxonomy input falls back to source text", func(t *testing.T) {
		assert.True(t, embeddingReconcileConfigured(base()))
	})

	t.Run("explicit opt in is required", func(t *testing.T) {
		cfg := base()
		cfg.Embedding.ReconcileEnabled = false
		assert.False(t, embeddingReconcileConfigured(cfg))
	})

	t.Run("embedding model is required", func(t *testing.T) {
		cfg := base()
		cfg.Embedding.Model = ""
		assert.False(t, embeddingReconcileConfigured(cfg))
	})

	t.Run("taxonomy integration is required", func(t *testing.T) {
		cfg := base()
		cfg.Taxonomy.ServiceURL = ""
		assert.False(t, embeddingReconcileConfigured(cfg))
	})
}
