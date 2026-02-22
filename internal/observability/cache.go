package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// CacheMetrics records cache hit/miss metrics with bounded cardinality (cache name).
type CacheMetrics interface {
	RecordHit(ctx context.Context, cacheName string)
	RecordMiss(ctx context.Context, cacheName string)
}

// cacheMetrics implements CacheMetrics.
type cacheMetrics struct {
	hits   metric.Int64Counter
	misses metric.Int64Counter
}

// NewCacheMetrics creates CacheMetrics. Returns (nil, nil) when meter is nil (metrics disabled).
func NewCacheMetrics(meter metric.Meter) (CacheMetrics, error) {
	if meter == nil {
		//nolint:nilnil // intentional: callers use "if metrics != nil" when metrics disabled
		return nil, nil
	}

	hitDesc := "Number of cache lookups that returned a cached value. " +
		"Label cache: webhook_list, webhook_get_by_id. " +
		"Hit ratio = rate(hits) / (rate(hits) + rate(misses)) per cache."

	hits, err := meter.Int64Counter(
		MetricNameCacheHits, metric.WithDescription(hitDesc), metric.WithUnit("1"),
	)
	if err != nil {
		return nil, fmt.Errorf("create cache hits counter: %w", err)
	}

	missDesc := "Number of cache lookups that missed and triggered a load from the backing store. " +
		"Label cache: webhook_list, webhook_get_by_id."

	misses, err := meter.Int64Counter(
		MetricNameCacheMisses, metric.WithDescription(missDesc), metric.WithUnit("1"),
	)
	if err != nil {
		return nil, fmt.Errorf("create cache misses counter: %w", err)
	}

	return &cacheMetrics{hits: hits, misses: misses}, nil
}

func attrCache(name string) attribute.KeyValue {
	return attribute.String("cache", NormalizeCacheName(name))
}

func (c *cacheMetrics) RecordHit(ctx context.Context, cacheName string) {
	c.hits.Add(ctx, 1, metric.WithAttributes(attrCache(cacheName)))
}

func (c *cacheMetrics) RecordMiss(ctx context.Context, cacheName string) {
	c.misses.Add(ctx, 1, metric.WithAttributes(attrCache(cacheName)))
}
