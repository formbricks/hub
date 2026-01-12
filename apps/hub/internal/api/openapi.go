package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/formbricks/hub/apps/hub/internal/config"
	"github.com/formbricks/hub/apps/hub/internal/ent"
	"github.com/formbricks/hub/apps/hub/internal/queue"
	"github.com/formbricks/hub/apps/hub/internal/webhook"
	"github.com/go-chi/chi/v5"
)

// GenerateOpenAPISpec generates the OpenAPI specification without running the server
func GenerateOpenAPISpec(cfg *config.Config, client *ent.Client, dispatcher *webhook.Dispatcher, enrichmentQueue queue.Queue, logger *slog.Logger) ([]byte, error) {
	// Create a temporary router just to generate the spec
	router := chi.NewRouter()

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
			URL:         fmt.Sprintf("http://%s:%d", cfg.Host, cfg.Port),
			Description: "API server",
		},
	}
	// Disable default docs
	humaConfig.DocsPath = ""

	api := humachi.New(router, humaConfig)

	// Create a temporary server just to register routes
	tempServer := &Server{
		config:          cfg,
		client:          client,
		dispatcher:      dispatcher,
		enrichmentQueue: enrichmentQueue,
		logger:          logger,
		api:             api,
		router:          router,
	}

	// Register all routes
	tempServer.registerRoutes()

	// Extract the OpenAPI spec from Huma
	spec := api.OpenAPI()

	// Marshal to JSON
	return json.MarshalIndent(spec, "", "  ")
}

// ExportOpenAPISpec exports the OpenAPI spec to a writer in JSON format
func ExportOpenAPISpec(cfg *config.Config, client *ent.Client, dispatcher *webhook.Dispatcher, enrichmentQueue queue.Queue, logger *slog.Logger, w io.Writer) error {
	spec, err := GenerateOpenAPISpec(cfg, client, dispatcher, enrichmentQueue, logger)
	if err != nil {
		return fmt.Errorf("failed to generate OpenAPI spec: %w", err)
	}

	_, err = w.Write(spec)
	if err != nil {
		return fmt.Errorf("failed to write OpenAPI spec: %w", err)
	}

	return nil
}

// ServeOpenAPISpec is a handler that serves the OpenAPI spec
func ServeOpenAPISpec(cfg *config.Config, client *ent.Client, dispatcher *webhook.Dispatcher, enrichmentQueue queue.Queue, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := ExportOpenAPISpec(cfg, client, dispatcher, enrichmentQueue, logger, w); err != nil {
			logger.Error("failed to serve OpenAPI spec", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}
}
