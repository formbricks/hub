package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/observability"
)

// TestMetricsEndpointExposesExpectedMetrics ensures that when metrics are enabled,
// the /metrics handler exposes the expected metric names (Prometheus format).
func TestMetricsEndpointExposesExpectedMetrics(t *testing.T) {
	ctx := context.Background()
	provider, metricsHandler, metrics, err := observability.NewMeterProvider(ctx, observability.MeterProviderConfig{})
	require.NoError(t, err)

	defer func() { _ = provider.Shutdown(ctx) }()

	// Record at least one sample per metric so they appear in the output
	metrics.RecordRequest(ctx, "GET", "GET /v1/feedback-records", "2xx", 10*time.Millisecond)
	metrics.RecordEventDropped(ctx, "feedback_record.created")
	metrics.RecordWebhookJobsEnqueued(ctx, "webhook.created", 1)
	metrics.RecordWebhookEnqueueError(ctx, "feedback_record.created")
	metrics.RecordWebhookDelivery(ctx, "feedback_record.created", "success", 50*time.Millisecond)
	metrics.RecordWebhookDisabled(ctx, "410_gone")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	metricsHandler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "metrics endpoint should return 200")
	body := rec.Body.String()

	// Prometheus exporter typically normalizes names (e.g. dots to underscores).
	// Check that expected metric name stems appear in the output.
	expectedStems := []string{
		"http_server_request_count",
		"http_server_duration",
		"events_dropped_total",
		"webhook_jobs_enqueued_total",
		"webhook_jobs_enqueue_errors_total",
		"webhook_deliveries_total",
		"webhook_delivery_duration_seconds",
		"webhook_disabled_total",
	}
	for _, stem := range expectedStems {
		require.Contains(t, body, stem,
			"metrics response should contain %q; got body (first 2k): %s", stem, truncate(body, 2000))
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	return s[:maxLen] + "..."
}
