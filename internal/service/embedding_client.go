package service

import "context"

// EmbeddingClient generates embedding vectors for text.
// Implemented by provider-specific clients (e.g. OpenAI, Google Gemini).
type EmbeddingClient interface {
	CreateEmbedding(ctx context.Context, input string) ([]float32, error)
}
