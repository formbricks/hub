package connector

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// WebhookInputConnector defines the interface for webhook-based input connectors
type WebhookInputConnector interface {
	// HandleWebhook processes incoming webhook payload and creates feedback records
	// The payload is the raw request body (could be JSON, form data, etc.)
	HandleWebhook(ctx context.Context, payload []byte) error
}

// WebhookRouter manages routing of webhooks to registered webhook input connectors
type WebhookRouter struct {
	mu         sync.RWMutex
	connectors map[string]registeredConnector
}

// registeredConnector holds a connector and its API key
type registeredConnector struct {
	connector WebhookInputConnector
	apiKey    string
}

// NewWebhookRouter creates a new webhook router
func NewWebhookRouter() *WebhookRouter {
	return &WebhookRouter{
		connectors: make(map[string]registeredConnector),
	}
}

// Register adds a webhook input connector to the router
// The name is used for routing webhooks to this connector
// The apiKey is used to authenticate incoming webhook requests
func (r *WebhookRouter) Register(name string, connector WebhookInputConnector, apiKey string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.connectors[name]; exists {
		return fmt.Errorf("connector %q is already registered", name)
	}

	r.connectors[name] = registeredConnector{
		connector: connector,
		apiKey:    apiKey,
	}

	slog.Info("Registered webhook input connector",
		"name", name,
	)

	return nil
}

// Route dispatches a webhook payload to the appropriate connector
// Returns an error if the connector is not found or processing fails
func (r *WebhookRouter) Route(ctx context.Context, name string, payload []byte) error {
	r.mu.RLock()
	registered, ok := r.connectors[name]
	r.mu.RUnlock()

	if !ok {
		return fmt.Errorf("connector %q not found", name)
	}

	slog.Info("Routing webhook to connector",
		"connector", name,
		"payload_size", len(payload),
	)

	if err := registered.connector.HandleWebhook(ctx, payload); err != nil {
		slog.Error("Webhook processing failed",
			"connector", name,
			"error", err,
		)
		return fmt.Errorf("failed to process webhook: %w", err)
	}

	slog.Info("Webhook processed successfully",
		"connector", name,
	)

	return nil
}

// ValidateAPIKey checks if the provided API key is valid for the connector
func (r *WebhookRouter) ValidateAPIKey(name, apiKey string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	registered, exists := r.connectors[name]
	if !exists {
		return false
	}
	return registered.apiKey == apiKey
}

// HasConnector checks if a connector with the given name is registered
func (r *WebhookRouter) HasConnector(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.connectors[name]
	return exists
}

// ListConnectors returns a list of registered connector names
func (r *WebhookRouter) ListConnectors() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.connectors))
	for name := range r.connectors {
		names = append(names, name)
	}
	return names
}
