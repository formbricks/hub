package observability

import (
	"fmt"

	"go.opentelemetry.io/otel/metric"
)

// Metrics holds all hub metric collectors (events, webhooks, embeddings, cache).
// When metrics are disabled, Metrics is nil and all fields are nil.
// Components that accept an interface (EventMetrics, WebhookMetrics, EmbeddingMetrics, CacheMetrics)
// can receive the corresponding field; they already handle nil.
type Metrics struct {
	Events     EventMetrics
	Webhooks   WebhookMetrics
	Embeddings EmbeddingMetrics
	Cache      CacheMetrics
}

// NewMetrics creates EventMetrics, WebhookMetrics, and EmbeddingMetrics from the given meter.
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

	embeddings, err := NewEmbeddingMetrics(meter)
	if err != nil {
		return nil, fmt.Errorf("embedding metrics: %w", err)
	}

	cache, err := NewCacheMetrics(meter)
	if err != nil {
		return nil, fmt.Errorf("cache metrics: %w", err)
	}

	return &Metrics{
		Events:     events,
		Webhooks:   webhooks,
		Embeddings: embeddings,
		Cache:      cache,
	}, nil
}
