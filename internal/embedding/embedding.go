// Package embedding provides vector embedding generation using OpenAI's embedding models.
// Embeddings are used for semantic search and are stored in PostgreSQL using pgvector.
// All operations are designed to be called asynchronously by background workers.
package embedding

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/pgvector/pgvector-go"
)

const (
	// maxTextLength is the maximum text length before truncation (8000 chars ≈ 2000 tokens)
	maxTextLength = 8000
)

// Service handles AI-powered text embedding generation
type Service struct {
	client  openai.Client
	model   string
	timeout time.Duration
	logger  *slog.Logger
}

// NewService creates a new embedding service
func NewService(apiKey string, model string, timeoutSeconds int, logger *slog.Logger) *Service {
	return &Service{
		client:  openai.NewClient(option.WithAPIKey(apiKey)),
		model:   model,
		timeout: time.Duration(timeoutSeconds) * time.Second,
		logger:  logger,
	}
}

// GenerateEmbedding creates an embedding vector for the given text
// Returns a pgvector.Vector suitable for storage in PostgreSQL
func (s *Service) GenerateEmbedding(ctx context.Context, text string) (pgvector.Vector, error) {
	// Apply timeout
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	// Truncate very long text to avoid token limits
	if len(text) > maxTextLength {
		text = text[:maxTextLength] + "..."
	}

	// Call OpenAI embeddings API
	resp, err := s.client.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{
			OfArrayOfStrings: []string{text},
		},
		Model: s.model,
	})

	if err != nil {
		return pgvector.Vector{}, fmt.Errorf("openai embeddings api error: %w", err)
	}

	if len(resp.Data) == 0 {
		return pgvector.Vector{}, fmt.Errorf("no embeddings returned from openai")
	}

	// Convert float64 slice to float32 for pgvector
	embeddingData := resp.Data[0].Embedding
	float32Slice := make([]float32, len(embeddingData))
	for i, v := range embeddingData {
		float32Slice[i] = float32(v)
	}

	return pgvector.NewVector(float32Slice), nil
}

// BuildEmbeddingText combines field label and value text for contextual embedding
// If fieldLabel is empty, returns just the valueText
func BuildEmbeddingText(fieldLabel, valueText string) string {
	if fieldLabel == "" {
		return valueText
	}
	return fmt.Sprintf("Question: %s\nResponse: %s", fieldLabel, valueText)
}

// Model returns the model name being used
func (s *Service) Model() string {
	return s.model
}
