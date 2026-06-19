package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/api/handlers"
	"github.com/formbricks/hub/internal/api/middleware"
	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/database"
)

type testEventProvider interface {
	PublishEvent(ctx context.Context, event service.Event)
}

// defaultTestDatabaseURL is the default Postgres URL used by compose (postgres/postgres/test_db).
// Used when DATABASE_URL is not set (e.g. CI uses job env; local can rely on .env).
const defaultTestDatabaseURL = "postgres://postgres:postgres@localhost:5432/test_db?sslmode=disable"

const (
	tenantDataCleanupTimeout = 2 * time.Second
	testWebhookURL           = "https://192.0.2.1/webhook"
	testWebhookURLV2         = "https://192.0.2.1/webhook-v2"
)

// requireUUIDv7 asserts that an ID uses UUID version 7 with the RFC4122 variant.
func requireUUIDv7(t *testing.T, id uuid.UUID) {
	t.Helper()

	require.NotEqual(t, uuid.Nil, id)
	require.Equal(t, uuid.Version(7), id.Version())
	require.Equal(t, uuid.RFC4122, id.Variant())
}

// setupTestServer creates a test HTTP server with all routes configured.
// Database URL comes from env (DATABASE_URL) when set; otherwise config.Load() uses its default.
func setupTestServer(t *testing.T) (server *httptest.Server, cleanup func()) {
	return setupTestServerWithEventProviders(t)
}

func setupTestServerWithEventProviders(
	t *testing.T, providers ...testEventProvider,
) (server *httptest.Server, cleanup func()) {
	ctx := context.Background()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultTestDatabaseURL
	}

	t.Setenv("API_KEY", testAPIKey)
	t.Setenv("DATABASE_URL", databaseURL)

	// Load configuration (cleanenv reads .env if present and env vars)
	cfg, err := config.Load()
	require.NoError(t, err, "Failed to load configuration")

	// Initialize database connection
	db, err := database.NewPostgresPool(ctx, cfg.Database.URL,
		database.WithPoolConfig(cfg.Database.PoolConfig()),
	)
	require.NoError(t, err, "Failed to connect to database")

	// Initialize message publisher manager for tests (no providers required)
	perEventTimeout := time.Duration(cfg.MessagePublisher.PerEventTimeoutSec) * time.Second

	messageManager := service.NewMessagePublisherManager(cfg.MessagePublisher.BufferSize, perEventTimeout, nil)
	for _, provider := range providers {
		messageManager.RegisterProvider(provider)
	}

	// Webhooks
	webhooksRepo := repository.NewWebhooksRepository(db)
	webhooksService := service.NewWebhooksService(webhooksRepo, messageManager, cfg.Webhook.MaxCount, cfg.Webhook.URLBlacklist)
	webhooksHandler := handlers.NewWebhooksHandler(webhooksService)

	// Initialize repository, service, and handler layers
	feedbackRecordsRepo := repository.NewFeedbackRecordsRepository(db)
	embeddingsRepo := repository.NewEmbeddingsRepository(db)
	tenantDataRepo := repository.NewTenantDataRepository(db, cfg.TenantData.PurgeLockTimeout.Duration())
	feedbackRecordsService := service.NewFeedbackRecordsService(
		feedbackRecordsRepo,
		embeddingsRepo,
		"model-name",
		messageManager,
		nil,
		"",
		0,
	)
	feedbackRecordsHandler := handlers.NewFeedbackRecordsHandler(feedbackRecordsService)
	tenantDataService := service.NewTenantDataService(tenantDataRepo)
	tenantDataHandler := handlers.NewTenantDataHandler(tenantDataService)
	tenantSettingsRepo := repository.NewTenantSettingsRepository(db)
	tenantSettingsService := service.NewTenantSettingsService(tenantSettingsRepo)
	tenantSettingsHandler := handlers.NewTenantSettingsHandler(tenantSettingsService)
	healthHandler := handlers.NewHealthHandler()

	// Set up public endpoints
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("GET /health", healthHandler.Check)

	var publicHandler http.Handler = publicMux

	// Set up protected endpoints
	protectedMux := http.NewServeMux()
	protectedMux.HandleFunc("POST /v1/feedback-records", feedbackRecordsHandler.Create)
	protectedMux.HandleFunc("GET /v1/feedback-records", feedbackRecordsHandler.List)
	protectedMux.HandleFunc("GET /v1/feedback-records/{id}", feedbackRecordsHandler.Get)
	protectedMux.HandleFunc("PATCH /v1/feedback-records/{id}", feedbackRecordsHandler.Update)
	protectedMux.HandleFunc("DELETE /v1/feedback-records/{id}", feedbackRecordsHandler.Delete)
	protectedMux.HandleFunc("DELETE /v1/feedback-records", feedbackRecordsHandler.DeleteByUser)
	protectedMux.HandleFunc("POST /v1/webhooks", webhooksHandler.Create)
	protectedMux.HandleFunc("GET /v1/webhooks", webhooksHandler.List)
	protectedMux.HandleFunc("GET /v1/webhooks/{id}", webhooksHandler.Get)
	protectedMux.HandleFunc("PATCH /v1/webhooks/{id}", webhooksHandler.Update)
	protectedMux.HandleFunc("DELETE /v1/webhooks/{id}", webhooksHandler.Delete)
	protectedMux.HandleFunc("DELETE /v1/tenants/{tenant_id}/data", tenantDataHandler.Delete)
	protectedMux.HandleFunc("GET /v1/tenants/{tenant_id}/settings", tenantSettingsHandler.Get)
	protectedMux.HandleFunc("PUT /v1/tenants/{tenant_id}/settings", tenantSettingsHandler.Update)

	var protectedHandler http.Handler = protectedMux

	protectedHandler = middleware.Auth(cfg.Server.HubAPIKey)(protectedHandler)

	// Combine both handlers
	mainMux := http.NewServeMux()
	mainMux.Handle("/v1/", protectedHandler)
	mainMux.Handle("/", publicHandler)

	// Create test server
	server = httptest.NewServer(mainMux)

	// Cleanup function: stop message publisher worker, then server and db
	cleanup = func() {
		server.Close()
		messageManager.Shutdown()
		db.Close()
	}

	return server, cleanup
}

// decodeData decodes JSON responses directly from the response body.
// The API handlers use RespondJSON which encodes responses directly without wrapping.
func decodeData(resp *http.Response, v any) error {
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

func TestHealthEndpoint(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/health", http.NoBody)
	require.NoError(t, err)
	resp, err := (&http.Client{}).Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Health endpoint returns plain text "OK"
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "OK", string(body))
}

func TestCreateFeedbackRecord(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Test without authentication
	t.Run("Unauthorized without API key", func(t *testing.T) {
		reqBody := map[string]any{
			"source_type":   "formbricks",
			"submission_id": "feedback",
			"field_id":      "feedback",
			"field_type":    "text",
			"value_text":    "Great product!",
		}
		body, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{}).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	})

	// Test with invalid API key
	t.Run("Unauthorized with invalid API key", func(t *testing.T) {
		reqBody := map[string]any{
			"source_type":   "formbricks",
			"submission_id": "feedback",
			"field_id":      "feedback",
			"field_type":    "text",
			"value_text":    "Great product!",
		}
		body, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer wrong-key-12345")
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	})

	// Test with empty API key in header
	t.Run("Unauthorized with empty API key", func(t *testing.T) {
		reqBody := map[string]any{
			"source_type":   "formbricks",
			"submission_id": "feedback",
			"field_id":      "feedback",
			"field_type":    "text",
			"value_text":    "Great product!",
		}
		body, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer ")
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	})

	// Test with malformed Authorization header
	t.Run("Unauthorized with malformed Authorization header", func(t *testing.T) {
		reqBody := map[string]any{
			"source_type":   "formbricks",
			"submission_id": "feedback",
			"field_id":      "feedback",
			"field_type":    "text",
			"value_text":    "Great product!",
		}
		body, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "InvalidFormat")
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	})

	// Test with valid authentication (use unique submission_id to avoid 409 from leftover data)
	t.Run("Success with valid API key", func(t *testing.T) {
		userID := "create-test-user"
		reqBody := map[string]any{
			"source_type":   "formbricks",
			"submission_id": uuid.New().String(),
			"tenant_id":     "test-tenant",
			"user_id":       userID,
			"field_id":      "feedback",
			"field_type":    "text",
			"value_text":    "Great product!",
		}
		body, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)

		var result models.FeedbackRecord

		err = decodeData(resp, &result)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		requireUUIDv7(t, result.ID)
		assert.Equal(t, "formbricks", result.SourceType)
		assert.Equal(t, "feedback", result.FieldID)
		assert.Equal(t, models.FieldTypeText, result.FieldType)
		require.NotNil(t, result.UserID)
		assert.Equal(t, userID, *result.UserID)
		assert.NotNil(t, result.ValueText)
		assert.Equal(t, "Great product!", *result.ValueText)
	})

	// Test with invalid request body
	t.Run("Bad request with missing required fields", func(t *testing.T) {
		reqBody := map[string]any{
			"field_id": "feedback",
		}
		body, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	})
}

func TestListFeedbackRecords(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Test with invalid API key
	t.Run("Unauthorized with invalid API key", func(t *testing.T) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
			server.URL+"/v1/feedback-records?tenant_id=test-tenant", http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer wrong-key-12345")

		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	})

	// Create a test feedback record first (unique submission_id per run)
	reqBody := map[string]any{
		"source_type":   "formbricks",
		"submission_id": uuid.New().String(),
		"tenant_id":     "test-tenant",
		"field_id":      "nps_score",
		"field_type":    "number",
		"value_number":  9,
	}
	body, err := json.Marshal(reqBody)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")
	createResp, err := client.Do(req)
	require.NoError(t, err)
	// decodeData not needed for this create; we only need a record to list
	require.NoError(t, createResp.Body.Close())

	// Test missing tenant_id returns 400
	t.Run("Missing tenant_id returns 400", func(t *testing.T) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/v1/feedback-records", http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	})

	// Test listing feedback records
	t.Run("List all feedback records", func(t *testing.T) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
			server.URL+"/v1/feedback-records?tenant_id=test-tenant", http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.ListFeedbackRecordsResponse

		err = decodeData(resp, &result)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		assert.NotEmpty(t, result.Data)
	})

	// Test with filters
	t.Run("List with source_type filter", func(t *testing.T) {
		listURL := server.URL + "/v1/feedback-records?tenant_id=test-tenant&source_type=formbricks&limit=10"
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, listURL, http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.ListFeedbackRecordsResponse

		err = decodeData(resp, &result)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		for _, exp := range result.Data {
			assert.Equal(t, "formbricks", exp.SourceType)
		}
	})

	t.Run("List with user_id filter", func(t *testing.T) {
		tenantID := "tenant-user-id-filter-" + uuid.New().String()
		userID := "list-filter-user"

		for _, item := range []struct {
			fieldID string
			userID  string
		}{
			{"matching", userID},
			{"other", "other-list-filter-user"},
		} {
			body, err := json.Marshal(map[string]any{
				"source_type":   "formbricks",
				"submission_id": uuid.New().String(),
				"tenant_id":     tenantID,
				"user_id":       item.userID,
				"field_id":      item.fieldID,
				"field_type":    "text",
				"value_text":    item.fieldID,
			})
			require.NoError(t, err)
			req, err := http.NewRequestWithContext(
				context.Background(), http.MethodPost, server.URL+"/v1/feedback-records", bytes.NewBuffer(body),
			)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+testAPIKey)
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			require.NoError(t, err)
			require.Equal(t, http.StatusCreated, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
		}

		listURL := server.URL + "/v1/feedback-records?tenant_id=" + tenantID + "&user_id=" + userID
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, listURL, http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.ListFeedbackRecordsResponse

		err = decodeData(resp, &result)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		require.Len(t, result.Data, 1)
		require.NotNil(t, result.Data[0].UserID)
		assert.Equal(t, userID, *result.Data[0].UserID)
	})

	// Test cursor pagination
	t.Run("Cursor pagination", func(t *testing.T) {
		tenantID := "tenant-cursor-test"
		// Create 3 records for pagination
		for i := range 3 {
			body, _ := json.Marshal(map[string]any{
				"source_type":   "formbricks",
				"submission_id": uuid.New().String(),
				"tenant_id":     tenantID,
				"field_id":      "q1",
				"field_type":    "text",
				"value_text":    fmt.Sprintf("record %d", i),
			})
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
			req.Header.Set("Authorization", "Bearer "+testAPIKey)
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			require.NoError(t, err)
			require.Equal(t, http.StatusCreated, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
		}

		// First page (limit=2)
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
			server.URL+"/v1/feedback-records?tenant_id="+tenantID+"&limit=2", http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var page1 models.ListFeedbackRecordsResponse

		err = decodeData(resp, &page1)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		assert.Len(t, page1.Data, 2)
		assert.NotEmpty(t, page1.NextCursor)

		// Second page using cursor (URL-encode cursor since it may contain = padding)
		listURL := fmt.Sprintf("%s/v1/feedback-records?tenant_id=%s&limit=2&cursor=%s",
			server.URL, url.QueryEscape(tenantID), url.QueryEscape(page1.NextCursor))
		req2, err := http.NewRequestWithContext(context.Background(), http.MethodGet, listURL, http.NoBody)
		require.NoError(t, err)
		req2.Header.Set("Authorization", "Bearer "+testAPIKey)
		resp2, err := client.Do(req2)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp2.StatusCode)

		var page2 models.ListFeedbackRecordsResponse

		err = decodeData(resp2, &page2)
		require.NoError(t, err)
		require.NoError(t, resp2.Body.Close())

		assert.GreaterOrEqual(t, len(page2.Data), 1)

		// Invalid cursor returns 400
		req3, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
			server.URL+"/v1/feedback-records?tenant_id="+tenantID+"&cursor=invalid", http.NoBody)
		require.NoError(t, err)
		req3.Header.Set("Authorization", "Bearer "+testAPIKey)
		resp3, err := client.Do(req3)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp3.StatusCode)
		require.NoError(t, resp3.Body.Close())
	})
}

func TestFeedbackRecordsSubmissionID(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}
	subID := uuid.New().String() // unique per run to avoid 409 from leftover data
	tenantID := "tenant-submission-test"

	// Create two records with same submission_id (multi-field submission)
	createPayload := func(fieldID string, value any) map[string]any {
		p := map[string]any{
			"source_type":   "formbricks",
			"submission_id": subID,
			"tenant_id":     tenantID,
			"field_id":      fieldID,
			"field_type":    "text",
		}
		if v, ok := value.(string); ok {
			p["value_text"] = v
		}

		return p
	}

	for _, item := range []struct {
		fieldID string
		value   string
	}{
		{"reason", "cancelled"},
		{"comment", "Too expensive"},
	} {
		body, err := json.Marshal(createPayload(item.fieldID, item.value))
		require.NoError(t, err)
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	}

	// List by submission_id
	t.Run("List by submission_id", func(t *testing.T) {
		listURL := server.URL + "/v1/feedback-records?submission_id=" + subID + "&tenant_id=" + tenantID
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, listURL, http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.ListFeedbackRecordsResponse

		err = decodeData(resp, &result)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		assert.GreaterOrEqual(t, len(result.Data), 2)

		for _, rec := range result.Data {
			assert.Equal(t, subID, rec.SubmissionID)
			assert.Equal(t, tenantID, rec.TenantID)
		}
	})
}

func TestFeedbackRecordsSubmissionIDUniqueConstraint(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}
	subID := uuid.New().String() // unique per run so first create succeeds
	tenantID := "tenant-unique"
	fieldID := "reason"

	reqBody := map[string]any{
		"source_type":   "formbricks",
		"submission_id": subID,
		"tenant_id":     tenantID,
		"field_id":      fieldID,
		"field_type":    "text",
		"value_text":    "first",
	}
	body, err := json.Marshal(reqBody)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	// Duplicate (same tenant_id, submission_id, field_id) must return 409
	req2, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
	require.NoError(t, err)
	req2.Header.Set("Authorization", "Bearer "+testAPIKey)
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := client.Do(req2)
	require.NoError(t, err)

	defer func() { _ = resp2.Body.Close() }()

	assert.Equal(t, http.StatusConflict, resp2.StatusCode)
}

func TestGetFeedbackRecord(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Test with invalid API key
	t.Run("Unauthorized with invalid API key", func(t *testing.T) {
		badURL := server.URL + "/v1/feedback-records/00000000-0000-0000-0000-000000000000"
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, badURL, http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer wrong-key-12345")

		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	})

	// Create a test feedback record (unique submission_id per run)
	reqBody := map[string]any{
		"source_type":   "formbricks",
		"submission_id": uuid.New().String(),
		"tenant_id":     "test-tenant",
		"field_id":      "rating",
		"field_type":    "number",
		"value_number":  5,
	}
	body, err := json.Marshal(reqBody)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(req)
	require.NoError(t, err)

	var created models.FeedbackRecord

	err = decodeData(createResp, &created)
	require.NoError(t, err)
	require.NoError(t, createResp.Body.Close())

	// Test getting the feedback record by ID
	t.Run("Get existing feedback record", func(t *testing.T) {
		getURL := fmt.Sprintf("%s/v1/feedback-records/%s", server.URL, created.ID)
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, getURL, http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.FeedbackRecord

		err = decodeData(resp, &result)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		assert.Equal(t, created.ID, result.ID)
		assert.Equal(t, "formbricks", result.SourceType)
	})

	t.Run("Get non-existent feedback record", func(t *testing.T) {
		notFoundURL := server.URL + "/v1/feedback-records/00000000-0000-0000-0000-000000000000"
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, notFoundURL, http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	})
}

func TestUpdateFeedbackRecord(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Test with invalid API key
	t.Run("Unauthorized with invalid API key", func(t *testing.T) {
		updateBody := map[string]any{
			"value_text": "Updated comment",
		}
		body, err := json.Marshal(updateBody)
		require.NoError(t, err)

		baseURL := server.URL + "/v1/feedback-records/00000000-0000-0000-0000-000000000000"
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPatch, baseURL, bytes.NewBuffer(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer wrong-key-12345")
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	})

	// Create a test feedback record (unique submission_id per run)
	reqBody := map[string]any{
		"source_type":   "formbricks",
		"submission_id": uuid.New().String(),
		"tenant_id":     "test-tenant",
		"field_id":      "comment",
		"field_type":    "text",
		"value_text":    "Initial comment",
	}
	body, err := json.Marshal(reqBody)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(req)
	require.NoError(t, err)

	var created models.FeedbackRecord

	err = decodeData(createResp, &created)
	require.NoError(t, err)
	require.NoError(t, createResp.Body.Close())

	// Test updating the feedback record
	t.Run("Update feedback record", func(t *testing.T) {
		userID := "updated-user"
		updateBody := map[string]any{
			"value_text": "Updated comment",
			"user_id":    userID,
		}
		body, err := json.Marshal(updateBody)
		require.NoError(t, err)

		patchURL := fmt.Sprintf("%s/v1/feedback-records/%s", server.URL, created.ID)
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPatch, patchURL, bytes.NewBuffer(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.FeedbackRecord

		err = decodeData(resp, &result)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		assert.Equal(t, created.ID, result.ID)
		require.NotNil(t, result.UserID)
		assert.Equal(t, userID, *result.UserID)
		assert.NotNil(t, result.ValueText)
		assert.Equal(t, "Updated comment", *result.ValueText)
	})
}

func TestDeleteFeedbackRecord(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Test with invalid API key
	t.Run("Unauthorized with invalid API key", func(t *testing.T) {
		deleteURL := server.URL + "/v1/feedback-records/00000000-0000-0000-0000-000000000000"
		req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, deleteURL, http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer wrong-key-12345")

		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	})

	// Create a test feedback record (unique submission_id per run)
	reqBody := map[string]any{
		"source_type":   "formbricks",
		"submission_id": uuid.New().String(),
		"tenant_id":     "test-tenant",
		"field_id":      "temp",
		"field_type":    "text",
		"value_text":    "To be deleted",
	}
	body, err := json.Marshal(reqBody)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(req)
	require.NoError(t, err)

	var created models.FeedbackRecord

	err = decodeData(createResp, &created)
	require.NoError(t, err)
	require.NoError(t, createResp.Body.Close())

	// Test deleting the feedback record
	t.Run("Delete feedback record", func(t *testing.T) {
		delURL := fmt.Sprintf("%s/v1/feedback-records/%s", server.URL, created.ID)
		req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, delURL, http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	})

	// Verify it's deleted
	t.Run("Verify deletion", func(t *testing.T) {
		verifyURL := fmt.Sprintf("%s/v1/feedback-records/%s", server.URL, created.ID)
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, verifyURL, http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	})
}

func TestDeleteFeedbackRecordsByUser(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}
	userID := "user-delete-test-user-" + uuid.New().String()
	subID := uuid.New().String() // unique per run to avoid 409 from leftover data

	// Create several feedback records with the same user_id across tenants.
	tenantA := "test-tenant-a"
	tenantB := "test-tenant-b"
	createRecord := func(fieldID, tenantID string, valueNum float64) string {
		t.Helper()

		body, err := json.Marshal(map[string]any{
			"source_type":   "formbricks",
			"submission_id": subID,
			"tenant_id":     tenantID,
			"user_id":       userID,
			"field_id":      fieldID,
			"field_type":    "number",
			"value_number":  valueNum,
		})
		require.NoError(t, err)

		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode, "create record %s/%s", tenantID, fieldID)

		var rec models.FeedbackRecord

		err = decodeData(resp, &rec)
		require.NoError(t, err)

		require.NoError(t, resp.Body.Close())

		return rec.ID.String()
	}

	requireStatus := func(id string, status int) {
		t.Helper()

		getReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/v1/feedback-records/"+id, http.NoBody)
		require.NoError(t, err)
		getReq.Header.Set("Authorization", "Bearer "+testAPIKey)

		getResp, err := client.Do(getReq)
		require.NoError(t, err)
		assert.Equal(t, status, getResp.StatusCode)
		require.NoError(t, getResp.Body.Close())
	}

	tenantAID := createRecord("nps_1", tenantA, 8)
	tenantBID1 := createRecord("nps_2", tenantB, 9)
	tenantBID2 := createRecord("nps_3", tenantB, 10)

	// Providing tenant_id scopes deletion to only that tenant.
	scopedDelURL := fmt.Sprintf("%s/v1/feedback-records?user_id=%s&tenant_id=%s",
		server.URL, url.QueryEscape(userID), url.QueryEscape(tenantA))
	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, scopedDelURL, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var scopedResp models.DeleteFeedbackRecordsByUserResponse

	err = decodeData(resp, &scopedResp)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, int64(1), scopedResp.DeletedCount)

	requireStatus(tenantAID, http.StatusNotFound)
	requireStatus(tenantBID1, http.StatusOK)
	requireStatus(tenantBID2, http.StatusOK)

	tenantAID2 := createRecord("nps_4", tenantA, 7)

	// Omitting tenant_id deletes every remaining matching record for GDPR erasure, regardless of tenant.
	userDeleteURL := server.URL + "/v1/feedback-records?user_id=" + url.QueryEscape(userID)
	req, err = http.NewRequestWithContext(context.Background(), http.MethodDelete, userDeleteURL, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)

	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var userDeleteResp models.DeleteFeedbackRecordsByUserResponse

	err = decodeData(resp, &userDeleteResp)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, int64(3), userDeleteResp.DeletedCount)
	assert.Equal(t, "Successfully deleted 3 feedback records", userDeleteResp.Message)

	// Verify records are gone
	for _, id := range []string{tenantAID2, tenantBID1, tenantBID2} {
		requireStatus(id, http.StatusNotFound)
	}

	// Deleting again with no matching records returns 0.
	userDeleteURL2 := server.URL + "/v1/feedback-records?user_id=" + url.QueryEscape(userID)
	req2, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, userDeleteURL2, http.NoBody)
	require.NoError(t, err)
	req2.Header.Set("Authorization", "Bearer "+testAPIKey)
	resp2, err := client.Do(req2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var userDeleteResp2 models.DeleteFeedbackRecordsByUserResponse

	err = decodeData(resp2, &userDeleteResp2)
	require.NoError(t, err)
	require.NoError(t, resp2.Body.Close())
	assert.Equal(t, int64(0), userDeleteResp2.DeletedCount)
}

func TestDeleteTenantData(t *testing.T) {
	eventRecorder := &tenantDataEventRecorder{}

	server, cleanup := setupTestServerWithEventProviders(t, eventRecorder)
	defer cleanup()

	ctx := context.Background()
	cfg, err := config.Load()
	require.NoError(t, err)
	db, err := database.NewPostgresPool(ctx, cfg.Database.URL,
		database.WithPoolConfig(cfg.Database.PoolConfig()),
	)
	require.NoError(t, err)

	defer db.Close()

	embeddingsRepo := repository.NewEmbeddingsRepository(db)
	client := &http.Client{}
	tenantA := "tenant-data-delete-a-" + uuid.NewString()
	tenantB := "tenant-data-delete-b-" + uuid.NewString()
	subID := uuid.NewString()
	modelName := "model-name"

	tenantARecord1 := createTenantDataFeedbackRecord(ctx, t, client, server.URL, tenantA, subID, "tenant-data-delete-a-1")
	tenantARecord2 := createTenantDataFeedbackRecord(ctx, t, client, server.URL, tenantA, subID, "tenant-data-delete-a-2")
	tenantBRecord := createTenantDataFeedbackRecord(ctx, t, client, server.URL, tenantB, subID, "tenant-data-delete-b-1")
	tenantAWebhook := createTenantDataWebhook(ctx, t, client, server.URL, tenantA, "tenant-data-delete-a")

	tenantBWebhook := createTenantDataWebhook(ctx, t, client, server.URL, tenantB, "tenant-data-delete-b")
	defer cleanupTenantDataBestEffort(ctx, client, server.URL, tenantB)

	embedding := make([]float32, models.EmbeddingVectorDimensions)
	embedding[0] = 0.25
	require.NoError(t, embeddingsRepo.Upsert(ctx, tenantARecord1.ID, modelName, embedding))
	require.NoError(t, embeddingsRepo.Upsert(ctx, tenantARecord2.ID, modelName, embedding))
	require.NoError(t, embeddingsRepo.Upsert(ctx, tenantBRecord.ID, modelName, embedding))

	tenantATaxonomyRunID := createTenantDataTaxonomyGraph(
		ctx, t, db, tenantA, tenantARecord1.ID, "tenant-data-delete-a-taxonomy-"+uuid.NewString(), tenantARecord1.FieldID,
	)
	tenantBTaxonomyRunID := createTenantDataTaxonomyGraph(
		ctx, t, db, tenantB, tenantBRecord.ID, "tenant-data-delete-b-taxonomy-"+uuid.NewString(), tenantBRecord.FieldID,
	)

	deleteResp := deleteTenantData(ctx, t, client, server.URL, tenantA)
	assert.Equal(t, tenantA, deleteResp.TenantID)
	assert.Equal(t, int64(2), deleteResp.DeletedFeedbackRecords)
	assert.Equal(t, int64(2), deleteResp.DeletedEmbeddings)
	assert.Equal(t, int64(1), deleteResp.DeletedWebhooks)
	requireNoTenantDataDeleteEvents(t, eventRecorder)

	requireTenantDataResourceStatus(
		ctx, t, client, fmt.Sprintf("%s/v1/feedback-records/%s", server.URL, tenantARecord1.ID), http.StatusNotFound,
	)
	requireTenantDataResourceStatus(
		ctx, t, client, fmt.Sprintf("%s/v1/feedback-records/%s", server.URL, tenantARecord2.ID), http.StatusNotFound,
	)
	requireTenantDataResourceStatus(
		ctx, t, client, fmt.Sprintf("%s/v1/webhooks/%s", server.URL, tenantAWebhook.ID), http.StatusNotFound,
	)
	_, err = embeddingsRepo.GetEmbeddingByFeedbackRecordAndModel(ctx, tenantARecord1.ID, modelName)
	require.ErrorIs(t, err, repository.ErrEmbeddingNotFound)
	requireTenantDataTaxonomyRunDeleted(ctx, t, db, tenantATaxonomyRunID)

	requireTenantDataResourceStatus(
		ctx, t, client, fmt.Sprintf("%s/v1/feedback-records/%s", server.URL, tenantBRecord.ID), http.StatusOK,
	)
	requireTenantDataResourceStatus(ctx, t, client, fmt.Sprintf("%s/v1/webhooks/%s", server.URL, tenantBWebhook.ID), http.StatusOK)
	_, err = embeddingsRepo.GetEmbeddingByFeedbackRecordAndModel(ctx, tenantBRecord.ID, modelName)
	require.NoError(t, err)
	requireTenantDataTaxonomyRunPresent(ctx, t, db, tenantBTaxonomyRunID)

	repeatedResp := deleteTenantData(ctx, t, client, server.URL, tenantA)
	assert.Equal(t, int64(0), repeatedResp.DeletedFeedbackRecords)
	assert.Equal(t, int64(0), repeatedResp.DeletedEmbeddings)
	assert.Equal(t, int64(0), repeatedResp.DeletedWebhooks)
	requireNoTenantDataDeleteEvents(t, eventRecorder)
}

type tenantDataEventRecorder struct {
	mu     sync.Mutex
	events []service.Event
}

func (r *tenantDataEventRecorder) PublishEvent(_ context.Context, event service.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.events = append(r.events, event)
}

func (r *tenantDataEventRecorder) tenantDataDeleteEventCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	count := 0

	for _, event := range r.events {
		if event.Type == datatypes.FeedbackRecordDeleted || event.Type == datatypes.WebhookDeleted {
			count++
		}
	}

	return count
}

func requireNoTenantDataDeleteEvents(t *testing.T, recorder *tenantDataEventRecorder) {
	t.Helper()

	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if count := recorder.tenantDataDeleteEventCount(); count > 0 {
			t.Fatalf("tenant data purge published %d delete events, want 0", count)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func createTenantDataFeedbackRecord(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	serverURL string,
	tenantID string,
	submissionID string,
	fieldID string,
) models.FeedbackRecord {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"source_type":   "formbricks",
		"submission_id": submissionID,
		"tenant_id":     tenantID,
		"user_id":       "tenant-data-delete-user-" + uuid.NewString(),
		"field_id":      fieldID,
		"field_type":    "text",
		"value_text":    "delete tenant data",
	})
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/v1/feedback-records", bytes.NewBuffer(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var rec models.FeedbackRecord

	err = decodeData(resp, &rec)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	return rec
}

func createTenantDataWebhook(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	serverURL string,
	tenantID string,
	path string,
) models.Webhook {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"url":       testWebhookURL + "/" + path,
		"tenant_id": tenantID,
	})
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/v1/webhooks", bytes.NewBuffer(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var webhook models.Webhook

	err = decodeData(resp, &webhook)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	return webhook
}

func createTenantDataTaxonomyGraph(
	ctx context.Context,
	t *testing.T,
	db *pgxpool.Pool,
	tenantID string,
	feedbackRecordID uuid.UUID,
	sourceID string,
	fieldID string,
) uuid.UUID {
	t.Helper()

	var runID uuid.UUID

	err := db.QueryRow(ctx, `
		INSERT INTO taxonomy_runs (
			tenant_id, source_type, source_id, field_id, field_label, status,
			record_count, embedding_count
		)
		VALUES ($1, 'formbricks', $2, $3, 'Feedback', 'succeeded'::taxonomy_run_status_enum, 1, 1)
		RETURNING id`,
		tenantID, sourceID, fieldID,
	).Scan(&runID)
	require.NoError(t, err)

	var clusterID uuid.UUID

	err = db.QueryRow(ctx, `
		INSERT INTO taxonomy_clusters (run_id, cluster_key, label, llm_label, keywords, size)
		VALUES ($1, 1, 'login', 'Login issues', '["login"]'::jsonb, 1)
		RETURNING id`,
		runID,
	).Scan(&clusterID)
	require.NoError(t, err)

	_, err = db.Exec(ctx, `
		INSERT INTO taxonomy_cluster_memberships (run_id, tenant_id, cluster_id, feedback_record_id, confidence)
		VALUES ($1, $2, $3, $4, 0.95)`,
		runID, tenantID, clusterID, feedbackRecordID,
	)
	require.NoError(t, err)

	var rootID uuid.UUID

	err = db.QueryRow(ctx, `
		INSERT INTO taxonomy_nodes (run_id, node_type, label, original_label, level, sort_order)
		VALUES ($1, 'root'::taxonomy_node_type_enum, 'Feedback', 'Feedback', 0, 0)
		RETURNING id`,
		runID,
	).Scan(&rootID)
	require.NoError(t, err)

	var branchID uuid.UUID

	err = db.QueryRow(ctx, `
		INSERT INTO taxonomy_nodes (run_id, parent_id, node_type, label, original_label, level, sort_order)
		VALUES ($1, $2, 'branch'::taxonomy_node_type_enum, 'Product Access', 'Product Access', 1, 0)
		RETURNING id`,
		runID, rootID,
	).Scan(&branchID)
	require.NoError(t, err)

	var leafID uuid.UUID

	err = db.QueryRow(ctx, `
		INSERT INTO taxonomy_nodes (run_id, parent_id, cluster_id, node_type, label, original_label, level, sort_order)
		VALUES ($1, $2, $3, 'leaf'::taxonomy_node_type_enum, 'Login Problems', 'Login Problems', 2, 0)
		RETURNING id`,
		runID, branchID, clusterID,
	).Scan(&leafID)
	require.NoError(t, err)

	_, err = db.Exec(ctx, `
		INSERT INTO taxonomy_active_runs (tenant_id, source_type, source_id, field_id, run_id, activated_by)
		VALUES ($1, 'formbricks', $2, $3, $4, 'tenant-data-test')`,
		tenantID, sourceID, fieldID, runID,
	)
	require.NoError(t, err)

	_, err = db.Exec(ctx, `
		INSERT INTO taxonomy_node_events (
			tenant_id, source_type, source_id, field_id, run_id, node_id,
			event_type, actor_id, old_value, new_value
		)
		VALUES (
			$1, 'formbricks', $2, $3, $4, $5,
			'rename'::taxonomy_node_event_type_enum, 'tenant-data-test',
			'{"label":"Login Problems"}'::jsonb, '{"label":"Authentication Problems"}'::jsonb
		)`,
		tenantID, sourceID, fieldID, runID, leafID,
	)
	require.NoError(t, err)

	return runID
}

func requireTenantDataTaxonomyRunDeleted(ctx context.Context, t *testing.T, db *pgxpool.Pool, runID uuid.UUID) {
	t.Helper()

	assert.Equal(t, int64(0), countTenantDataRows(ctx, t, db, `SELECT COUNT(*) FROM taxonomy_runs WHERE id = $1`, runID))
	assert.Equal(t, int64(0), countTenantDataRows(ctx, t, db, `SELECT COUNT(*) FROM taxonomy_clusters WHERE run_id = $1`, runID))
	assert.Equal(t, int64(0), countTenantDataRows(ctx, t, db, `SELECT COUNT(*) FROM taxonomy_cluster_memberships WHERE run_id = $1`, runID))
	assert.Equal(t, int64(0), countTenantDataRows(ctx, t, db, `SELECT COUNT(*) FROM taxonomy_nodes WHERE run_id = $1`, runID))
	assert.Equal(t, int64(0), countTenantDataRows(ctx, t, db, `SELECT COUNT(*) FROM taxonomy_active_runs WHERE run_id = $1`, runID))
	assert.Equal(t, int64(0), countTenantDataRows(ctx, t, db, `SELECT COUNT(*) FROM taxonomy_node_events WHERE run_id = $1`, runID))
}

func requireTenantDataTaxonomyRunPresent(ctx context.Context, t *testing.T, db *pgxpool.Pool, runID uuid.UUID) {
	t.Helper()

	assert.Equal(t, int64(1), countTenantDataRows(ctx, t, db, `SELECT COUNT(*) FROM taxonomy_runs WHERE id = $1`, runID))
	assert.Equal(t, int64(1), countTenantDataRows(ctx, t, db, `SELECT COUNT(*) FROM taxonomy_cluster_memberships WHERE run_id = $1`, runID))
	assert.Equal(t, int64(1), countTenantDataRows(ctx, t, db, `SELECT COUNT(*) FROM taxonomy_active_runs WHERE run_id = $1`, runID))
	assert.Equal(t, int64(1), countTenantDataRows(ctx, t, db, `SELECT COUNT(*) FROM taxonomy_node_events WHERE run_id = $1`, runID))
}

func countTenantDataRows(ctx context.Context, t *testing.T, db *pgxpool.Pool, query string, args ...any) int64 {
	t.Helper()

	var count int64

	err := db.QueryRow(ctx, query, args...).Scan(&count)
	require.NoError(t, err)

	return count
}

func requireTenantDataResourceStatus(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	resourceURL string,
	status int,
) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, status, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}

func deleteTenantData(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	serverURL string,
	tenantID string,
) models.TenantDataDeleteResponse {
	t.Helper()

	deleteURL := fmt.Sprintf("%s/v1/tenants/%s/data", serverURL, url.PathEscape(tenantID))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var deleteResp models.TenantDataDeleteResponse

	err = decodeData(resp, &deleteResp)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	return deleteResp
}

func cleanupTenantDataBestEffort(ctx context.Context, client *http.Client, serverURL, tenantID string) {
	cleanupCtx, cancel := context.WithTimeout(ctx, tenantDataCleanupTimeout)
	defer cancel()

	cleanupURL := fmt.Sprintf("%s/v1/tenants/%s/data", serverURL, url.PathEscape(tenantID))

	req, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, cleanupURL, http.NoBody)
	if err != nil {
		return
	}

	req.Header.Set("Authorization", "Bearer "+testAPIKey)

	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}

// TestFeedbackRecordsRepository_DeleteByUser tests the repository DeleteByUser return value (deleted IDs).
func TestFeedbackRecordsRepository_DeleteByUser(t *testing.T) {
	ctx := context.Background()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultTestDatabaseURL
	}

	t.Setenv("API_KEY", testAPIKey)
	t.Setenv("DATABASE_URL", databaseURL)

	cfg, err := config.Load()
	require.NoError(t, err)
	db, err := database.NewPostgresPool(ctx, cfg.Database.URL,
		database.WithPoolConfig(cfg.Database.PoolConfig()),
	)
	require.NoError(t, err)

	defer db.Close()

	repo := repository.NewFeedbackRecordsRepository(db)
	userID := "repo-user-delete-user-" + uuid.New().String()
	sourceType := "formbricks"

	// Create records with same user_id across tenants.
	const deleteByUserTenant = "user-delete-tenant"

	const otherDeleteByUserTenant = "user-delete-tenant-other"

	req1 := &models.CreateFeedbackRecordRequest{
		SourceType:   sourceType,
		SubmissionID: userID,
		TenantID:     deleteByUserTenant,
		FieldID:      "f1",
		FieldType:    models.FieldTypeNumber,
		UserID:       &userID,
	}
	valueNumber1 := 1.0
	req1.ValueNumber = &valueNumber1
	rec1, err := repo.Create(ctx, req1)
	require.NoError(t, err)
	require.NotEmpty(t, rec1.ID)

	req2 := &models.CreateFeedbackRecordRequest{
		SourceType:   sourceType,
		SubmissionID: userID,
		TenantID:     deleteByUserTenant,
		FieldID:      "f2",
		FieldType:    models.FieldTypeNumber,
		UserID:       &userID,
	}
	valueNumber2 := 2.0
	req2.ValueNumber = &valueNumber2
	rec2, err := repo.Create(ctx, req2)
	require.NoError(t, err)
	require.NotEmpty(t, rec2.ID)

	valueText := "delete me too"
	req3 := &models.CreateFeedbackRecordRequest{
		SourceType:   sourceType,
		SubmissionID: userID,
		TenantID:     otherDeleteByUserTenant,
		FieldID:      "f3",
		FieldType:    models.FieldTypeText,
		ValueText:    &valueText,
		UserID:       &userID,
	}
	rec3, err := repo.Create(ctx, req3)
	require.NoError(t, err)
	require.NotEmpty(t, rec3.ID)

	// DeleteByUser with tenant_id restricts deletion to that tenant and returns tenant-safe groups.
	tenantFilter := deleteByUserTenant
	deletedGroups, err := repo.DeleteByUser(ctx, &models.DeleteFeedbackRecordsByUserFilters{
		UserID:   userID,
		TenantID: &tenantFilter,
	})
	require.NoError(t, err)
	require.Len(t, deletedGroups, 1)
	require.Equal(t, deleteByUserTenant, deletedGroups[0].TenantID)
	assert.ElementsMatch(t, []uuid.UUID{rec1.ID, rec2.ID}, deletedGroups[0].IDs)

	_, err = repo.GetByID(ctx, rec1.ID)
	require.Error(t, err)
	_, err = repo.GetByID(ctx, rec2.ID)
	require.Error(t, err)
	remaining, err := repo.GetByID(ctx, rec3.ID)
	require.NoError(t, err)
	require.Equal(t, otherDeleteByUserTenant, remaining.TenantID)

	// Omitting tenant_id deletes the rest of the user records across tenants.
	deletedGroups, err = repo.DeleteByUser(ctx, &models.DeleteFeedbackRecordsByUserFilters{UserID: userID})
	require.NoError(t, err)
	require.Len(t, deletedGroups, 1)
	require.Equal(t, otherDeleteByUserTenant, deletedGroups[0].TenantID)
	assert.ElementsMatch(t, []uuid.UUID{rec3.ID}, deletedGroups[0].IDs)

	_, err = repo.GetByID(ctx, rec3.ID)
	require.Error(t, err)
}

// TestWebhooksCRUD tests webhook create, get, list, update, delete.
func TestWebhooksCRUD(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create webhook (no signing key = auto-generated)
	webhookTenantID := "org-123"
	createBody := map[string]any{
		"url":         testWebhookURL,
		"tenant_id":   webhookTenantID,
		"event_types": []string{"feedback_record.created", "feedback_record.updated"},
	}
	body, err := json.Marshal(createBody)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/webhooks", bytes.NewBuffer(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, createResp.StatusCode)

	var created models.Webhook

	err = decodeData(createResp, &created)
	require.NoError(t, err)
	require.NoError(t, createResp.Body.Close())
	requireUUIDv7(t, created.ID)
	assert.Equal(t, testWebhookURL, created.URL)
	assert.NotEmpty(t, created.SigningKey)
	assert.True(t, created.Enabled)
	require.NotNil(t, created.TenantID)
	assert.Equal(t, webhookTenantID, *created.TenantID)
	assert.Len(t, created.EventTypes, 2)

	// Get webhook
	getWebhookURL := fmt.Sprintf("%s/v1/webhooks/%s", server.URL, created.ID)
	getReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, getWebhookURL, http.NoBody)
	require.NoError(t, err)
	getReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	getResp, err := client.Do(getReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getResp.StatusCode)

	var got map[string]any

	err = json.NewDecoder(getResp.Body).Decode(&got)
	require.NoError(t, err)
	require.NoError(t, getResp.Body.Close())
	assert.Equal(t, created.ID.String(), got["id"])
	assert.Equal(t, created.URL, got["url"])
	_, hasSigningKey := got["signing_key"]
	assert.False(t, hasSigningKey, "GET response must not include signing_key")

	// List webhooks
	listReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/v1/webhooks", http.NoBody)
	require.NoError(t, err)
	listReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	listResp, err := client.Do(listReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, listResp.StatusCode)

	var listRaw map[string]any

	err = json.NewDecoder(listResp.Body).Decode(&listRaw)
	require.NoError(t, err)
	require.NoError(t, listResp.Body.Close())

	data, ok := listRaw["data"].([]any)
	require.True(t, ok)

	totalVal, hasTotal := listRaw["total"]
	if hasTotal {
		assert.GreaterOrEqual(t, int(totalVal.(float64)), 1)
	}

	assert.GreaterOrEqual(t, len(data), 1)
	// signing_key must not be in LIST response (redacted for security)
	for i, item := range data {
		itemMap, ok := item.(map[string]any)
		require.True(t, ok)

		_, hasSigningKey := itemMap["signing_key"]
		assert.False(t, hasSigningKey, "LIST response item %d must not include signing_key", i)
	}

	// Test invalid cursor returns 400
	invalidCursorReq, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet,
		server.URL+"/v1/webhooks?cursor=invalid", http.NoBody,
	)
	require.NoError(t, err)
	invalidCursorReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	invalidCursorResp, err := client.Do(invalidCursorReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, invalidCursorResp.StatusCode)
	require.NoError(t, invalidCursorResp.Body.Close())

	// Update webhook (including tenant_id)
	updateBody := map[string]any{
		"url":       testWebhookURLV2,
		"enabled":   false,
		"tenant_id": "org-456",
	}
	updateJSON, err := json.Marshal(updateBody)
	require.NoError(t, err)

	updateURL := fmt.Sprintf("%s/v1/webhooks/%s", server.URL, created.ID)
	updateReq, err := http.NewRequestWithContext(context.Background(), http.MethodPatch, updateURL, bytes.NewBuffer(updateJSON))
	require.NoError(t, err)
	updateReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp, err := client.Do(updateReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, updateResp.StatusCode)

	var updated models.Webhook

	err = decodeData(updateResp, &updated)
	require.NoError(t, err)
	require.NoError(t, updateResp.Body.Close())
	assert.Equal(t, testWebhookURLV2, updated.URL)
	assert.False(t, updated.Enabled)
	require.NotNil(t, updated.TenantID)
	assert.Equal(t, "org-456", *updated.TenantID)

	// PATCH tenant_id to empty string is rejected; webhooks cannot be global.
	clearTenantBody := map[string]any{"tenant_id": ""}
	clearTenantJSON, err := json.Marshal(clearTenantBody)
	require.NoError(t, err)

	clearTenantURL := fmt.Sprintf("%s/v1/webhooks/%s", server.URL, created.ID)
	clearTenantReq, err := http.NewRequestWithContext(context.Background(), http.MethodPatch, clearTenantURL, bytes.NewBuffer(clearTenantJSON))
	require.NoError(t, err)
	clearTenantReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	clearTenantReq.Header.Set("Content-Type", "application/json")
	clearTenantResp, err := client.Do(clearTenantReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, clearTenantResp.StatusCode)
	require.NoError(t, clearTenantResp.Body.Close())

	// Delete webhook
	deleteWebhookURL := fmt.Sprintf("%s/v1/webhooks/%s", server.URL, created.ID)
	deleteReq, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, deleteWebhookURL, http.NoBody)
	require.NoError(t, err)
	deleteReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	deleteResp, err := client.Do(deleteReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, deleteResp.StatusCode)
	require.NoError(t, deleteResp.Body.Close())

	// Verify deleted
	getAfterURL := fmt.Sprintf("%s/v1/webhooks/%s", server.URL, created.ID)
	getAfterReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, getAfterURL, http.NoBody)
	require.NoError(t, err)
	getAfterReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	getAfterResp, err := client.Do(getAfterReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, getAfterResp.StatusCode)
	require.NoError(t, getAfterResp.Body.Close())
}

func TestWebhooksCreateRequiresTenantID(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	body, err := json.Marshal(map[string]any{
		"url":         testWebhookURL,
		"event_types": []string{"feedback_record.created"},
	})
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/webhooks", bytes.NewBuffer(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var problem response.ProblemDetails

	err = json.NewDecoder(resp.Body).Decode(&problem)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "Validation Error", problem.Title)
	require.Len(t, problem.InvalidParams, 1)
	assert.Equal(t, "tenant_id", problem.InvalidParams[0].Name)
	assert.Contains(t, problem.InvalidParams[0].Reason, "required")
}

// TestWebhooksInvalidSigningKey asserts that create and update reject invalid signing_key with 400.
func TestWebhooksInvalidSigningKey(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create with invalid signing_key
	createBody := map[string]any{
		"url":         testWebhookURL,
		"signing_key": "not-valid",
		"tenant_id":   "org-123",
		"event_types": []string{"feedback_record.created"},
	}
	body, err := json.Marshal(createBody)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/webhooks", bytes.NewBuffer(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, createResp.StatusCode)
	assert.Contains(t, createResp.Header.Get("Content-Type"), "application/problem+json")

	var problem response.ProblemDetails

	err = json.NewDecoder(createResp.Body).Decode(&problem)
	require.NoError(t, err)
	require.NoError(t, createResp.Body.Close())
	assert.Equal(t, "Validation Error", problem.Title)
	require.Len(t, problem.InvalidParams, 1)
	assert.Equal(t, "signing_key", problem.InvalidParams[0].Name)
	assert.Contains(t, problem.InvalidParams[0].Reason, "Standard Webhooks")

	// Create a valid webhook first for update test
	validBody := map[string]any{
		"url":         testWebhookURL,
		"tenant_id":   "org-123",
		"event_types": []string{"feedback_record.created"},
	}
	validJSON, err := json.Marshal(validBody)
	require.NoError(t, err)
	createReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/webhooks", bytes.NewBuffer(validJSON))
	require.NoError(t, err)
	createReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	createReq.Header.Set("Content-Type", "application/json")
	validResp, err := client.Do(createReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, validResp.StatusCode)

	var created models.Webhook

	err = decodeData(validResp, &created)
	require.NoError(t, err)
	require.NoError(t, validResp.Body.Close())

	// Update with invalid signing_key
	updateBody := map[string]any{"signing_key": "bad_key"}
	updateJSON, err := json.Marshal(updateBody)
	require.NoError(t, err)

	updateURL := fmt.Sprintf("%s/v1/webhooks/%s", server.URL, created.ID)
	updateReq, err := http.NewRequestWithContext(context.Background(), http.MethodPatch, updateURL, bytes.NewBuffer(updateJSON))
	require.NoError(t, err)
	updateReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp, err := client.Do(updateReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, updateResp.StatusCode)

	var updateProblem response.ProblemDetails

	err = json.NewDecoder(updateResp.Body).Decode(&updateProblem)
	require.NoError(t, err)
	require.NoError(t, updateResp.Body.Close())
	require.Len(t, updateProblem.InvalidParams, 1)
	assert.Equal(t, "signing_key", updateProblem.InvalidParams[0].Name)
}
