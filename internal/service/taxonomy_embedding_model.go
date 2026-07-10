package service

import "strings"

// TaxonomyEmbeddingModel returns the embeddings.model key used for taxonomy-specific embeddings.
func TaxonomyEmbeddingModel(embeddingModel, override string) string {
	if trimmed := strings.TrimSpace(override); trimmed != "" {
		return trimmed
	}

	trimmed := strings.TrimSpace(embeddingModel)
	if trimmed == "" {
		return ""
	}

	return "taxonomy:" + trimmed + ":translated-v1"
}
