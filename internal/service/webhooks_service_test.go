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
	count        int64
	createResult *models.Webhook
	updateResult *models.Webhook
}

func (m *mockWebhooksRepo) Create(_ context.Context, _ *models.CreateWebhookRequest) (*models.Webhook, error) {
	return m.createResult, nil
}

func (m *mockWebhooksRepo) GetByID(_ context.Context, _ uuid.UUID) (*models.WebhookResponse, error) {
	return nil, nil
}

func (m *mockWebhooksRepo) GetByIDInternal(_ context.Context, _ uuid.UUID) (*models.Webhook, error) {
	return nil, nil
}

func (m *mockWebhooksRepo) List(_ context.Context, _ *models.ListWebhooksFilters) ([]models.WebhookResponse, error) {
	return nil, nil
}

func (m *mockWebhooksRepo) ListInternal(_ context.Context, _ *models.ListWebhooksFilters) ([]models.Webhook, error) {
	return nil, nil
}

func (m *mockWebhooksRepo) Count(_ context.Context, _ *models.ListWebhooksFilters) (int64, error) {
	return m.count, nil
}

func (m *mockWebhooksRepo) Update(_ context.Context, _ uuid.UUID, _ *models.UpdateWebhookRequest) (*models.Webhook, error) {
	return m.updateResult, nil
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

type capturingPublisher struct {
	eventType          datatypes.EventType
	eventData          any
	changedEventType   datatypes.EventType
	changedEventData   any
	changedEventFields []string
}

func (p *capturingPublisher) PublishEvent(_ context.Context, eventType datatypes.EventType, data any) {
	p.eventType = eventType
	p.eventData = data
}

func (p *capturingPublisher) PublishEventWithChangedFields(
	_ context.Context, eventType datatypes.EventType, data any, changedFields []string,
) {
	p.changedEventType = eventType
	p.changedEventData = data
	p.changedEventFields = changedFields
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

func TestWebhooksService_CreateWebhook_PublishesSanitizedPayload(t *testing.T) {
	ctx := context.Background()
	tenantID := "org-123"
	repoWebhook := &models.Webhook{
		ID:         uuid.Must(uuid.NewV7()),
		URL:        "https://example.com/webhook",
		SigningKey: "whsec_super_secret",
		Enabled:    true,
		TenantID:   &tenantID,
		EventTypes: []datatypes.EventType{datatypes.FeedbackRecordCreated},
	}
	repo := &mockWebhooksRepo{count: 0, createResult: repoWebhook}
	publisher := &capturingPublisher{}
	svc := NewWebhooksService(repo, publisher, 10)

	created, err := svc.CreateWebhook(ctx, &models.CreateWebhookRequest{
		URL: repoWebhook.URL,
	})
	if err != nil {
		t.Fatalf("CreateWebhook returned error: %v", err)
	}

	if created == nil || created.SigningKey == "" {
		t.Fatalf("CreateWebhook should still return internal model with signing key for internal callers")
	}

	if publisher.eventType != datatypes.WebhookCreated {
		t.Fatalf("published event type = %v, want %v", publisher.eventType, datatypes.WebhookCreated)
	}

	payload, ok := publisher.eventData.(*models.WebhookResponse)
	if !ok {
		t.Fatalf("published payload type = %T, want *models.WebhookResponse", publisher.eventData)
	}

	if payload.ID != repoWebhook.ID {
		t.Fatalf("payload ID = %v, want %v", payload.ID, repoWebhook.ID)
	}
}

func TestWebhooksService_UpdateWebhook_PublishesSanitizedPayload(t *testing.T) {
	ctx := context.Background()
	updatedURL := "https://example.com/webhook-v2"
	repoWebhook := &models.Webhook{
		ID:         uuid.Must(uuid.NewV7()),
		URL:        updatedURL,
		SigningKey: "whsec_super_secret_rotated",
		Enabled:    true,
		EventTypes: []datatypes.EventType{datatypes.FeedbackRecordUpdated},
	}
	repo := &mockWebhooksRepo{count: 0, updateResult: repoWebhook}
	publisher := &capturingPublisher{}
	svc := NewWebhooksService(repo, publisher, 10)
	req := &models.UpdateWebhookRequest{URL: &updatedURL}

	_, err := svc.UpdateWebhook(ctx, repoWebhook.ID, req)
	if err != nil {
		t.Fatalf("UpdateWebhook returned error: %v", err)
	}

	if publisher.changedEventType != datatypes.WebhookUpdated {
		t.Fatalf("published event type = %v, want %v", publisher.changedEventType, datatypes.WebhookUpdated)
	}

	payload, ok := publisher.changedEventData.(*models.WebhookResponse)
	if !ok {
		t.Fatalf("published payload type = %T, want *models.WebhookResponse", publisher.changedEventData)
	}

	if payload.URL != updatedURL {
		t.Fatalf("payload URL = %q, want %q", payload.URL, updatedURL)
	}

	if len(publisher.changedEventFields) != 1 || publisher.changedEventFields[0] != "url" {
		t.Fatalf("changed fields = %v, want [url]", publisher.changedEventFields)
	}
}
