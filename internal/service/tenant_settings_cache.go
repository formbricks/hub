package service

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
)

// cacheNameTenantSettings is the bounded-cardinality metrics label for the
// tenant-settings cache (registered in observability.allowedCacheNames).
const cacheNameTenantSettings = "tenant_settings"

// TenantSettingsReader is the read surface the cache wraps. *TenantSettingsService
// satisfies it, so the cache is a drop-in for any consumer that only needs reads
// (the translation enqueue gate and worker).
type TenantSettingsReader interface {
	GetSettings(ctx context.Context, tenantID string) (*models.TenantSettings, error)
}

// CachedTenantSettings wraps a TenantSettingsReader with a per-process,
// size-bounded, TTL-expiring LRU cache. Feedback-record creation is high volume and
// the translation enqueue gate must resolve a tenant's target language per event,
// so caching avoids a tenant_settings read on every feedback event. Staleness is
// bounded by the TTL; because the worker persists the target it actually used, a
// changed target self-corrects on the next write. Safe for concurrent use
// (expirable.LRU is internally locked).
type CachedTenantSettings struct {
	delegate TenantSettingsReader
	cache    *expirable.LRU[string, *models.TenantSettings]
	metrics  observability.CacheMetrics // nil when metrics are disabled
}

// NewCachedTenantSettings wraps delegate with an LRU of at most size entries, each
// expiring after ttl. A non-positive size or ttl disables caching (every read hits
// the delegate), keeping small deployments and tests simple.
func NewCachedTenantSettings(
	delegate TenantSettingsReader, size int, ttl time.Duration, metrics observability.CacheMetrics,
) *CachedTenantSettings {
	cached := &CachedTenantSettings{delegate: delegate, metrics: metrics}
	if size > 0 && ttl > 0 {
		cached.cache = expirable.NewLRU[string, *models.TenantSettings](size, nil, ttl)
	}

	return cached
}

// GetSettings returns the tenant's settings, serving a fresh cached value when
// present and otherwise loading from the delegate and caching the result. Errors
// are never cached.
func (c *CachedTenantSettings) GetSettings(
	ctx context.Context, tenantID string,
) (*models.TenantSettings, error) {
	if c.cache != nil {
		if settings, ok := c.cache.Get(tenantID); ok {
			if c.metrics != nil {
				c.metrics.RecordHit(ctx, cacheNameTenantSettings)
			}

			return settings, nil
		}

		if c.metrics != nil {
			c.metrics.RecordMiss(ctx, cacheNameTenantSettings)
		}
	}

	settings, err := c.delegate.GetSettings(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load tenant settings: %w", err)
	}

	if c.cache != nil {
		c.cache.Add(tenantID, settings)
	}

	return settings, nil
}
