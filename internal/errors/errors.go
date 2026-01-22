package errors

import "fmt"

// ErrNotFound represents a "not found" error
// This should be used when a requested resource doesn't exist
var ErrNotFound = &NotFoundError{}

// NotFoundError is a sentinel error for resources that are not found
type NotFoundError struct {
	Resource string
	Message  string
}

// Error implements the error interface
func (e *NotFoundError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Resource != "" {
		return fmt.Sprintf("%s not found", e.Resource)
	}
	return "resource not found"
}

// Is implements the error interface for error comparison
func (e *NotFoundError) Is(target error) bool {
	_, ok := target.(*NotFoundError)
	return ok
}

// NewNotFoundError creates a new NotFoundError with a custom message
func NewNotFoundError(resource, message string) *NotFoundError {
	return &NotFoundError{
		Resource: resource,
		Message:  message,
	}
}

// ErrValidation represents a validation error
// This should be used when client input fails validation
var ErrValidation = &ValidationError{}

// ValidationError is a sentinel error for validation failures
type ValidationError struct {
	Field   string
	Message string
}

// Error implements the error interface
func (e *ValidationError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Field != "" {
		return fmt.Sprintf("validation failed for field: %s", e.Field)
	}
	return "validation error"
}

// Is implements the error interface for error comparison
func (e *ValidationError) Is(target error) bool {
	_, ok := target.(*ValidationError)
	return ok
}

// NewValidationError creates a new ValidationError with a custom message
func NewValidationError(field, message string) *ValidationError {
	return &ValidationError{
		Field:   field,
		Message: message,
	}
}

// ErrInvalidInput represents an invalid input error
// This should be used when client input is invalid (e.g., NULL bytes, out of range)
var ErrInvalidInput = &InvalidInputError{}

// InvalidInputError is a sentinel error for invalid input data
type InvalidInputError struct {
	Field   string
	Message string
}

// Error implements the error interface
func (e *InvalidInputError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Field != "" {
		return fmt.Sprintf("invalid input for field: %s", e.Field)
	}
	return "invalid input"
}

// Is implements the error interface for error comparison
func (e *InvalidInputError) Is(target error) bool {
	_, ok := target.(*InvalidInputError)
	return ok
}

// NewInvalidInputError creates a new InvalidInputError with a custom message
func NewInvalidInputError(field, message string) *InvalidInputError {
	return &InvalidInputError{
		Field:   field,
		Message: message,
	}
}
