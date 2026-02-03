package service

import (
	"context"
	"time"

	"github.com/formbricks/hub/internal/datatypes"
)

// Event represents an event that can be published to message providers (webhooks, email, etc.)
type Event struct {
	Type          datatypes.EventType // Event type enum (e.g., FeedbackRecordCreated, WebhookCreated)
	Timestamp     int64               // Unix timestamp
	Data          any                 // Event data (FeedbackRecord, Webhook, etc.)
	ChangedFields []string            // Only for updates
}

// MessagePublisher defines the interface for publishing events
type MessagePublisher interface {
	// PublishEvent publishes a single event immediately
	PublishEvent(ctx context.Context, event Event)
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
func (m *MessagePublisherManager) PublishEvent(ctx context.Context, event Event) {
	event.Timestamp = time.Now().Unix()
	// Send to all providers in parallel (goroutines)
	// Collect errors but don't fail if one provider fails
	for _, provider := range m.providers {
		go provider.PublishEvent(ctx, event)
	}
}
