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

	"github.com/formbricks/hub/internal/api/handlers"
	"github.com/formbricks/hub/internal/api/middleware"
	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestServer creates a test HTTP server with all routes configured
func setupTestServer(t *testing.T) (*httptest.Server, func()) {
	ctx := context.Background()

	// Set test API key in environment for authentication (must be set before loading config)
	t.Setenv("API_KEY", testAPIKey)

	// Load configuration
	cfg, err := config.Load()
	require.NoError(t, err, "Failed to load configuration")

	// Initialize database connection
	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	require.NoError(t, err, "Failed to connect to database")

	// Initialize repository, service, and handler layers
	feedbackRecordsRepo := repository.NewFeedbackRecordsRepository(db)
	feedbackRecordsService := service.NewFeedbackRecordsService(feedbackRecordsRepo)
	feedbackRecordsHandler := handlers.NewFeedbackRecordsHandler(feedbackRecordsService)

	knowledgeRecordsRepo := repository.NewKnowledgeRecordsRepository(db)
	knowledgeRecordsService := service.NewKnowledgeRecordsService(knowledgeRecordsRepo)
	knowledgeRecordsHandler := handlers.NewKnowledgeRecordsHandler(knowledgeRecordsService)

	topicsRepo := repository.NewTopicsRepository(db)
	topicsService := service.NewTopicsService(topicsRepo)
	topicsHandler := handlers.NewTopicsHandler(topicsService)

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

	protectedMux.HandleFunc("POST /v1/knowledge-records", knowledgeRecordsHandler.Create)
	protectedMux.HandleFunc("GET /v1/knowledge-records", knowledgeRecordsHandler.List)
	protectedMux.HandleFunc("GET /v1/knowledge-records/{id}", knowledgeRecordsHandler.Get)
	protectedMux.HandleFunc("PATCH /v1/knowledge-records/{id}", knowledgeRecordsHandler.Update)
	protectedMux.HandleFunc("DELETE /v1/knowledge-records/{id}", knowledgeRecordsHandler.Delete)
	protectedMux.HandleFunc("DELETE /v1/knowledge-records", knowledgeRecordsHandler.BulkDelete)

	protectedMux.HandleFunc("POST /v1/topics", topicsHandler.Create)
	protectedMux.HandleFunc("GET /v1/topics", topicsHandler.List)
	protectedMux.HandleFunc("GET /v1/topics/{id}", topicsHandler.Get)
	protectedMux.HandleFunc("PATCH /v1/topics/{id}", topicsHandler.Update)
	protectedMux.HandleFunc("DELETE /v1/topics/{id}", topicsHandler.Delete)

	var protectedHandler http.Handler = protectedMux
	protectedHandler = middleware.Auth(cfg.APIKey)(protectedHandler)

	// Combine both handlers
	mainMux := http.NewServeMux()
	mainMux.Handle("/v1/", protectedHandler)
	mainMux.Handle("/", publicHandler)

	// Create test server
	server := httptest.NewServer(mainMux)

	// Cleanup function
	cleanup := func() {
		server.Close()
		db.Close()
	}

	return server, cleanup
}

// decodeData decodes JSON responses directly from the response body.
// The API handlers use RespondJSON which encodes responses directly without wrapping.
func decodeData(resp *http.Response, v interface{}) error {
	return json.NewDecoder(resp.Body).Decode(v)
}

func TestHealthEndpoint(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	resp, err := http.Get(server.URL + "/health")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Health endpoint returns plain text "OK"
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "OK", string(body))
}

func TestCreateFeedbackRecord(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Test without authentication
	t.Run("Unauthorized without API key", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"source_type": "formbricks",
			"field_id":    "feedback",
			"field_type":  "text",
			"value_text":  "Great product!",
		}
		body, _ := json.Marshal(reqBody)

		resp, err := http.Post(server.URL+"/v1/feedback-records", "application/json", bytes.NewBuffer(body))
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	// Test with invalid API key
	t.Run("Unauthorized with invalid API key", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"source_type": "formbricks",
			"field_id":    "feedback",
			"field_type":  "text",
			"value_text":  "Great product!",
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer wrong-key-12345")
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	// Test with empty API key in header
	t.Run("Unauthorized with empty API key", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"source_type": "formbricks",
			"field_id":    "feedback",
			"field_type":  "text",
			"value_text":  "Great product!",
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer ")
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	// Test with malformed Authorization header
	t.Run("Unauthorized with malformed Authorization header", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"source_type": "formbricks",
			"field_id":    "feedback",
			"field_type":  "text",
			"value_text":  "Great product!",
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "InvalidFormat")
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	// Test with valid authentication
	t.Run("Success with valid API key", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"source_type": "formbricks",
			"field_id":    "feedback",
			"field_type":  "text",
			"value_text":  "Great product!",
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)

		var result models.FeedbackRecord
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.NotEmpty(t, result.ID)
		assert.Equal(t, "formbricks", result.SourceType)
		assert.Equal(t, "feedback", result.FieldID)
		assert.Equal(t, "text", result.FieldType)
		assert.NotNil(t, result.ValueText)
		assert.Equal(t, "Great product!", *result.ValueText)
	})

	// Test with invalid request body
	t.Run("Bad request with missing required fields", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"field_id": "feedback",
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestListFeedbackRecords(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Test with invalid API key
	t.Run("Unauthorized with invalid API key", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records", nil)
		req.Header.Set("Authorization", "Bearer wrong-key-12345")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	// Create a test feedback record first
	reqBody := map[string]interface{}{
		"source_type":  "formbricks",
		"field_id":     "nps_score",
		"field_type":   "number",
		"value_number": 9,
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")
	_, _ = client.Do(req)

	// Test listing feedback records
	t.Run("List all feedback records", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.ListFeedbackRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.NotEmpty(t, result.Data)
	})

	// Test with filters
	t.Run("List with source_type filter", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records?source_type=formbricks&limit=10", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.ListFeedbackRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

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
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records/00000000-0000-0000-0000-000000000000", nil)
		req.Header.Set("Authorization", "Bearer wrong-key-12345")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	// Create a test feedback record
	reqBody := map[string]interface{}{
		"source_type":  "formbricks",
		"field_id":     "rating",
		"field_type":   "number",
		"value_number": 5,
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = createResp.Body.Close() }()

	var created models.FeedbackRecord
	err = decodeData(createResp, &created)
	require.NoError(t, err)

	// Test getting the feedback record by ID
	t.Run("Get existing feedback record", func(t *testing.T) {
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/v1/feedback-records/%s", server.URL, created.ID), nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.FeedbackRecord
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.Equal(t, created.ID, result.ID)
		assert.Equal(t, "formbricks", result.SourceType)
	})

	t.Run("Get non-existent feedback record", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records/00000000-0000-0000-0000-000000000000", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestUpdateFeedbackRecord(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Test with invalid API key
	t.Run("Unauthorized with invalid API key", func(t *testing.T) {
		updateBody := map[string]interface{}{
			"value_text": "Updated comment",
		}
		body, _ := json.Marshal(updateBody)

		req, _ := http.NewRequest("PATCH", server.URL+"/v1/feedback-records/00000000-0000-0000-0000-000000000000", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer wrong-key-12345")
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	// Create a test feedback record
	reqBody := map[string]interface{}{
		"source_type": "formbricks",
		"field_id":    "comment",
		"field_type":  "text",
		"value_text":  "Initial comment",
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = createResp.Body.Close() }()

	var created models.FeedbackRecord
	err = decodeData(createResp, &created)
	require.NoError(t, err)

	// Test updating the feedback record
	t.Run("Update feedback record", func(t *testing.T) {
		updateBody := map[string]interface{}{
			"value_text": "Updated comment",
		}
		body, _ := json.Marshal(updateBody)

		req, _ := http.NewRequest("PATCH", fmt.Sprintf("%s/v1/feedback-records/%s", server.URL, created.ID), bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.FeedbackRecord
		err = decodeData(resp, &result)
		require.NoError(t, err)

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
		req, _ := http.NewRequest("DELETE", server.URL+"/v1/feedback-records/00000000-0000-0000-0000-000000000000", nil)
		req.Header.Set("Authorization", "Bearer wrong-key-12345")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	// Create a test feedback record
	reqBody := map[string]interface{}{
		"source_type": "formbricks",
		"field_id":    "temp",
		"field_type":  "text",
		"value_text":  "To be deleted",
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = createResp.Body.Close() }()

	var created models.FeedbackRecord
	err = decodeData(createResp, &created)
	require.NoError(t, err)

	// Test deleting the feedback record
	t.Run("Delete feedback record", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/v1/feedback-records/%s", server.URL, created.ID), nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})

	// Verify it's deleted
	t.Run("Verify deletion", func(t *testing.T) {
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/v1/feedback-records/%s", server.URL, created.ID), nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

// =============================================================================
// Knowledge Records Tests
// =============================================================================

func TestCreateKnowledgeRecord(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	t.Run("Success with valid API key", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"content":   "This is a test knowledge record content.",
			"tenant_id": "test-tenant",
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", server.URL+"/v1/knowledge-records", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)

		var result models.KnowledgeRecord
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.NotEmpty(t, result.ID)
		assert.Equal(t, "This is a test knowledge record content.", result.Content)
		assert.NotNil(t, result.TenantID)
		assert.Equal(t, "test-tenant", *result.TenantID)
	})

	t.Run("Bad request with missing content", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"tenant_id": "test-tenant",
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", server.URL+"/v1/knowledge-records", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("Bad request with content too long", func(t *testing.T) {
		// Create content longer than 10000 characters
		longContent := make([]byte, 10001)
		for i := range longContent {
			longContent[i] = 'a'
		}

		reqBody := map[string]interface{}{
			"content": string(longContent),
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", server.URL+"/v1/knowledge-records", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestListKnowledgeRecords(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create a test knowledge record first
	reqBody := map[string]interface{}{
		"content":   "Test content for listing",
		"tenant_id": "list-test-tenant",
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", server.URL+"/v1/knowledge-records", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")
	_, _ = client.Do(req)

	t.Run("List all knowledge records", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/knowledge-records", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.ListKnowledgeRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.NotEmpty(t, result.Data)
	})

	t.Run("List with tenant_id filter", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/knowledge-records?tenant_id=list-test-tenant&limit=10", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.ListKnowledgeRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		for _, record := range result.Data {
			assert.NotNil(t, record.TenantID)
			assert.Equal(t, "list-test-tenant", *record.TenantID)
		}
	})
}

func TestGetKnowledgeRecord(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create a test knowledge record
	reqBody := map[string]interface{}{
		"content": "Test content for get",
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", server.URL+"/v1/knowledge-records", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = createResp.Body.Close() }()

	var created models.KnowledgeRecord
	err = decodeData(createResp, &created)
	require.NoError(t, err)

	t.Run("Get existing knowledge record", func(t *testing.T) {
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/v1/knowledge-records/%s", server.URL, created.ID), nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.KnowledgeRecord
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.Equal(t, created.ID, result.ID)
		assert.Equal(t, "Test content for get", result.Content)
	})

	t.Run("Get non-existent knowledge record", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/knowledge-records/00000000-0000-0000-0000-000000000000", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestUpdateKnowledgeRecord(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create a test knowledge record
	reqBody := map[string]interface{}{
		"content": "Initial content",
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", server.URL+"/v1/knowledge-records", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = createResp.Body.Close() }()

	var created models.KnowledgeRecord
	err = decodeData(createResp, &created)
	require.NoError(t, err)

	t.Run("Update knowledge record", func(t *testing.T) {
		updateBody := map[string]interface{}{
			"content": "Updated content",
		}
		body, _ := json.Marshal(updateBody)

		req, _ := http.NewRequest("PATCH", fmt.Sprintf("%s/v1/knowledge-records/%s", server.URL, created.ID), bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.KnowledgeRecord
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.Equal(t, created.ID, result.ID)
		assert.Equal(t, "Updated content", result.Content)
	})
}

func TestDeleteKnowledgeRecord(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create a test knowledge record
	reqBody := map[string]interface{}{
		"content": "To be deleted",
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", server.URL+"/v1/knowledge-records", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = createResp.Body.Close() }()

	var created models.KnowledgeRecord
	err = decodeData(createResp, &created)
	require.NoError(t, err)

	t.Run("Delete knowledge record", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/v1/knowledge-records/%s", server.URL, created.ID), nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})

	t.Run("Verify deletion", func(t *testing.T) {
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/v1/knowledge-records/%s", server.URL, created.ID), nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestBulkDeleteKnowledgeRecords(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create test knowledge records with specific tenant_id
	for i := 0; i < 3; i++ {
		reqBody := map[string]interface{}{
			"content":   fmt.Sprintf("Bulk delete test content %d", i),
			"tenant_id": "bulk-delete-tenant",
		}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", server.URL+"/v1/knowledge-records", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")
		resp, _ := client.Do(req)
		_ = resp.Body.Close()
	}

	t.Run("Bulk delete knowledge records by tenant_id", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", server.URL+"/v1/knowledge-records?tenant_id=bulk-delete-tenant", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.BulkDeleteKnowledgeRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.Equal(t, int64(3), result.DeletedCount)
	})

	t.Run("Bulk delete with no matches returns 0", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", server.URL+"/v1/knowledge-records?tenant_id=non-existent-tenant", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.BulkDeleteKnowledgeRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.Equal(t, int64(0), result.DeletedCount)
	})
}

// =============================================================================
// Topics Tests
// =============================================================================

func TestCreateTopic(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	t.Run("Success with valid API key", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"title":     "Test Topic",
			"level":     1,
			"tenant_id": "test-tenant",
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", server.URL+"/v1/topics", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)

		var result models.Topic
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.NotEmpty(t, result.ID)
		assert.Equal(t, "Test Topic", result.Title)
		assert.Equal(t, 1, result.Level)
		assert.NotNil(t, result.TenantID)
		assert.Equal(t, "test-tenant", *result.TenantID)
	})

	t.Run("Bad request with missing title", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"level":     1,
			"tenant_id": "test-tenant",
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", server.URL+"/v1/topics", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestCreateTopicWithLevel(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	t.Run("Create Level 1 topic", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"title":     "Level 1 Test Topic",
			"level":     1,
			"tenant_id": "level-test-tenant",
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", server.URL+"/v1/topics", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)

		var result models.Topic
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.Equal(t, "Level 1 Test Topic", result.Title)
		assert.Equal(t, 1, result.Level)
	})

	t.Run("Create Level 2 topic", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"title":     "Level 2 Test Topic",
			"level":     2,
			"tenant_id": "level-test-tenant",
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", server.URL+"/v1/topics", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)

		var result models.Topic
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.Equal(t, "Level 2 Test Topic", result.Title)
		assert.Equal(t, 2, result.Level)
	})

	t.Run("Create topic with invalid level returns 400", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"title":     "Invalid Level Topic",
			"level":     3,
			"tenant_id": "level-test-tenant",
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", server.URL+"/v1/topics", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestTopicTitleUniqueness(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create first Level 1 topic
	firstReq := map[string]interface{}{
		"title":     "Unique Title L1",
		"level":     1,
		"tenant_id": "uniqueness-test-tenant",
	}
	body, _ := json.Marshal(firstReq)
	req, _ := http.NewRequest("POST", server.URL+"/v1/topics", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	firstResp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = firstResp.Body.Close() }()

	var firstTopic models.Topic
	err = decodeData(firstResp, &firstTopic)
	require.NoError(t, err)

	t.Run("Create duplicate title at same level returns 409", func(t *testing.T) {
		duplicateReq := map[string]interface{}{
			"title":     "Unique Title L1", // Same title
			"level":     1,
			"tenant_id": "uniqueness-test-tenant",
		}
		body, _ := json.Marshal(duplicateReq)

		req, _ := http.NewRequest("POST", server.URL+"/v1/topics", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusConflict, resp.StatusCode)
	})

	t.Run("Create same title at different level succeeds", func(t *testing.T) {
		// Create Level 2 topic with same title as first Level 1 topic
		level2Req := map[string]interface{}{
			"title":     "Unique Title L1", // Same title as first topic, but different level
			"level":     2,
			"tenant_id": "uniqueness-test-tenant",
		}
		body, _ := json.Marshal(level2Req)

		req, _ := http.NewRequest("POST", server.URL+"/v1/topics", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)
	})
}

func TestListTopics(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create a test topic
	reqBody := map[string]interface{}{
		"title":     "List Test Topic",
		"level":     1,
		"tenant_id": "list-test-tenant",
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", server.URL+"/v1/topics", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")
	_, _ = client.Do(req)

	t.Run("List all topics", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/topics", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.ListTopicsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.NotEmpty(t, result.Data)
	})

	t.Run("List with level filter", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/topics?level=1&limit=10", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.ListTopicsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		for _, topic := range result.Data {
			assert.Equal(t, 1, topic.Level)
		}
	})
}

func TestGetTopic(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create a test topic
	reqBody := map[string]interface{}{
		"title": "Get Test Topic",
		"level": 1,
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", server.URL+"/v1/topics", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = createResp.Body.Close() }()

	var created models.Topic
	err = decodeData(createResp, &created)
	require.NoError(t, err)

	t.Run("Get existing topic", func(t *testing.T) {
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/v1/topics/%s", server.URL, created.ID), nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.Topic
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.Equal(t, created.ID, result.ID)
		assert.Equal(t, "Get Test Topic", result.Title)
	})

	t.Run("Get non-existent topic", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/topics/00000000-0000-0000-0000-000000000000", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestUpdateTopic(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create a test topic
	reqBody := map[string]interface{}{
		"title":     "Initial Title",
		"level":     1,
		"tenant_id": "update-test-tenant",
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", server.URL+"/v1/topics", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = createResp.Body.Close() }()

	var created models.Topic
	err = decodeData(createResp, &created)
	require.NoError(t, err)

	t.Run("Update topic title", func(t *testing.T) {
		updateBody := map[string]interface{}{
			"title": "Updated Title",
		}
		body, _ := json.Marshal(updateBody)

		req, _ := http.NewRequest("PATCH", fmt.Sprintf("%s/v1/topics/%s", server.URL, created.ID), bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.Topic
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.Equal(t, created.ID, result.ID)
		assert.Equal(t, "Updated Title", result.Title)
	})

	t.Run("Update to same title (idempotent) succeeds", func(t *testing.T) {
		updateBody := map[string]interface{}{
			"title": "Updated Title",
		}
		body, _ := json.Marshal(updateBody)

		req, _ := http.NewRequest("PATCH", fmt.Sprintf("%s/v1/topics/%s", server.URL, created.ID), bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("Update title to duplicate returns 409", func(t *testing.T) {
		// Create another topic
		otherReq := map[string]interface{}{
			"title":     "Other Topic",
			"level":     1,
			"tenant_id": "update-test-tenant",
		}
		body, _ := json.Marshal(otherReq)
		req, _ := http.NewRequest("POST", server.URL+"/v1/topics", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")
		otherResp, _ := client.Do(req)
		_ = otherResp.Body.Close()

		// Try to update first topic to have same title as other topic
		updateBody := map[string]interface{}{
			"title": "Other Topic",
		}
		body, _ = json.Marshal(updateBody)

		req, _ = http.NewRequest("PATCH", fmt.Sprintf("%s/v1/topics/%s", server.URL, created.ID), bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusConflict, resp.StatusCode)
	})
}

func TestDeleteTopic(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create a test topic
	reqBody := map[string]interface{}{
		"title": "To be deleted",
		"level": 1,
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", server.URL+"/v1/topics", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = createResp.Body.Close() }()

	var created models.Topic
	err = decodeData(createResp, &created)
	require.NoError(t, err)

	t.Run("Delete topic", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/v1/topics/%s", server.URL, created.ID), nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})

	t.Run("Verify deletion", func(t *testing.T) {
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/v1/topics/%s", server.URL, created.ID), nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestDeleteTopicIndependently(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create Level 1 topic
	level1Req := map[string]interface{}{
		"title":     "Level 1 to Delete",
		"level":     1,
		"tenant_id": "delete-test-tenant",
	}
	body, _ := json.Marshal(level1Req)
	req, _ := http.NewRequest("POST", server.URL+"/v1/topics", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	level1Resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = level1Resp.Body.Close() }()

	var level1Topic models.Topic
	err = decodeData(level1Resp, &level1Topic)
	require.NoError(t, err)

	// Create Level 2 topic
	level2Req := map[string]interface{}{
		"title":     "Level 2 Independent",
		"level":     2,
		"tenant_id": "delete-test-tenant",
	}
	body, _ = json.Marshal(level2Req)
	req, _ = http.NewRequest("POST", server.URL+"/v1/topics", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	level2Resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = level2Resp.Body.Close() }()

	var level2Topic models.Topic
	err = decodeData(level2Resp, &level2Topic)
	require.NoError(t, err)

	t.Run("Delete Level 1 topic does not affect Level 2 topics", func(t *testing.T) {
		// Delete Level 1 topic
		req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/v1/topics/%s", server.URL, level1Topic.ID), nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusNoContent, resp.StatusCode)

		// Verify Level 2 topic still exists (no cascade)
		req, _ = http.NewRequest("GET", fmt.Sprintf("%s/v1/topics/%s", server.URL, level2Topic.ID), nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err = client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}
