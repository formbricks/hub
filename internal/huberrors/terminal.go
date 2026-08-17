package huberrors

import (
	"errors"
	"fmt"
)

// ErrTerminalProvider represents a provider outcome that is permanent for the given input.
// Use with errors.Is to test whether retrying is pointless.
var ErrTerminalProvider = &TerminalProviderError{}

// TerminalReason names why the provider will never produce a result for this input. The set is
// closed and small so it is safe as a metric label, as a database CHECK, and as a UI string.
type TerminalReason string

const (
	// TerminalReasonContentFilter is a safety / content-policy block. The provider examined the
	// text and declined to process it.
	TerminalReasonContentFilter TerminalReason = "content_filter"
	// TerminalReasonRefusal is the model explicitly declining, returned as a refusal message
	// rather than as a filter verdict.
	TerminalReasonRefusal TerminalReason = "refusal"
	// TerminalReasonLength is the input or output exceeding what the model can handle. Permanent
	// for this text at this model, though it can change if either one changes.
	TerminalReasonLength TerminalReason = "length"
	// TerminalReasonRecitation is the provider suppressing output that reproduces training data.
	TerminalReasonRecitation TerminalReason = "recitation"
)

// TerminalProviderError marks a provider failure that is a property of the INPUT, not of the
// provider's health. Retrying it is guaranteed to fail again and costs a call every time.
//
// This distinction is the whole point of the type. A 500 and a content-filter block are both
// "the provider did not give us a result", but one succeeds on the next attempt and the other
// never will. Collapsing them means either abandoning records that would have worked, or
// retrying records that cannot, forever — and any reconciler that re-enqueues unfinished work
// will loop on the latter until something tells it to stop.
//
// Classification is deliberately CONSERVATIVE: only outcomes known to be determined by the
// content are terminal, and anything ambiguous stays retryable. The asymmetry is intentional —
// a false terminal permanently abandons a record, while a false transient only wastes a few
// calls.
type TerminalProviderError struct {
	Reason TerminalReason
	Err    error
}

// NewTerminalProviderError wraps err as a terminal provider failure with the given reason.
func NewTerminalProviderError(reason TerminalReason, err error) *TerminalProviderError {
	return &TerminalProviderError{Reason: reason, Err: err}
}

// Error implements the error interface.
func (e *TerminalProviderError) Error() string {
	reason := string(e.Reason)
	if reason == "" {
		reason = "unspecified"
	}

	if e.Err != nil {
		return fmt.Sprintf("terminal provider failure (%s): %v", reason, e.Err)
	}

	return fmt.Sprintf("terminal provider failure (%s)", reason)
}

// Is implements error comparison so errors.Is(err, ErrTerminalProvider) matches any reason.
// Callers that need the specific reason use errors.As.
func (e *TerminalProviderError) Is(target error) bool {
	_, ok := target.(*TerminalProviderError)

	return ok
}

// Unwrap exposes the underlying provider error for errors.Is/As.
func (e *TerminalProviderError) Unwrap() error { return e.Err }

// TerminalReasonOf returns the reason carried by the first TerminalProviderError in err's chain,
// and whether one was found. It keeps the errors.As dance in one place for callers that record
// the reason as a label or a column.
func TerminalReasonOf(err error) (TerminalReason, bool) {
	var terminal *TerminalProviderError
	if !errors.As(err, &terminal) {
		return "", false
	}

	return terminal.Reason, true
}
