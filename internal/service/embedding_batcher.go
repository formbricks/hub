package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrEmbeddingBatcherClosed is returned after shutdown stops accepting document calls.
	ErrEmbeddingBatcherClosed = errors.New("embedding batcher is closed")
	// ErrEmbeddingBatchResultCount is returned when a batch provider violates one-result-per-input.
	ErrEmbeddingBatchResultCount = errors.New("embedding batch result count mismatch")
)

const defaultEmbeddingBatchMaxWait = 25 * time.Millisecond

// EmbeddingBatchMetrics is the bounded observability seam used by the batching client.
type EmbeddingBatchMetrics interface {
	RecordEmbeddingBatch(ctx context.Context, inputs int64, duration time.Duration, status string)
	AddEmbeddingBatchInFlight(ctx context.Context, delta int64)
}

// EmbeddingBatchConfig controls document micro-batching. BatchSize <= 1 disables batching.
type EmbeddingBatchConfig struct {
	BatchSize   int
	MaxWait     time.Duration
	MaxInFlight int
}

type embeddingBatchRequest struct {
	ctx    context.Context //nolint:containedctx // each coalesced request retains its own cancellation boundary
	input  string
	result chan embeddingBatchResult
}

type embeddingBatchResult struct {
	vector []float32
	err    error
}

// BatchingEmbeddingClient coalesces concurrent document calls while leaving query calls
// synchronous. It is only constructed when the provider implements BatchEmbeddingClient.
type BatchingEmbeddingClient struct {
	client      EmbeddingClient
	batchClient BatchEmbeddingClient
	metrics     EmbeddingBatchMetrics
	batchSize   int
	maxWait     time.Duration
	semaphore   chan struct{}

	mu      sync.Mutex
	pending []*embeddingBatchRequest
	timer   *time.Timer
	closed  bool
	active  sync.WaitGroup

	forceStop     chan struct{}
	forceStopOnce sync.Once
}

// NewBatchingEmbeddingClient wraps client when batching is enabled and supported. The bool reports
// whether a wrapper was created; callers should keep using client when it is false.
func NewBatchingEmbeddingClient(
	client EmbeddingClient,
	cfg EmbeddingBatchConfig,
	metrics EmbeddingBatchMetrics,
) (*BatchingEmbeddingClient, bool) {
	batchClient, ok := client.(BatchEmbeddingClient)
	if !ok || cfg.BatchSize <= 1 {
		return nil, false
	}

	if cfg.MaxWait <= 0 {
		cfg.MaxWait = defaultEmbeddingBatchMaxWait
	}

	if cfg.MaxInFlight <= 0 {
		cfg.MaxInFlight = 1
	}

	return &BatchingEmbeddingClient{
		client:      client,
		batchClient: batchClient,
		metrics:     metrics,
		batchSize:   cfg.BatchSize,
		maxWait:     cfg.MaxWait,
		semaphore:   make(chan struct{}, cfg.MaxInFlight),
		forceStop:   make(chan struct{}),
	}, true
}

// CreateEmbedding queues a document for the next micro-batch.
func (b *BatchingEmbeddingClient) CreateEmbedding(ctx context.Context, input string) ([]float32, error) {
	request := &embeddingBatchRequest{
		ctx:    ctx,
		input:  input,
		result: make(chan embeddingBatchResult, 1),
	}

	var batch []*embeddingBatchRequest

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()

		return nil, ErrEmbeddingBatcherClosed
	}

	b.pending = append(b.pending, request)
	if len(b.pending) == 1 {
		b.timer = time.AfterFunc(b.maxWait, b.flush)
	}

	if len(b.pending) >= b.batchSize {
		batch = b.takePendingLocked()
		b.active.Add(1)
	}
	b.mu.Unlock()

	if len(batch) > 0 {
		go b.dispatch(batch) //nolint:contextcheck // dispatch derives cancellation from every request in the batch
	}

	select {
	case result := <-request.result:
		return result.vector, result.err
	case <-ctx.Done():
		return nil, fmt.Errorf("embedding batch request: %w", ctx.Err())
	}
}

// CreateEmbeddingForQuery deliberately bypasses batching; query latency and the foreground API
// path remain independent from background document throughput.
func (b *BatchingEmbeddingClient) CreateEmbeddingForQuery(ctx context.Context, input string) ([]float32, error) {
	vector, err := b.client.CreateEmbeddingForQuery(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("create query embedding: %w", err)
	}

	return vector, nil
}

// Shutdown stops accepting new work, flushes the final partial batch, and waits for provider
// requests to finish. When ctx expires, in-flight provider requests are cancelled.
func (b *BatchingEmbeddingClient) Shutdown(ctx context.Context) error {
	b.mu.Lock()
	b.closed = true

	batch := b.takePendingLocked()
	if len(batch) > 0 {
		b.active.Add(1)
	}
	b.mu.Unlock()

	if len(batch) > 0 {
		go b.dispatch(batch) //nolint:contextcheck // dispatch derives cancellation from every request in the batch
	}

	done := make(chan struct{})

	go func() {
		b.active.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		b.forceStopOnce.Do(func() { close(b.forceStop) })

		return fmt.Errorf("embedding batch shutdown: %w", ctx.Err())
	}
}

func (b *BatchingEmbeddingClient) flush() {
	b.mu.Lock()

	batch := b.takePendingLocked()
	if len(batch) > 0 {
		b.active.Add(1)
	}
	b.mu.Unlock()

	if len(batch) > 0 {
		go b.dispatch(batch)
	}
}

func (b *BatchingEmbeddingClient) takePendingLocked() []*embeddingBatchRequest {
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}

	batch := b.pending
	b.pending = nil

	return batch
}

func (b *BatchingEmbeddingClient) dispatch(batch []*embeddingBatchRequest) {
	defer b.active.Done()

	active := batch[:0]
	for _, request := range batch {
		if request.ctx.Err() == nil {
			active = append(active, request)
		}
	}

	if len(active) == 0 {
		return
	}

	select {
	case b.semaphore <- struct{}{}:
		defer func() { <-b.semaphore }()
	case <-b.forceStop:
		b.deliver(active, nil, ErrEmbeddingBatcherClosed)

		return
	}

	providerCtx, cancel, cleanupContext := b.providerContext(active)
	defer cancel()
	defer cleanupContext()

	inputs := make([]string, len(active))
	for i, request := range active {
		inputs[i] = request.input
	}

	if b.metrics != nil {
		b.metrics.AddEmbeddingBatchInFlight(providerCtx, 1)
		defer b.metrics.AddEmbeddingBatchInFlight(context.WithoutCancel(providerCtx), -1)
	}

	started := time.Now()

	vectors, err := b.batchClient.CreateEmbeddings(providerCtx, inputs)
	if err == nil && len(vectors) != len(active) {
		err = ErrEmbeddingBatchResultCount
	}

	status := "success"
	if err != nil {
		status = "error"
	}

	if b.metrics != nil {
		b.metrics.RecordEmbeddingBatch(context.WithoutCancel(providerCtx), int64(len(inputs)), time.Since(started), status)
	}

	b.deliver(active, vectors, err)
}

func (b *BatchingEmbeddingClient) providerContext(
	requests []*embeddingBatchRequest,
) (context.Context, context.CancelFunc, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	remaining := atomic.Int64{}
	remaining.Store(int64(len(requests)))

	stops := make([]func() bool, 0, len(requests)+1)
	for _, request := range requests {
		stops = append(stops, context.AfterFunc(request.ctx, func() {
			if remaining.Add(-1) == 0 {
				cancel()
			}
		}))
	}

	providerDone := make(chan struct{})

	go func() {
		select {
		case <-b.forceStop:
			cancel()
		case <-providerDone:
		}
	}()

	cleanup := func() {
		close(providerDone)

		for _, stop := range stops {
			stop()
		}
	}

	return ctx, cancel, cleanup
}

func (b *BatchingEmbeddingClient) deliver(
	requests []*embeddingBatchRequest,
	vectors [][]float32,
	err error,
) {
	for i, request := range requests {
		result := embeddingBatchResult{err: err}
		if err == nil {
			result.vector = vectors[i]
		}

		request.result <- result
	}
}

var _ EmbeddingClient = (*BatchingEmbeddingClient)(nil)
