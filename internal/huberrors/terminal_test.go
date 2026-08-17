package huberrors

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminalProviderError(t *testing.T) {
	cause := errors.New("model refused")
	err := NewTerminalProviderError(TerminalReasonRefusal, cause)

	t.Run("matches the sentinel regardless of reason", func(t *testing.T) {
		require.ErrorIs(t, err, ErrTerminalProvider)
		require.ErrorIs(t, NewTerminalProviderError(TerminalReasonLength, nil), ErrTerminalProvider)
	})

	t.Run("survives wrapping", func(t *testing.T) {
		// The worker sees this error only after the service layer has wrapped it with operation
		// context, so matching has to work through the chain rather than on the bare value.
		wrapped := fmt.Errorf("classify sentiment: %w", fmt.Errorf("openai: %w", err))

		require.ErrorIs(t, wrapped, ErrTerminalProvider)

		reason, ok := TerminalReasonOf(wrapped)
		require.True(t, ok)
		assert.Equal(t, TerminalReasonRefusal, reason)
	})

	t.Run("unwraps to the provider cause", func(t *testing.T) {
		require.ErrorIs(t, err, cause)
	})

	t.Run("a plain error is not terminal", func(t *testing.T) {
		// The distinction this type exists for: an ordinary provider failure must NOT be
		// mistaken for a permanent one, or the record is abandoned.
		plain := fmt.Errorf("openai chat completion: %w", errors.New("500 internal server error"))

		require.NotErrorIs(t, plain, ErrTerminalProvider)

		_, ok := TerminalReasonOf(plain)
		assert.False(t, ok)
	})

	t.Run("a rate limit is not terminal", func(t *testing.T) {
		// Both are provider failures; only one is permanent. Confusing them would either abandon
		// throttled records or retry filtered ones forever.
		limited := NewRateLimitError(0, errors.New("429"))

		require.NotErrorIs(t, limited, ErrTerminalProvider)
	})

	t.Run("nil error is safe to report", func(t *testing.T) {
		_, ok := TerminalReasonOf(nil)
		assert.False(t, ok)
	})
}
