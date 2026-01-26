package embeddings

import (
	"context"
	"fmt"

	"github.com/sashabaranov/go-openai"
)

// OpenAIClient implements the Client interface using OpenAI's embedding API.
type OpenAIClient struct {
	client *openai.Client
	model  openai.EmbeddingModel
}

// Ensure OpenAIClient implements Client interface
var _ Client = (*OpenAIClient)(nil)

// NewOpenAIClient creates a new OpenAI embedding client.
// Uses text-embedding-3-small by default (1536 dimensions).
// Panics if apiKey is empty.
func NewOpenAIClient(apiKey string) *OpenAIClient {
	if apiKey == "" {
		panic("embeddings: OpenAI API key cannot be empty")
	}
	return &OpenAIClient{
		client: openai.NewClient(apiKey),
		model:  openai.SmallEmbedding3, // text-embedding-3-small, 1536 dims
	}
}

// NewOpenAIClientWithModel creates a new OpenAI embedding client with a custom model.
func NewOpenAIClientWithModel(apiKey string, model openai.EmbeddingModel) *OpenAIClient {
	return &OpenAIClient{
		client: openai.NewClient(apiKey),
		model:  model,
	}
}

// GetEmbedding generates an embedding vector for the given text.
func (c *OpenAIClient) GetEmbedding(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, fmt.Errorf("text cannot be empty")
	}

	resp, err := c.client.CreateEmbeddings(ctx, openai.EmbeddingRequest{
		Input: []string{text},
		Model: c.model,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding returned from API")
	}

	return resp.Data[0].Embedding, nil
}

// GetEmbeddings generates embedding vectors for multiple texts in a batch.
// Returns an error if any text in the input is empty.
func (c *OpenAIClient) GetEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("texts cannot be empty")
	}

	// Validate all texts are non-empty
	for i, t := range texts {
		if t == "" {
			return nil, fmt.Errorf("text at index %d cannot be empty", i)
		}
	}

	resp, err := c.client.CreateEmbeddings(ctx, openai.EmbeddingRequest{
		Input: texts,
		Model: c.model,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create embeddings: %w", err)
	}

	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("unexpected number of embeddings returned: got %d, expected %d", len(resp.Data), len(texts))
	}

	embeddings := make([][]float32, len(resp.Data))
	for i, data := range resp.Data {
		embeddings[i] = data.Embedding
	}

	return embeddings, nil
}
