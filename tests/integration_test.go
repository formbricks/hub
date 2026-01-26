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
