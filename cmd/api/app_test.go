package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/formbricks/hub/internal/api/handlers"
	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/service"
)

func TestEmbeddingProviderAndModel(t *testing.T) {
	tests := []struct {
		name         string
		embedding    config.EmbeddingConfig
		wantProvider string
		wantModel    string
	}{
		{
			name: "disabled without provider",
			embedding: config.EmbeddingConfig{
				Model: "text-embedding-3-small",
			},
		},
		{
			name: "disabled without model",
			embedding: config.EmbeddingConfig{
				Provider: service.EmbeddingProviderOpenAI,
			},
		},
		{
			name: "disabled for unsupported provider",
			embedding: config.EmbeddingConfig{
				Provider: "unsupported",
				Model:    "text-embedding-3-small",
			},
		},
		{
			name: "returns normalized supported provider",
			embedding: config.EmbeddingConfig{
				Provider: " google-vertex ",
				Model:    "gemini-embedding-001",
			},
			wantProvider: service.EmbeddingProviderGoogleGemini,
			wantModel:    "gemini-embedding-001",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotProvider, gotModel := embeddingProviderAndModel(&config.Config{Embedding: tt.embedding})

			if gotProvider != tt.wantProvider {
				t.Fatalf("embeddingProviderAndModel() provider = %q, want %q", gotProvider, tt.wantProvider)
			}

			if gotModel != tt.wantModel {
				t.Fatalf("embeddingProviderAndModel() model = %q, want %q", gotModel, tt.wantModel)
			}
		})
	}
}

func TestSetupMetricsDisabled(t *testing.T) {
	meterProvider, metrics, err := setupMetrics(&config.Config{})
	if err != nil {
		t.Fatalf("setupMetrics() error = %v, want nil", err)
	}

	if meterProvider != nil {
		t.Fatal("setupMetrics() meterProvider != nil, want nil")
	}

	if metrics != nil {
		t.Fatal("setupMetrics() metrics != nil, want nil")
	}
}

func TestSetupMetricsDisabledWithNilConfig(t *testing.T) {
	meterProvider, metrics, err := setupMetrics(nil)
	if err != nil {
		t.Fatalf("setupMetrics(nil) error = %v, want nil", err)
	}

	if meterProvider != nil {
		t.Fatal("setupMetrics(nil) meterProvider != nil, want nil")
	}

	if metrics != nil {
		t.Fatal("setupMetrics(nil) metrics != nil, want nil")
	}
}

// NewApp builds its River client insert-only — no Workers, no Queues — so the API never needs the
// enrichment credentials that only hub-worker's workers use (ENG-1916). River rejects a config that
// sets Queues without Workers, so omitting both is the only legal way to express insert-only; this
// asserts that config stays valid, which is what lets NewApp boot without a worker bundle.
//
// That the resulting client may insert kinds it registers no worker for needs a database, so it is
// covered by the integration suite rather than here.
func TestRiverClientIsInsertOnly(t *testing.T) {
	if _, err := river.NewClient(riverpgxv5.New(nil), &river.Config{}); err != nil {
		t.Fatalf("river.NewClient(insert-only config) error = %v, want nil", err)
	}

	// The constraint that forces the shape above: declaring queues obliges us to supply a worker
	// bundle, which is what used to drag the enrichment clients (and their credentials) into this
	// process. If a River upgrade ever drops this rule, revisit whether the API can declare queues
	// again for insert-time validation without taking on the credentials.
	_, err := river.NewClient(riverpgxv5.New(nil), &river.Config{
		Queues: map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}},
	})
	if err == nil {
		t.Fatal("river.NewClient(queues without workers) error = nil, want a validation error")
	}
}

func TestSetupEmbeddingSearchHandler(t *testing.T) {
	cfg := &config.Config{
		Embedding: config.EmbeddingConfig{
			ProviderAPIKey: "test-api-key",
		},
	}

	handler, err := setupEmbeddingSearchHandler(
		context.Background(),
		cfg,
		service.EmbeddingProviderOpenAI,
		"text-embedding-3-small",
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("setupEmbeddingSearchHandler() error = %v, want nil", err)
	}

	if handler == nil {
		t.Fatal("setupEmbeddingSearchHandler() handler = nil, want non-nil")
	}
}

func TestSetupEmbeddingSearchHandlerValidatesConfig(t *testing.T) {
	_, err := setupEmbeddingSearchHandler(
		context.Background(),
		&config.Config{},
		service.EmbeddingProviderOpenAI,
		"text-embedding-3-small",
		nil,
		nil,
		nil,
	)
	if !errors.Is(err, service.ErrEmbeddingProviderAPIKey) {
		t.Fatalf("setupEmbeddingSearchHandler() error = %v, want %v", err, service.ErrEmbeddingProviderAPIKey)
	}
}

func TestShutdownObservabilityWithNilProviders(t *testing.T) {
	if err := shutdownObservability(context.Background(), nil, nil); err != nil {
		t.Fatalf("shutdownObservability() error = %v, want nil", err)
	}
}

func TestShutdownObservabilityWithProviders(t *testing.T) {
	tracerProvider := sdktrace.NewTracerProvider()
	meterProvider := sdkmetric.NewMeterProvider()

	if err := shutdownObservability(context.Background(), tracerProvider, meterProvider); err != nil {
		t.Fatalf("shutdownObservability() error = %v, want nil", err)
	}
}

// lifecycleRecorder orders the shutdown steps a test cares about. Both the exporter below and the
// test goroutine append to it, so it has to be mutex-guarded.
type lifecycleRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *lifecycleRecorder) record(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.events = append(r.events, event)
}

func (r *lifecycleRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.events)
}

// recordingExporter notes when the meter provider tears down. Its Shutdown is the step that must not
// happen before the backlog poller has withdrawn its gauge; Export is recorded too so the final
// collect is visible when instruments carry data.
type recordingExporter struct {
	recorder *lifecycleRecorder
}

func (e recordingExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(kind)
}

func (e recordingExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}

func (e recordingExporter) Export(_ context.Context, _ *metricdata.ResourceMetrics) error {
	e.recorder.record("metrics-exported")

	return nil
}

func (e recordingExporter) ForceFlush(context.Context) error { return nil }

func (e recordingExporter) Shutdown(context.Context) error {
	e.recorder.record("metrics-shutdown")

	return nil
}

// TestShutdownWaitsForEnrichmentBacklogPoller pins the lifecycle ordering that makes the poller's
// deferred ClearEnrichmentPending observable at all.
//
// Run returns the moment its context is cancelled without joining the poller, so Shutdown used to
// race it: the meter provider's final collect-and-export could publish a backlog reading for a
// process that had already lost leadership -- the exact stale series the clear exists to prevent.
// The poller is slowed here so an unsynchronized Shutdown reliably loses the race. Verified by
// deleting the awaitEnrichmentBacklogPoller defer: the recorded events become
// [metrics-exported metrics-shutdown], i.e. the gauge is exported and the provider torn down before
// the clear runs at all.
func TestShutdownWaitsForEnrichmentBacklogPoller(t *testing.T) {
	recorder := &lifecycleRecorder{}

	riverClient, err := river.NewClient(riverpgxv5.New(nil), &river.Config{})
	if err != nil {
		t.Fatalf("river.NewClient() error = %v, want nil", err)
	}

	done := make(chan struct{})

	app := &App{
		cfg:     &config.Config{Server: config.ServerConfig{Port: "0"}},
		server:  &http.Server{Addr: "127.0.0.1:0", ReadHeaderTimeout: time.Second},
		river:   riverClient,
		message: service.NewMessagePublisherManager(1, time.Second, nil),
		meterProvider: sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(recordingExporter{recorder: recorder})),
		),
		enrichmentBacklogDone: done,
	}

	// Stands in for the poller returning: it withdraws the gauge, then signals. The delay is what
	// makes the ordering assertion meaningful rather than incidentally true.
	go func() {
		time.Sleep(50 * time.Millisecond)
		recorder.record("backlog-cleared")
		close(done)
	}()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := app.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v, want nil", err)
	}

	events := recorder.snapshot()

	clearedAt := slices.Index(events, "backlog-cleared")
	if clearedAt < 0 {
		t.Fatalf("Shutdown() events = %v, want the backlog clear to have happened", events)
	}

	shutdownAt := slices.Index(events, "metrics-shutdown")
	if shutdownAt < 0 {
		t.Fatalf("Shutdown() events = %v, want the meter provider to have been shut down", events)
	}

	if clearedAt > shutdownAt {
		t.Fatalf("Shutdown() events = %v, want the backlog clear before the meter provider teardown", events)
	}
}

// TestShutdownWithoutEnrichmentBacklogPollerDoesNotBlock covers the metrics-disabled path, where the
// poller never started and there is nothing to join.
func TestShutdownWithoutEnrichmentBacklogPollerDoesNotBlock(t *testing.T) {
	riverClient, err := river.NewClient(riverpgxv5.New(nil), &river.Config{})
	if err != nil {
		t.Fatalf("river.NewClient() error = %v, want nil", err)
	}

	app := &App{
		cfg:     &config.Config{Server: config.ServerConfig{Port: "0"}},
		server:  &http.Server{Addr: "127.0.0.1:0", ReadHeaderTimeout: time.Second},
		river:   riverClient,
		message: service.NewMessagePublisherManager(1, time.Second, nil),
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := app.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v, want nil", err)
	}
}

// TestAwaitEnrichmentBacklogPollerGivesUpOnDeadline pins the bound: a poller stuck in its leader
// cleanup must not hold shutdown past its budget.
func TestAwaitEnrichmentBacklogPollerGivesUpOnDeadline(t *testing.T) {
	app := &App{enrichmentBacklogDone: make(chan struct{})} // never closed

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()

	app.awaitEnrichmentBacklogPoller(ctx)

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("awaitEnrichmentBacklogPoller() blocked for %v, want it to give up on the deadline", elapsed)
	}
}

func TestAppRunReturnsServerError(t *testing.T) {
	app := &App{
		cfg: &config.Config{
			Server: config.ServerConfig{
				Port: "bad",
			},
		},
		server: &http.Server{
			Addr:              "127.0.0.1:bad",
			ReadHeaderTimeout: time.Second,
		},
	}

	err := app.Run(context.Background())
	if err == nil {
		t.Fatal("Run() error = nil, want server error")
	}

	if !strings.Contains(err.Error(), "server:") {
		t.Fatalf("Run() error = %v, want server context", err)
	}
}

func TestNewHTTPServerServesOpenAPIWithoutAuth(t *testing.T) {
	server := newTestHTTPServer(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/openapi.json", nil)

	server.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /openapi.json status = %d, want %d", recorder.Code, http.StatusOK)
	}

	if !strings.Contains(recorder.Body.String(), "https://hub.example.com/base") {
		t.Fatalf("GET /openapi.json body = %s, want configured public base URL", recorder.Body.String())
	}
}

func TestNewHTTPServerServesOpenAPIYAMLWithoutAuthWhenPublicBaseURLUnset(t *testing.T) {
	t.Setenv("PUBLIC_BASE_URL", "")

	server := newTestHTTPServerWithPublicBaseURL(t, "")

	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/openapi.yaml", nil)
	request.Host = "attacker.example.com"

	server.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /openapi.yaml status = %d, want %d", recorder.Code, http.StatusOK)
	}

	if !strings.Contains(recorder.Body.String(), "http://localhost:8080") {
		t.Fatalf("GET /openapi.yaml body = %s, want local development base URL", recorder.Body.String())
	}

	if strings.Contains(recorder.Body.String(), "attacker.example.com") {
		t.Fatalf("GET /openapi.yaml reflected request host in body: %s", recorder.Body.String())
	}
}

func TestNewHTTPServerKeepsV1RoutesProtected(t *testing.T) {
	server := newTestHTTPServer(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/feedback-records", strings.NewReader(`{}`))

	server.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("POST /v1/feedback-records status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestNewHTTPServerKeepsTenantDataRoutesProtected(t *testing.T) {
	server := newTestHTTPServer(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodDelete,
		"/v1/tenants/test-tenant-id/data",
		http.NoBody,
	)

	server.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("DELETE /v1/tenants/{tenant_id}/data status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestNewHTTPServerInternalTaxonomyRouteRequiresInternalToken(t *testing.T) {
	server := newTestHTTPServer(t)

	tests := []struct {
		name          string
		authHeader    string
		wantStatus    int
		wantBodyMatch string
	}{
		{
			name:       "missing auth",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "malformed auth",
			authHeader: "Basic test-internal-token",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "public API key rejected",
			authHeader: "Bearer test-api-key",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong internal token rejected",
			authHeader: "Bearer wrong-token",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:          "internal token accepted",
			authHeader:    "Bearer test-internal-token",
			wantStatus:    http.StatusOK,
			wantBodyMatch: "hub-taxonomy-internal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()

			request := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodGet,
				"/internal/v1/taxonomy/auth-check",
				http.NoBody,
			)
			if tt.authHeader != "" {
				request.Header.Set("Authorization", tt.authHeader)
			}

			server.Handler.ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("GET /internal/v1/taxonomy/auth-check status = %d, want %d; body=%s",
					recorder.Code, tt.wantStatus, recorder.Body.String())
			}

			if tt.wantBodyMatch != "" && !strings.Contains(recorder.Body.String(), tt.wantBodyMatch) {
				t.Fatalf("GET /internal/v1/taxonomy/auth-check body = %s, want %q",
					recorder.Body.String(), tt.wantBodyMatch)
			}
		})
	}
}

func TestNewHTTPServerInternalTaxonomyRouteDisabledWithoutToken(t *testing.T) {
	server := newTestHTTPServerWithConfig(t, "https://hub.example.com/base", config.TaxonomyConfig{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/internal/v1/taxonomy/auth-check",
		http.NoBody,
	)

	server.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("GET /internal/v1/taxonomy/auth-check status = %d, want %d",
			recorder.Code, http.StatusNotFound)
	}
}

func newTestHTTPServer(t *testing.T) *http.Server {
	t.Helper()

	return newTestHTTPServerWithPublicBaseURL(t, "https://hub.example.com/base")
}

func newTestHTTPServerWithPublicBaseURL(t *testing.T, publicBaseURL string) *http.Server {
	t.Helper()

	return newTestHTTPServerWithConfig(t, publicBaseURL, config.TaxonomyConfig{
		HubInternalAPIToken: "test-internal-token",
	})
}

func newTestHTTPServerWithConfig(t *testing.T, publicBaseURL string, taxonomy config.TaxonomyConfig) *http.Server {
	t.Helper()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:      "0",
			HubAPIKey: "test-api-key",
		},
		Taxonomy: taxonomy,
	}

	return newHTTPServer(
		cfg,
		handlers.NewHealthHandler(),
		newTestOpenAPIHandler(t, publicBaseURL),
		handlers.NewFeedbackRecordsHandler(nil),
		handlers.NewWebhooksHandler(nil),
		handlers.NewTenantDataHandler(nil),
		handlers.NewTenantSettingsHandler(nil),
		handlers.NewSearchHandler(nil),
		handlers.NewTaxonomyHandler(nil),
		handlers.NewTaxonomyInternalHandler(),
		handlers.NewEnrichmentStatusHandler(nil),
		nil,
		nil,
	)
}

func newTestOpenAPIHandler(t *testing.T, publicBaseURL string) *handlers.OpenAPIHandler {
	t.Helper()

	specPath := filepath.Join(t.TempDir(), "openapi.yaml")
	spec := []byte("openapi: 3.0.0\ninfo:\n  title: Test API\n  version: 1.0.0\npaths: {}\n")

	if err := os.WriteFile(specPath, spec, 0o600); err != nil {
		t.Fatalf("write openapi spec: %v", err)
	}

	handler, err := handlers.NewOpenAPIHandler(specPath, publicBaseURL)
	if err != nil {
		t.Fatalf("NewOpenAPIHandler() error = %v", err)
	}

	return handler
}
