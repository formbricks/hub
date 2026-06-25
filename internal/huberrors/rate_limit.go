package huberrors

import (
	"fmt"
	"time"
)

// RateLimitError marks a provider rate-limit response (HTTP 429 / RESOURCE_EXHAUSTED).
// RetryAfter is the provider-suggested delay before retrying, or 0 when unknown. Enrichment
// workers snooze for RetryAfter instead of consuming a retry attempt, so a burst against a
// rate-limited model defers work rather than dropping it.
type RateLimitError struct {
	RetryAfter time.Duration
	Err        error
}

// NewRateLimitError wraps err as a rate-limit error carrying the provider's retry-after hint
// (0 when the provider gave none).
func NewRateLimitError(retryAfter time.Duration, err error) *RateLimitError {
	return &RateLimitError{RetryAfter: retryAfter, Err: err}
}

func (e *RateLimitError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("rate limited (retry after %s): %v", e.RetryAfter, e.Err)
	}

	return fmt.Sprintf("rate limited (retry after %s)", e.RetryAfter)
}

// Unwrap exposes the underlying provider error for errors.Is/As.
func (e *RateLimitError) Unwrap() error { return e.Err }
