// Package huberrors provides sentinel and custom error types for the application.
package huberrors

// ErrNotFound represents a "not found" error.
// Use when a requested resource doesn't exist.
var ErrNotFound = &NotFoundError{}

// NotFoundError is a sentinel error for resources that are not found.
type NotFoundError struct {
	Resource string
	Message  string
}

// NewNotFoundError creates a new NotFoundError with a custom message.
func NewNotFoundError(resource, message string) *NotFoundError {
	return &NotFoundError{
		Resource: resource,
		Message:  message,
	}
}

// Error implements the error interface.
func (e *NotFoundError) Error() string {
	if e.Message != "" {
		return e.Message
	}

	if e.Resource != "" {
		return e.Resource + " not found"
	}

	return "resource not found"
}

// Is implements the error interface for error comparison.
func (e *NotFoundError) Is(target error) bool {
	_, ok := target.(*NotFoundError)

	return ok
}

// ErrValidation represents a validation error.
// Use when client input fails validation.
var ErrValidation = &ValidationError{}

// ValidationError is a sentinel error for validation failures.
type ValidationError struct {
	Field   string
	Message string
}

// NewValidationError creates a new ValidationError with a custom message.
func NewValidationError(field, message string) *ValidationError {
	return &ValidationError{
		Field:   field,
		Message: message,
	}
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	if e.Message != "" {
		return e.Message
	}

	if e.Field != "" {
		return "validation failed for field: " + e.Field
	}

	return "validation error"
}

// Is implements the error interface for error comparison.
func (e *ValidationError) Is(target error) bool {
	_, ok := target.(*ValidationError)

	return ok
}

// ErrLimitExceeded is the sentinel for limit-exceeded errors (e.g. webhook max count).
// Use when an operation is rejected because a configured limit was reached.
var ErrLimitExceeded = &LimitExceededError{}

// LimitExceededError is a sentinel error for limit-exceeded conditions.
type LimitExceededError struct {
	Message string
}

// NewLimitExceededError creates a LimitExceededError with a custom message.
func NewLimitExceededError(message string) *LimitExceededError {
	return &LimitExceededError{Message: message}
}

// Error implements the error interface.
func (e *LimitExceededError) Error() string {
	if e.Message != "" {
		return e.Message
	}

	return "limit exceeded"
}

// Is implements the error interface for error comparison.
func (e *LimitExceededError) Is(target error) bool {
	_, ok := target.(*LimitExceededError)

	return ok
}

// ErrConflict is the sentinel for conflict errors (e.g. duplicate tenant_id + submission_id + field_id).
var ErrConflict = &ConflictError{}

// ConflictError is a sentinel error for resource conflicts.
type ConflictError struct {
	Message string
}

// NewConflictError creates a ConflictError with a custom message.
func NewConflictError(message string) *ConflictError {
	return &ConflictError{Message: message}
}

// Error implements the error interface.
func (e *ConflictError) Error() string {
	if e.Message != "" {
		return e.Message
	}

	return "conflict"
}

// Is implements the error interface for error comparison.
func (e *ConflictError) Is(target error) bool {
	_, ok := target.(*ConflictError)

	return ok
}

// ErrTranslationSuperseded is the sentinel for a feedback-record translation write skipped
// because the tenant's current target_language no longer matches the target the job was
// enqueued for — e.g. the target changed, or the job was enqueued from a stale tenant-settings
// cache read. The newer-target write owns the row; the stale write is a benign no-op, not a
// failure, so callers should record it as skipped rather than retry or fail.
var ErrTranslationSuperseded = &TranslationSupersededError{}

// TranslationSupersededError is a sentinel for a stale-target translation write. It is
// deliberately distinct from NotFoundError (the record still exists) and ConflictError
// (nothing is wrong with the data) so an out-of-order translation is treated as a skip.
type TranslationSupersededError struct {
	Message string
}

// Error implements the error interface.
func (e *TranslationSupersededError) Error() string {
	if e.Message != "" {
		return e.Message
	}

	return "translation superseded by a newer target language"
}

// Is implements the error interface for error comparison.
func (e *TranslationSupersededError) Is(target error) bool {
	_, ok := target.(*TranslationSupersededError)

	return ok
}

// ErrEmbeddingSuperseded is the sentinel for an embedding write skipped because the record's
// value_text/field_label changed between the job reading the record and writing the vector —
// a newer job owns the row, and letting the stale vector land last would permanently attach an
// old text's embedding to the record (the missing-rows-only backfill can never repair that).
// A benign no-op, not a failure: callers should record it as skipped rather than retry.
var ErrEmbeddingSuperseded = &EmbeddingSupersededError{}

// EmbeddingSupersededError is a sentinel for a stale-content embedding write. Deliberately
// distinct from NotFoundError (the record still exists) and ConflictError (nothing is wrong
// with the data) so an out-of-order embedding is treated as a skip.
type EmbeddingSupersededError struct {
	Message string
}

// Error implements the error interface.
func (e *EmbeddingSupersededError) Error() string {
	if e.Message != "" {
		return e.Message
	}

	return "embedding superseded by newer record content"
}

// Is implements the error interface for error comparison.
func (e *EmbeddingSupersededError) Is(target error) bool {
	_, ok := target.(*EmbeddingSupersededError)

	return ok
}

// ErrTenantWriteConflict is the sentinel for tenant write coordination conflicts:
// a tenant-owned write rejected because a tenant data purge is in progress, or a
// purge that could not acquire its lock while tenant-owned writes were in flight.
// Both directions are retryable.
var ErrTenantWriteConflict = &TenantWriteConflictError{}

// TenantWriteConflictError is a sentinel error for tenant write coordination conflicts.
// It is deliberately distinct from ConflictError so retryable lock conflicts are never
// confused with terminal resource conflicts (e.g. duplicate submissions).
type TenantWriteConflictError struct {
	Message string
}

// NewTenantWriteConflictError creates a TenantWriteConflictError with a custom message.
func NewTenantWriteConflictError(message string) *TenantWriteConflictError {
	return &TenantWriteConflictError{Message: message}
}

// Error implements the error interface.
func (e *TenantWriteConflictError) Error() string {
	if e.Message != "" {
		return e.Message
	}

	return "tenant write conflict"
}

// Is implements the error interface for error comparison.
func (e *TenantWriteConflictError) Is(target error) bool {
	_, ok := target.(*TenantWriteConflictError)

	return ok
}
