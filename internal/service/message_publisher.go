package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/google/uuid"
)

// eventChanBufferSize is the buffer size for the event channel (creates backpressure when full).
const eventChanBufferSize = 1024

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
	eventChan chan Event
	providers []eventPublisher
}

// NewMessagePublisherManager creates a new message publisher manager
func NewMessagePublisherManager() *MessagePublisherManager {
	m := &MessagePublisherManager{
		eventChan: make(chan Event, eventChanBufferSize),
		providers: make([]eventPublisher, 0),
	}

	// Start the worker in a dedicated goroutine
	go m.startWorker()

	return m
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

	select {
	case m.eventChan <- event:
		slog.Debug("Event published to channel", "event_id", event.ID, "event_type", event.Type)
	default:
		slog.Warn("Event channel full, event dropped", "event_id", event.ID, "event_type", event.Type)
	}
}

// startWorker runs in a dedicated goroutine, reading events from the channel
// and fanning out each event to all registered providers. It is started with go
// in NewMessagePublisherManager and runs for the lifetime of the manager.
func (m *MessagePublisherManager) startWorker() {
	ctx := context.Background()

	for event := range m.eventChan {
		for _, provider := range m.providers {
			provider.PublishEvent(ctx, event)
		}
	}
}
