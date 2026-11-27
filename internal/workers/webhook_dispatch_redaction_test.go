package workers

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/service"
)

// For Slack, Discord, Teams or Zapier the webhook URL is the credential. Every failed delivery goes
// through the worker's log lines, the error River stores in river_job.errors, and on the last
// attempt the webhook's disabled_reason; none of them may carry the URL's path or query (ENG-3616).
func TestWebhookDispatchWorker_Work_DoesNotLeakWebhookURL(t *testing.T) {
	const (
		maxAttempts = 3
		secretPath  = "s3cretPathToken"
		secretQuery = "s3cretQueryToken"
	)

	// A closed server: the real sender's dial fails with a *url.Error that carries the URL.
	closed := httptest.NewServer(http.NotFoundHandler())
	closedOrigin := closed.URL
	closed.Close()

	// A receiver answering with a malformed Location: net/http parses it before CheckRedirect runs
	// and quotes it in the error's cause.
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/services/"+secretPath+"/%zz?token="+secretQuery)
		w.WriteHeader(http.StatusFound)
	}))
	defer redirecting.Close()

	tenantID := "org-123"
	sender := service.NewWebhookSenderImpl(nil, nil, service.SSRFPolicy{}, 5*time.Second,
		&http.Client{Timeout: 5 * time.Second})

	assertNoSecret := func(t *testing.T, where, text string) {
		t.Helper()

		for _, s := range []string{secretPath, secretQuery} {
			if strings.Contains(text, s) {
				t.Errorf("%s contains %q: %s", where, s, text)
			}
		}
	}

	for _, testCase := range []struct {
		name    string
		origin  string
		attempt int
	}{
		{name: "transport error, retry", origin: closedOrigin, attempt: 1},
		{name: "transport error, final attempt", origin: closedOrigin, attempt: maxAttempts},
		{name: "malformed redirect, retry", origin: redirecting.URL, attempt: 1},
		{name: "malformed redirect, final attempt", origin: redirecting.URL, attempt: maxAttempts},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			webhook := &models.Webhook{
				ID:         uuid.Must(uuid.NewV7()),
				Enabled:    true,
				URL:        testCase.origin + "/services/T000/B000/" + secretPath + "?token=" + secretQuery,
				SigningKey: "whsec_" + "abcdefghijklmnopqrstuvwxyz123456",
				TenantID:   &tenantID,
			}
			args := service.WebhookDispatchArgs{
				EventID:   uuid.Must(uuid.NewV7()),
				EventType: "feedback_record.created",
				Timestamp: time.Now(),
				TenantID:  &tenantID,
				WebhookID: webhook.ID,
			}

			var logs bytes.Buffer

			prev := slog.Default()

			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
			t.Cleanup(func() { slog.SetDefault(prev) })

			repo := &mockDispatchRepo{webhook: webhook}
			worker := NewWebhookDispatchWorker(repo, sender, 5*time.Second, nil)
			job := &river.Job[service.WebhookDispatchArgs]{
				JobRow: &rivertype.JobRow{Attempt: testCase.attempt, MaxAttempts: maxAttempts},
				Args:   args,
			}

			err := worker.Work(context.Background(), job)
			if err == nil {
				t.Fatal("Work() error = nil, want the delivery error")
			}

			if logs.Len() == 0 {
				t.Fatal("expected the failed delivery to be logged")
			}

			assertNoSecret(t, "returned error (river_job.errors)", err.Error())
			assertNoSecret(t, "log output", logs.String())

			if repo.update != nil && repo.update.DisabledReason != nil {
				assertNoSecret(t, "disabled_reason", *repo.update.DisabledReason)
			}

			if testCase.attempt == maxAttempts && (repo.update == nil || repo.update.DisabledReason == nil) {
				t.Error("final attempt should disable the webhook with a reason")
			}
		})
	}
}
