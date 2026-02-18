package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/pkg/cache"
)

const (
	cacheNameWebhookList    = "webhook_list"
	cacheNameWebhookGetByID = "webhook_get_by_id"
)

// cachingWebhooksRepo wraps a WebhooksRepository with caches for ListEnabledForEventType and GetByID.
type cachingWebhooksRepo struct {
	inner        WebhooksRepository
	listCache    *cache.LoaderCache[string, []models.Webhook]
	getByIDCache *cache.LoaderCache[uuid.UUID, *models.Webhook]
	metrics      observability.CacheMetrics
}

// NewCachingWebhooksRepository returns a WebhooksRepository that caches ListEnabledForEventType and GetByID.
// listCache is invalidated on Create, Update, Delete. getByIDCache is invalidated per ID on Update and Delete.
// metrics may be nil (no cache metrics recorded).
func NewCachingWebhooksRepository(
	inner WebhooksRepository,
	listCache *cache.LoaderCache[string, []models.Webhook],
	getByIDCache *cache.LoaderCache[uuid.UUID, *models.Webhook],
	metrics observability.CacheMetrics,
) WebhooksRepository {
	return &cachingWebhooksRepo{
		inner:        inner,
		listCache:    listCache,
		getByIDCache: getByIDCache,
		metrics:      metrics,
	}
}

func (r *cachingWebhooksRepo) Create(ctx context.Context, req *models.CreateWebhookRequest) (*models.Webhook, error) {
	w, err := r.inner.Create(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("create webhook: %w", err)
	}

	r.listCache.InvalidateAll()

	return w, nil
}

func (r *cachingWebhooksRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.Webhook, error) {
	if r.metrics != nil {
		w, hit, err := r.getByIDCache.GetWithStats(ctx, id, r.inner.GetByID)
		if err != nil {
			return nil, fmt.Errorf("get webhook by id: %w", err)
		}

		if hit {
			r.metrics.RecordHit(ctx, cacheNameWebhookGetByID)
		} else {
			r.metrics.RecordMiss(ctx, cacheNameWebhookGetByID)
		}

		return w, nil
	}

	w, err := r.getByIDCache.Get(ctx, id, r.inner.GetByID)
	if err != nil {
		return nil, fmt.Errorf("get webhook by id: %w", err)
	}

	return w, nil
}

func (r *cachingWebhooksRepo) List(ctx context.Context, filters *models.ListWebhooksFilters) ([]models.Webhook, error) {
	webhooks, err := r.inner.List(ctx, filters)
	if err != nil {
		return nil, fmt.Errorf("list webhooks: %w", err)
	}

	return webhooks, nil
}

func (r *cachingWebhooksRepo) Count(ctx context.Context, filters *models.ListWebhooksFilters) (int64, error) {
	n, err := r.inner.Count(ctx, filters)
	if err != nil {
		return 0, fmt.Errorf("count webhooks: %w", err)
	}

	return n, nil
}

func (r *cachingWebhooksRepo) Update(ctx context.Context, id uuid.UUID, req *models.UpdateWebhookRequest) (*models.Webhook, error) {
	w, err := r.inner.Update(ctx, id, req)
	if err != nil {
		return nil, fmt.Errorf("update webhook: %w", err)
	}

	r.listCache.InvalidateAll()
	r.getByIDCache.Invalidate(id)

	return w, nil
}

func (r *cachingWebhooksRepo) Delete(ctx context.Context, id uuid.UUID) error {
	if err := r.inner.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete webhook: %w", err)
	}

	r.listCache.InvalidateAll()
	r.getByIDCache.Invalidate(id)

	return nil
}

func (r *cachingWebhooksRepo) ListEnabled(ctx context.Context) ([]models.Webhook, error) {
	webhooks, err := r.inner.ListEnabled(ctx)
	if err != nil {
		return nil, fmt.Errorf("list enabled webhooks: %w", err)
	}

	return webhooks, nil
}

func (r *cachingWebhooksRepo) ListEnabledForEventType(ctx context.Context, eventType string) ([]models.Webhook, error) {
	if r.metrics != nil {
		webhooks, hit, err := r.listCache.GetWithStats(ctx, eventType, func(ctx context.Context, key string) ([]models.Webhook, error) {
			return r.inner.ListEnabledForEventType(ctx, key)
		})
		if err != nil {
			return nil, fmt.Errorf("list enabled for event type: %w", err)
		}

		if hit {
			r.metrics.RecordHit(ctx, cacheNameWebhookList)
		} else {
			r.metrics.RecordMiss(ctx, cacheNameWebhookList)
		}

		return webhooks, nil
	}

	webhooks, err := r.listCache.Get(ctx, eventType, func(ctx context.Context, key string) ([]models.Webhook, error) {
		return r.inner.ListEnabledForEventType(ctx, key)
	})
	if err != nil {
		return nil, fmt.Errorf("list enabled for event type: %w", err)
	}

	return webhooks, nil
}
