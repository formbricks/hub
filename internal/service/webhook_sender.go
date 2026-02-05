package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/formbricks/hub/internal/models"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

// WebhookSender sends a single webhook payload to an endpoint (Standard Webhooks: signing, headers, 410 handling).
type WebhookSender interface {
	Send(ctx context.Context, webhook *models.Webhook, payload *WebhookPayload) error
}

// WebhookSenderImpl implements WebhookSender with Standard Webhooks conformance.
type WebhookSenderImpl struct {
	repo       WebhooksRepository
	httpClient *http.Client
}

// NewWebhookSenderImpl creates a sender that uses the given repo.
// HTTP client uses 15s timeout and does not follow redirects.
func NewWebhookSenderImpl(repo WebhooksRepository) *WebhookSenderImpl {
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &WebhookSenderImpl{
		repo:       repo,
		httpClient: client,
	}
}

// Send signs and POSTs the payload to the webhook URL. On 410 Gone, disables the webhook and returns an error.
func (s *WebhookSenderImpl) Send(ctx context.Context, webhook *models.Webhook, payload *WebhookPayload) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	messageID := payload.ID.String()

	wh, err := standardwebhooks.NewWebhook(webhook.SigningKey)
	if err != nil {
		return fmt.Errorf("create webhook signer: %w", err)
	}

	timestamp := time.Now()
	signature, err := wh.Sign(messageID, timestamp, payloadJSON)
	if err != nil {
		return fmt.Errorf("sign webhook: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook.URL, bytes.NewReader(payloadJSON))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(standardwebhooks.HeaderWebhookID, messageID)
	req.Header.Set(standardwebhooks.HeaderWebhookSignature, signature)
	req.Header.Set(standardwebhooks.HeaderWebhookTimestamp, strconv.FormatInt(timestamp.Unix(), 10))

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send webhook: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("failed to close webhook response body", "webhook_id", webhook.ID, "error", closeErr)
		}
	}()

	if resp.StatusCode == http.StatusGone {
		enabled := false
		reason := "Endpoint returned 410 Gone"
		now := time.Now()
		_, updateErr := s.repo.Update(ctx, webhook.ID, &models.UpdateWebhookRequest{
			Enabled:        &enabled,
			DisabledReason: &reason,
			DisabledAt:     &now,
		})
		if updateErr != nil {
			slog.Error("failed to disable webhook after 410 Gone",
				"webhook_id", webhook.ID,
				"url", webhook.URL,
				"error", updateErr,
			)
		} else {
			slog.Info("webhook disabled after 410 Gone (endpoint no longer accepts delivery)",
				"webhook_id", webhook.ID,
				"url", webhook.URL,
			)
		}
		return fmt.Errorf("webhook returned 410 Gone (endpoint disabled): %s", webhook.URL)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned non-2xx status: %d", resp.StatusCode)
	}

	return nil
}
