package service

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/observability"
)

// Event represents an event that can be published to message providers (webhooks, email, etc.)
type Event struct {
	ID            uuid.UUID           // Unique event id (UUID v7, time-ordered)
	Type          datatypes.EventType // Event type enum (e.g., FeedbackRecordCreated, WebhookCreated)
	Timestamp     time.Time           // Event creation time
	Data          any                 // Event data (FeedbackRecord, Webhook, etc.)
	ChangedFields []string            // Only for updates
}

// MessagePublisher defines the interface for publishing events.
type MessagePublisher interface {
	// PublishEvent publishes a single event with data (no changed fields)
	PublishEvent(ctx context.Context, eventType datatypes.EventType, data any)
	// PublishEventWithChangedFields publishes a single event with data and optional changed fields (for updates)
	PublishEventWithChangedFields(ctx context.Context, eventType datatypes.EventType, data any, changedFields []string)
	// Publish publishes a caller-built event. Used when work for the same event was already
	// enqueued transactionally under its ID, so every job and delivery for it correlates.
	Publish(ctx context.Context, event Event)
}

// NewEvent builds an event with a fresh time-ordered ID. Data is set by the caller once the
// written record exists.
func NewEvent(eventType datatypes.EventType, data any, changedFields []string) Event {
	return Event{
		ID:            uuid.Must(uuid.NewV7()),
		Type:          eventType,
		Timestamp:     time.Now(),
		Data:          data,
		ChangedFields: changedFields,
	}
}

// eventPublisher is the internal interface for providers that receive a full Event.
type eventPublisher interface {
	PublishEvent(ctx context.Context, event Event)
}

// MessagePublisherManager coordinates multiple message providers.
type MessagePublisherManager struct {
	eventChan       chan Event
	providers       []eventPublisher
	perEventTimeout time.Duration
	metrics         observability.EventMetrics
	wg              sync.WaitGroup
}

// NewMessagePublisherManager creates a new message publisher manager.
// bufferSize is the event channel capacity; perEventTimeout limits how long processing one event may take.
// metrics may be nil when metrics are disabled.
func NewMessagePublisherManager(
	bufferSize int, perEventTimeout time.Duration, metrics observability.EventMetrics,
) *MessagePublisherManager {
	m := &MessagePublisherManager{
		eventChan:       make(chan Event, bufferSize),
		providers:       make([]eventPublisher, 0),
		perEventTimeout: perEventTimeout,
		metrics:         metrics,
	}

	// Start the worker in a dedicated goroutine
	m.wg.Add(1)

	go m.startWorker()

	return m
}

// RegisterProvider registers a message provider (webhooks, email, SMS, etc.).
// Must only be called during startup, before any events are published.
func (m *MessagePublisherManager) RegisterProvider(provider eventPublisher) {
	m.providers = append(m.providers, provider)
}

// PublishEvent publishes an event with data to all registered providers (convenience for no changed fields).
func (m *MessagePublisherManager) PublishEvent(ctx context.Context, eventType datatypes.EventType, data any) {
	m.PublishEventWithChangedFields(ctx, eventType, data, nil)
}

// PublishEventWithChangedFields publishes an event with data to all registered providers.
func (m *MessagePublisherManager) PublishEventWithChangedFields(
	ctx context.Context, eventType datatypes.EventType, data any, changedFields []string,
) {
	m.Publish(ctx, NewEvent(eventType, data, changedFields))
}

// Publish hands a caller-built event to every registered provider. Delivery is best effort: a
// full channel drops the event, and a process exit loses whatever is buffered. Work that must not
// be lost is enqueued in the write's own transaction instead (see EmbeddingEnqueuer).
func (m *MessagePublisherManager) Publish(ctx context.Context, event Event) {
	select {
	case m.eventChan <- event:
		slog.Debug("Event published to channel", "event_id", event.ID, "event_type", event.Type)
	default:
		if m.metrics != nil {
			m.metrics.RecordEventDiscarded(ctx, event.Type.String())
		}

		slog.Warn("Event channel full, event dropped", "event_id", event.ID, "event_type", event.Type)
	}

	if m.metrics != nil {
		m.metrics.SetChannelDepth(len(m.eventChan))
	}
}

// Shutdown stops the background worker and waits for the buffer to drain.
func (m *MessagePublisherManager) Shutdown() {
	close(m.eventChan)
	m.wg.Wait()
}

// startWorker runs in a dedicated goroutine, reading events from the channel
// and fanning out each event to all registered providers. It is started with go
// in NewMessagePublisherManager and runs for the lifetime of the manager.
//
// Providers run CONCURRENTLY per event: with webhooks plus four enrichment enqueues each
// doing a DB round trip, a sequential fan-out capped throughput at a few hundred events/s —
// past which the bounded channel starts dropping events, permanently losing enrichment for
// the affected records. Events themselves stay strictly ordered (the next event starts only
// after every provider finished this one), so per-provider ordering guarantees (e.g. webhook
// delivery order) are unchanged.
func (m *MessagePublisherManager) startWorker() {
	defer m.wg.Done()

	bgCtx := context.Background()

	// This loop automatically breaks when m.eventChan is closed
	for event := range m.eventChan {
		if m.metrics != nil {
			m.metrics.SetChannelDepth(len(m.eventChan))
		}

		start := time.Now()
		// Timeout per event so one stuck provider doesn't freeze the worker
		ctx, cancel := context.WithTimeout(bgCtx, m.perEventTimeout)

		var fanOut sync.WaitGroup

		for _, provider := range m.providers {
			fanOut.Go(func() {
				// Isolate a provider panic to its own goroutine: an unrecovered panic here would
				// be process-fatal and take down the whole event pipeline, so recover, log, and
				// let the other providers (and fanOut.Wait) complete.
				defer func() {
					if r := recover(); r != nil {
						if m.metrics != nil {
							m.metrics.RecordProviderPanic(ctx, event.Type.String())
						}

						slog.Error("message provider panicked; isolating failure from the fan-out",
							"provider", fmt.Sprintf("%T", provider),
							"event_id", event.ID, "event_type", event.Type,
							"panic", r, "stack", string(debug.Stack()))
					}
				}()

				provider.PublishEvent(ctx, event)
			})
		}

		fanOut.Wait()
		cancel()

		if m.metrics != nil {
			m.metrics.RecordFanOutDuration(bgCtx, time.Since(start), event.Type.String())
		}
	}
}
