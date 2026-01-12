// Package api contains integration tests for the Hub API.
//
// These tests use testcontainers-go to spin up real PostgreSQL 18 containers with pgvector,
// ensuring production parity and testing Postgres-specific features like JSONB
// columns with GIN indexes and vector embeddings.
//
// Requirements:
//   - Docker must be running
//   - First run downloads pgvector/pgvector:pg18 image (~90MB)
//   - Each test file gets a fresh container for isolation
//
// Run tests: go test ./internal/api/
package api

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/formbricks/hub/apps/hub/internal/config"
	"github.com/formbricks/hub/apps/hub/internal/ent"
	"github.com/formbricks/hub/apps/hub/internal/webhook"
)

// setupTestAPI creates a test API with a real Postgres container
func setupTestAPI(t *testing.T) (humatest.TestAPI, *ent.Client, func()) {
	t.Helper()

	ctx := context.Background()

	// Start Postgres container with pgvector
	postgresContainer, err := postgres.Run(ctx,
		"pgvector/pgvector:pg18",
		postgres.WithDatabase("test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}

	// Get connection string
	connStr, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	// Enable pgvector extension before creating Ent client
	// Use standard database/sql to execute the extension creation
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("failed to open database connection: %v", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		_ = db.Close()
		t.Fatalf("failed to enable pgvector extension: %v", err)
	}
	_ = db.Close()

	// Connect with Ent
	client, err := ent.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}

	// Run migrations
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	// Setup logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Create test config - disable rate limiting for tests
	cfg := &config.Config{
		Port:                 8080,
		Host:                 "localhost",
		Environment:          "test",
		RateLimitPerIP:       999999, // Effectively disable for tests
		RateLimitBurst:       999999,
		RateLimitGlobal:      999999,
		RateLimitGlobalBurst: 999999,
	}

	// Create webhook dispatcher (no webhooks in tests)
	dispatcher := webhook.NewDispatcher([]string{}, logger)

	// Create server (no enrichment queue in tests)
	server := NewServer(cfg, client, dispatcher, nil, logger)

	// Routes are already registered via NewServer.registerRoutes()

	// Create test API
	testAPI := humatest.Wrap(t, server.api)

	// Cleanup function
	cleanup := func() {
		if err := client.Close(); err != nil {
			t.Logf("failed to close database connection: %v", err)
		}
		if err := testcontainers.TerminateContainer(postgresContainer); err != nil {
			t.Logf("failed to terminate container: %v", err)
		}
	}

	return testAPI, client, cleanup
}

func TestCreateFeedbackRecord(t *testing.T) {
	api, _, cleanup := setupTestAPI(t)
	defer cleanup()

	t.Run("create with required fields only", func(t *testing.T) {
		resp := api.Post("/v1/feedback-records", map[string]interface{}{
			"source_type": "survey",
			"field_id":    "q1",
			"field_type":  "rating",
		})

		if resp.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
		}

		// Check response body exists and has valid content
		bodyStr := resp.Body.String()
		if bodyStr == "" {
			t.Fatal("expected non-empty response body")
		}
		// Basic check for ID presence in JSON
		if !strings.Contains(bodyStr, `"id"`) {
			t.Fatal("expected response to contain id field")
		}
	})

	t.Run("create with all fields", func(t *testing.T) {
		resp := api.Post("/v1/feedback-records", map[string]interface{}{
			"source_type":     "nps",
			"source_id":       "survey-123",
			"source_name":     "Q1 NPS Survey",
			"field_id":        "nps_score",
			"field_label":     "How likely are you to recommend us?",
			"field_type":      "nps",
			"value_number":    9.0,
			"metadata":        map[string]interface{}{"country": "US"},
			"language":        "en",
			"user_identifier": "user-abc",
		})

		if resp.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
		}

		// Check value_number in response
		if !strings.Contains(resp.Body.String(), `"value_number"`) {
			t.Fatal("expected response to contain value_number field")
		}
	})

	t.Run("validation error - missing required field", func(t *testing.T) {
		resp := api.Post("/v1/feedback-records", map[string]interface{}{
			"source_type": "survey",
			// Missing field_id and field_type
		})

		if resp.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected status 422, got %d", resp.Code)
		}
	})

	t.Run("validation error - source_type too short", func(t *testing.T) {
		resp := api.Post("/v1/feedback-records", map[string]interface{}{
			"source_type": "", // Too short
			"field_id":    "q1",
			"field_type":  "text",
		})

		if resp.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected status 422, got %d", resp.Code)
		}
	})
}

func TestGetFeedbackRecord(t *testing.T) {
	api, client, cleanup := setupTestAPI(t)
	defer cleanup()

	// Create a test feedback record
	ctx := context.Background()
	fr, err := client.FeedbackRecord.Create().
		SetSourceType("survey").
		SetFieldID("q1").
		SetFieldType("text").
		Save(ctx)
	if err != nil {
		t.Fatalf("failed to create test feedback record: %v", err)
	}

	t.Run("get existing feedback record", func(t *testing.T) {
		resp := api.Get("/v1/feedback-records/" + fr.ID.String())

		if resp.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
		}

		// Verify response has expected fields
		if !strings.Contains(resp.Body.String(), fr.ID.String()) {
			t.Fatal("expected response to contain the feedback record ID")
		}
	})

	t.Run("get non-existing feedback record", func(t *testing.T) {
		resp := api.Get("/v1/feedback-records/01932c8a-8b9e-7000-8000-000000000000")

		if resp.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", resp.Code)
		}
	})

	t.Run("invalid UUID format", func(t *testing.T) {
		resp := api.Get("/v1/feedback-records/invalid-uuid")

		// Huma returns 422 for format validation errors
		if resp.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected status 422, got %d", resp.Code)
		}
	})
}

func TestListFeedbackRecords(t *testing.T) {
	api, client, cleanup := setupTestAPI(t)
	defer cleanup()

	// Create test feedback records
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, err := client.FeedbackRecord.Create().
			SetSourceType("survey").
			SetFieldID("q1").
			SetFieldType("rating").
			Save(ctx)
		if err != nil {
			t.Fatalf("failed to create test feedback record: %v", err)
		}
	}

	t.Run("list all feedback records", func(t *testing.T) {
		resp := api.Get("/v1/feedback-records")

		if resp.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
		}

		// Check response has data and total fields
		bodyStr := resp.Body.String()
		if !strings.Contains(bodyStr, `"total"`) || !strings.Contains(bodyStr, `"data"`) {
			t.Fatal("expected response to contain total and data fields")
		}
	})

	t.Run("list with pagination", func(t *testing.T) {
		resp := api.Get("/v1/feedback-records?limit=2&offset=1")

		if resp.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp.Code)
		}

		bodyStr := resp.Body.String()
		if !strings.Contains(bodyStr, `"limit":2`) {
			t.Fatal("expected response to contain limit:2")
		}
	})

	t.Run("filter by source_type", func(t *testing.T) {
		// Create feedback record with different source_type
		_, err := client.FeedbackRecord.Create().
			SetSourceType("nps").
			SetFieldID("q1").
			SetFieldType("number").
			Save(ctx)
		if err != nil {
			t.Fatal(err)
		}

		resp := api.Get("/v1/feedback-records?source_type=nps")

		if resp.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp.Code)
		}

		// Just check we get a valid response
		if !strings.Contains(resp.Body.String(), `"total"`) {
			t.Fatal("expected response to contain total field")
		}
	})
}

func TestUpdateFeedbackRecord(t *testing.T) {
	api, client, cleanup := setupTestAPI(t)
	defer cleanup()

	// Create a test feedback record
	ctx := context.Background()
	fr, err := client.FeedbackRecord.Create().
		SetSourceType("survey").
		SetFieldID("q1").
		SetFieldType("text").
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("update feedback record", func(t *testing.T) {
		resp := api.Patch("/v1/feedback-records/"+fr.ID.String(), map[string]interface{}{
			"value_number": 8.5,
		})

		if resp.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
		}

		// Verify updated value in response
		if !strings.Contains(resp.Body.String(), `"value_number":8.5`) {
			t.Fatal("expected response to contain updated value_number")
		}
	})

	t.Run("update non-existing feedback record", func(t *testing.T) {
		resp := api.Patch("/v1/feedback-records/01932c8a-8b9e-7000-8000-000000000000", map[string]interface{}{
			"value_number": 5.0,
		})

		if resp.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", resp.Code)
		}
	})
}

func TestDeleteFeedbackRecord(t *testing.T) {
	api, client, cleanup := setupTestAPI(t)
	defer cleanup()

	// Create a test feedback record
	ctx := context.Background()
	fr, err := client.FeedbackRecord.Create().
		SetSourceType("survey").
		SetFieldID("q1").
		SetFieldType("text").
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("delete existing feedback record", func(t *testing.T) {
		resp := api.Delete("/v1/feedback-records/" + fr.ID.String())

		if resp.Code != http.StatusNoContent {
			t.Fatalf("expected status 204, got %d: %s", resp.Code, resp.Body.String())
		}

		// Verify it's deleted
		_, err := client.FeedbackRecord.Get(ctx, fr.ID)
		if !ent.IsNotFound(err) {
			t.Fatal("expected feedback record to be deleted")
		}
	})

	t.Run("delete non-existing feedback record", func(t *testing.T) {
		resp := api.Delete("/v1/feedback-records/01932c8a-8b9e-7000-8000-000000000000")

		if resp.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", resp.Code)
		}
	})

	t.Run("create feedback record with invalid field type", func(t *testing.T) {
		resp := api.Post("/v1/feedback-records", map[string]any{
			"source_type": "test",
			"field_id":    "invalid_type_test",
			"field_type":  "invalid_type_name",
			"value_text":  "test",
		})

		if resp.Code != http.StatusUnprocessableEntity && resp.Code != http.StatusBadRequest {
			t.Fatalf("expected status 422 or 400 for invalid field_type, got %d", resp.Code)
		}

		body := resp.Body.String()
		if !strings.Contains(body, "field_type") {
			t.Fatalf("expected error message to mention field_type, got: %s", body)
		}
	})
}
