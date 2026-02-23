// Package openai provides a thin wrapper around the official OpenAI Go SDK for embeddings.
package openai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
)

var (
	// ErrEmptyInput is returned when CreateEmbedding is called with empty input.
	ErrEmptyInput = errors.New("openai: input text is empty")
	// ErrInvalidDims is returned when dimensions is not positive.
	ErrInvalidDims = errors.New("openai: embedding dimensions must be positive")
	// ErrNoEmbeddingInResponse is returned when the API response contains no embedding data.
	ErrNoEmbeddingInResponse = errors.New("openai: no embedding in response")
	// ErrDimensionMismatch is returned when the response embedding length does not match configured dimensions.
	ErrDimensionMismatch = errors.New("openai: embedding dimension mismatch")
)

const defaultDimension = 1536

// Client calls the OpenAI embeddings API via the official SDK.
type Client struct {
	sdk        openaisdk.Client
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

// NewClient creates an OpenAI embeddings client using the official SDK.
func NewClient(apiKey string, opts ...ClientOption) *Client {
	client := &Client{
		sdk:        openaisdk.NewClient(option.WithAPIKey(apiKey)),
		dimensions: defaultDimension,
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// CreateEmbedding returns the embedding vector for the given text using text-embedding-3-small.
// The returned slice length equals the configured dimensions.
func (c *Client) CreateEmbedding(ctx context.Context, input string) ([]float32, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, ErrEmptyInput
	}

	if c.dimensions <= 0 {
		return nil, ErrInvalidDims
	}

	resp, err := c.sdk.Embeddings.New(ctx, openaisdk.EmbeddingNewParams{
		Input: openaisdk.EmbeddingNewParamsInputUnion{
			OfString: param.NewOpt(input),
		},
		Model:      openaisdk.EmbeddingModelTextEmbedding3Small,
		Dimensions: param.NewOpt(int64(c.dimensions)),
	})
	if err != nil {
		return nil, fmt.Errorf("openai embedding: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, ErrNoEmbeddingInResponse
	}

	emb := resp.Data[0].Embedding
	if len(emb) != c.dimensions {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrDimensionMismatch, len(emb), c.dimensions)
	}

	out := make([]float32, len(emb))
	for i := range emb {
		out[i] = float32(emb[i])
	}

	return out, nil
}
