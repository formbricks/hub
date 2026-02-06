package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	"github.com/formbricks/hub/internal/models"
)

type mockSenderRepo struct {
	updateCalled bool
	updateErr    error
}

func (m *mockSenderRepo) Update(_ context.Context, _ uuid.UUID, _ *models.UpdateWebhookRequest) (*models.Webhook, error) {
	m.updateCalled = true

	return nil, m.updateErr
}

func (m *mockSenderRepo) Create(_ context.Context, _ *models.CreateWebhookRequest) (*models.Webhook, error) {
	return nil, nil
}

func (m *mockSenderRepo) GetByID(_ context.Context, _ uuid.UUID) (*models.Webhook, error) {
	return nil, nil
}

func (m *mockSenderRepo) List(_ context.Context, _ *models.ListWebhooksFilters) ([]models.Webhook, error) {
	return nil, nil
}

func (m *mockSenderRepo) Count(_ context.Context, _ *models.ListWebhooksFilters) (int64, error) {
	return 0, nil
}

func (m *mockSenderRepo) Delete(_ context.Context, _ uuid.UUID) error {
	return nil
}

func (m *mockSenderRepo) ListEnabled(_ context.Context) ([]models.Webhook, error) {
	return nil, nil
}

func (m *mockSenderRepo) ListEnabledForEventType(_ context.Context, _ string) ([]models.Webhook, error) {
	return nil, nil
}

func TestWebhookSenderImpl_Send(t *testing.T) {
	ctx := context.Background()
	webhookID := uuid.Must(uuid.NewV7())
	signingKey := "whsec_" + "abcdefghijklmnopqrstuvwxyz123456" // 32 bytes base64-ish for standardwebhooks
	webhook := &models.Webhook{
		ID:         webhookID,
		URL:        "",
		SigningKey: signingKey,
		Enabled:    true,
	}

	t.Run("returns nil on 200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", r.Method)
			}

			if r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
			}

			if r.Header.Get(standardwebhooks.HeaderWebhookID) == "" {
				t.Error("webhook-id header missing")
			}

			if r.Header.Get(standardwebhooks.HeaderWebhookSignature) == "" {
				t.Error("webhook-signature header missing")
			}

			if r.Header.Get(standardwebhooks.HeaderWebhookTimestamp) == "" {
				t.Error("webhook-timestamp header missing")
			}

			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		webhook.URL = server.URL

		repo := &mockSenderRepo{}
		sender := NewWebhookSenderImpl(repo, nil)
		payload := &WebhookPayload{
			ID:        uuid.Must(uuid.NewV7()),
			Type:      "feedback_record.created",
			Timestamp: time.Now(),
			Data:      map[string]string{"id": "123"},
		}

		err := sender.Send(ctx, webhook, payload)
		if err != nil {
			t.Errorf("Send() error = %v", err)
		}

		if repo.updateCalled {
			t.Error("Update should not be called on 200")
		}
	})

	t.Run("disables webhook and returns error on 410", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusGone)
		}))
		defer server.Close()

		webhook.URL = server.URL

		repo := &mockSenderRepo{}
		sender := NewWebhookSenderImpl(repo, nil)
		payload := &WebhookPayload{ID: uuid.Must(uuid.NewV7()), Type: "test", Timestamp: time.Now(), Data: nil}

		err := sender.Send(ctx, webhook, payload)
		if err == nil {
			t.Error("Send() error = nil, want error on 410")
		}

		if !repo.updateCalled {
			t.Error("Update should be called on 410 to disable webhook")
		}
	})

	t.Run("returns error on non-2xx", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		webhook.URL = server.URL

		repo := &mockSenderRepo{}
		sender := NewWebhookSenderImpl(repo, nil)
		payload := &WebhookPayload{ID: uuid.Must(uuid.NewV7()), Type: "test", Timestamp: time.Now(), Data: nil}

		err := sender.Send(ctx, webhook, payload)
		if err == nil {
			t.Error("Send() error = nil, want error on 500")
		}

		if repo.updateCalled {
			t.Error("Update should not be called on 500")
		}
	})
}
