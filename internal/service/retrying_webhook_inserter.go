package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/formbricks/hub/internal/observability"
)

var errBaseInserterNotConfigured = errors.New("webhook inserter: BaseInserter is not configured")

// RetryingWebhookDispatchInserterConfig configures retry behavior for webhook enqueue.
type RetryingWebhookDispatchInserterConfig struct {
	MaxRetries     int           // Number of retries after first attempt; default 3
	InitialBackoff time.Duration // First backoff; default 100ms
	MaxBackoff     time.Duration // Cap on backoff; default 2s
	Metrics        observability.WebhookMetrics
	BaseInserter   WebhookDispatchInserter
}

// RetryingWebhookDispatchInserter wraps a WebhookDispatchInserter with exponential backoff + jitter on InsertMany failures.
type RetryingWebhookDispatchInserter struct {
	cfg RetryingWebhookDispatchInserterConfig
}

// NewRetryingWebhookDispatchInserter creates a RetryingWebhookDispatchInserter.
func NewRetryingWebhookDispatchInserter(cfg RetryingWebhookDispatchInserterConfig) *RetryingWebhookDispatchInserter {
	return &RetryingWebhookDispatchInserter{cfg: cfg}
}

// InsertMany delegates to the base inserter; on failure, retries with exponential backoff + jitter.
func (r *RetryingWebhookDispatchInserter) InsertMany(
	ctx context.Context, params []river.InsertManyParams,
) ([]*rivertype.JobInsertResult, error) {
	base := r.cfg.BaseInserter
	if base == nil {
		return nil, errBaseInserterNotConfigured
	}

	maxRetries := max(r.cfg.MaxRetries, 0)

	result, err := base.InsertMany(ctx, params)
	if err == nil {
		return result, nil
	}

	const defaultInitialBackoffMs = 100

	initialBackoff := r.cfg.InitialBackoff
	if initialBackoff <= 0 {
		initialBackoff = defaultInitialBackoffMs * time.Millisecond
	}

	maxBackoff := max(r.cfg.MaxBackoff, initialBackoff)

	eventID := extractEventIDFromParams(params)
	lastErr := err

	for attempt := range maxRetries {
		backoff := computeBackoff(initialBackoff, maxBackoff, attempt)
		slog.Warn("webhook enqueue failed, retrying",
			"attempt", attempt+1,
			"max_retries", maxRetries,
			"backoff", backoff,
			"error", lastErr,
			"event_id", eventID,
		)

		select {
		case <-ctx.Done():
			err := ctx.Err()
			slog.Error("webhook enqueue retry cancelled", "error", err, "event_id", eventID)

			return nil, fmt.Errorf("webhook enqueue retry: %w", err)
		case <-time.After(backoff):
			if r.cfg.Metrics != nil {
				r.cfg.Metrics.RecordEnqueueRetry(ctx)
			}

			result, err = base.InsertMany(ctx, params)
			if err == nil {
				return result, nil
			}

			lastErr = err
		}
	}

	slog.Error("webhook enqueue failed after retries",
		"attempts", maxRetries+1,
		"error", lastErr,
		"event_id", eventID,
	)

	return nil, lastErr
}

func extractEventIDFromParams(params []river.InsertManyParams) any {
	if len(params) == 0 {
		return nil
	}

	args, ok := params[0].Args.(WebhookDispatchArgs)
	if !ok {
		return nil
	}

	return args.EventID
}

func computeBackoff(initial, maxBackoff time.Duration, attempt int) time.Duration {
	// Exponential: initial * 2^attempt, capped at maxBackoff
	backoff := initial
	for range attempt {
		backoff *= 2
		if backoff >= maxBackoff {
			backoff = maxBackoff

			break
		}
	}

	// Full jitter: sleep random(0, backoff) to avoid thundering herd.
	// math/rand is acceptable here: Go 1.20+ auto-seeds; we need non-crypto randomness for backoff jitter only.
	jitter := time.Duration(rand.Int63n(int64(backoff))) //nolint:gosec // G404: non-crypto randomness is fine for backoff jitter

	return jitter
}
