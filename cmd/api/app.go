package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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
	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/internal/workers"
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
}

var (
	errEmbeddingProviderAPIKeyRequired     = errors.New("EMBEDDING_PROVIDER_API_KEY is required for this provider")
	errEmbeddingGoogleGeminiConfigRequired = errors.New(
		"google-gemini requires EMBEDDING_GOOGLE_CLOUD_PROJECT and EMBEDDING_GOOGLE_CLOUD_LOCATION")
)

const (
	riverQueueDepthInterval = 15 * time.Second
	startupCleanupTimeout   = 5 * time.Second
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

// setupEmbeddingSearchHandler creates embedding client, worker, and search handler when embeddings are enabled.
// Returns (handler, nil) or (nil, err). Caller should use errors.Is for service.ErrEmbeddingProviderAPIKey and
// service.ErrEmbeddingGoogleGeminiConfig to return app-level sentinel errors.
func setupEmbeddingSearchHandler(
	ctx context.Context,
	cfg *config.Config,
	embeddingProviderName, embeddingModel, embeddingDocPrefix string,
	feedbackRecordsService *service.FeedbackRecordsService,
	embeddingsRepo *repository.EmbeddingsRepository,
	embeddingMetrics observability.EmbeddingMetrics,
	metrics *observability.Metrics,
	meterProvider *sdkmetric.MeterProvider,
	riverWorkers *river.Workers,
) (*handlers.SearchHandler, error) {
	embeddingCfg := service.EmbeddingClientConfig{
		Provider:            embeddingProviderName,
		ProviderAPIKey:      cfg.Embedding.ProviderAPIKey,
		Model:               embeddingModel,
		BaseURL:             cfg.Embedding.BaseURL,
		Normalize:           cfg.Embedding.Normalize,
		GoogleCloudProject:  cfg.Embedding.GoogleCloudProject,
		GoogleCloudLocation: cfg.Embedding.GoogleCloudLocation,
	}
	if err := service.ValidateEmbeddingConfig(embeddingCfg); err != nil {
		return nil, fmt.Errorf("embedding config: %w", err)
	}

	embeddingClient, err := service.NewEmbeddingClient(ctx, embeddingCfg)
	if err != nil {
		return nil, fmt.Errorf("create embedding client: %w", err)
	}

	embeddingWorker := workers.NewFeedbackEmbeddingWorker(
		feedbackRecordsService, embeddingClient, embeddingDocPrefix, embeddingMetrics)
	river.AddWorker(riverWorkers, embeddingWorker)

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

	// Install TraceContextHandler unconditionally so request_id (and trace_id/span_id when tracing is on) appear in logs.
	defaultHandler := slog.Default().Handler()
	slog.SetDefault(slog.New(observability.NewTraceContextHandler(defaultHandler)))

	if tracerProvider != nil {
		otel.SetTracerProvider(tracerProvider)
	}

	if meterProvider != nil {
		otel.SetMeterProvider(meterProvider)
	}

	perEventTimeout := time.Duration(cfg.MessagePublisher.PerEventTimeoutSec) * time.Second
	messageManager := service.NewMessagePublisherManager(cfg.MessagePublisher.BufferSize, perEventTimeout, eventMetrics)

	webhooksRepo := repository.NewWebhooksRepository(db)
	webhookSender := service.NewWebhookSenderImpl(
		webhooksRepo, webhookMetrics, cfg.Webhook.URLBlacklist, cfg.Webhook.HTTPTimeout.Duration(), nil)

	deps := workers.RiverDeps{
		WebhooksRepo:       webhooksRepo,
		WebhookSender:      webhookSender,
		WebhookHTTPTimeout: cfg.Webhook.HTTPTimeout.Duration(),
		WebhookMetrics:     webhookMetrics,
	}

	feedbackRecordsRepo := repository.NewFeedbackRecordsRepository(db)
	embeddingsRepo := repository.NewEmbeddingsRepository(db)
	tenantDataRepo := repository.NewTenantDataRepository(db, cfg.TenantData.PurgeLockTimeout.Duration())
	embeddingProviderName, embeddingModel := embeddingProviderAndModel(cfg)
	embeddingModelForDB := embeddingModel

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

	// The eager-clear (nulling stale enrichment outputs on a value_text edit) fires only on this
	// API PATCH path, so wire its counter here; the worker/backfill service instances leave it unset.
	if metrics != nil {
		feedbackRecordsService.SetEnrichmentClearMetrics(metrics.EnrichmentClear)
	}

	// Tenant settings service: shared by the emotions worker's authoritative gate (registered
	// below), the settings HTTP handler, and the enqueue-path settings cache.
	tenantSettingsRepo := repository.NewTenantSettingsRepository(db)
	tenantSettingsService := service.NewTenantSettingsService(tenantSettingsRepo)

	// Shared worker/queue registration first (webhook + optional embedding added below).
	riverWorkers, queues := workers.NewRiverWorkersAndQueues(cfg, deps, 1)

	var searchHandler *handlers.SearchHandler

	if embeddingProviderName != "" {
		embeddingDocPrefix := service.EmbeddingPrefixForProvider(embeddingProviderName)

		var err error

		searchHandler, err = setupEmbeddingSearchHandler(
			context.Background(), cfg,
			embeddingProviderName, embeddingModel, embeddingDocPrefix,
			feedbackRecordsService, embeddingsRepo, embeddingMetrics,
			metrics, meterProvider, riverWorkers)
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

		queues[service.EmbeddingsQueueName] = river.QueueConfig{MaxWorkers: 1}
	} else {
		searchHandler = handlers.NewSearchHandler(nil) // 503 when embeddings disabled
	}

	// Register the translation worker and declare its queue so the River client can
	// enqueue translation jobs (River requires the job kind registered and the queue
	// declared at insert time); the jobs are processed by hub-worker, not in this
	// process. Gated on TRANSLATION_PROVIDER+MODEL like embeddings; the enqueue
	// provider is registered below, after the River client and tenant settings exist.
	if cfg.Translation.Provider != "" && cfg.Translation.Model != "" {
		translationCfg := service.TranslationClientConfig{
			Provider:            cfg.Translation.Provider,
			ProviderAPIKey:      cfg.Translation.ProviderAPIKey,
			Model:               cfg.Translation.Model,
			BaseURL:             cfg.Translation.BaseURL,
			GoogleCloudProject:  cfg.Translation.GoogleCloudProject,
			GoogleCloudLocation: cfg.Translation.GoogleCloudLocation,
		}

		translationClient, translationErr := service.NewTranslationClient(context.Background(), translationCfg)
		if translationErr != nil {
			cleanupNewAppStartupFailure(context.Background(), messageManager, nil, tracerProvider, meterProvider)

			return nil, fmt.Errorf("translation config: %w", translationErr)
		}

		river.AddWorker(riverWorkers, workers.NewFeedbackTranslationWorker(feedbackRecordsService, translationClient, translationMetrics))

		queues[service.TranslationsQueueName] = river.QueueConfig{MaxWorkers: 1}

		// Per-tenant re-translation backfill, enqueued by the settings-change listener
		// below. Registered here only so the River client can validate the kind and queue
		// at insert time; the fan-out is processed by hub-worker.
		river.AddWorker(riverWorkers, workers.NewTenantTranslationBackfillWorker(feedbackRecordsService, cfg.Translation.MaxAttempts))

		queues[service.TranslationBackfillsQueueName] = river.QueueConfig{MaxWorkers: 1}
	}

	// Register the sentiment worker and declare its queue so the River client can enqueue
	// sentiment jobs (kind + queue must be known at insert time); the jobs are processed by
	// hub-worker, not in this process. Gated on SENTIMENT_PROVIDER+MODEL; the enqueue provider
	// is registered below, after the River client exists.
	if cfg.Sentiment.Enabled() {
		sentimentClient, sentimentErr := service.NewSentimentClient(context.Background(), service.SentimentClientConfig{
			Provider:            cfg.Sentiment.Provider,
			ProviderAPIKey:      cfg.Sentiment.ProviderAPIKey,
			Model:               cfg.Sentiment.Model,
			BaseURL:             cfg.Sentiment.BaseURL,
			GoogleCloudProject:  cfg.Sentiment.GoogleCloudProject,
			GoogleCloudLocation: cfg.Sentiment.GoogleCloudLocation,
		})
		if sentimentErr != nil {
			cleanupNewAppStartupFailure(context.Background(), messageManager, nil, tracerProvider, meterProvider)

			return nil, fmt.Errorf("sentiment config: %w", sentimentErr)
		}

		river.AddWorker(riverWorkers, workers.NewFeedbackSentimentWorker(
			feedbackRecordsService, tenantSettingsService, sentimentClient, sentimentMetrics))

		queues[service.SentimentsQueueName] = river.QueueConfig{MaxWorkers: 1}
	}

	// Register the emotions worker and declare its queue so the River client can enqueue emotion
	// jobs (kind + queue must be known at insert time); the jobs are processed by hub-worker, not
	// in this process. Gated on EMOTIONS_PROVIDER+MODEL; the enqueue provider is registered below,
	// after the River client exists.
	if cfg.Emotions.Enabled() {
		emotionsClient, emotionsErr := service.NewEmotionsClient(context.Background(), service.EmotionsClientConfig{
			Provider:            cfg.Emotions.Provider,
			ProviderAPIKey:      cfg.Emotions.ProviderAPIKey,
			Model:               cfg.Emotions.Model,
			BaseURL:             cfg.Emotions.BaseURL,
			GoogleCloudProject:  cfg.Emotions.GoogleCloudProject,
			GoogleCloudLocation: cfg.Emotions.GoogleCloudLocation,
		})
		if emotionsErr != nil {
			cleanupNewAppStartupFailure(context.Background(), messageManager, nil, tracerProvider, meterProvider)

			return nil, fmt.Errorf("emotions config: %w", emotionsErr)
		}

		river.AddWorker(riverWorkers, workers.NewFeedbackEmotionsWorker(
			feedbackRecordsService, tenantSettingsService, emotionsClient, emotionsMetrics))

		queues[service.EmotionsQueueName] = river.QueueConfig{MaxWorkers: 1}
	}

	riverClient, err := river.NewClient(riverpgxv5.New(db), &river.Config{
		Queues:  queues,
		Workers: riverWorkers,
	})
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
	}

	webhooksService := service.NewWebhooksService(webhooksRepo, messageManager, cfg.Webhook.MaxCount, cfg.Webhook.URLBlacklist)
	webhooksHandler := handlers.NewWebhooksHandler(webhooksService)
	tenantDataService := service.NewTenantDataService(tenantDataRepo)
	tenantDataHandler := handlers.NewTenantDataHandler(tenantDataService)

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

	taxonomyService := service.NewTaxonomyService(service.NewTaxonomyServiceParams{
		Repo:                  taxonomyRepo,
		Starter:               taxonomyStarter,
		EmbeddingModel:        embeddingModelForDB,
		MinimumEmbeddingCount: cfg.Taxonomy.MinimumEmbeddedRecords,
	})
	taxonomyHandler := handlers.NewTaxonomyHandler(taxonomyService)
	feedbackRecordsHandler := handlers.NewFeedbackRecordsHandler(feedbackRecordsService)
	taxonomyInternalHandler := handlers.NewTaxonomyInternalHandler(taxonomyService)
	healthHandler := handlers.NewHealthHandler()

	openapiHandler, err := handlers.NewOpenAPIHandler(handlers.ResolveOpenAPISpecPath(), cfg.Server.PublicBaseURL)
	if err != nil {
		cleanupNewAppStartupFailure(context.Background(), messageManager, riverClient, tracerProvider, meterProvider)

		return nil, fmt.Errorf("create openapi handler: %w", err)
	}

	server := newHTTPServer(
		cfg, healthHandler, openapiHandler, feedbackRecordsHandler, webhooksHandler, tenantDataHandler,
		tenantSettingsHandler, searchHandler,
		taxonomyHandler, taxonomyInternalHandler,
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
	webhooks *handlers.WebhooksHandler,
	tenantData *handlers.TenantDataHandler,
	tenantSettings *handlers.TenantSettingsHandler,
	search *handlers.SearchHandler,
	taxonomy *handlers.TaxonomyHandler,
	taxonomyInternal *handlers.TaxonomyInternalHandler,
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
	protected.HandleFunc("GET /v1/tenants/{tenant_id}/settings", tenantSettings.Get)
	protected.HandleFunc("PUT /v1/tenants/{tenant_id}/settings", tenantSettings.Update)
	protected.HandleFunc("PATCH /v1/tenants/{tenant_id}/settings", tenantSettings.Patch)

	// Search endpoints are always registered; when embeddings are disabled, the handler returns 503.
	protected.HandleFunc("POST /v1/feedback-records/search/semantic", search.SemanticSearch)
	protected.HandleFunc("GET /v1/feedback-records/{id}/similar", search.SimilarFeedback)

	protected.HandleFunc("GET /v1/taxonomy/fields", taxonomy.ListFields)
	protected.HandleFunc("POST /v1/taxonomy/runs", taxonomy.CreateRun)
	protected.HandleFunc("GET /v1/taxonomy/runs", taxonomy.ListRuns)
	protected.HandleFunc("GET /v1/taxonomy/runs/active/tree", taxonomy.GetActiveTree)
	protected.HandleFunc("GET /v1/taxonomy/runs/{run_id}", taxonomy.GetRun)
	protected.HandleFunc("GET /v1/taxonomy/runs/{run_id}/tree", taxonomy.GetTree)
	protected.HandleFunc("PATCH /v1/taxonomy/nodes/{node_id}", taxonomy.RenameNode)
	protected.HandleFunc("DELETE /v1/taxonomy/nodes/{node_id}", taxonomy.RemoveNode)
	protected.HandleFunc("GET /v1/taxonomy/nodes/{node_id}/records", taxonomy.ListNodeRecords)

	protectedWithAuth := middleware.Auth(cfg.Server.HubAPIKey)(protected)

	mux := http.NewServeMux()
	mux.Handle("/v1/", protectedWithAuth)

	if cfg.Taxonomy.HubInternalAPIToken != "" {
		internalTaxonomy := http.NewServeMux()
		internalTaxonomy.HandleFunc("GET /internal/v1/taxonomy/auth-check", taxonomyInternal.AuthCheck)
		internalTaxonomy.HandleFunc("GET /internal/v1/taxonomy/runs/{run_id}/input", taxonomyInternal.GetRunInput)
		internalTaxonomy.HandleFunc("PUT /internal/v1/taxonomy/runs/{run_id}/result", taxonomyInternal.CompleteRun)
		internalTaxonomy.HandleFunc("POST /internal/v1/taxonomy/runs/{run_id}/failed", taxonomyInternal.FailRun)
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

// riverDepthQueues is the fixed queue set the depth poller reports — every queue the Hub
// declares. The list bounds the gauge's queue-label cardinality; a queue with no backlog is
// reported as 0 so dashboards see the series exist.
var riverDepthQueues = []string{
	river.QueueDefault,
	service.EmbeddingsQueueName,
	service.TranslationsQueueName,
	service.TranslationBackfillsQueueName,
	service.SentimentsQueueName,
	service.EmotionsQueueName,
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
