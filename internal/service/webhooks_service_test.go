package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

type mockWebhooksRepo struct {
	count     int64
	webhook   *models.Webhook
	deletedID uuid.UUID
}

func (m *mockWebhooksRepo) Create(_ context.Context, _ *models.CreateWebhookRequest) (*models.Webhook, error) {
	return nil, nil
}

func (m *mockWebhooksRepo) GetByID(_ context.Context, _ uuid.UUID) (*models.Webhook, error) {
	if m.webhook != nil {
		return m.webhook, nil
	}

	return nil, nil
}

func (m *mockWebhooksRepo) List(_ context.Context, _ *models.ListWebhooksFilters) ([]models.Webhook, bool, error) {
	return nil, false, nil
}

func (m *mockWebhooksRepo) ListAfterCursor(
	_ context.Context, _ *models.ListWebhooksFilters, _ time.Time, _ uuid.UUID,
) ([]models.Webhook, bool, error) {
	return nil, false, nil
}

func (m *mockWebhooksRepo) Count(_ context.Context, _ *models.ListWebhooksFilters) (int64, error) {
	return m.count, nil
}

func (m *mockWebhooksRepo) Update(_ context.Context, _ uuid.UUID, _ *models.UpdateWebhookRequest) (*models.Webhook, error) {
	return nil, nil
}

func (m *mockWebhooksRepo) Delete(_ context.Context, id uuid.UUID) error {
	m.deletedID = id

	return nil
}

type noopPublisher struct{}

func (noopPublisher) PublishEvent(_ context.Context, _ datatypes.EventType, _ any) {}

func (noopPublisher) PublishEventWithChangedFields(_ context.Context, _ datatypes.EventType, _ any, _ []string) {
}

type capturePublisher struct {
	eventType     datatypes.EventType
	data          any
	changedFields []string
	callCount     int
	events        []capturedEvent
}

type capturedEvent struct {
	eventType     datatypes.EventType
	data          any
	changedFields []string
}

func (p *capturePublisher) PublishEvent(_ context.Context, eventType datatypes.EventType, data any) {
	p.eventType = eventType
	p.data = data
	p.callCount++
	p.events = append(p.events, capturedEvent{eventType: eventType, data: data})
}

func (p *capturePublisher) PublishEventWithChangedFields(
	_ context.Context, eventType datatypes.EventType, data any, changedFields []string,
) {
	p.eventType = eventType
	p.data = data
	p.changedFields = changedFields
	p.callCount++
	p.events = append(p.events, capturedEvent{eventType: eventType, data: data, changedFields: changedFields})
}

func TestWebhooksService_CreateWebhook_InvalidSigningKey(t *testing.T) {
	ctx := context.Background()
	svc := NewWebhooksService(&mockWebhooksRepo{count: 0}, noopPublisher{}, 10, nil)
	tenantID := "org-123"

	req := &models.CreateWebhookRequest{
		URL:        "https://example.com/webhook",
		SigningKey: "not-valid",
		TenantID:   &tenantID,
		EventTypes: []datatypes.EventType{datatypes.FeedbackRecordCreated},
	}

	_, err := svc.CreateWebhook(ctx, req)
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestWebhooksService_UpdateWebhook_InvalidSigningKey(t *testing.T) {
	ctx := context.Background()
	svc := NewWebhooksService(&mockWebhooksRepo{count: 0}, noopPublisher{}, 10, nil)
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

// ssrfBlacklist is used by SSRF validation tests (matches default config: localhost, loopback, cloud metadata).
var ssrfBlacklist = map[string]struct{}{
	"localhost":       {},
	"127.0.0.1":       {},
	"::1":             {},
	"169.254.169.254": {},
	"blocked.local":   {},
}

func TestWebhooksService_CreateWebhook_RejectsSSRFHosts(t *testing.T) {
	ctx := context.Background()
	svc := NewWebhooksService(&mockWebhooksRepo{count: 0}, noopPublisher{}, 10, ssrfBlacklist)
	validKey := "whsec_" + "abcdefghijklmnopqrstuvwxyz123456"
	tenantID := "org-123"

	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{"loopback IPv4 (blacklisted)", "https://127.0.0.1/webhook", "blacklisted"},
		{"loopback IPv6 (blacklisted)", "https://[::1]/webhook", "blacklisted"},
		{"private range (IP check, not in blacklist)", "https://10.0.0.1/webhook", "private/internal"},
		{"blacklisted hostname", "https://blocked.local/webhook", "blacklisted"},
		{"blacklisted IP (cloud metadata)", "https://169.254.169.254/metadata", "blacklisted"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &models.CreateWebhookRequest{
				URL:        tt.url,
				SigningKey: validKey,
				TenantID:   &tenantID,
				EventTypes: []datatypes.EventType{datatypes.FeedbackRecordCreated},
			}

			_, err := svc.CreateWebhook(ctx, req)
			if !errors.Is(err, huberrors.ErrValidation) {
				t.Fatalf("expected ErrValidation, got %v", err)
			}

			var verr *huberrors.ValidationError
			if errors.As(err, &verr) && tt.wantErr != "" && !strings.Contains(verr.Message, tt.wantErr) {
				t.Errorf("error message %q does not contain %q", verr.Message, tt.wantErr)
			}
		})
	}
}

func TestWebhooksService_CreateWebhook_RequiresTenantID(t *testing.T) {
	ctx := context.Background()
	svc := NewWebhooksService(&mockWebhooksRepo{count: 0}, noopPublisher{}, 10, nil)

	req := &models.CreateWebhookRequest{
		URL:        "https://example.com/webhook",
		SigningKey: "whsec_" + "abcdefghijklmnopqrstuvwxyz123456",
		EventTypes: []datatypes.EventType{datatypes.FeedbackRecordCreated},
	}

	_, err := svc.CreateWebhook(ctx, req)
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestWebhooksService_UpdateWebhook_RejectsEmptyTenantID(t *testing.T) {
	ctx := context.Background()
	svc := NewWebhooksService(&mockWebhooksRepo{count: 0}, noopPublisher{}, 10, nil)
	id := uuid.Must(uuid.NewV7())
	tenantID := "   "

	req := &models.UpdateWebhookRequest{TenantID: &tenantID}

	_, err := svc.UpdateWebhook(ctx, id, req)
	if !errors.Is(err, huberrors.ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestWebhooksService_DeleteWebhook_PublishesTenantAwareDeletedEvent(t *testing.T) {
	ctx := context.Background()
	webhookID := uuid.Must(uuid.NewV7())
	tenantID := "org-123"
	repo := &mockWebhooksRepo{webhook: &models.Webhook{ID: webhookID, TenantID: &tenantID}}
	publisher := &capturePublisher{}
	svc := NewWebhooksService(repo, publisher, 10, nil)

	err := svc.DeleteWebhook(ctx, webhookID)
	if err != nil {
		t.Fatalf("DeleteWebhook() error = %v", err)
	}

	if repo.deletedID != webhookID {
		t.Fatalf("deletedID = %v, want %v", repo.deletedID, webhookID)
	}

	if publisher.callCount != 1 || publisher.eventType != datatypes.WebhookDeleted {
		t.Fatalf("published event = (%d, %s), want one webhook.deleted", publisher.callCount, publisher.eventType)
	}

	data, ok := publisher.data.(models.DeletedIDsEventData)
	if !ok {
		t.Fatalf("published data type = %T, want DeletedIDsEventData", publisher.data)
	}

	if data.TenantID != tenantID {
		t.Errorf("TenantID = %q, want %q", data.TenantID, tenantID)
	}

	if len(data.IDs) != 1 || data.IDs[0] != webhookID {
		t.Errorf("IDs = %v, want [%v]", data.IDs, webhookID)
	}
}

func TestWebhooksService_UpdateWebhook_RejectsSSRFHosts(t *testing.T) {
	ctx := context.Background()
	svc := NewWebhooksService(&mockWebhooksRepo{count: 0}, noopPublisher{}, 10, ssrfBlacklist)
	id := uuid.Must(uuid.NewV7())

	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{"loopback IPv4 (blacklisted)", "https://127.0.0.1/webhook", "blacklisted"},
		{"private range", "https://10.0.0.1/webhook", "private/internal"},
		{"blacklisted hostname", "https://blocked.local/webhook", "blacklisted"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := tt.url
			req := &models.UpdateWebhookRequest{URL: &url}

			_, err := svc.UpdateWebhook(ctx, id, req)
			if !errors.Is(err, huberrors.ErrValidation) {
				t.Fatalf("expected ErrValidation, got %v", err)
			}
		})
	}
}
