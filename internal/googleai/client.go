// Package googleai provides a thin wrapper around the Google Gen AI SDK for embeddings (Gemini API).
package googleai

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"google.golang.org/genai"
)

var (
	// ErrEmptyInput is returned when CreateEmbedding is called with empty input.
	ErrEmptyInput = errors.New("googleai: input text is empty")
	// ErrInvalidDims is returned when dimensions is not positive.
	ErrInvalidDims = errors.New("googleai: embedding dimensions must be positive")
	// ErrNoEmbeddingInResponse is returned when the API response contains no embedding data.
	ErrNoEmbeddingInResponse = errors.New("googleai: no embedding in response")
	// ErrDimensionMismatch is returned when the response embedding length does not match configured dimensions.
	ErrDimensionMismatch = errors.New("googleai: embedding dimension mismatch")
)

const (
	defaultDimension = 1536
	defaultModel     = "gemini-embedding-001"
)

// Client calls the Gemini embeddings API via the Google Gen AI SDK.
type Client struct {
	client     *genai.Client
	model      string
	dimensions int
}

// ClientOption configures the Client.
type ClientOption func(*Client)

// WithDimensions sets the requested embedding dimension (must match DB column).
func WithDimensions(dim int) ClientOption {
	return func(c *Client) {
		c.dimensions = dim
	}
}

// WithModel sets the embedding model name (e.g. gemini-embedding-001). Empty uses default.
func WithModel(model string) ClientOption {
	return func(c *Client) {
		c.model = model
	}
}

// NewClient creates a Gemini embeddings client.
func NewClient(ctx context.Context, apiKey string, opts ...ClientOption) (*Client, error) {
	genaiClient, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("googleai client: %w", err)
	}

	client := &Client{
		client:     genaiClient,
		model:      defaultModel,
		dimensions: defaultDimension,
	}
	for _, opt := range opts {
		opt(client)
	}

	return client, nil
}

// CreateEmbedding returns the embedding vector for the given text using the configured model.
// The returned slice length equals the configured dimensions when OutputDimensionality is supported.
func (c *Client) CreateEmbedding(ctx context.Context, input string) ([]float32, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, ErrEmptyInput
	}

	if c.dimensions <= 0 || c.dimensions > math.MaxInt32 {
		return nil, ErrInvalidDims
	}

	model := c.model
	if model == "" {
		model = defaultModel
	}

	contents := []*genai.Content{genai.NewContentFromText(input, genai.RoleUser)}
	//nolint:gosec // G115: c.dimensions is bounded above by math.MaxInt32
	dimInt32 := int32(c.dimensions)

	resp, err := c.client.Models.EmbedContent(ctx, model, contents, &genai.EmbedContentConfig{
		OutputDimensionality: &dimInt32,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini embedding: %w", err)
	}

	if len(resp.Embeddings) == 0 {
		return nil, ErrNoEmbeddingInResponse
	}

	emb := resp.Embeddings[0].Values
	if len(emb) != c.dimensions {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrDimensionMismatch, len(emb), c.dimensions)
	}

	out := make([]float32, len(emb))
	copy(out, emb)

	return out, nil
}
