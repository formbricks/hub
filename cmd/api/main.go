package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	httpSwagger "github.com/swaggo/http-swagger"
	_ "github.com/xernobyl/formbricks_worktrial/docs" // Import generated docs

	"github.com/xernobyl/formbricks_worktrial/internal/api/handlers"
	"github.com/xernobyl/formbricks_worktrial/internal/api/middleware"
	"github.com/xernobyl/formbricks_worktrial/internal/config"
	"github.com/xernobyl/formbricks_worktrial/internal/repository"
	"github.com/xernobyl/formbricks_worktrial/internal/service"
	"github.com/xernobyl/formbricks_worktrial/pkg/database"
)

// @title Formbricks Hub API
// @version 1.0
// @description API for managing experience data collection
//
// @contact.name Tiago
// @contact.email xxxxx@xxxxx.com

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and your API key.

func main() {
	ctx := context.Background()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Initialize database connection
	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Initialize repository, service, and handler layers
	experienceRepo := repository.NewExperienceRepository(db)
	experienceService := service.NewExperienceService(experienceRepo)
	experienceHandler := handlers.NewExperienceHandler(experienceService)
	healthHandler := handlers.NewHealthHandler()

	// Initialize API key repository for authentication
	apiKeyRepo := repository.NewAPIKeyRepository(db)

	// Set up public endpoints (no authentication required)
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("GET /health", healthHandler.Check)
	publicMux.HandleFunc("GET /swagger/", httpSwagger.WrapHandler)

	// Apply middleware to public endpoints
	var publicHandler http.Handler = publicMux
	// publicHandler = middleware.CORS(publicHandler) // CORS disabled

	// Set up protected endpoints (authentication required)
	protectedMux := http.NewServeMux()
	protectedMux.HandleFunc("POST /v1/experiences", experienceHandler.Create)
	protectedMux.HandleFunc("GET /v1/experiences", experienceHandler.List)
	protectedMux.HandleFunc("GET /v1/experiences/{id}", experienceHandler.Get)
	protectedMux.HandleFunc("PATCH /v1/experiences/{id}", experienceHandler.Update)
	protectedMux.HandleFunc("DELETE /v1/experiences/{id}", experienceHandler.Delete)

	protectedMux.HandleFunc("GET /v1/experiences/search", experienceHandler.Search)

	// Apply middleware to protected endpoints
	var protectedHandler http.Handler = protectedMux
	protectedHandler = middleware.Auth(apiKeyRepo)(protectedHandler)
	// protectedHandler = middleware.CORS(protectedHandler)	// CORS disabled

	// Combine both handlers
	mainMux := http.NewServeMux()
	mainMux.Handle("/v1/", protectedHandler)
	mainMux.Handle("/", publicHandler) // Catch-all for public routes (/health, /swagger/, etc.)

	// Apply logging to all requests
	handler := middleware.Logging(mainMux)

	// Create HTTP server
	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		slog.Info("Starting server", "port", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
		os.Exit(1)
	}

	slog.Info("Server exited")
}
