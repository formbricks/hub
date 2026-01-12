package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/formbricks/hub/apps/hub/internal/config"
	"github.com/formbricks/hub/apps/hub/internal/ent"
	custommiddleware "github.com/formbricks/hub/apps/hub/internal/middleware"
	"github.com/formbricks/hub/apps/hub/internal/queue"
	"github.com/formbricks/hub/apps/hub/internal/webhook"
)

// Server holds the HTTP server and dependencies
type Server struct {
	config          *config.Config
	client          *ent.Client
	dispatcher      *webhook.Dispatcher
	logger          *slog.Logger
	api             huma.API
	router          *chi.Mux
	enrichmentQueue queue.Queue
}

// NewServer creates a new API server
func NewServer(cfg *config.Config, client *ent.Client, dispatcher *webhook.Dispatcher, enrichmentQueue queue.Queue, logger *slog.Logger) *Server {
	// Create Chi router
	router := chi.NewRouter()

	// Add Chi middleware (router-specific, runs first)
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	// Limit request body size to 10MB to prevent memory exhaustion attacks
	router.Use(middleware.Compress(5))
	router.Use(custommiddleware.MaxBodySize(10 * 1024 * 1024)) // 10MB limit

	// Rate limiting - protects against DoS and excessive OpenAI API usage
	rateLimiter := custommiddleware.NewRateLimiter(
		cfg.RateLimitPerIP,
		cfg.RateLimitBurst,
		cfg.RateLimitGlobal,
		cfg.RateLimitGlobalBurst,
		logger,
	)
	router.Use(rateLimiter.Middleware())
	logger.Info("rate limiting enabled",
		"per_ip_rate", cfg.RateLimitPerIP,
		"per_ip_burst", cfg.RateLimitBurst,
		"global_rate", cfg.RateLimitGlobal,
		"global_burst", cfg.RateLimitGlobalBurst)

	// Health check endpoint (outside of Huma API and auth)
	router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"status":"ok"}`)
	})

	// Create Huma API with Scalar docs
	humaConfig := huma.DefaultConfig("Formbricks Hub API", "1.0.0")
	humaConfig.Info.Description = `Feedback record storage service for the Formbricks ecosystem.

📚 Full Documentation: https://hub.formbricks.com
🚀 Quick Start: https://hub.formbricks.com/quickstart
🔌 Connector Ecosystem: Coming soon`
	humaConfig.Info.Contact = &huma.Contact{
		Name:  "Formbricks Team",
		URL:   "https://formbricks.com",
		Email: "support@formbricks.com",
	}
	humaConfig.Info.License = &huma.License{
		Name: "Apache-2.0",
		URL:  "https://www.apache.org/licenses/LICENSE-2.0",
	}
	humaConfig.Servers = []*huma.Server{
		{
			URL:         fmt.Sprintf("http://localhost:%d", cfg.Port),
			Description: "Development server",
		},
	}
	// Disable default docs (we'll use Scalar instead)
	humaConfig.DocsPath = ""

	api := humachi.New(router, humaConfig)

	// Add Huma middleware (router-agnostic, runs after Chi middleware)
	// Logging middleware
	api.UseMiddleware(custommiddleware.Logging(logger))

	// Optional API key authentication
	if cfg.APIKey != "" {
		logger.Info("API key authentication enabled")
		api.UseMiddleware(custommiddleware.APIKeyAuth(api, cfg.APIKey))
	}

	// Custom /docs endpoint using Scalar with enhanced configuration
	router.Get("/docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<!doctype html>
<html>
  <head>
    <title>Formbricks Hub API Documentation</title>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <meta name="description" content="Interactive API documentation for Formbricks Hub" />
    <style>
      body {
        margin: 0;
        padding: 0;
      }
    </style>
  </head>
  <body>
    <script
      id="api-reference"
      data-url="/openapi.json"
      data-proxy-url="https://api.scalar.com/request-proxy"
      data-show-sidebar="true"
      data-default-open-all-tags="false"></script>
    <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
  </body>
</html>`
		_, _ = w.Write([]byte(html))
	})

	server := &Server{
		config:          cfg,
		client:          client,
		dispatcher:      dispatcher,
		logger:          logger,
		api:             api,
		router:          router,
		enrichmentQueue: enrichmentQueue,
	}

	// Register API routes
	server.registerRoutes()

	return server
}

// registerRoutes registers all API routes
func (s *Server) registerRoutes() {
	// Feedback record endpoints
	RegisterFeedbackRecordRoutes(s.api, s.client, s.dispatcher, s.logger, s.enrichmentQueue)

	// Search endpoints
	RegisterSearchRoutes(s.api, s.config, s.client, s.logger)
}

// Router returns the underlying Chi router for serving
func (s *Server) Router() http.Handler {
	return s.router
}

// Start starts the HTTP server
func (s *Server) Start(ctx context.Context) error {
	addr := s.config.Address()
	s.logger.Info("starting server",
		"address", addr,
		"environment", s.config.Environment)

	server := &http.Server{
		Addr:    addr,
		Handler: s.Router(),
	}

	// Start server in a goroutine
	errChan := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	s.logger.Info("server started successfully",
		"address", addr,
		"docs", fmt.Sprintf("http://%s/docs", addr),
		"openapi", fmt.Sprintf("http://%s/openapi.json", addr))

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		s.logger.Info("shutting down server gracefully...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errChan:
		return err
	}
}
