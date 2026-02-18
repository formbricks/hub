package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type flakyInserter struct {
	callCount int
	failUntil int // InsertMany fails until callCount reaches this; then succeeds.
}

func (f *flakyInserter) InsertMany(_ context.Context, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error) {
	f.callCount++
	if f.callCount < f.failUntil {
		return nil, errors.New("transient error")
	}

	results := make([]*rivertype.JobInsertResult, len(params))
	for i := range results {
		results[i] = &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: int64(i + 1)}}
	}

	return results, nil
}

func TestRetryingWebhookDispatchInserter_success_after_retries(t *testing.T) {
	inner := &flakyInserter{failUntil: 3}
	cfg := RetryingWebhookDispatchInserterConfig{
		MaxRetries:     5,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
	}
	w := NewRetryingWebhookDispatchInserter(inner, cfg)
	ctx := context.Background()
	params := []river.InsertManyParams{{Args: WebhookDispatchArgs{}}}

	results, err := w.InsertMany(ctx, params)
	if err != nil {
		t.Fatalf("InsertMany: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("len(results) = %d, want 1", len(results))
	}

	if inner.callCount != 3 {
		t.Errorf("inner called %d times, want 3 (2 failures + 1 success)", inner.callCount)
	}
}

func TestRetryingWebhookDispatchInserter_exhausted_retries(t *testing.T) {
	inner := &flakyInserter{failUntil: 99}
	cfg := RetryingWebhookDispatchInserterConfig{
		MaxRetries:     2,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     5 * time.Millisecond,
	}
	w := NewRetryingWebhookDispatchInserter(inner, cfg)
	ctx := context.Background()
	params := []river.InsertManyParams{{Args: WebhookDispatchArgs{}}}

	_, err := w.InsertMany(ctx, params)
	if err == nil {
		t.Fatal("expected error after exhausted retries")
	}

	wantCalls := 3
	if inner.callCount != wantCalls {
		t.Errorf("inner called %d times, want %d (1 initial + 2 retries)", inner.callCount, wantCalls)
	}
}

func TestRetryingWebhookDispatchInserter_success_first_try(t *testing.T) {
	inner := &flakyInserter{failUntil: 1}
	cfg := RetryingWebhookDispatchInserterConfig{
		MaxRetries:     2,
		InitialBackoff: time.Hour,
		MaxBackoff:     time.Hour,
	}
	w := NewRetryingWebhookDispatchInserter(inner, cfg)
	ctx := context.Background()
	params := []river.InsertManyParams{{Args: WebhookDispatchArgs{}}}

	_, err := w.InsertMany(ctx, params)
	if err != nil {
		t.Fatalf("InsertMany: %v", err)
	}

	if inner.callCount != 1 {
		t.Errorf("inner called %d times, want 1", inner.callCount)
	}
}

func TestRetryingWebhookDispatchInserter_zero_retries(t *testing.T) {
	inner := &flakyInserter{failUntil: 2}
	cfg := RetryingWebhookDispatchInserterConfig{
		MaxRetries:     0,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Second,
	}
	w := NewRetryingWebhookDispatchInserter(inner, cfg)
	ctx := context.Background()
	params := []river.InsertManyParams{{Args: WebhookDispatchArgs{}}}

	_, err := w.InsertMany(ctx, params)
	if err == nil {
		t.Fatal("expected error (no retries)")
	}

	if inner.callCount != 1 {
		t.Errorf("inner called %d times, want 1", inner.callCount)
	}
}

func TestRetryingWebhookDispatchInserter_context_cancel_during_backoff(t *testing.T) {
	inner := &flakyInserter{failUntil: 99}
	cfg := RetryingWebhookDispatchInserterConfig{
		MaxRetries:     5,
		InitialBackoff: 1 * time.Hour,
		MaxBackoff:     1 * time.Hour,
	}
	w := NewRetryingWebhookDispatchInserter(inner, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	params := []river.InsertManyParams{{Args: WebhookDispatchArgs{}}}

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	_, err := w.InsertMany(ctx, params)
	if err == nil {
		t.Fatal("expected error (context cancelled)")
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}

	if inner.callCount != 1 {
		t.Errorf("inner called %d times, want 1 (then cancel during backoff)", inner.callCount)
	}
}
