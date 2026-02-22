package service

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/formbricks/hub/internal/observability"
)

const (
	defaultInitialBackoffWhenZero = 500 * time.Millisecond
	backoffMultiplier             = 2
)

// RetryingWebhookDispatchInserter wraps a WebhookDispatchInserter and retries InsertMany
// on failure with exponential backoff and jitter. Use for transient River/DB errors.
type RetryingWebhookDispatchInserter struct {
	inner          WebhookDispatchInserter
	maxRetries     int
	initialBackoff time.Duration
	maxBackoff     time.Duration
	metrics        observability.WebhookMetrics
}

// RetryingWebhookDispatchInserterConfig holds configuration for the retrying inserter.
type RetryingWebhookDispatchInserterConfig struct {
	MaxRetries     int           // Number of retries after the first attempt (total attempts = 1 + MaxRetries).
	InitialBackoff time.Duration // Backoff after first failure; doubles each attempt, capped by MaxBackoff.
	MaxBackoff     time.Duration // Upper bound on backoff between attempts.
	Metrics        observability.WebhookMetrics
}

// NewRetryingWebhookDispatchInserter returns a WebhookDispatchInserter that retries
// InsertMany on error with exponential backoff and jitter. maxRetries is the number
// of retries (total attempts = 1 + maxRetries). initialBackoff and maxBackoff bound
// the sleep between attempts; jitter is applied to avoid thundering herd.
func NewRetryingWebhookDispatchInserter(
	inner WebhookDispatchInserter, cfg RetryingWebhookDispatchInserterConfig,
) *RetryingWebhookDispatchInserter {
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}

	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = defaultInitialBackoffWhenZero
	}

	if cfg.MaxBackoff < cfg.InitialBackoff {
		cfg.MaxBackoff = cfg.InitialBackoff
	}

	return &RetryingWebhookDispatchInserter{
		inner:          inner,
		maxRetries:     cfg.MaxRetries,
		initialBackoff: cfg.InitialBackoff,
		maxBackoff:     cfg.MaxBackoff,
		metrics:        cfg.Metrics,
	}
}

// InsertMany calls the inner inserter; on error, retries up to maxRetries times with
// exponential backoff and jitter. Respects context cancellation during backoff.
func (r *RetryingWebhookDispatchInserter) InsertMany(
	ctx context.Context, params []river.InsertManyParams,
) ([]*rivertype.JobInsertResult, error) {
	var lastErr error

	backoff := r.initialBackoff

	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		results, err := r.inner.InsertMany(ctx, params)
		if err == nil {
			return results, nil
		}

		lastErr = err

		if attempt == r.maxRetries {
			break
		}

		if r.metrics != nil {
			r.metrics.RecordEnqueueRetry(ctx)
		}

		sleep := r.jitter(backoff)
		slog.Warn("webhook enqueue failed, retrying after backoff",
			"attempt", attempt+1,
			"max_attempts", r.maxRetries+1,
			"backoff", sleep,
			"error", err,
		)

		if err := r.sleep(ctx, sleep); err != nil {
			return nil, err
		}

		backoff = min(backoff*backoffMultiplier, r.maxBackoff)
	}

	return nil, lastErr
}

// jitter returns a duration between 50% and 100% of duration to avoid thundering herd.
func (r *RetryingWebhookDispatchInserter) jitter(duration time.Duration) time.Duration {
	const jitterHalf = 2

	half := duration / jitterHalf

	if half <= 0 {
		return duration
	}

	var buf [8]byte

	if _, err := rand.Read(buf[:]); err != nil {
		return half
	}

	randVal := binary.BigEndian.Uint64(buf[:])
	halfNanos := half.Nanoseconds()

	if halfNanos <= 0 {
		return half
	}

	// randVal % halfNanos is in [0, halfNanos); duration nanos fit in int64
	//nolint:gosec // G115: modulo result is in [0, halfNanos), safe to convert to int64
	jitterNanos := int64(randVal % uint64(halfNanos))

	return half + time.Duration(jitterNanos)
}

// sleep blocks for the given duration or until ctx is cancelled; returns ctx.Err() if cancelled.
func (r *RetryingWebhookDispatchInserter) sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("backoff interrupted: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

// Ensure RetryingWebhookDispatchInserter implements WebhookDispatchInserter.
var _ WebhookDispatchInserter = (*RetryingWebhookDispatchInserter)(nil)
