package handlers

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/connector"
)

// WebhookHandler handles incoming webhook requests from external services
type WebhookHandler struct {
	router *connector.WebhookRouter
}

// NewWebhookHandler creates a new webhook handler
func NewWebhookHandler(router *connector.WebhookRouter) *WebhookHandler {
	return &WebhookHandler{
		router: router,
	}
}

// Handle processes incoming webhook requests
// Route: POST /webhooks/{connector}?apiKey=xxx
func (h *WebhookHandler) Handle(w http.ResponseWriter, r *http.Request) {
	// Get connector name from path
	connectorName := r.PathValue("connector")
	if connectorName == "" {
		slog.Warn("Missing connector name in webhook request",
			"method", r.Method,
			"path", r.URL.Path,
		)
		response.RespondBadRequest(w, "Connector name is required")
		return
	}

	// Validate API key from query parameter
	apiKey := r.URL.Query().Get("apiKey")
	if apiKey == "" {
		slog.Warn("Missing API key in webhook request",
			"connector", connectorName,
			"method", r.Method,
			"path", r.URL.Path,
		)
		response.RespondUnauthorized(w, "API key is required")
		return
	}

	// Check if connector exists
	if !h.router.HasConnector(connectorName) {
		slog.Warn("Unknown connector in webhook request",
			"connector", connectorName,
			"method", r.Method,
			"path", r.URL.Path,
		)
		response.RespondNotFound(w, "Connector not found")
		return
	}

	// Validate API key
	if !h.router.ValidateAPIKey(connectorName, apiKey) {
		slog.Warn("Invalid API key in webhook request",
			"connector", connectorName,
			"method", r.Method,
			"path", r.URL.Path,
		)
		response.RespondUnauthorized(w, "Invalid API key")
		return
	}

	// Read the request body
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("Failed to read webhook request body",
			"connector", connectorName,
			"error", err,
		)
		response.RespondBadRequest(w, "Failed to read request body")
		return
	}
	defer func() {
		if err := r.Body.Close(); err != nil {
			slog.Warn("Failed to close request body",
				"connector", connectorName,
				"error", err,
			)
		}
	}()

	slog.Debug("Received webhook request",
		"connector", connectorName,
		"content_type", r.Header.Get("Content-Type"),
		"payload_size", len(payload),
	)

	// Route the webhook to the connector
	if err := h.router.Route(r.Context(), connectorName, payload); err != nil {
		slog.Error("Failed to process webhook",
			"connector", connectorName,
			"error", err,
		)
		response.RespondInternalServerError(w, "Failed to process webhook")
		return
	}

	// Return success response
	response.RespondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Webhook processed successfully",
	})
}
