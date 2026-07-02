package observability

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// EventMetrics records event-pipeline and message-publisher metrics.
// Methods accept ctx for future exemplar support (linking metric samples to trace IDs).
type EventMetrics interface {
	RecordEventDiscarded(ctx context.Context, eventType string)
	RecordFanOutDuration(ctx context.Context, duration time.Duration, eventType string)
	SetChannelDepth(depth int)
	// SetRiverQueueDepth records the backlog of one named queue. The queue label is emitted on
	// the gauge, so cardinality is bounded by the caller's fixed queue set (the poller's).
	SetRiverQueueDepth(queue string, depth int)
}

// eventMetrics implements EventMetrics.
type eventMetrics struct {
	eventsDiscarded   metric.Int64Counter
	fanOutDuration    metric.Float64Histogram
	channelDepth      atomic.Int64
	channelDepthGauge metric.Float64ObservableGauge
	riverQueueGauge   metric.Float64ObservableGauge

	// riverQueueDepths holds the latest polled backlog per queue name (a small fixed set from
	// the poller), read by the observable-gauge callback.
	riverQueueMu     sync.Mutex
	riverQueueDepths map[string]int64
}

// NewEventMetrics creates EventMetrics and registers gauges. Returns (nil, nil) when meter is nil (metrics disabled).
func NewEventMetrics(meter metric.Meter) (EventMetrics, error) {
	if meter == nil {
		//nolint:nilnil // intentional: callers use "if metrics != nil" when metrics disabled
		return nil, nil
	}

	eventsDiscarded, err := meter.Int64Counter(
		MetricNameEventsDiscarded,
		metric.WithDescription("Total number of events discarded (channel full)"),
	)
	if err != nil {
		return nil, fmt.Errorf("create events discarded counter: %w", err)
	}

	fanOutDuration, err := meter.Float64Histogram(
		MetricNameFanOutDuration,
		metric.WithDescription("Time to process one event across all providers (seconds)"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create fan-out duration histogram: %w", err)
	}

	evtMetrics := &eventMetrics{
		eventsDiscarded:  eventsDiscarded,
		fanOutDuration:   fanOutDuration,
		riverQueueDepths: map[string]int64{},
	}

	channelDepthGauge, err := meter.Float64ObservableGauge(
		MetricNameEventChannelDepth,
		metric.WithDescription("Current event channel depth"),
		metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
			o.Observe(float64(evtMetrics.channelDepth.Load()))

			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("create channel depth gauge: %w", err)
	}

	evtMetrics.channelDepthGauge = channelDepthGauge

	riverQueueGauge, err := meter.Float64ObservableGauge(
		MetricNameRiverQueueDepth,
		metric.WithDescription("Current River job queue depth per queue (available/retryable/scheduled)"),
		metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
			evtMetrics.riverQueueMu.Lock()
			defer evtMetrics.riverQueueMu.Unlock()

			for queue, depth := range evtMetrics.riverQueueDepths {
				o.Observe(float64(depth), metric.WithAttributes(attribute.String(AttrQueue, queue)))
			}

			return nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("create river queue depth gauge: %w", err)
	}

	evtMetrics.riverQueueGauge = riverQueueGauge

	return evtMetrics, nil
}

func attrEventType(v string) attribute.KeyValue {
	return attribute.String(AttrEventType, v)
}

func (e *eventMetrics) RecordEventDiscarded(ctx context.Context, eventType string) {
	eventType = NormalizeEventType(eventType)
	e.eventsDiscarded.Add(ctx, 1, metric.WithAttributes(attrEventType(eventType)))
}

func (e *eventMetrics) RecordFanOutDuration(ctx context.Context, duration time.Duration, eventType string) {
	eventType = NormalizeEventType(eventType)
	e.fanOutDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(attrEventType(eventType)))
}

func (e *eventMetrics) SetChannelDepth(depth int) {
	e.channelDepth.Store(int64(depth))
}

func (e *eventMetrics) SetRiverQueueDepth(queue string, depth int) {
	e.riverQueueMu.Lock()
	defer e.riverQueueMu.Unlock()

	e.riverQueueDepths[queue] = int64(depth)
}
