package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/api/handlers"
	"github.com/formbricks/hub/internal/api/middleware"
	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/database"
)

// defaultTestDatabaseURL is the default Postgres URL used by compose (postgres/postgres/test_db).
// Setting it here before config.Load() ensures tests do not use a different DATABASE_URL from .env,
// which would cause "password authentication failed" when .env points at another database.
const defaultTestDatabaseURL = "postgres://postgres:postgres@localhost:5432/test_db?sslmode=disable"

// setupTestServer creates a test HTTP server with all routes configured.
func setupTestServer(t *testing.T) (server *httptest.Server, cleanup func()) {
	ctx := context.Background()

	// Set test env before loading config so config.Load() uses test values and is not affected by .env.
	t.Setenv("API_KEY", testAPIKey)
	t.Setenv("DATABASE_URL", defaultTestDatabaseURL)

	// Load configuration
	cfg, err := config.Load()
	require.NoError(t, err, "Failed to load configuration")

	// Initialize database connection
	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	require.NoError(t, err, "Failed to connect to database")

	// Initialize message publisher manager for tests (no providers required)
	messageManager := service.NewMessagePublisherManager(cfg.MessagePublisherBufferSize, cfg.MessagePublisherPerEventTimeout)

	// Webhooks
	webhooksRepo := repository.NewWebhooksRepository(db)
	webhooksService := service.NewWebhooksService(webhooksRepo, messageManager, cfg.WebhookMaxCount)
	webhooksHandler := handlers.NewWebhooksHandler(webhooksService)

	// Initialize repository, service, and handler layers
	feedbackRecordsRepo := repository.NewFeedbackRecordsRepository(db)
	feedbackRecordsService := service.NewFeedbackRecordsService(feedbackRecordsRepo, messageManager)
	feedbackRecordsHandler := handlers.NewFeedbackRecordsHandler(feedbackRecordsService)
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
	protectedMux.HandleFunc("DELETE /v1/feedback-records", feedbackRecordsHandler.BulkDelete)
	protectedMux.HandleFunc("POST /v1/webhooks", webhooksHandler.Create)
	protectedMux.HandleFunc("GET /v1/webhooks", webhooksHandler.List)
	protectedMux.HandleFunc("GET /v1/webhooks/{id}", webhooksHandler.Get)
	protectedMux.HandleFunc("PATCH /v1/webhooks/{id}", webhooksHandler.Update)
	protectedMux.HandleFunc("DELETE /v1/webhooks/{id}", webhooksHandler.Delete)

	var protectedHandler http.Handler = protectedMux

	protectedHandler = middleware.Auth(cfg.APIKey)(protectedHandler)

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
			"source_type": "formbricks",
			"field_id":    "feedback",
			"field_type":  "text",
			"value_text":  "Great product!",
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
			"source_type": "formbricks",
			"field_id":    "feedback",
			"field_type":  "text",
			"value_text":  "Great product!",
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
			"source_type": "formbricks",
			"field_id":    "feedback",
			"field_type":  "text",
			"value_text":  "Great product!",
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
			"source_type": "formbricks",
			"field_id":    "feedback",
			"field_type":  "text",
			"value_text":  "Great product!",
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

	// Test with valid authentication
	t.Run("Success with valid API key", func(t *testing.T) {
		reqBody := map[string]any{
			"source_type": "formbricks",
			"field_id":    "feedback",
			"field_type":  "text",
			"value_text":  "Great product!",
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

		assert.NotEmpty(t, result.ID)
		assert.Equal(t, "formbricks", result.SourceType)
		assert.Equal(t, "feedback", result.FieldID)
		assert.Equal(t, models.FieldTypeText, result.FieldType)
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
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/v1/feedback-records", http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer wrong-key-12345")

		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	})

	// Create a test feedback record first
	reqBody := map[string]any{
		"source_type":  "formbricks",
		"field_id":     "nps_score",
		"field_type":   "number",
		"value_number": 9,
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

	// Test listing feedback records
	t.Run("List all feedback records", func(t *testing.T) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/v1/feedback-records", http.NoBody)
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
		listURL := server.URL + "/v1/feedback-records?source_type=formbricks&limit=10"
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

	// Create a test feedback record
	reqBody := map[string]any{
		"source_type":  "formbricks",
		"field_id":     "rating",
		"field_type":   "number",
		"value_number": 5,
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

	// Create a test feedback record
	reqBody := map[string]any{
		"source_type": "formbricks",
		"field_id":    "comment",
		"field_type":  "text",
		"value_text":  "Initial comment",
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
		updateBody := map[string]any{
			"value_text": "Updated comment",
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

	// Create a test feedback record
	reqBody := map[string]any{
		"source_type": "formbricks",
		"field_id":    "temp",
		"field_type":  "text",
		"value_text":  "To be deleted",
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

func TestBulkDeleteFeedbackRecords(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}
	userID := "bulk-delete-test-user-123"

	// Create several feedback records with the same user_identifier
	createPayload := func(fieldID string, valueNum float64) map[string]any {
		return map[string]any{
			"source_type":     "formbricks",
			"user_identifier": userID,
			"field_id":        fieldID,
			"field_type":      "number",
			"value_number":    valueNum,
		}
	}
	createdIDs := make([]string, 0, 3)

	for i, p := range []map[string]any{
		createPayload("nps_1", 8),
		createPayload("nps_2", 9),
		createPayload("nps_3", 10),
	} {
		body, err := json.Marshal(p)
		require.NoError(t, err)
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode, "create record %d", i+1)

		var rec models.FeedbackRecord

		err = decodeData(resp, &rec)
		require.NoError(t, err)

		createdIDs = append(createdIDs, rec.ID.String())

		require.NoError(t, resp.Body.Close())
	}

	// Bulk delete by user_identifier
	bulkDelURL := server.URL + "/v1/feedback-records?user_identifier=" + userID
	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, bulkDelURL, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var bulkResp models.BulkDeleteResponse

	err = decodeData(resp, &bulkResp)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, int64(3), bulkResp.DeletedCount)
	assert.Equal(t, "Successfully deleted 3 feedback records", bulkResp.Message)

	// Verify records are gone
	for _, id := range createdIDs {
		getReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/v1/feedback-records/"+id, http.NoBody)
		require.NoError(t, err)
		getReq.Header.Set("Authorization", "Bearer "+testAPIKey)
		getResp, err := client.Do(getReq)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, getResp.StatusCode)
		require.NoError(t, getResp.Body.Close())
	}

	// Bulk delete again (no matching records) returns 0
	bulkDelURL2 := server.URL + "/v1/feedback-records?user_identifier=" + userID
	req2, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, bulkDelURL2, http.NoBody)
	require.NoError(t, err)
	req2.Header.Set("Authorization", "Bearer "+testAPIKey)
	resp2, err := client.Do(req2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var bulkResp2 models.BulkDeleteResponse

	err = decodeData(resp2, &bulkResp2)
	require.NoError(t, err)
	require.NoError(t, resp2.Body.Close())
	assert.Equal(t, int64(0), bulkResp2.DeletedCount)

	// Bulk delete with tenant_id: only records for that tenant are deleted
	t.Run("Bulk delete with tenant_id filter", func(t *testing.T) {
		tenantA, tenantB := "tenant-bulk-a", "tenant-bulk-b"
		userIDTenant := "bulk-delete-tenant-user"

		// Create one record with tenant_a, two with tenant_b
		for _, item := range []struct {
			tenantID string
			fieldID  string
		}{
			{tenantA, "fa"},
			{tenantB, "fb1"},
			{tenantB, "fb2"},
		} {
			body, err := json.Marshal(map[string]any{
				"source_type":     "formbricks",
				"user_identifier": userIDTenant,
				"tenant_id":       item.tenantID,
				"field_id":        item.fieldID,
				"field_type":      "text",
				"value_text":      "x",
			})
			require.NoError(t, err)
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+testAPIKey)
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			require.NoError(t, err)
			require.Equal(t, http.StatusCreated, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
		}

		// Delete only tenant_a
		delURL := server.URL + "/v1/feedback-records?user_identifier=" + userIDTenant + "&tenant_id=" + tenantA
		delReq, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, delURL, http.NoBody)
		require.NoError(t, err)
		delReq.Header.Set("Authorization", "Bearer "+testAPIKey)
		delResp, err := client.Do(delReq)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, delResp.StatusCode)

		var delResult models.BulkDeleteResponse

		err = decodeData(delResp, &delResult)
		require.NoError(t, err)
		require.NoError(t, delResp.Body.Close())
		assert.Equal(t, int64(1), delResult.DeletedCount)

		// Delete remaining (tenant_b) — should delete 2
		delURL2 := server.URL + "/v1/feedback-records?user_identifier=" + userIDTenant + "&tenant_id=" + tenantB
		delReq2, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, delURL2, http.NoBody)
		require.NoError(t, err)
		delReq2.Header.Set("Authorization", "Bearer "+testAPIKey)
		delResp2, err := client.Do(delReq2)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, delResp2.StatusCode)
		err = decodeData(delResp2, &delResult)
		require.NoError(t, err)
		require.NoError(t, delResp2.Body.Close())
		assert.Equal(t, int64(2), delResult.DeletedCount)
	})
}

// TestFeedbackRecordsRepository_BulkDelete tests the repository BulkDelete return value (deleted IDs).
func TestFeedbackRecordsRepository_BulkDelete(t *testing.T) {
	ctx := context.Background()

	t.Setenv("API_KEY", testAPIKey)
	t.Setenv("DATABASE_URL", defaultTestDatabaseURL)

	cfg, err := config.Load()
	require.NoError(t, err)
	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	require.NoError(t, err)

	defer db.Close()

	repo := repository.NewFeedbackRecordsRepository(db)
	userID := "repo-bulk-delete-user"
	sourceType := "formbricks"

	// Create two records with same user_identifier
	req1 := &models.CreateFeedbackRecordRequest{
		SourceType:     sourceType,
		FieldID:        "f1",
		FieldType:      models.FieldTypeNumber,
		ValueNumber:    ptrFloat64(1),
		UserIdentifier: strPtr(userID),
	}
	rec1, err := repo.Create(ctx, req1)
	require.NoError(t, err)
	require.NotEmpty(t, rec1.ID)

	req2 := &models.CreateFeedbackRecordRequest{
		SourceType:     sourceType,
		FieldID:        "f2",
		FieldType:      models.FieldTypeNumber,
		ValueNumber:    ptrFloat64(2),
		UserIdentifier: strPtr(userID),
	}
	rec2, err := repo.Create(ctx, req2)
	require.NoError(t, err)
	require.NotEmpty(t, rec2.ID)

	// BulkDelete returns the deleted IDs
	deletedIDs, err := repo.BulkDelete(ctx, userID, nil)
	require.NoError(t, err)
	require.Len(t, deletedIDs, 2)
	ids := map[string]bool{deletedIDs[0].String(): true, deletedIDs[1].String(): true}
	assert.True(t, ids[rec1.ID.String()])
	assert.True(t, ids[rec2.ID.String()])
}

func strPtr(s string) *string       { return &s }
func ptrFloat64(f float64) *float64 { return &f }

// TestWebhooksCRUD tests webhook create, get, list, update, delete.
func TestWebhooksCRUD(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create webhook (no signing key = auto-generated)
	createBody := map[string]any{
		"url":         "https://example.com/webhook",
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
	assert.NotEmpty(t, created.ID.String())
	assert.Equal(t, "https://example.com/webhook", created.URL)
	assert.NotEmpty(t, created.SigningKey)
	assert.True(t, created.Enabled)
	assert.Len(t, created.EventTypes, 2)

	// Get webhook
	getWebhookURL := fmt.Sprintf("%s/v1/webhooks/%s", server.URL, created.ID)
	getReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, getWebhookURL, http.NoBody)
	require.NoError(t, err)
	getReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	getResp, err := client.Do(getReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getResp.StatusCode)

	var got models.Webhook

	err = decodeData(getResp, &got)
	require.NoError(t, err)
	require.NoError(t, getResp.Body.Close())
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, created.URL, got.URL)

	// List webhooks
	listReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/v1/webhooks", http.NoBody)
	require.NoError(t, err)
	listReq.Header.Set("Authorization", "Bearer "+testAPIKey)
	listResp, err := client.Do(listReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, listResp.StatusCode)

	var listResult models.ListWebhooksResponse

	err = decodeData(listResp, &listResult)
	require.NoError(t, err)
	require.NoError(t, listResp.Body.Close())
	assert.GreaterOrEqual(t, listResult.Total, int64(1))
	assert.GreaterOrEqual(t, len(listResult.Data), 1)

	// Update webhook (including tenant_id)
	updateBody := map[string]any{
		"url":       "https://example.com/webhook-v2",
		"enabled":   false,
		"tenant_id": "org-123",
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
	assert.Equal(t, "https://example.com/webhook-v2", updated.URL)
	assert.False(t, updated.Enabled)
	require.NotNil(t, updated.TenantID)
	assert.Equal(t, "org-123", *updated.TenantID)

	// PATCH tenant_id to empty string to clear it
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
	assert.Equal(t, http.StatusOK, clearTenantResp.StatusCode)

	var afterClear models.Webhook

	err = decodeData(clearTenantResp, &afterClear)
	require.NoError(t, err)
	require.NoError(t, clearTenantResp.Body.Close())
	assert.Nil(t, afterClear.TenantID)

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

// TestWebhooksInvalidSigningKey asserts that create and update reject invalid signing_key with 400.
func TestWebhooksInvalidSigningKey(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create with invalid signing_key
	createBody := map[string]any{
		"url":         "https://example.com/webhook",
		"signing_key": "not-valid",
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
	require.Len(t, problem.Errors, 1)
	assert.Equal(t, "signing_key", problem.Errors[0].Location)
	assert.Contains(t, problem.Errors[0].Message, "Standard Webhooks")

	// Create a valid webhook first for update test
	validBody := map[string]any{
		"url":         "https://example.com/webhook",
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
	require.Len(t, updateProblem.Errors, 1)
	assert.Equal(t, "signing_key", updateProblem.Errors[0].Location)
}
