package observability

import (
	"fmt"

	"go.opentelemetry.io/otel/metric"
)

// Metrics holds all hub metric collectors. When metrics are disabled, all fields are nil.
// Components that accept an interface (EventMetrics, WebhookMetrics, CacheMetrics, APIMetrics) can
// receive the corresponding field; they already handle nil.
type Metrics struct {
	Events   EventMetrics
	Webhooks WebhookMetrics
	Cache    CacheMetrics
	API      APIMetrics
}

// NewMetrics creates EventMetrics, WebhookMetrics, and CacheMetrics from the given meter.
// Returns (nil, nil) when meter is nil (metrics disabled).
func NewMetrics(meter metric.Meter) (*Metrics, error) {
	if meter == nil {
		//nolint:nilnil // intentional: callers use "if metrics != nil" when metrics disabled
		return nil, nil
	}

	events, err := NewEventMetrics(meter)
	if err != nil {
		return nil, fmt.Errorf("event metrics: %w", err)
	}

	webhooks, err := NewWebhookMetrics(meter)
	if err != nil {
		return nil, fmt.Errorf("webhook metrics: %w", err)
	}

	cache, err := NewCacheMetrics(meter)
	if err != nil {
		return nil, fmt.Errorf("cache metrics: %w", err)
	}

	api, err := NewAPIMetrics(meter)
	if err != nil {
		return nil, fmt.Errorf("api metrics: %w", err)
	}

	return &Metrics{
		Events:   events,
		Webhooks: webhooks,
		Cache:    cache,
		API:      api,
	}, nil
}
