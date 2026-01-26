package embeddings

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
)

// MockClient implements the Client interface for testing purposes.
// It generates deterministic embeddings based on the input text hash.
type MockClient struct {
	dimensions int
}

// NewMockClient creates a new mock embedding client.
// Default dimensions is 1536 to match OpenAI's text-embedding-3-small.
func NewMockClient() *MockClient {
	return &MockClient{dimensions: 1536}
}

// NewMockClientWithDimensions creates a mock client with custom dimensions.
func NewMockClientWithDimensions(dimensions int) *MockClient {
	return &MockClient{dimensions: dimensions}
}

// GetEmbedding generates a deterministic embedding based on the text hash.
func (c *MockClient) GetEmbedding(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, fmt.Errorf("text cannot be empty")
	}
	return c.generateDeterministicEmbedding(text), nil
}

// GetEmbeddings generates embeddings for multiple texts.
// Returns an error if any text is empty.
func (c *MockClient) GetEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("texts cannot be empty")
	}

	for i, text := range texts {
		if text == "" {
			return nil, fmt.Errorf("text at index %d cannot be empty", i)
		}
	}

	embeddings := make([][]float32, len(texts))
	for i, text := range texts {
		embeddings[i] = c.generateDeterministicEmbedding(text)
	}
	return embeddings, nil
}

// generateDeterministicEmbedding creates a normalized embedding vector from text hash.
func (c *MockClient) generateDeterministicEmbedding(text string) []float32 {
	hash := sha256.Sum256([]byte(text))
	embedding := make([]float32, c.dimensions)

	// Generate embedding values from hash bytes
	for i := 0; i < c.dimensions; i++ {
		// Use hash bytes cyclically to generate float values
		byteIdx := i % len(hash)
		// Convert to float in range [-1, 1]
		embedding[i] = (float32(hash[byteIdx]) / 127.5) - 1.0
	}

	// Normalize the embedding
	return normalize(embedding)
}

// normalize normalizes a vector to unit length.
func normalize(v []float32) []float32 {
	var sum float64
	for _, val := range v {
		sum += float64(val * val)
	}
	magnitude := float32(math.Sqrt(sum))

	if magnitude == 0 {
		return v
	}

	normalized := make([]float32, len(v))
	for i, val := range v {
		normalized[i] = val / magnitude
	}
	return normalized
}

// Ensure MockClient implements Client interface
var _ Client = (*MockClient)(nil)
