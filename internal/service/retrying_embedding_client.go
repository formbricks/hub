package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrEmbeddingClientInitialization marks a failed provider client setup for readiness classification.
var ErrEmbeddingClientInitialization = errors.New("embedding client initialization failed")

// RetryingEmbeddingClient retries a failed provider client setup on the next preflight. Once
// initialized, jobs and preflight share the same client instance and request route.
type RetryingEmbeddingClient struct {
	mu     sync.Mutex
	create func(context.Context) (EmbeddingClient, error)
	client EmbeddingClient
}

// NewRetryingEmbeddingClient creates a client whose provider setup can recover without a worker restart.
func NewRetryingEmbeddingClient(cfg EmbeddingClientConfig) *RetryingEmbeddingClient {
	return &RetryingEmbeddingClient{create: func(ctx context.Context) (EmbeddingClient, error) {
		return NewEmbeddingClient(ctx, cfg)
	}}
}

// CreateEmbedding delegates to the initialized provider client.
func (c *RetryingEmbeddingClient) CreateEmbedding(ctx context.Context, input string) ([]float32, error) {
	client, err := c.get(ctx)
	if err != nil {
		return nil, err
	}

	vector, err := client.CreateEmbedding(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("embedding request: %w", err)
	}

	return vector, nil
}

// CreateEmbeddingForQuery delegates to the initialized provider client.
func (c *RetryingEmbeddingClient) CreateEmbeddingForQuery(ctx context.Context, input string) ([]float32, error) {
	client, err := c.get(ctx)
	if err != nil {
		return nil, err
	}

	vector, err := client.CreateEmbeddingForQuery(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("embedding query request: %w", err)
	}

	return vector, nil
}

// RetryingBatchEmbeddingClient advertises batching only for providers whose direct client
// supports it, so a deferred setup does not enable batching for another provider by accident.
type RetryingBatchEmbeddingClient struct{ *RetryingEmbeddingClient }

// NewRetryingBatchEmbeddingClient retries setup while preserving the native batch capability.
func NewRetryingBatchEmbeddingClient(cfg EmbeddingClientConfig) *RetryingBatchEmbeddingClient {
	return &RetryingBatchEmbeddingClient{RetryingEmbeddingClient: NewRetryingEmbeddingClient(cfg)}
}

// CreateEmbeddings preserves the configured batch route after deferred provider setup recovers.
func (c *RetryingBatchEmbeddingClient) CreateEmbeddings(ctx context.Context, inputs []string) ([][]float32, error) {
	client, err := c.get(ctx)
	if err != nil {
		return nil, err
	}

	if batch, ok := client.(BatchEmbeddingClient); ok {
		vectors, err := batch.CreateEmbeddings(ctx, inputs)
		if err != nil {
			return nil, fmt.Errorf("embedding batch request: %w", err)
		}

		return vectors, nil
	}

	vectors := make([][]float32, 0, len(inputs))
	for _, input := range inputs {
		vector, err := client.CreateEmbedding(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("embedding request: %w", err)
		}

		vectors = append(vectors, vector)
	}

	return vectors, nil
}

func (c *RetryingEmbeddingClient) get(ctx context.Context) (EmbeddingClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		return c.client, nil
	}

	client, err := c.create(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrEmbeddingClientInitialization, err)
	}

	c.client = client

	return client, nil
}
