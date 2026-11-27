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
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
)

// Sentinel errors for webhook delivery (err113).
var (
	ErrWebhookGone   = errors.New("webhook returned 410 Gone (endpoint disabled)")
	ErrWebhookNon2xx = errors.New("webhook returned non-2xx status")
)

// redactedWebhookURL stands in for a webhook URL that cannot be parsed, so not even its origin is known.
const redactedWebhookURL = "[redacted webhook url]"

// RedactWebhookURL returns only the scheme and host (with port) of a webhook URL, for logs and errors.
// For many receivers (Slack, Discord, Teams, Zapier, anything with a ?token= parameter) the path,
// query or userinfo is the credential, and logs reach more people than the tenant's webhook config.
// The webhook_id is the identifier to log; the origin is kept only to tell endpoints apart at a glance.
func RedactWebhookURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return redactedWebhookURL
	}

	return u.Scheme + "://" + u.Host
}

// redactURLError replaces the URL in the *url.Error that url.Parse and http.Client.Do return (its
// Error() quotes the full URL) with the redacted form. Op and the cause are kept, so errors.Is,
// errors.As and Timeout() behave as before. Any other error is returned unchanged.
func redactURLError(err error) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}

	return &url.Error{Op: urlErr.Op, URL: RedactWebhookURL(urlErr.URL), Err: urlErr.Err}
}

// WebhookSender sends a single webhook payload to an endpoint (Standard Webhooks: signing, headers, 410 handling).
type WebhookSender interface {
	Send(ctx context.Context, webhook *models.Webhook, payload *WebhookPayload) error
}

// WebhookSenderRepository persists webhook state changes caused by delivery.
type WebhookSenderRepository interface {
	Update(ctx context.Context, id uuid.UUID, req *models.UpdateWebhookRequest) (*models.Webhook, error)
}

// WebhookSenderImpl implements WebhookSender with Standard Webhooks conformance.
type WebhookSenderImpl struct {
	repo       WebhookSenderRepository
	httpClient *http.Client
	metrics    observability.WebhookMetrics
}

// NewWebhookSenderImpl creates a sender that uses the given repo.
// ssrfPolicy restricts which hosts may be dialed; its zero value still rejects private/reserved
// ranges. It is enforced in the transport's DialContext, so it is not retained on the struct — an
// injected httpClient (below) is expected to carry its own dialer.
// httpTimeout is the HTTP client timeout; job timeout should be httpTimeout + buffer (e.g. 5s).
// Client does not follow redirects and validates resolved IPs at dial time (DNS rebinding protection).
// metrics may be nil when metrics are disabled.
// If httpClient is non-nil, it is used as-is (e.g. for tests that hit loopback); otherwise a secured client is built.
func NewWebhookSenderImpl(
	repo WebhookSenderRepository, metrics observability.WebhookMetrics, ssrfPolicy SSRFPolicy,
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

				allowed, err := resolveWebhookHost(ctx, host, ssrfPolicy)
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
		repo:       repo,
		httpClient: httpClient,
		metrics:    metrics,
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
		return fmt.Errorf("create request: %w", redactURLError(err))
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(standardwebhooks.HeaderWebhookID, messageID)
	req.Header.Set(standardwebhooks.HeaderWebhookSignature, signature)
	req.Header.Set(standardwebhooks.HeaderWebhookTimestamp, strconv.FormatInt(timestamp.Unix(), 10))

	resp, err := s.httpClient.Do(req) // #nosec G704 -- URL validated at create/update and in DialContext (DNS rebinding)
	if err != nil {
		return fmt.Errorf("send webhook: %w", redactURLError(err))
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
				"url", RedactWebhookURL(webhook.URL),
				"error", updateErr,
			)
		} else {
			slog.Info("webhook disabled after 410 Gone (endpoint no longer accepts delivery)",
				"webhook_id", webhook.ID,
				"url", RedactWebhookURL(webhook.URL),
			)
		}

		return fmt.Errorf("%w: %s", ErrWebhookGone, RedactWebhookURL(webhook.URL))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: %d", ErrWebhookNon2xx, resp.StatusCode)
	}

	return nil
}
