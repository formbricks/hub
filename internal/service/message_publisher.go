package service

import (
	"context"
	"time"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/google/uuid"
)

// Event represents an event that can be published to message providers (webhooks, email, etc.)
type Event struct {
	ID            uuid.UUID           // Unique event id (UUID v7, time-ordered)
	Type          datatypes.EventType // Event type enum (e.g., FeedbackRecordCreated, WebhookCreated)
	Timestamp     int64               // Unix timestamp
	Data          any                 // Event data (FeedbackRecord, Webhook, etc.)
	ChangedFields []string            // Only for updates
}

// MessagePublisher defines the interface for publishing events
type MessagePublisher interface {
	// PublishEvent publishes a single event with data (no changed fields)
	PublishEvent(ctx context.Context, eventType datatypes.EventType, data any)
	// PublishEventWithChangedFields publishes a single event with data and optional changed fields (for updates)
	PublishEventWithChangedFields(ctx context.Context, eventType datatypes.EventType, data any, changedFields []string)
}

// eventPublisher is the internal interface for providers that receive a full Event
type eventPublisher interface {
	PublishEvent(ctx context.Context, event Event)
}

// MessagePublisherManager coordinates multiple message providers
type MessagePublisherManager struct {
	providers []eventPublisher
}

// NewMessagePublisherManager creates a new message publisher manager
func NewMessagePublisherManager() *MessagePublisherManager {
	return &MessagePublisherManager{
		providers: make([]eventPublisher, 0),
	}
}

// RegisterProvider registers a message provider (webhooks, email, SMS, etc.)
func (m *MessagePublisherManager) RegisterProvider(provider eventPublisher) {
	m.providers = append(m.providers, provider)
}

// PublishEvent publishes an event with data to all registered providers (convenience for no changed fields)
func (m *MessagePublisherManager) PublishEvent(ctx context.Context, eventType datatypes.EventType, data any) {
	m.PublishEventWithChangedFields(ctx, eventType, data, nil)
}

// PublishEventWithChangedFields publishes an event with data to all registered providers
func (m *MessagePublisherManager) PublishEventWithChangedFields(ctx context.Context, eventType datatypes.EventType, data any, changedFields []string) {
	event := Event{
		ID:            uuid.Must(uuid.NewV7()),
		Type:          eventType,
		Timestamp:     time.Now().Unix(),
		Data:          data,
		ChangedFields: changedFields,
	}
	for _, provider := range m.providers {
		go provider.PublishEvent(ctx, event)
	}
}
