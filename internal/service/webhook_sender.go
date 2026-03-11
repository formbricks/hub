package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
)

// Sentinel errors for webhook delivery (err113).
var (
	ErrWebhookGone   = errors.New("webhook returned 410 Gone (endpoint disabled)")
	ErrWebhookNon2xx = errors.New("webhook returned non-2xx status")
)

// WebhookSender sends a single webhook payload to an endpoint (Standard Webhooks: signing, headers, 410 handling).
type WebhookSender interface {
	Send(ctx context.Context, webhook *models.Webhook, payload *WebhookPayload) error
}

// WebhookSenderImpl implements WebhookSender with Standard Webhooks conformance.
type WebhookSenderImpl struct {
	repo             WebhooksRepository
	httpClient       *http.Client
	metrics          observability.WebhookMetrics
	urlHostBlacklist map[string]struct{}
}

// NewWebhookSenderImpl creates a sender that uses the given repo.
// urlHostBlacklist is the SSRF blacklist (hosts/IPs); may be nil (address checks still run).
// httpTimeout is the HTTP client timeout; job timeout should be httpTimeout + buffer (e.g. 5s).
// Client does not follow redirects and validates resolved IPs at dial time (DNS rebinding protection).
// metrics may be nil when metrics are disabled.
// If httpClient is non-nil, it is used as-is (e.g. for tests that hit loopback); otherwise a secured client is built.
func NewWebhookSenderImpl(
	repo WebhooksRepository, metrics observability.WebhookMetrics, urlHostBlacklist map[string]struct{},
	httpTimeout time.Duration, httpClient *http.Client,
) *WebhookSenderImpl {
	if httpClient == nil {
		dialer := &net.Dialer{}
		transport := &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, fmt.Errorf("invalid address %q: %w", addr, err)
				}

				allowed, err := resolveWebhookHost(ctx, host, urlHostBlacklist)
				if err != nil {
					return nil, err
				}

				// Try each validated IP in order until one connects (preserves multi-address fallback for CDNs, dual-stack).
				var lastErr error

				for _, a := range allowed {
					target := net.JoinHostPort(a.String(), port)

					conn, dialErr := dialer.DialContext(ctx, network, target)
					if dialErr == nil {
						return conn, nil
					}

					lastErr = dialErr
				}

				return nil, lastErr
			},
		}

		httpClient = &http.Client{
			Transport: transport,
			Timeout:   httpTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	return &WebhookSenderImpl{
		repo:             repo,
		httpClient:       httpClient,
		metrics:          metrics,
		urlHostBlacklist: urlHostBlacklist,
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

	resp, err := s.httpClient.Do(req) // #nosec G704 -- URL validated at create/update and in DialContext (DNS rebinding)
	if err != nil {
		return fmt.Errorf("send webhook: %w", err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("failed to close webhook response body", "webhook_id", webhook.ID, "error", closeErr)
		}
	}()

	if resp.StatusCode == http.StatusGone {
		if s.metrics != nil {
			s.metrics.RecordWebhookDisabled(ctx, "410_gone")
		}

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

		return fmt.Errorf("%w: %s", ErrWebhookGone, webhook.URL)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: %d", ErrWebhookNon2xx, resp.StatusCode)
	}

	return nil
}
