package service

import "context"

// EmbeddingClient generates embedding vectors for text.
// CreateEmbedding is for embedding documents (e.g. feedback records) for storage.
// CreateEmbeddingForQuery is for embedding search queries; some providers (e.g. Google) use a different task type for asymmetric retrieval.
type EmbeddingClient interface {
	CreateEmbedding(ctx context.Context, input string) ([]float32, error)
	CreateEmbeddingForQuery(ctx context.Context, input string) ([]float32, error)
}

// BatchEmbeddingClient is an optional provider capability for embedding multiple documents in one
// request. Workers use it when batching is enabled; providers that do not implement it continue to
// receive one request per document.
type BatchEmbeddingClient interface {
	CreateEmbeddings(ctx context.Context, inputs []string) ([][]float32, error)
}
