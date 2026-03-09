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
	count int64
}

func (m *mockWebhooksRepo) Create(_ context.Context, _ *models.CreateWebhookRequest) (*models.Webhook, error) {
	return nil, nil
}

func (m *mockWebhooksRepo) GetByID(_ context.Context, _ uuid.UUID) (*models.Webhook, error) {
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
	svc := NewWebhooksService(&mockWebhooksRepo{count: 0}, noopPublisher{}, 10, nil)

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
