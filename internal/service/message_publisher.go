package service

import (
	"context"
	"log/slog"

	"github.com/formbricks/hub/internal/datatypes"
)

// Event represents an event that can be published to message providers (webhooks, email, etc.)
type Event struct {
	Type          datatypes.EventType // Event type enum (e.g., FeedbackRecordCreated, WebhookCreated)
	Timestamp     int64               // Unix timestamp
	Data          interface{}         // Event data (FeedbackRecord, Webhook, etc.)
	ChangedFields []string            // Only for updates
}

// MessagePublisher defines the interface for publishing events
type MessagePublisher interface {
	// PublishEvent publishes a single event immediately
	PublishEvent(ctx context.Context, event Event) error

	// PublishEvents publishes multiple events (for future batching)
	PublishEvents(ctx context.Context, events []Event) error
}

// MessagePublisherManager coordinates multiple message providers
type MessagePublisherManager struct {
	providers []MessagePublisher
}

// NewMessagePublisherManager creates a new message publisher manager
func NewMessagePublisherManager() *MessagePublisherManager {
	return &MessagePublisherManager{
		providers: make([]MessagePublisher, 0),
	}
}

// RegisterProvider registers a message provider (webhooks, email, SMS, etc.)
func (m *MessagePublisherManager) RegisterProvider(provider MessagePublisher) {
	m.providers = append(m.providers, provider)
}

// PublishEvent sends event to all registered providers
func (m *MessagePublisherManager) PublishEvent(ctx context.Context, event Event) error {
	// Send to all providers in parallel (goroutines)
	// Collect errors but don't fail if one provider fails
	for _, provider := range m.providers {
		go func(p MessagePublisher) {
			if err := p.PublishEvent(ctx, event); err != nil {
				slog.Warn("Failed to publish event to provider", "error", err)
			}
		}(provider)
	}
	return nil
}

// PublishEvents sends multiple events to all registered providers
func (m *MessagePublisherManager) PublishEvents(ctx context.Context, events []Event) error {
	for _, provider := range m.providers {
		go func(p MessagePublisher) {
			if err := p.PublishEvents(ctx, events); err != nil {
				slog.Warn("Failed to publish events to provider", "error", err)
			}
		}(provider)
	}
	return nil
}
