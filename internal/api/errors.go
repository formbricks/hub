// Package api provides HTTP request handlers and error handling utilities for the Hub API.
// It uses Huma v2 for OpenAPI-compliant REST endpoints with automatic validation.
package api

import (
	"log/slog"

	"github.com/danielgtaylor/huma/v2"
	"github.com/formbricks/hub/internal/ent"
	"github.com/google/uuid"
)

// Error message constants for consistent client-facing messages
const (
	ErrMsgNotFound       = "The requested resource does not exist"
	ErrMsgConstraint     = "A resource with these attributes already exists or violates a constraint"
	ErrMsgDatabase       = "A database error occurred. Please try again later."
	ErrMsgServiceUnavail = "The requested service is temporarily unavailable. Please try again later."
	ErrMsgInvalidUUID    = "Invalid UUID format: must be a valid UUID"
	ErrMsgInvalidInput   = "Invalid input: "
)

// handleDatabaseError is a specialized error handler for database operations.
// It logs the full error details internally but returns sanitized error messages to clients.
// This prevents leaking internal implementation details like stack traces or database errors.
func handleDatabaseError(logger *slog.Logger, err error, operation string, resourceID string) error {
	// Log full error details internally for debugging
	logger.Error("database "+operation+" failed",
		"error", err.Error(),
		"resource_id", resourceID)

	// Return sanitized error based on error type
	if ent.IsNotFound(err) {
		return huma.Error404NotFound(ErrMsgNotFound)
	}

	if ent.IsConstraintError(err) {
		return huma.Error409Conflict(ErrMsgConstraint)
	}

	// Don't expose internal database errors to clients
	return huma.Error500InternalServerError(ErrMsgDatabase)
}

// handleServiceError handles errors from service layer (AI enrichment, embeddings, etc).
// Logs detailed errors but returns generic messages to clients.
func handleServiceError(logger *slog.Logger, err error, service string, operation string) error {
	logger.Error(service+" "+operation+" failed",
		"error", err.Error(),
		"service", service)

	// Return generic error - don't expose service implementation details
	return huma.Error503ServiceUnavailable(ErrMsgServiceUnavail)
}

// parseUUID parses a UUID string and returns an error if invalid.
// This helper reduces duplication and provides consistent error messages.
func parseUUID(id string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, huma.Error400BadRequest(ErrMsgInvalidUUID)
	}
	return parsed, nil
}
