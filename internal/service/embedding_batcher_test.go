package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
)

type batcherTestClient struct {
	mu          sync.Mutex
	batches     [][]string
	batchErr    error
	batchBlock  <-chan struct{}
	queryCalls  int
	singleCalls int
}

func (c *batcherTestClient) CreateEmbedding(_ context.Context, input string) ([]float32, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.singleCalls++

	return []float32{float32(len(input))}, nil
}

func (c *batcherTestClient) CreateEmbeddingForQuery(_ context.Context, input string) ([]float32, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.queryCalls++

	return []float32{float32(len(input))}, nil
}

func (c *batcherTestClient) CreateEmbeddings(ctx context.Context, inputs []string) ([][]float32, error) {
	if c.batchBlock != nil {
		select {
		case <-c.batchBlock:
		case <-ctx.Done():
			return nil, fmt.Errorf("test batch provider: %w", ctx.Err())
		}
	}

	c.mu.Lock()
	c.batches = append(c.batches, append([]string(nil), inputs...))
	err := c.batchErr
	c.mu.Unlock()

	if err != nil {
		return nil, err
	}

	vectors := make([][]float32, len(inputs))
	for i, input := range inputs {
		vectors[i] = []float32{float32(len(input))}
	}

	return vectors, nil
}

func TestBatchingEmbeddingClientGroupsAndMapsResults(t *testing.T) {
	provider := &batcherTestClient{}

	batcher, ok := NewBatchingEmbeddingClient(provider, EmbeddingBatchConfig{
		BatchSize: 3, MaxWait: time.Second, MaxInFlight: 1,
	}, nil)
	if !ok {
		t.Fatal("batch client was not enabled")
	}

	inputs := []string{"a", "bb", "ccc"}
	results := make([][]float32, len(inputs))
	errs := make([]error, len(inputs))

	var waitGroup sync.WaitGroup
	for i := range inputs {
		waitGroup.Go(func() {
			results[i], errs[i] = batcher.CreateEmbedding(context.Background(), inputs[i])
		})
	}

	waitGroup.Wait()

	for i := range inputs {
		if errs[i] != nil {
			t.Fatalf("input %d error = %v", i, errs[i])
		}

		if !reflect.DeepEqual(results[i], []float32{float32(len(inputs[i]))}) {
			t.Fatalf("input %d result = %v", i, results[i])
		}
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()

	if len(provider.batches) != 1 || len(provider.batches[0]) != 3 {
		t.Fatalf("batches = %v, want one batch of 3", provider.batches)
	}
}

func TestBatchingEmbeddingClientPartialBatchAndQueryBypass(t *testing.T) {
	provider := &batcherTestClient{}
	batcher, _ := NewBatchingEmbeddingClient(provider, EmbeddingBatchConfig{
		BatchSize: 8, MaxWait: 5 * time.Millisecond, MaxInFlight: 1,
	}, nil)

	vector, err := batcher.CreateEmbedding(context.Background(), "document")
	if err != nil || !reflect.DeepEqual(vector, []float32{8}) {
		t.Fatalf("document result = %v, %v", vector, err)
	}

	if _, err := batcher.CreateEmbeddingForQuery(context.Background(), "query"); err != nil {
		t.Fatalf("query error = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()

	if len(provider.batches) != 1 || provider.queryCalls != 1 || provider.singleCalls != 0 {
		t.Fatalf("batches=%v query=%d single=%d", provider.batches, provider.queryCalls, provider.singleCalls)
	}
}

func TestBatchingEmbeddingClientProviderErrorReachesEveryRequest(t *testing.T) {
	wantErr := errors.New("provider unavailable")
	provider := &batcherTestClient{batchErr: wantErr}
	batcher, _ := NewBatchingEmbeddingClient(provider, EmbeddingBatchConfig{
		BatchSize: 2, MaxWait: time.Second, MaxInFlight: 1,
	}, nil)

	errs := make(chan error, 2)

	for _, input := range []string{"first", "second"} {
		go func() {
			_, err := batcher.CreateEmbedding(context.Background(), input)
			errs <- err
		}()
	}

	for range 2 {
		if err := <-errs; !errors.Is(err, wantErr) {
			t.Fatalf("error = %v, want provider error", err)
		}
	}
}

func TestBatchingEmbeddingClientCancellationDoesNotPoisonPeers(t *testing.T) {
	provider := &batcherTestClient{}
	batcher, _ := NewBatchingEmbeddingClient(provider, EmbeddingBatchConfig{
		BatchSize: 8, MaxWait: 20 * time.Millisecond, MaxInFlight: 1,
	}, nil)

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancelledDone := make(chan error, 1)

	go func() {
		_, err := batcher.CreateEmbedding(cancelledCtx, "cancelled")
		cancelledDone <- err
	}()

	cancel()

	peer, err := batcher.CreateEmbedding(context.Background(), "peer")
	if err != nil || !reflect.DeepEqual(peer, []float32{4}) {
		t.Fatalf("peer result = %v, %v", peer, err)
	}

	if err := <-cancelledDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()

	if len(provider.batches) != 1 || !reflect.DeepEqual(provider.batches[0], []string{"peer"}) {
		t.Fatalf("provider batches = %v, want only active peer", provider.batches)
	}
}

func TestBatchingEmbeddingClientShutdownFlushesAndCloses(t *testing.T) {
	provider := &batcherTestClient{}
	batcher, _ := NewBatchingEmbeddingClient(provider, EmbeddingBatchConfig{
		BatchSize: 8, MaxWait: time.Hour, MaxInFlight: 1,
	}, nil)

	done := make(chan error, 1)

	go func() {
		_, err := batcher.CreateEmbedding(context.Background(), "pending")
		done <- err
	}()

	deadline := time.Now().Add(time.Second)

	for {
		batcher.mu.Lock()
		pending := len(batcher.pending)
		batcher.mu.Unlock()

		if pending == 1 {
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("request did not enter the pending batch")
		}

		runtime.Gosched()
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := batcher.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("pending request error = %v", err)
	}

	if _, err := batcher.CreateEmbedding(context.Background(), "late"); !errors.Is(err, ErrEmbeddingBatcherClosed) {
		t.Fatalf("late request error = %v, want closed", err)
	}
}

type singleOnlyEmbeddingClient struct{}

func (singleOnlyEmbeddingClient) CreateEmbedding(context.Context, string) ([]float32, error) {
	return []float32{1}, nil
}

func (singleOnlyEmbeddingClient) CreateEmbeddingForQuery(context.Context, string) ([]float32, error) {
	return []float32{1}, nil
}

func TestNewBatchingEmbeddingClientFallback(t *testing.T) {
	if _, ok := NewBatchingEmbeddingClient(singleOnlyEmbeddingClient{}, EmbeddingBatchConfig{BatchSize: 8}, nil); ok {
		t.Fatal("non-batch provider must retain single-request behavior")
	}

	if _, ok := NewBatchingEmbeddingClient(&batcherTestClient{}, EmbeddingBatchConfig{BatchSize: 1}, nil); ok {
		t.Fatal("batch size 1 must disable batching")
	}
}
