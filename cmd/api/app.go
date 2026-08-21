package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/formbricks/hub/internal/api/handlers"
	"github.com/formbricks/hub/internal/api/middleware"
	"github.com/formbricks/hub/internal/api/routes"
	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/llm"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
)

// App holds all server dependencies and coordinates startup and shutdown.
type App struct {
	cfg            *config.Config
	db             *pgxpool.Pool
	server         *http.Server
	river          *river.Client[pgx.Tx]
	message        *service.MessagePublisherManager
	meterProvider  *sdkmetric.MeterProvider
	tracerProvider *sdktrace.TracerProvider
	metrics        *observability.Metrics
	taxonomyRepo   *repository.TaxonomyRepository
	// enrichmentBacklogDone closes when the enrichment-backlog poller goroutine has returned,
	// including its deferred cleanup. Nil when the poller was never started (metrics disabled).
	enrichmentBacklogDone chan struct{}
}

var (
	errEmbeddingProviderAPIKeyRequired     = errors.New("EMBEDDING_PROVIDER_API_KEY is required for this provider")
	errEmbeddingGoogleGeminiConfigRequired = errors.New(
		"google-gemini requires EMBEDDING_GOOGLE_CLOUD_PROJECT and EMBEDDING_GOOGLE_CLOUD_LOCATION")
)

const (
	riverQueueDepthInterval = 15 * time.Second
	startupCleanupTimeout   = 5 * time.Second
	// enrichmentBacklogInterval is deliberately much slower than the River depth poll: the backlog
	// query is a full-table aggregate over feedback_records (a high-write table), so it runs
	// infrequently to minimize shared-DB load and keep the MVCC snapshot it holds short-lived
	// relative to VACUUM. Backlog is a slow-moving trend signal, so 5-minute resolution is ample.
	// The poller only runs at all when metrics are enabled (see App.Run).
	enrichmentBacklogInterval = 5 * time.Minute
	// enrichmentBacklogQueryTimeout bounds each aggregate scan so a slow query cannot pin a pool
	// connection, stall the ticker, or hold a long snapshot that delays VACUUM on feedback_records.
	enrichmentBacklogQueryTimeout = 30 * time.Second
	// enrichmentBacklogFailuresBeforeError is how many consecutive failed refreshes escalate the
	// log from warn to error (a transient blip is expected; a sustained run means a stale gauge).
	enrichmentBacklogFailuresBeforeError = 3
)

// embeddingProviderAndModel returns (provider, model) when embeddings are enabled: both EMBEDDING_PROVIDER
// and EMBEDDING_MODEL must be set and the provider must be supported. Otherwise returns ("", "") so no
// embedding provider or jobs run. No default for model; embeddings are disabled if either is unset.
// Provider name is normalized via the embedding registry (consistent with backfill-embeddings).
func embeddingProviderAndModel(cfg *config.Config) (provider, model string) {
	if cfg.Embedding.Provider == "" || cfg.Embedding.Model == "" {
		return "", ""
	}

	providerCanonical := service.NormalizeEmbeddingProvider(cfg.Embedding.Provider)
	if _, ok := service.SupportedEmbeddingProviders()[providerCanonical]; !ok {
		slog.Info("embeddings disabled: unsupported EMBEDDING_PROVIDER",
			"provider", cfg.Embedding.Provider, "model", cfg.Embedding.Model)

		return "", ""
	}

	return providerCanonical, cfg.Embedding.Model
}

const searchQueryCacheSize = 1000

// setupEmbeddingSearchHandler creates the embedding client and search handler when embeddings are
// enabled. The client is used synchronously on the search path (it embeds the incoming query), which
// is why the API needs embedding credentials even though it works no embedding jobs.
// Returns (handler, nil) or (nil, err). Caller should use errors.Is for service.ErrEmbeddingProviderAPIKey and
// service.ErrEmbeddingGoogleGeminiConfig to return app-level sentinel errors.
func setupEmbeddingSearchHandler(
	ctx context.Context,
	cfg *config.Config,
	embeddingProviderName, embeddingModel string,
	embeddingsRepo *repository.EmbeddingsRepository,
	metrics *observability.Metrics,
	meterProvider *sdkmetric.MeterProvider,
) (*handlers.SearchHandler, error) {
	// The API works no enrichment jobs, but it does embed SEARCH QUERIES, and those are real
	// provider calls with real cost. Recording them here means the token metric covers everything
	// the deployment actually spends rather than only the worker's share.
	var genAIUsage llm.UsageRecorder

	if meterProvider != nil {
		recorder, err := observability.NewGenAIMetrics(meterProvider.Meter("hub"))
		if err != nil {
			return nil, fmt.Errorf("create gen_ai metrics: %w", err)
		}

		genAIUsage = recorder
	}

	embeddingCfg := service.EmbeddingClientConfig{
		Provider:            embeddingProviderName,
		ProviderAPIKey:      cfg.Embedding.ProviderAPIKey,
		Model:               embeddingModel,
		BaseURL:             cfg.Embedding.BaseURL,
		Normalize:           cfg.Embedding.Normalize,
		GoogleCloudProject:  cfg.Embedding.GoogleCloudProject,
		GoogleCloudLocation: cfg.Embedding.GoogleCloudLocation,
		UsageRecorder:       genAIUsage,
	}
	if err := service.ValidateEmbeddingConfig(embeddingCfg); err != nil {
		return nil, fmt.Errorf("embedding config: %w", err)
	}

	embeddingClient, err := service.NewEmbeddingClient(ctx, embeddingCfg)
	if err != nil {
		return nil, fmt.Errorf("create embedding client: %w", err)
	}

	queryCache, err := lru.New[string, []float32](searchQueryCacheSize)
	if err != nil {
		return nil, fmt.Errorf("create search query cache: %w", err)
	}

	var cacheMetrics observability.CacheMetrics
	if metrics != nil {
		cacheMetrics = metrics.Cache
	}

	searchService := service.NewSearchService(service.SearchServiceParams{
		EmbeddingClient: embeddingClient,
		EmbeddingsRepo:  embeddingsRepo,
		Model:           embeddingModel,
		QueryCache:      queryCache,
		CacheMetrics:    cacheMetrics,
		Logger:          slog.Default(),
	})

	// Surface HNSW iterative-scan degradation (pgvector < 0.8 fallback) as a gauge so capped recall
	// is alertable, not just a one-time log line. No-op meter when metrics are disabled.
	var meter metric.Meter
	if meterProvider != nil {
		meter = meterProvider.Meter("hub")
	}

	if err := observability.RegisterHNSWIterativeScanGauge(meter, embeddingsRepo.IterativeScanDegraded); err != nil {
		return nil, fmt.Errorf("register hnsw iterative scan gauge: %w", err)
	}

	return handlers.NewSearchHandler(searchService), nil
}

// setupMetrics creates meter provider and hub metrics when metrics are enabled.
// When NewMeterProvider returns nil (unsupported or disabled exporter), returns (nil, nil, nil) (metrics disabled).
func setupMetrics(cfg *config.Config) (*sdkmetric.MeterProvider, *observability.Metrics, error) {
	mp, err := observability.NewMeterProvider(cfg, "hub-api")
	if err != nil {
		return nil, nil, fmt.Errorf("create meter provider: %w", err)
	}

	if mp == nil {
		return nil, nil, nil
	}

	metrics, err := observability.NewMetrics(mp.Meter("hub"))
	if err != nil {
		err2 := observability.ShutdownMeterProvider(context.Background(), mp)
		if err2 != nil {
			slog.Error("shutdown meter provider after metrics error", "error", err2)
		}

		return nil, nil, fmt.Errorf("create metrics: %w", err)
	}

	return mp, metrics, nil
}

// NewApp builds and wires all components. It does not start the HTTP server or River;
// call Run to start and block until shutdown or failure.
func NewApp(cfg *config.Config, db *pgxpool.Pool) (*App, error) {
	var (
		err           error
		meterProvider *sdkmetric.MeterProvider
		metrics       *observability.Metrics
	)

	if cfg.Observability.MetricsExporter == "" {
		slog.Warn("metrics not enabled (OTEL_METRICS_EXPORTER empty or unset)")
	} else {
		meterProvider, metrics, err = setupMetrics(cfg)
		if err != nil {
			return nil, err
		}
	}

	var (
		eventMetrics       observability.EventMetrics
		webhookMetrics     observability.WebhookMetrics
		embeddingMetrics   observability.EmbeddingMetrics
		translationMetrics observability.TranslationMetrics
		sentimentMetrics   observability.SentimentMetrics
		emotionsMetrics    observability.EmotionsMetrics
	)
	if metrics != nil {
		eventMetrics = metrics.Events
		webhookMetrics = metrics.Webhooks
		embeddingMetrics = metrics.Embeddings
		translationMetrics = metrics.Translation
		sentimentMetrics = metrics.Sentiment
		emotionsMetrics = metrics.Emotions
	}

	var tracerProvider *sdktrace.TracerProvider

	if cfg.Observability.TracesExporter == "" {
		slog.Warn("tracing not enabled (OTEL_TRACES_EXPORTER empty or unset)")
	} else {
		tracerProvider, err = observability.NewTracerProvider(cfg, "hub-api")
		if err != nil {
			if meterProvider != nil {
				if err2 := observability.ShutdownMeterProvider(context.Background(), meterProvider); err2 != nil {
					slog.Error("shutdown meter provider after tracer provider error", "error", err2)
				}
			}

			return nil, fmt.Errorf("create tracer provider: %w", err)
		}
	}

	if tracerProvider != nil {
		otel.SetTracerProvider(tracerProvider)
	}

	if meterProvider != nil {
		otel.SetMeterProvider(meterProvider)
	}

	perEventTimeout := time.Duration(cfg.MessagePublisher.PerEventTimeoutSec) * time.Second
	messageManager := service.NewMessagePublisherManager(cfg.MessagePublisher.BufferSize, perEventTimeout, eventMetrics)

	webhooksRepo := repository.NewWebhooksRepository(db)

	feedbackRecordsRepo := repository.NewFeedbackRecordsRepository(db)
	embeddingsRepo := repository.NewEmbeddingsRepository(db)
	tenantDataRepo := repository.NewTenantDataRepository(db, cfg.TenantData.PurgeLockTimeout.Duration())
	embeddingProviderName, embeddingModel := embeddingProviderAndModel(cfg)
	embeddingModelForDB := embeddingModel
	taxonomyEmbeddingModel := service.TaxonomyEmbeddingModel(embeddingModelForDB, cfg.Taxonomy.EmbeddingModel)

	taxonomyEmbeddingEnqueueModel := taxonomyEmbeddingModel

	if embeddingProviderName == "" {
		taxonomyEmbeddingEnqueueModel = ""
	}

	feedbackRecordsService := service.NewFeedbackRecordsService(
		feedbackRecordsRepo,
		embeddingsRepo,
		embeddingModelForDB,
		messageManager,
		nil, // riverClient set below after creation
		service.EmbeddingsQueueName,
		cfg.Embedding.MaxAttempts,
		cfg.Translation.DefaultLanguage,
	)
	feedbackRecordsService.SetTaxonomyEmbeddingModel(taxonomyEmbeddingEnqueueModel)

	// The eager-clear (nulling stale enrichment outputs on a value_text edit) fires only on this
	// API PATCH path, so wire its counter here; the worker/backfill service instances leave it unset.
	if metrics != nil {
		feedbackRecordsService.SetEnrichmentClearMetrics(metrics.EnrichmentClear)
	}

	// Tenant settings service: shared by the emotions worker's authoritative gate (registered
	// below), the settings HTTP handler, and the enqueue-path settings cache.
	tenantSettingsRepo := repository.NewTenantSettingsRepository(db)
	tenantSettingsService := service.NewTenantSettingsService(tenantSettingsRepo)

	var searchHandler *handlers.SearchHandler

	if embeddingProviderName != "" {
		var err error

		searchHandler, err = setupEmbeddingSearchHandler(
			context.Background(), cfg,
			embeddingProviderName, embeddingModel, embeddingsRepo,
			metrics, meterProvider)
		if err != nil {
			cleanupNewAppStartupFailure(context.Background(), messageManager, nil, tracerProvider, meterProvider)

			if errors.Is(err, service.ErrEmbeddingProviderAPIKey) {
				return nil, fmt.Errorf("%w: %s", errEmbeddingProviderAPIKeyRequired, embeddingProviderName)
			}

			if errors.Is(err, service.ErrEmbeddingGoogleGeminiConfig) {
				return nil, errEmbeddingGoogleGeminiConfigRequired
			}

			return nil, fmt.Errorf("embedding config: %w", err)
		}
	} else {
		searchHandler = handlers.NewSearchHandler(nil) // 503 when embeddings disabled
	}

	// Insert-only River client: this process enqueues jobs but works none of them, so it registers
	// no workers and declares no queues (River requires Workers and Queues to be set together, and
	// checks that a kind has a registered worker only when Workers is non-nil). hub-worker owns
	// registration — see workers.NewRiverWorkersAndQueues, and the parity test that keeps the kinds
	// this process inserts in step with the ones hub-worker registers.
	//
	// This is what keeps the API independent of enrichment credentials: sentiment, emotions, and
	// translation clients are only ever needed by the process that runs their workers, so a missing
	// or unreadable credential takes down hub-worker (loudly, on purpose) instead of the whole API.
	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{})
	if err != nil {
		cleanupNewAppStartupFailure(context.Background(), messageManager, nil, tracerProvider, meterProvider)

		return nil, fmt.Errorf("create River client: %w", err)
	}

	// Enable backfill on the same service instance the embedding worker uses (avoids nil inserter if worker ever calls BackfillEmbeddings).
	feedbackRecordsService.SetEmbeddingInserter(riverClient)

	webhookEnqueueInitialBackoff := time.Duration(cfg.Webhook.EnqueueInitialBackoffMs) * time.Millisecond

	webhookEnqueueMaxBackoff := max(time.Duration(cfg.Webhook.EnqueueMaxBackoffMs)*time.Millisecond, webhookEnqueueInitialBackoff)

	webhookProvider := service.NewWebhookProvider(
		riverClient, webhooksRepo,
		cfg.Webhook.DeliveryMaxAttempts, cfg.Webhook.MaxFanOutPerEvent,
		cfg.Webhook.EnqueueMaxRetries, webhookEnqueueInitialBackoff, webhookEnqueueMaxBackoff,
		webhookMetrics,
	)
	messageManager.RegisterProvider(webhookProvider)

	if embeddingProviderName != "" {
		docPrefix := service.EmbeddingPrefixForProvider(embeddingProviderName)
		embeddingProv := service.NewEmbeddingProvider(
			riverClient,
			embeddingModelForDB,
			service.EmbeddingsQueueName,
			cfg.Embedding.MaxAttempts,
			docPrefix,
			embeddingMetrics,
		)
		messageManager.RegisterProvider(embeddingProv)

		if taxonomyEmbeddingEnqueueModel != "" {
			taxonomyEmbeddingProv := service.NewEmbeddingProviderForInputKind(
				riverClient,
				taxonomyEmbeddingEnqueueModel,
				service.EmbeddingsQueueName,
				cfg.Embedding.MaxAttempts,
				docPrefix,
				embeddingMetrics,
				models.EmbeddingInputKindTaxonomyTranslated,
			)
			messageManager.RegisterProvider(taxonomyEmbeddingProv)
		}
	}

	webhooksService := service.NewWebhooksService(
		webhooksRepo, messageManager, cfg.Webhook.MaxCount,
		service.NewSSRFPolicy(cfg.Webhook.URLBlacklist, cfg.Webhook.AllowedCIDRs),
	)
	webhooksHandler := handlers.NewWebhooksHandler(webhooksService)
	tenantDataService := service.NewTenantDataService(tenantDataRepo)
	tenantDataHandler := handlers.NewTenantDataHandler(tenantDataService)

	// The API only ever enqueues the feedback-records purge; hub-worker performs it. The repo is
	// still wired in because the service owns both halves, but this process never reaches Purge.
	feedbackRecordsPurgeService := service.NewFeedbackRecordsPurgeService(tenantDataRepo, riverClient)
	feedbackRecordsPurgeHandler := handlers.NewFeedbackRecordsPurgeHandler(feedbackRecordsPurgeService)

	tenantSettingsHandler := handlers.NewTenantSettingsHandler(tenantSettingsService)

	// Translation, sentiment, and emotion enqueue providers all resolve a per-tenant setting on
	// the enqueue path (translation's target language; the sentiment and emotion per-directory
	// switches), so they share one short-TTL cache over tenant settings. The cache is evicted on a
	// settings write (below) so a toggle is visible to the gates immediately, not after TTL expiry.
	translationEnabled := cfg.Translation.Provider != "" && cfg.Translation.Model != ""

	var tenantSettingsCache *service.CachedTenantSettings

	if translationEnabled || cfg.Sentiment.Enabled() || cfg.Emotions.Enabled() {
		var cacheMetrics observability.CacheMetrics
		if metrics != nil {
			cacheMetrics = metrics.Cache
		}

		tenantSettingsCache = service.NewCachedTenantSettings(
			tenantSettingsService,
			cfg.TenantSettingsCache.Size, cfg.TenantSettingsCache.TTL.Duration(),
			cacheMetrics,
		)
	}

	// Translation enqueue provider: resolves the tenant's target language and enqueues a
	// translation job. Gated on TRANSLATION_PROVIDER+MODEL.
	if translationEnabled {
		messageManager.RegisterProvider(service.NewTranslationProvider(
			riverClient, tenantSettingsCache, service.TranslationsQueueName, cfg.Translation.MaxAttempts,
			cfg.Translation.DefaultLanguage, translationMetrics))
	}

	// Sentiment enqueue provider: on a create/update with open text it enqueues a sentiment job,
	// skipping tenants that have switched sentiment off. Gated on SENTIMENT_PROVIDER+MODEL.
	if cfg.Sentiment.Enabled() {
		messageManager.RegisterProvider(service.NewSentimentProvider(
			riverClient, tenantSettingsCache, service.SentimentsQueueName, cfg.Sentiment.MaxAttempts,
			sentimentMetrics))
	}

	// Emotions enqueue provider: on a create/update with open text it enqueues an emotion job,
	// skipping tenants that have switched emotions off. Gated on EMOTIONS_PROVIDER+MODEL.
	if cfg.Emotions.Enabled() {
		messageManager.RegisterProvider(service.NewEmotionsProvider(
			riverClient, tenantSettingsCache, service.EmotionsQueueName, cfg.Emotions.MaxAttempts,
			emotionsMetrics))
	}

	// On a settings write: evict the shared cache (so a changed setting is visible to the enqueue
	// gates immediately) and, when translation is enabled, enqueue a per-tenant re-translation
	// backfill (so existing records pick up a new target, not only newly ingested ones).
	if tenantSettingsCache != nil {
		listeners := []service.SettingsChangeListener{tenantSettingsCache}
		if translationEnabled {
			listeners = append(listeners, service.NewTranslationSettingsListener(
				riverClient, service.TranslationBackfillsQueueName, cfg.Translation.MaxAttempts))
		}

		tenantSettingsService.SetSettingsChangeListener(service.NewCompositeSettingsChangeListener(listeners...))
	}

	taxonomyRepo := repository.NewTaxonomyRepository(db)

	var taxonomyStarter service.TaxonomyRunStarter

	if cfg.Taxonomy.ServiceURL != "" || cfg.Taxonomy.ServiceToken != "" {
		taxonomyClient, err := service.NewTaxonomyClient(service.TaxonomyClientConfig{
			ServiceURL:   cfg.Taxonomy.ServiceURL,
			ServiceToken: cfg.Taxonomy.ServiceToken,
		}, nil)
		if err != nil {
			cleanupNewAppStartupFailure(context.Background(), messageManager, riverClient, tracerProvider, meterProvider)

			return nil, fmt.Errorf("create taxonomy client: %w", err)
		}

		taxonomyStarter = taxonomyClient
	}

	var taxonomyMetrics observability.TaxonomyMetrics
	if metrics != nil {
		taxonomyMetrics = metrics.Taxonomy
	}

	taxonomyService := service.NewTaxonomyService(service.NewTaxonomyServiceParams{
		Repo:                  taxonomyRepo,
		Starter:               taxonomyStarter,
		EmbeddingModel:        taxonomyEmbeddingModel,
		MinimumEmbeddingCount: cfg.Taxonomy.MinimumEmbeddedRecords,
		Metrics:               taxonomyMetrics,
	})
	taxonomyHandler := handlers.NewTaxonomyHandler(taxonomyService)
	feedbackRecordsHandler := handlers.NewFeedbackRecordsHandler(feedbackRecordsService)
	taxonomyInternalHandler := handlers.NewTaxonomyInternalHandler(taxonomyService)

	enrichmentStatusService := service.NewEnrichmentStatusService(service.NewEnrichmentStatusServiceParams{
		Repo:                  repository.NewEnrichmentStatusRepository(db),
		Settings:              tenantSettingsService,
		DefaultLang:           cfg.Translation.DefaultLanguage,
		TranslationConfigured: cfg.Translation.Provider != "" && cfg.Translation.Model != "",
		SentimentConfigured:   cfg.Sentiment.Enabled(),
		EmotionsConfigured:    cfg.Emotions.Enabled(),
	})
	enrichmentStatusHandler := handlers.NewEnrichmentStatusHandler(enrichmentStatusService)

	// The retry service resolves the same gates as the status service above, from the same config
	// and the same settings reader, so the two cannot disagree about whether an enrichment is
	// running — refusing a retry for an enrichment the status endpoint reports as enabled (or the
	// reverse) would be indefensible to a caller looking at both.
	enrichmentRetryService := service.NewEnrichmentRetryService(service.NewEnrichmentRetryServiceParams{
		Repo:                  repository.NewEnrichmentRetryRepository(db),
		Settings:              tenantSettingsService,
		DefaultLang:           cfg.Translation.DefaultLanguage,
		TranslationConfigured: cfg.Translation.Provider != "" && cfg.Translation.Model != "",
		SentimentConfigured:   cfg.Sentiment.Enabled(),
		EmotionsConfigured:    cfg.Emotions.Enabled(),
	})
	enrichmentRetryHandler := handlers.NewEnrichmentRetryHandler(enrichmentRetryService)

	healthHandler := handlers.NewHealthHandler()

	openapiHandler, err := handlers.NewOpenAPIHandler(handlers.ResolveOpenAPISpecPath(), cfg.Server.PublicBaseURL)
	if err != nil {
		cleanupNewAppStartupFailure(context.Background(), messageManager, riverClient, tracerProvider, meterProvider)

		return nil, fmt.Errorf("create openapi handler: %w", err)
	}

	server := newHTTPServer(
		cfg, healthHandler, openapiHandler, feedbackRecordsHandler, feedbackRecordsPurgeHandler,
		webhooksHandler, tenantDataHandler,
		tenantSettingsHandler, searchHandler,
		taxonomyHandler, taxonomyInternalHandler, enrichmentStatusHandler, enrichmentRetryHandler,
		meterProvider, tracerProvider,
	)

	return &App{
		cfg:            cfg,
		db:             db,
		server:         server,
		river:          riverClient,
		message:        messageManager,
		meterProvider:  meterProvider,
		tracerProvider: tracerProvider,
		metrics:        metrics,
		taxonomyRepo:   taxonomyRepo,
	}, nil
}

// newHTTPServer builds the HTTP server and muxes (no auth on /health or /openapi.*, API key on /v1/,
// internal taxonomy token on /internal/v1/taxonomy/ when configured).
// Handler chain: RequestID -> otelhttp(Logging(mux)) so access logs get trace_id/span_id from context.
func newHTTPServer(
	cfg *config.Config,
	health *handlers.HealthHandler,
	openapi *handlers.OpenAPIHandler,
	feedback *handlers.FeedbackRecordsHandler,
	feedbackPurge *handlers.FeedbackRecordsPurgeHandler,
	webhooks *handlers.WebhooksHandler,
	tenantData *handlers.TenantDataHandler,
	tenantSettings *handlers.TenantSettingsHandler,
	search *handlers.SearchHandler,
	taxonomy *handlers.TaxonomyHandler,
	taxonomyInternal *handlers.TaxonomyInternalHandler,
	enrichmentStatus *handlers.EnrichmentStatusHandler,
	enrichmentRetry *handlers.EnrichmentRetryHandler,
	meterProvider *sdkmetric.MeterProvider,
	tracerProvider *sdktrace.TracerProvider,
) *http.Server {
	public := http.NewServeMux()
	public.HandleFunc("GET /health", health.Check)
	public.HandleFunc("GET /openapi.yaml", openapi.YAML)
	public.HandleFunc("GET /openapi.json", openapi.JSON)

	protected := http.NewServeMux()
	protected.HandleFunc("POST /v1/feedback-records", feedback.Create)
	protected.HandleFunc("GET /v1/feedback-records", feedback.List)
	protected.HandleFunc("GET /v1/feedback-records/count", feedback.Count)
	protected.HandleFunc("GET /v1/feedback-records/{id}", feedback.Get)
	protected.HandleFunc("PATCH /v1/feedback-records/{id}", feedback.Update)
	protected.HandleFunc("DELETE /v1/feedback-records/{id}", feedback.Delete)
	protected.HandleFunc("DELETE /v1/feedback-records", feedback.DeleteByUser)

	protected.HandleFunc("POST /v1/webhooks", webhooks.Create)
	protected.HandleFunc("GET /v1/webhooks", webhooks.List)
	protected.HandleFunc("GET /v1/webhooks/{id}", webhooks.Get)
	protected.HandleFunc("PATCH /v1/webhooks/{id}", webhooks.Update)
	protected.HandleFunc("DELETE /v1/webhooks/{id}", webhooks.Delete)
	protected.HandleFunc("DELETE /v1/tenants/{tenant_id}/data", tenantData.Delete)
	// Under /v1/tenants (which the gateway does not route publicly) rather than beside the
	// feedback-records collection — see FeedbackRecordsPurgeHandler.Purge.
	protected.HandleFunc("DELETE /v1/tenants/{tenant_id}/feedback-records", feedbackPurge.Purge)
	protected.HandleFunc("GET /v1/tenants/{tenant_id}/settings", tenantSettings.Get)
	protected.HandleFunc("PUT /v1/tenants/{tenant_id}/settings", tenantSettings.Update)
	protected.HandleFunc("PATCH /v1/tenants/{tenant_id}/settings", tenantSettings.Patch)

	protected.HandleFunc("GET /v1/enrichment-status", enrichmentStatus.GetStatus)
	// Under /v1/tenants/, deliberately — see EnrichmentRetryHandler.Retry. Nothing routes that
	// prefix publicly, which is what keeps a provider-spending bulk operation off the internet.
	protected.HandleFunc("POST /v1/tenants/{tenant_id}/enrichments/retry", enrichmentRetry.Retry)

	// Search endpoints are always registered; when embeddings are disabled, the handler returns 503.
	protected.HandleFunc("POST /v1/feedback-records/search/semantic", search.SemanticSearch)
	protected.HandleFunc("GET /v1/feedback-records/{id}/similar", search.SimilarFeedback)

	routes.RegisterPublicTaxonomy(protected, taxonomy)

	protectedWithAuth := middleware.Auth(cfg.Server.HubAPIKey)(protected)

	mux := http.NewServeMux()
	mux.Handle("/v1/", protectedWithAuth)

	if cfg.Taxonomy.HubInternalAPIToken != "" {
		internalTaxonomy := http.NewServeMux()
		routes.RegisterInternalTaxonomy(internalTaxonomy, taxonomyInternal)
		internalTaxonomyWithAuth := middleware.Auth(cfg.Taxonomy.HubInternalAPIToken)(internalTaxonomy)
		mux.Handle("/internal/v1/taxonomy/", internalTaxonomyWithAuth)
	}

	mux.Handle("/", public)

	otelOpts := []otelhttp.Option{
		// Skip tracing and HTTP metrics for health checks to reduce noise.
		otelhttp.WithFilter(func(r *http.Request) bool {
			return r.URL.Path != "/health"
		}),
	}
	if meterProvider != nil {
		otelOpts = append(otelOpts, otelhttp.WithMeterProvider(meterProvider))
	}

	if tracerProvider != nil {
		otelOpts = append(otelOpts, otelhttp.WithTracerProvider(tracerProvider))
	}

	// ProblemErrors normalizes ServeMux's plain-text 404/405 into problem+json.
	// Logging runs inside otelhttp so r.Context() has the span when we log (trace_id/span_id in access logs).
	inner := middleware.Logging(middleware.ProblemErrors(mux))
	handler := otelhttp.NewHandler(inner, "hub-api", otelOpts...)
	handler = middleware.RequestID(handler)

	const (
		readTimeout  = 15 * time.Second
		writeTimeout = 15 * time.Second
		idleTimeout  = 60 * time.Second
	)

	return &http.Server{
		Addr:         ":" + cfg.Server.Port,
		Handler:      handler,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}
}

// Run starts the HTTP server (and optional queue depth poller for metrics), then blocks until ctx is cancelled.
// API is insert-only: no River workers run in this process; hub-worker runs them.
func (a *App) Run(ctx context.Context) error {
	runErr := make(chan error, 1)

	if a.metrics != nil && a.metrics.Events != nil {
		go runRiverQueueDepthPoller(ctx, a.db, a.metrics.Events)
	}

	if a.metrics != nil && a.metrics.EnrichmentBacklog != nil {
		translationConfigured := a.cfg.Translation.Provider != "" && a.cfg.Translation.Model != ""
		taxonomyEmbeddingModel := taxonomyEmbeddingBacklogModel(a.cfg)

		// Tracked, unlike the pollers above, because this one has cleanup that Shutdown must not
		// race: it withdraws the backlog series and releases the leader's advisory lock on the way
		// out. See App.awaitEnrichmentBacklogPoller.
		done := make(chan struct{})
		a.enrichmentBacklogDone = done

		go func() {
			defer close(done)

			runEnrichmentBacklogPoller(ctx, a.db, a.metrics.EnrichmentBacklog, a.metrics.EnrichmentFailures,
				enrichmentBacklogPollConfig{
					// Trim to stay consistent with NewEnrichmentStatusService (config already canonicalizes
					// this, so it's defensive symmetry) — the endpoint and the gauge resolve the same target.
					defaultLang:            strings.TrimSpace(a.cfg.Translation.DefaultLanguage),
					translationConfigured:  translationConfigured,
					sentimentConfigured:    a.cfg.Sentiment.Enabled(),
					emotionsConfigured:     a.cfg.Emotions.Enabled(),
					taxonomyEmbeddingModel: taxonomyEmbeddingModel,
				})
		}()
	}

	// Reap taxonomy runs orphaned in a non-terminal state, but only when the taxonomy service is wired
	// (no runs exist otherwise, so the sweep would be pointless).
	if a.taxonomyRepo != nil && (a.cfg.Taxonomy.ServiceURL != "" || a.cfg.Taxonomy.ServiceToken != "") {
		go runTaxonomyRunReaper(ctx, a.taxonomyRepo, taxonomyMetricsFromAggregate(a.metrics),
			a.cfg.Taxonomy.StuckRunTimeout.Duration(), a.cfg.Taxonomy.ReaperInterval.Duration())
	}

	go func() {
		slog.Info("Starting server", "port", a.cfg.Server.Port)

		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case runErr <- fmt.Errorf("server: %w", err):
			default:
			}
		}
	}()

	select {
	case err := <-runErr:
		return err
	case <-ctx.Done():
		return nil
	}
}

// taxonomyEmbeddingBacklogModel returns the model whose missing text records should be reported.
// Taxonomy embeddings are produced by successful translation writes, so the gauge is absent unless
// embeddings, translation, and the taxonomy integration are all configured.
func taxonomyEmbeddingBacklogModel(cfg *config.Config) string {
	provider, embeddingModel := embeddingProviderAndModel(cfg)
	if provider == "" || cfg.Translation.Provider == "" || cfg.Translation.Model == "" ||
		(cfg.Taxonomy.ServiceURL == "" && cfg.Taxonomy.ServiceToken == "") {
		return ""
	}

	return service.TaxonomyEmbeddingModel(embeddingModel, cfg.Taxonomy.EmbeddingModel)
}

// riverDepthQueues is the fixed queue set the depth poller reports — every queue the Hub
// declares, derived from service.JobKindSpecs so a new job kind cannot be added without its
// queue appearing on the gauge. The list bounds the gauge's queue-label cardinality; a queue with
// no backlog is reported as 0 so dashboards see the series exist.
var riverDepthQueues = service.JobQueueNames()

// enrichmentBacklogPollConfig configures runEnrichmentBacklogPoller: the deployment default target
// language and which enrichments are deployment-configured (only those emit a gauge).
type enrichmentBacklogPollConfig struct {
	defaultLang            string
	translationConfigured  bool
	sentimentConfigured    bool
	emotionsConfigured     bool
	taxonomyEmbeddingModel string
}

// runEnrichmentBacklogPoller periodically refreshes the aggregate enrichment-backlog gauge
// (eligible-but-unenriched records per enrichment, summed across all tenants) — a durable
// completeness signal complementing the transient River queue-depth gauge. Only
// deployment-configured enrichments are reported, and each scan is bounded by its own timeout.
func runEnrichmentBacklogPoller(
	ctx context.Context,
	db *pgxpool.Pool,
	backlog observability.EnrichmentBacklogMetrics,
	failures observability.EnrichmentFailureMetrics,
	cfg enrichmentBacklogPollConfig,
) {
	statusRepo := repository.NewEnrichmentStatusRepository(db)

	leader := repository.NewEnrichmentBacklogLeader(db)
	defer leader.Close(ctx)

	// Withdraw the series whenever this process stops being the leader, including the most common
	// case of all: shutdown. Close releases the advisory lock, so without this the meter provider's
	// final collect-and-export on shutdown would publish one last reading for a backlog this
	// process no longer owns.
	defer backlog.ClearEnrichmentPending()
	defer clearFailedRecords(failures)

	ticker := time.NewTicker(enrichmentBacklogInterval)
	defer ticker.Stop()

	consecutiveFailures := 0

	update := func() {
		queryCtx, cancel := context.WithTimeout(ctx, enrichmentBacklogQueryTimeout)
		defer cancel()

		// Exactly one replica holds leadership and scans; the rest skip until it goes away.
		counts, isLeader, err := leader.CountIfLeader(
			queryCtx, cfg.defaultLang, cfg.taxonomyEmbeddingModel)
		if err != nil {
			// Shutdown cancels the scan mid-flight. That is not a poll failure, and counting it
			// would fire the very alert this counter exists for on every rolling deploy.
			if ctx.Err() != nil {
				return
			}

			consecutiveFailures++

			// A failed scan also costs this process its leadership, so withdraw the series rather
			// than leave it frozen at the last good reading while the new leader publishes its own.
			backlog.ClearEnrichmentPending()
			clearFailedRecords(failures)

			// Count the failure so the gap is alertable, then escalate the log from warn to error
			// once failures persist: a single blip is noise, but a run of them means nobody is
			// publishing the backlog at all.
			backlog.RecordPollError(ctx)

			if consecutiveFailures >= enrichmentBacklogFailuresBeforeError {
				slog.ErrorContext(ctx, "enrichment backlog poll failing repeatedly; gauge is not being published",
					"error", err, "consecutive_failures", consecutiveFailures)
			} else {
				slog.WarnContext(ctx, "enrichment backlog poll failed",
					"error", err, "consecutive_failures", consecutiveFailures)
			}

			return
		}

		consecutiveFailures = 0

		if !isLeader {
			// Another replica owns this gauge and exports the single global series for it. Drop
			// anything this process exported while it was previously the leader, so a handover
			// leaves exactly one series rather than a live one plus a frozen one.
			backlog.ClearEnrichmentPending()
			clearFailedRecords(failures)

			return
		}

		if cfg.translationConfigured {
			backlog.SetEnrichmentPending(observability.EnrichmentTypeTranslation, counts.TranslationEligible-counts.TranslationDone)
		}

		if cfg.sentimentConfigured {
			backlog.SetEnrichmentPending(observability.EnrichmentTypeSentiment, counts.SentimentEligible-counts.SentimentDone)
		}

		if cfg.emotionsConfigured {
			backlog.SetEnrichmentPending(observability.EnrichmentTypeEmotions, counts.EmotionsEligible-counts.EmotionsDone)
		}

		if cfg.taxonomyEmbeddingModel != "" {
			backlog.SetEnrichmentPending(
				observability.EnrichmentTypeTaxonomyEmbedding, counts.TaxonomyEmbeddingPending)
		}

		// Refreshed by the SAME leader on the SAME tick, so the failure gauge cannot drift from the
		// backlog gauge and no second election is needed. Its query is cheap next to the backlog
		// scan: it reads the markers, of which there are few, not every feedback record.
		//
		// Its OWN deadline, not the leftover of queryCtx. Both need a bound — an unbounded count
		// can pin a pool connection until shutdown, which is what enrichmentBacklogQueryTimeout
		// exists to prevent — but sharing one budget couples them the wrong way round: the backlog
		// scan above is the documented whole-table sequential scan, so on a large deployment it
		// eats most of the window, and this count then times out on every tick and withdraws the
		// gauge for as long as the scan stays slow. A separate timeout off the same parent keeps
		// the bound and drops the coupling.
		failedCtx, cancelFailed := context.WithTimeout(ctx, enrichmentBacklogQueryTimeout)
		refreshFailedRecords(failedCtx, statusRepo, failures)
		cancelFailed()
	}

	update()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			update()
		}
	}
}

// runRiverQueueDepthPoller periodically updates the per-queue River backlog gauge. Covering
// every declared queue (not just default) means a provider outage or a backfill piling tens of
// thousands of jobs into an enrichment queue is visible in metrics before users notice the lag.
func runRiverQueueDepthPoller(ctx context.Context, db *pgxpool.Pool, eventMetrics observability.EventMetrics) {
	ticker := time.NewTicker(riverQueueDepthInterval)
	defer ticker.Stop()

	update := func() {
		rows, err := db.Query(ctx,
			`SELECT queue, COUNT(*), EXTRACT(EPOCH FROM (now() - MIN(created_at)))::float8 FROM river_job
			 WHERE queue = ANY($1) AND state IN ($2, $3, $4)
			 GROUP BY queue`,
			riverDepthQueues,
			rivertype.JobStateAvailable, rivertype.JobStateRetryable, rivertype.JobStateScheduled,
		)
		if err != nil {
			slog.WarnContext(ctx, "river queue depth poll failed", "error", err)

			return
		}
		defer rows.Close()

		counts := make(map[string]int, len(riverDepthQueues))
		ages := make(map[string]float64, len(riverDepthQueues))

		for rows.Next() {
			var (
				queue string
				count int
				age   float64
			)
			if err := rows.Scan(&queue, &count, &age); err != nil {
				slog.WarnContext(ctx, "river queue depth scan failed", "error", err)

				return
			}

			counts[queue] = count
			ages[queue] = age
		}

		if err := rows.Err(); err != nil {
			slog.WarnContext(ctx, "river queue depth poll failed", "error", err)

			return
		}

		for _, queue := range riverDepthQueues {
			eventMetrics.SetRiverQueueDepth(queue, counts[queue])
			eventMetrics.SetRiverQueueOldestAge(queue, ages[queue])
		}
	}

	update()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			update()
		}
	}
}

// stuckTaxonomyRunMessage is stored on runs the reaper force-fails. A stale run is retryable service
// unavailability, not an internal persistence invariant failure; this raw string is for operators.
const stuckTaxonomyRunMessage = "taxonomy run timed out without completing"

// runTaxonomyRunReaper periodically fails taxonomy runs orphaned in a non-terminal state — the
// taxonomy service crashed mid-run or its terminal callback was lost — so they stop being polled
// forever in the UI and generation can be retried. Idempotent: the repository's status filter skips
// runs that finished on their own between sweeps.
func runTaxonomyRunReaper(
	ctx context.Context, repo *repository.TaxonomyRepository, metrics observability.TaxonomyMetrics,
	timeout, interval time.Duration,
) {
	// A non-positive interval panics time.NewTicker, and a non-positive timeout would reap active
	// runs (cutoff would be now or in the future). Either is misconfiguration — disable the reaper.
	if interval <= 0 || timeout <= 0 {
		slog.WarnContext(ctx, "taxonomy stuck-run reaper disabled: non-positive interval or timeout",
			"interval", interval, "timeout", timeout)

		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	reap := func() {
		reaped, err := repo.FailStuckRuns(ctx, timeout, stuckTaxonomyRunMessage,
			models.TaxonomyRunFailureCodeServiceUnavailable)
		for _, run := range reaped {
			slog.ErrorContext(ctx, "taxonomy stuck-run reaper failed stalled run",
				"event", "hub.taxonomy.run.reaped", "run_id", run.ID, "tenant_id", run.TenantID,
				"scope_type", run.ScopeType, "source_type", run.SourceType,
				"source_id", run.SourceID, "field_id", run.FieldID,
				"failure_code", models.TaxonomyRunFailureCodeServiceUnavailable)
		}

		if len(reaped) > 0 && metrics != nil {
			metrics.RecordRunsReaped(ctx, int64(len(reaped)))

			for _, run := range reaped {
				metrics.RecordRunOutcome(ctx, string(models.TaxonomyRunStatusFailed),
					string(models.TaxonomyRunFailureCodeServiceUnavailable), string(run.ScopeType))

				started := run.CreatedAt
				if run.StartedAt != nil {
					started = *run.StartedAt
				}

				duration := max(run.FinishedAt.Sub(started), 0)
				metrics.RecordRunDuration(ctx, duration, string(models.TaxonomyRunStatusFailed),
					string(run.ScopeType))
			}
		}

		if err != nil {
			slog.WarnContext(ctx, "taxonomy stuck-run reaper failed", "error", err)
		}
	}

	reap()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reap()
		}
	}
}

func taxonomyMetricsFromAggregate(metrics *observability.Metrics) observability.TaxonomyMetrics {
	if metrics == nil {
		return nil
	}

	return metrics.Taxonomy
}

// shutdownObservability shuts down tracer and meter providers. Logs secondary errors, returns the first.
func shutdownObservability(ctx context.Context, tracer *sdktrace.TracerProvider, meter *sdkmetric.MeterProvider) error {
	var first error

	if tracer != nil {
		if err := observability.ShutdownTracerProvider(ctx, tracer); err != nil {
			first = err
		}
	}

	if meter != nil {
		if err := observability.ShutdownMeterProvider(ctx, meter); err != nil {
			if first == nil {
				first = err
			} else {
				slog.Error("shutdown meter provider", "error", err)
			}
		}
	}

	return first
}

func cleanupNewAppStartupFailure(
	ctx context.Context,
	messageManager *service.MessagePublisherManager,
	riverClient *river.Client[pgx.Tx],
	tracerProvider *sdktrace.TracerProvider,
	meterProvider *sdkmetric.MeterProvider,
) {
	cleanupCtx, cancel := context.WithTimeout(ctx, startupCleanupTimeout)
	defer cancel()

	if messageManager != nil {
		messageManager.Shutdown()
	}

	if riverClient != nil {
		if err := riverClient.Stop(cleanupCtx); err != nil {
			slog.Error("river stop after startup error", "error", err)
		}
	}

	if err := shutdownObservability(cleanupCtx, tracerProvider, meterProvider); err != nil {
		slog.Error("shutdown observability after startup error", "error", err)
	}
}

// Shutdown stops the server, message publisher, and River in order. Call after Run returns.
// Observability is shut down once via defer; its error is returned only when server and River shut down successfully.
func (a *App) Shutdown(ctx context.Context) (err error) {
	defer a.message.Shutdown()

	defer func() {
		obsErr := shutdownObservability(ctx, a.tracerProvider, a.meterProvider)
		if err == nil {
			err = obsErr
		} else if obsErr != nil {
			slog.Error("shutdown observability", "error", obsErr)
		}
	}()

	// Registered AFTER the observability defer so LIFO runs it FIRST: the meter provider's final
	// collect-and-export must not happen until the poller has withdrawn the backlog series, or
	// shutdown publishes one last reading for a backlog this process no longer owns -- the exact
	// thing the poller's ClearEnrichmentPending exists to prevent. A defer (not an inline call)
	// so the early-return error paths below are covered too.
	defer a.awaitEnrichmentBacklogPoller(ctx)

	if err = a.server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		if stopErr := a.river.Stop(ctx); stopErr != nil {
			slog.Error("river stop during server shutdown", "error", stopErr)
		}

		return fmt.Errorf("server shutdown: %w", err)
	}

	if err = a.river.Stop(ctx); err != nil {
		return fmt.Errorf("river stop: %w", err)
	}

	return nil
}

// awaitEnrichmentBacklogPoller blocks until the enrichment-backlog poller goroutine has returned,
// bounded by the shutdown context.
//
// Run returns as soon as its context is cancelled and does not join the poller, so without this the
// two race: Shutdown could reach the meter provider's final collect first and export a backlog
// reading for a process that is no longer the leader. Waiting also lets the leader's advisory-lock
// release and idle-timeout reset finish, which previously raced process exit with nothing awaiting
// them.
//
// On deadline this gives up rather than hanging shutdown past its budget -- the stale sample is a
// cosmetic gauge artifact, and a wedged shutdown is not. Postgres releases the session lock when the
// socket closes either way.
func (a *App) awaitEnrichmentBacklogPoller(ctx context.Context) {
	if a.enrichmentBacklogDone == nil {
		return
	}

	select {
	case <-a.enrichmentBacklogDone:
	case <-ctx.Done():
		slog.Warn("enrichment backlog poller did not finish before the shutdown deadline; " +
			"its final gauge reading may still be exported")
	}
}

// refreshFailedRecords republishes the cross-tenant failed-record gauge.
//
// A failed query WITHDRAWS this gauge rather than leaving its last reading published, following
// the rule EnrichmentBacklogMetrics states outright: exporting nothing is the honest state,
// because absence is visible and a stale value is not. The failure mode it avoids is specific —
// the backlog scan succeeds so this process keeps leadership, this query times out, and a
// dashboard would otherwise show a plausible, unchanging failure count that no longer reflects
// the database, with nothing absent to alert on.
//
// It withdraws only ITS OWN series. The backlog gauge is refreshed by a query that succeeded and
// has no reason to be torn down alongside.
func refreshFailedRecords(
	ctx context.Context,
	statusRepo *repository.EnrichmentStatusRepository,
	failures observability.EnrichmentFailureMetrics,
) {
	if failures == nil {
		return
	}

	counts, err := statusRepo.CountFailedRecordsAggregate(ctx)
	if err != nil {
		failures.ClearFailedRecords()

		// Shutdown cancels the query mid-flight; that is not a poll failure worth logging, for the
		// same reason the backlog poller skips it.
		if ctx.Err() == nil {
			slog.WarnContext(ctx, "enrichment failed-records poll failed; gauge withdrawn", "error", err)
		}

		return
	}

	// Zero the known buckets before applying, so an enrichment whose last failure was resolved
	// reports 0 rather than keeping its final non-zero reading forever. The query returns no row
	// for an empty bucket, which would otherwise be indistinguishable from "not refreshed".
	for _, enrichment := range []string{
		observability.EnrichmentTypeSentiment,
		observability.EnrichmentTypeEmotions,
		observability.EnrichmentTypeTranslation,
	} {
		failures.SetFailedRecords(enrichment, true, 0)
		failures.SetFailedRecords(enrichment, false, 0)
	}

	for _, count := range counts {
		failures.SetFailedRecords(count.Enrichment, count.Terminal, count.Count)
	}
}

// clearFailedRecords withdraws the gauge, for the same reason the backlog gauge is withdrawn: a
// process that is no longer the leader must export nothing rather than a frozen last reading.
func clearFailedRecords(failures observability.EnrichmentFailureMetrics) {
	if failures != nil {
		failures.ClearFailedRecords()
	}
}
