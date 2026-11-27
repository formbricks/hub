package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/models"
)

// Secret-shaped parts of a webhook URL. For Slack, Discord, Teams or Zapier the URL itself is the
// credential, so none of these may reach a log line or a returned error (ENG-3616).
const (
	secretUser  = "s3cretUser"
	secretPass  = "s3cretPass"
	secretPath  = "s3cretPathToken"
	secretQuery = "s3cretQueryToken"
)

const redactionTestSigningKey = "whsec_" + "abcdefghijklmnopqrstuvwxyz123456"

func secretWebhookURL(origin string) string {
	scheme, host, _ := strings.Cut(origin, "://")

	return scheme + "://" + secretUser + ":" + secretPass + "@" + host +
		"/services/T000/B000/" + secretPath + "?token=" + secretQuery
}

func assertNoSecret(t *testing.T, where, text string) {
	t.Helper()

	for _, s := range []string{secretUser, secretPass, secretPath, secretQuery} {
		if strings.Contains(text, s) {
			t.Errorf("%s contains %q: %s", where, s, text)
		}
	}
}

// captureLogs routes the default logger to a buffer for the test's duration.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	var logs bytes.Buffer

	prev := slog.Default()

	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	return &logs
}

func newRedactionTestSender(repo WebhookSenderRepository) *WebhookSenderImpl {
	return NewWebhookSenderImpl(repo, nil, SSRFPolicy{}, 5*time.Second, &http.Client{Timeout: 5 * time.Second})
}

func newRedactionTestPayload() *WebhookPayload {
	return &WebhookPayload{
		ID:        uuid.Must(uuid.NewV7()),
		Type:      "feedback_record.created",
		Timestamp: time.Now(),
		Data:      map[string]string{"id": "123"},
	}
}

func TestWebhookSenderImpl_Send_DoesNotLeakWebhookURL(t *testing.T) {
	ctx := context.Background()

	t.Run("transport error", func(t *testing.T) {
		logs := captureLogs(t)

		// A closed server: the dial fails and http.Client.Do returns a *url.Error carrying the URL.
		server := httptest.NewServer(http.NotFoundHandler())
		origin := server.URL
		server.Close()

		webhook := &models.Webhook{
			ID: uuid.Must(uuid.NewV7()), URL: secretWebhookURL(origin), SigningKey: redactionTestSigningKey, Enabled: true,
		}

		err := newRedactionTestSender(&mockSenderRepo{}).Send(ctx, webhook, newRedactionTestPayload())
		if err == nil {
			t.Fatal("Send() error = nil, want a transport error")
		}

		assertNoSecret(t, "returned error", err.Error())
		assertNoSecret(t, "log output", logs.String())

		if !strings.Contains(err.Error(), origin) {
			t.Errorf("returned error %q should still name the origin %q", err, origin)
		}
	})

	t.Run("transport timeout keeps its cause", func(t *testing.T) {
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))

		defer server.Close()
		defer close(release)

		webhook := &models.Webhook{
			ID: uuid.Must(uuid.NewV7()), URL: secretWebhookURL(server.URL), SigningKey: redactionTestSigningKey, Enabled: true,
		}
		sender := NewWebhookSenderImpl(&mockSenderRepo{}, nil, SSRFPolicy{}, time.Second,
			&http.Client{Timeout: 50 * time.Millisecond})

		err := sender.Send(ctx, webhook, newRedactionTestPayload())
		if err == nil {
			t.Fatal("Send() error = nil, want a timeout")
		}

		assertNoSecret(t, "returned error", err.Error())

		var timeoutErr interface{ Timeout() bool }
		if !errors.As(err, &timeoutErr) || !timeoutErr.Timeout() {
			t.Errorf("returned error %q should still report a timeout", err)
		}
	})

	for _, testCase := range []struct {
		name      string
		updateErr error
	}{
		{name: "410 Gone", updateErr: nil},
		{name: "410 Gone when disabling fails", updateErr: errors.New("db down")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			logs := captureLogs(t)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusGone)
			}))
			defer server.Close()

			webhook := &models.Webhook{
				ID: uuid.Must(uuid.NewV7()), URL: secretWebhookURL(server.URL), SigningKey: redactionTestSigningKey, Enabled: true,
			}
			repo := &mockSenderRepo{updateErr: testCase.updateErr}

			err := newRedactionTestSender(repo).Send(ctx, webhook, newRedactionTestPayload())
			if !errors.Is(err, ErrWebhookGone) {
				t.Fatalf("Send() error = %v, want ErrWebhookGone", err)
			}

			assertNoSecret(t, "returned error", err.Error())
			assertNoSecret(t, "log output", logs.String())
		})
	}

	t.Run("malformed redirect Location", func(t *testing.T) {
		logs := captureLogs(t)

		// net/http parses Location before CheckRedirect runs, and quotes it in the error's cause.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "/services/"+secretPath+"/%zz?token="+secretQuery)
			w.WriteHeader(http.StatusFound)
		}))
		defer server.Close()

		webhook := &models.Webhook{
			ID: uuid.Must(uuid.NewV7()), URL: secretWebhookURL(server.URL), SigningKey: redactionTestSigningKey, Enabled: true,
		}

		err := newRedactionTestSender(&mockSenderRepo{}).Send(ctx, webhook, newRedactionTestPayload())
		if err == nil {
			t.Fatal("Send() error = nil, want a redirect parse error")
		}

		assertNoSecret(t, "returned error", err.Error())
		assertNoSecret(t, "log output", logs.String())
	})

	t.Run("unparseable URL", func(t *testing.T) {
		// url.Parse errors quote the whole input. Validation rejects such URLs at create/update,
		// but a row written before that validation would still reach the sender.
		webhook := &models.Webhook{
			ID:         uuid.Must(uuid.NewV7()),
			URL:        "http://example.com/" + secretPath + "/%zz?token=" + secretQuery,
			SigningKey: redactionTestSigningKey,
			Enabled:    true,
		}

		err := newRedactionTestSender(&mockSenderRepo{}).Send(ctx, webhook, newRedactionTestPayload())
		if err == nil {
			t.Fatal("Send() error = nil, want a parse error")
		}

		assertNoSecret(t, "returned error", err.Error())
	})
}

func TestRedactWebhookURL(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "https://hooks.slack.com/services/T0/B0/secret", want: "https://hooks.slack.com"},
		{in: "https://user:pass@example.com:8443/hook?token=secret#frag", want: "https://example.com:8443"},
		{in: "http://127.0.0.1:9999", want: "http://127.0.0.1:9999"},
		{in: "http://example.com/%zz?token=secret", want: redactedWebhookURL},
		{in: "not a url", want: redactedWebhookURL},
		{in: "", want: redactedWebhookURL},
	}

	for _, tt := range tests {
		if got := RedactWebhookURL(tt.in); got != tt.want {
			t.Errorf("RedactWebhookURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
