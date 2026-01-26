package embeddings

import "context"

// Client defines the interface for generating text embeddings.
type Client interface {
	// GetEmbedding generates an embedding vector for the given text.
	// Returns a slice of float32 values representing the embedding.
	GetEmbedding(ctx context.Context, text string) ([]float32, error)

	// GetEmbeddings generates embedding vectors for multiple texts in a batch.
	// More efficient than calling GetEmbedding multiple times.
	GetEmbeddings(ctx context.Context, texts []string) ([][]float32, error)
}
