package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/pkg/cache"
)

// countingWebhooksRepo implements WebhooksRepository and counts calls to ListEnabledForEventType and GetByID.
type countingWebhooksRepo struct {
	listEnabledForEventTypeCalls int
	getByIDCalls                 int
	listResult                   []models.Webhook
	getByIDResult                *models.Webhook
	getByIDErr                   error
}

func (c *countingWebhooksRepo) Create(_ context.Context, _ *models.CreateWebhookRequest) (*models.Webhook, error) {
	return nil, errors.New("not implemented")
}

func (c *countingWebhooksRepo) GetByID(_ context.Context, _ uuid.UUID) (*models.Webhook, error) {
	c.getByIDCalls++
	if c.getByIDErr != nil {
		return nil, c.getByIDErr
	}

	return c.getByIDResult, nil
}

func (c *countingWebhooksRepo) List(_ context.Context, _ *models.ListWebhooksFilters) ([]models.Webhook, error) {
	return nil, errors.New("not implemented")
}

func (c *countingWebhooksRepo) Count(_ context.Context, _ *models.ListWebhooksFilters) (int64, error) {
	return 0, errors.New("not implemented")
}

func (c *countingWebhooksRepo) Update(_ context.Context, _ uuid.UUID, _ *models.UpdateWebhookRequest) (*models.Webhook, error) {
	return nil, errors.New("not implemented")
}

func (c *countingWebhooksRepo) Delete(_ context.Context, _ uuid.UUID) error {
	return errors.New("not implemented")
}

func (c *countingWebhooksRepo) ListEnabled(_ context.Context) ([]models.Webhook, error) {
	return nil, errors.New("not implemented")
}

func (c *countingWebhooksRepo) ListEnabledForEventType(_ context.Context, _ string) ([]models.Webhook, error) {
	c.listEnabledForEventTypeCalls++

	return c.listResult, nil
}

func TestCachingWebhooksRepository_ListEnabledForEventType_cached(t *testing.T) {
	inner := &countingWebhooksRepo{listResult: []models.Webhook{}}

	listCache, err := cache.NewLoaderCache[string, []models.Webhook](4, func(s string) string { return s })
	if err != nil {
		t.Fatal(err)
	}

	getByIDCache, err := cache.NewLoaderCache[uuid.UUID, *models.Webhook](4, func(id uuid.UUID) string { return id.String() })
	if err != nil {
		t.Fatal(err)
	}

	repo := NewCachingWebhooksRepository(inner, listCache, getByIDCache, nil)
	ctx := context.Background()

	_, _ = repo.ListEnabledForEventType(ctx, "feedback.created")
	_, _ = repo.ListEnabledForEventType(ctx, "feedback.created")

	if inner.listEnabledForEventTypeCalls != 1 {
		t.Errorf("ListEnabledForEventType calls = %d, want 1 (second call cached)", inner.listEnabledForEventTypeCalls)
	}
}

func TestCachingWebhooksRepository_GetByID_cached(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	w := &models.Webhook{ID: id}
	inner := &countingWebhooksRepo{getByIDResult: w}

	listCache, err := cache.NewLoaderCache[string, []models.Webhook](4, func(s string) string { return s })
	if err != nil {
		t.Fatal(err)
	}

	getByIDCache, err := cache.NewLoaderCache[uuid.UUID, *models.Webhook](4, func(u uuid.UUID) string { return u.String() })
	if err != nil {
		t.Fatal(err)
	}

	repo := NewCachingWebhooksRepository(inner, listCache, getByIDCache, nil)
	ctx := context.Background()

	_, _ = repo.GetByID(ctx, id)
	_, _ = repo.GetByID(ctx, id)

	if inner.getByIDCalls != 1 {
		t.Errorf("GetByID calls = %d, want 1 (second call cached)", inner.getByIDCalls)
	}
}
