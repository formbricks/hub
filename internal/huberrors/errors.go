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
