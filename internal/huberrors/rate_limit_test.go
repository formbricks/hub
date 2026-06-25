package huberrors

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimitError_WrapsAndUnwraps(t *testing.T) {
	cause := errors.New("429 from provider")
	err := NewRateLimitError(12*time.Second, cause)

	require.Equal(t, 12*time.Second, err.RetryAfter)
	require.ErrorIs(t, err, cause, "Unwrap exposes the underlying provider error")
	assert.Contains(t, err.Error(), "12s")
	assert.Contains(t, err.Error(), "429 from provider")

	var rateLimited *RateLimitError
	assert.ErrorAs(t, err, &rateLimited)
}

func TestRateLimitError_NilCause(t *testing.T) {
	err := NewRateLimitError(0, nil)

	require.NoError(t, err.Unwrap(), "no cause unwraps to nil")
	assert.Contains(t, err.Error(), "rate limited")
	assert.NotContains(t, err.Error(), "%!", "the message must not contain a bad fmt verb")
}
