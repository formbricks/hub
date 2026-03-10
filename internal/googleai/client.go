// Package googleai provides a thin wrapper around the Google Gen AI SDK for embeddings (Gemini API).
package googleai

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"google.golang.org/genai"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/pkg/embeddings"
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
	// ErrVertexProjectRequired is returned when NewVertexClient is called with empty project.
	ErrVertexProjectRequired = errors.New("googleai vertex client: project is required")
	// ErrVertexLocationRequired is returned when NewVertexClient is called with empty location.
	ErrVertexLocationRequired = errors.New("googleai vertex client: location is required")
)

// Client calls the Gemini embeddings API via the Google Gen AI SDK.
type Client struct {
	client     *genai.Client
	model      string
	dimensions int
	normalize  bool
}

// ClientOption configures the Client.
type ClientOption func(*Client)

// WithDimensions sets the requested embedding dimension (must match DB column).
func WithDimensions(dim int) ClientOption {
	return func(c *Client) {
		c.dimensions = dim
	}
}

// WithModel sets the embedding model name. Empty uses default.
func WithModel(model string) ClientOption {
	return func(c *Client) {
		c.model = model
	}
}

// WithNormalize enables L2 normalization of the embedding vector before returning (e.g. before storing or caching).
func WithNormalize(normalize bool) ClientOption {
	return func(c *Client) {
		c.normalize = normalize
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
		dimensions: models.EmbeddingVectorDimensions,
	}
	for _, opt := range opts {
		opt(client)
	}

	return client, nil
}

// NewVertexClient creates a Vertex AI embeddings client using Application Default Credentials (ADC).
// When running outside GCP (e.g. EKS, Railway), set GOOGLE_APPLICATION_CREDENTIALS to the path of a service account key JSON file.
// project is the GCP project ID; location is the region (e.g. europe-west3, global).
func NewVertexClient(ctx context.Context, project, location string, opts ...ClientOption) (*Client, error) {
	if strings.TrimSpace(project) == "" {
		return nil, ErrVertexProjectRequired
	}

	if strings.TrimSpace(location) == "" {
		return nil, ErrVertexLocationRequired
	}

	genaiClient, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:  project,
		Location: location,
		Backend:  genai.BackendVertexAI,
	})
	if err != nil {
		return nil, fmt.Errorf("googleai vertex client: %w", err)
	}

	client := &Client{
		client:     genaiClient,
		dimensions: models.EmbeddingVectorDimensions,
	}
	for _, opt := range opts {
		opt(client)
	}

	return client, nil
}

// CreateEmbedding returns the embedding vector for the given text using RETRIEVAL_DOCUMENT task type
// (for storing documents/feedback records). The returned slice length equals the configured dimensions.
func (c *Client) CreateEmbedding(ctx context.Context, input string) ([]float32, error) {
	return c.embedWithTaskType(ctx, input, "RETRIEVAL_DOCUMENT")
}

// CreateEmbeddingForQuery returns an embedding for the given search query using RETRIEVAL_QUERY task type,
// which optimizes the vector for asymmetric retrieval against documents embedded with RETRIEVAL_DOCUMENT.
func (c *Client) CreateEmbeddingForQuery(ctx context.Context, input string) ([]float32, error) {
	return c.embedWithTaskType(ctx, input, "RETRIEVAL_QUERY")
}

// embedWithTaskType calls the Gemini API with the given task type (e.g. RETRIEVAL_DOCUMENT, RETRIEVAL_QUERY).
func (c *Client) embedWithTaskType(ctx context.Context, input, taskType string) ([]float32, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, ErrEmptyInput
	}

	if c.dimensions <= 0 || c.dimensions > math.MaxInt32 {
		return nil, ErrInvalidDims
	}

	contents := []*genai.Content{genai.NewContentFromText(input, genai.RoleUser)}
	dimInt32 := int32(c.dimensions)

	resp, err := c.client.Models.EmbedContent(ctx, c.model, contents, &genai.EmbedContentConfig{
		TaskType:             taskType,
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

	if c.normalize {
		embeddings.NormalizeL2(out)
	}

	return out, nil
}
