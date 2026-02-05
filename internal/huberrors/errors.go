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

// NewNotFoundError creates a new NotFoundError with a custom message.
func NewNotFoundError(resource, message string) *NotFoundError {
	return &NotFoundError{
		Resource: resource,
		Message:  message,
	}
}

// ErrValidation represents a validation error.
// Use when client input fails validation.
var ErrValidation = &ValidationError{}

// ValidationError is a sentinel error for validation failures.
type ValidationError struct {
	Field   string
	Message string
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

// NewValidationError creates a new ValidationError with a custom message.
func NewValidationError(field, message string) *ValidationError {
	return &ValidationError{
		Field:   field,
		Message: message,
	}
}
