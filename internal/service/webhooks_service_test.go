package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

type mockWebhooksRepo struct {
	count int64
}

func (m *mockWebhooksRepo) Create(_ context.Context, _ *models.CreateWebhookRequest) (*models.Webhook, error) {
	return nil, nil
}

func (m *mockWebhooksRepo) GetByID(_ context.Context, _ uuid.UUID) (*models.Webhook, error) {
	return nil, nil
}

func (m *mockWebhooksRepo) List(_ context.Context, _ *models.ListWebhooksFilters) ([]models.Webhook, error) {
	return nil, nil
}

func (m *mockWebhooksRepo) Count(_ context.Context, _ *models.ListWebhooksFilters) (int64, error) {
	return m.count, nil
}

func (m *mockWebhooksRepo) Update(_ context.Context, _ uuid.UUID, _ *models.UpdateWebhookRequest) (*models.Webhook, error) {
	return nil, nil
}

func (m *mockWebhooksRepo) Delete(_ context.Context, _ uuid.UUID) error {
	return nil
}

func (m *mockWebhooksRepo) ListEnabled(_ context.Context) ([]models.Webhook, error) {
	return nil, nil
}

func (m *mockWebhooksRepo) ListEnabledForEventType(_ context.Context, _ string) ([]models.Webhook, error) {
	return nil, nil
}

type noopPublisher struct{}

func (noopPublisher) PublishEvent(_ context.Context, _ datatypes.EventType, _ any) {}

func (noopPublisher) PublishEventWithChangedFields(_ context.Context, _ datatypes.EventType, _ any, _ []string) {
}

func TestWebhooksService_CreateWebhook_InvalidSigningKey(t *testing.T) {
	ctx := context.Background()
	svc := NewWebhooksService(&mockWebhooksRepo{count: 0}, noopPublisher{}, 10)

	req := &models.CreateWebhookRequest{
		URL:        "https://example.com/webhook",
		SigningKey: "not-valid",
		EventTypes: []datatypes.EventType{datatypes.FeedbackRecordCreated},
	}

	_, err := svc.CreateWebhook(ctx, req)
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestWebhooksService_UpdateWebhook_InvalidSigningKey(t *testing.T) {
	ctx := context.Background()
	svc := NewWebhooksService(&mockWebhooksRepo{count: 0}, noopPublisher{}, 10)
	id := uuid.Must(uuid.NewV7())
	badKey := "bad_key"
	req := &models.UpdateWebhookRequest{
		SigningKey: &badKey,
	}

	_, err := svc.UpdateWebhook(ctx, id, req)
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}
