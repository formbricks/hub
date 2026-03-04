package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errInsertFailed = errors.New("insert failed")

func TestRetryingWebhookDispatchInserter_SuccessOnFirstTry(t *testing.T) {
	calls := 0
	base := &mockWebhookInserter{}
	base.insertManyErr = nil

	wrapper := &countingInserter{base: base, calls: &calls}
	r := NewRetryingWebhookDispatchInserter(RetryingWebhookDispatchInserterConfig{
		MaxRetries:     2,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
		BaseInserter:   wrapper,
	})

	ctx := context.Background()
	result, err := r.InsertMany(ctx, nil)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 1, calls)
}

func TestRetryingWebhookDispatchInserter_SuccessAfterRetry(t *testing.T) {
	failUntil := int32(2)
	base := &failingUntilInserter{failUntil: failUntil}
	r := NewRetryingWebhookDispatchInserter(RetryingWebhookDispatchInserterConfig{
		MaxRetries:     5,
		InitialBackoff: 5 * time.Millisecond,
		MaxBackoff:     20 * time.Millisecond,
		BaseInserter:   base,
	})

	ctx := context.Background()
	result, err := r.InsertMany(ctx, nil)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.GreaterOrEqual(t, base.calls.Load(), int32(2))
}

func TestRetryingWebhookDispatchInserter_FailureAfterAllRetries(t *testing.T) {
	base := &mockWebhookInserter{insertManyErr: errInsertFailed}
	r := NewRetryingWebhookDispatchInserter(RetryingWebhookDispatchInserterConfig{
		MaxRetries:     2,
		InitialBackoff: 5 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
		BaseInserter:   base,
	})

	ctx := context.Background()
	result, err := r.InsertMany(ctx, nil)
	require.Error(t, err)
	assert.Nil(t, result)
	require.ErrorIs(t, err, errInsertFailed)
	assert.Len(t, base.insertManyCalls, 3) // 1 initial + 2 retries
}

func TestRetryingWebhookDispatchInserter_ContextCancelledDuringBackoff(t *testing.T) {
	base := &mockWebhookInserter{insertManyErr: errInsertFailed}
	r := NewRetryingWebhookDispatchInserter(RetryingWebhookDispatchInserterConfig{
		MaxRetries:     5,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     200 * time.Millisecond,
		BaseInserter:   base,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := r.InsertMany(ctx, nil)
	require.Error(t, err)
	assert.Nil(t, result)
	require.ErrorIs(t, err, context.Canceled)
	assert.Len(t, base.insertManyCalls, 1)
}

func TestRetryingWebhookDispatchInserter_RecordEnqueueRetry(t *testing.T) {
	retryCount := 0
	metrics := &mockWebhookMetricsForRetry{onRetry: func() { retryCount++ }}

	base := &failingUntilInserter{failUntil: 2}
	r := NewRetryingWebhookDispatchInserter(RetryingWebhookDispatchInserterConfig{
		MaxRetries:     5,
		InitialBackoff: 5 * time.Millisecond,
		MaxBackoff:     20 * time.Millisecond,
		Metrics:        metrics,
		BaseInserter:   base,
	})

	ctx := context.Background()
	_, err := r.InsertMany(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, retryCount, "RecordEnqueueRetry should be called once (one retry)")
}

// countingInserter wraps an inserter and counts calls.
type countingInserter struct {
	base  *mockWebhookInserter
	calls *int
}

func (c *countingInserter) InsertMany(ctx context.Context, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error) {
	*c.calls++

	return c.base.InsertMany(ctx, params)
}

// failingUntilInserter fails until call count reaches failUntil, then succeeds.
type failingUntilInserter struct {
	failUntil int32
	calls     atomic.Int32
}

func (f *failingUntilInserter) InsertMany(_ context.Context, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error) {
	n := f.calls.Add(1)
	if n < f.failUntil {
		return nil, errInsertFailed
	}

	results := make([]*rivertype.JobInsertResult, len(params))
	for i := range results {
		results[i] = &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: int64(i + 1)}}
	}

	return results, nil
}

// mockWebhookMetricsForRetry implements WebhookMetrics with only RecordEnqueueRetry for tests.
type mockWebhookMetricsForRetry struct {
	onRetry func()
}

func (m *mockWebhookMetricsForRetry) RecordJobsEnqueued(context.Context, string, int64) {}
func (m *mockWebhookMetricsForRetry) RecordProviderError(context.Context, string)       {}
func (m *mockWebhookMetricsForRetry) RecordDelivery(context.Context, string, string)    {}
func (m *mockWebhookMetricsForRetry) RecordWebhookDisabled(context.Context, string)     {}
func (m *mockWebhookMetricsForRetry) RecordDispatchError(context.Context, string)       {}
func (m *mockWebhookMetricsForRetry) RecordWebhookDeliveryDuration(context.Context, time.Duration, string, string) {
}

func (m *mockWebhookMetricsForRetry) RecordEnqueueRetry(context.Context) {
	if m.onRetry != nil {
		m.onRetry()
	}
}
