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
	"github.com/xernobyl/formbricks_worktrial/internal/api/handlers"
	"github.com/xernobyl/formbricks_worktrial/internal/api/middleware"
	"github.com/xernobyl/formbricks_worktrial/internal/config"
	"github.com/xernobyl/formbricks_worktrial/internal/models"
	"github.com/xernobyl/formbricks_worktrial/internal/repository"
	"github.com/xernobyl/formbricks_worktrial/internal/service"
	"github.com/xernobyl/formbricks_worktrial/pkg/database"
)

const testAPIKey = "test-api-key-12345"

// setupTestServer creates a test HTTP server with all routes configured
func setupTestServer(t *testing.T) (*httptest.Server, func()) {
	ctx := context.Background()

	// Load configuration
	cfg, err := config.Load()
	require.NoError(t, err, "Failed to load configuration")

	// Initialize database connection
	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	require.NoError(t, err, "Failed to connect to database")

	// Initialize repository, service, and handler layers
	experienceRepo := repository.NewExperienceRepository(db)
	experienceService := service.NewExperienceService(experienceRepo)
	experienceHandler := handlers.NewExperienceHandler(experienceService)
	healthHandler := handlers.NewHealthHandler()

	// Initialize API key repository for authentication
	apiKeyRepo := repository.NewAPIKeyRepository(db)

	// Set up public endpoints
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("GET /health", healthHandler.Check)

	var publicHandler http.Handler = publicMux

	// Set up protected endpoints
	protectedMux := http.NewServeMux()
	protectedMux.HandleFunc("POST /v1/experiences", experienceHandler.Create)
	protectedMux.HandleFunc("GET /v1/experiences", experienceHandler.List)
	protectedMux.HandleFunc("GET /v1/experiences/{id}", experienceHandler.Get)
	protectedMux.HandleFunc("PATCH /v1/experiences/{id}", experienceHandler.Update)
	protectedMux.HandleFunc("DELETE /v1/experiences/{id}", experienceHandler.Delete)
	protectedMux.HandleFunc("GET /v1/experiences/search", experienceHandler.Search)

	var protectedHandler http.Handler = protectedMux
	protectedHandler = middleware.Auth(apiKeyRepo)(protectedHandler)

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

// decodeData decodes the {"data": ...} wrapper from API responses
func decodeData(resp *http.Response, v interface{}) error {
	var wrapper struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return err
	}
	return json.Unmarshal(wrapper.Data, v)
}

func TestHealthEndpoint(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	resp, err := http.Get(server.URL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Health endpoint returns plain text "OK"
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "OK", string(body))
}

func TestCreateExperience(t *testing.T) {
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

		resp, err := http.Post(server.URL+"/v1/experiences", "application/json", bytes.NewBuffer(body))
		require.NoError(t, err)
		defer resp.Body.Close()

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

		req, _ := http.NewRequest("POST", server.URL+"/v1/experiences", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)

		var result models.ExperienceData
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

		req, _ := http.NewRequest("POST", server.URL+"/v1/experiences", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestListExperiences(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create a test experience first
	reqBody := map[string]interface{}{
		"source_type":  "formbricks",
		"field_id":     "nps_score",
		"field_type":   "number",
		"value_number": 9,
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", server.URL+"/v1/experiences", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")
	_, _ = client.Do(req)

	// Test listing experiences
	t.Run("List all experiences", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/experiences", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result []models.ExperienceData
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.NotEmpty(t, result)
	})

	// Test with filters
	t.Run("List with source_type filter", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/experiences?source_type=formbricks&limit=10", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result []models.ExperienceData
		err = decodeData(resp, &result)
		require.NoError(t, err)

		for _, exp := range result {
			assert.Equal(t, "formbricks", exp.SourceType)
		}
	})
}

func TestGetExperience(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create a test experience
	reqBody := map[string]interface{}{
		"source_type":  "formbricks",
		"field_id":     "rating",
		"field_type":   "number",
		"value_number": 5,
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", server.URL+"/v1/experiences", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(req)
	require.NoError(t, err)
	defer createResp.Body.Close()

	var created models.ExperienceData
	decodeData(createResp, &created)

	// Test getting the experience by ID
	t.Run("Get existing experience", func(t *testing.T) {
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/v1/experiences/%s", server.URL, created.ID), nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.ExperienceData
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.Equal(t, created.ID, result.ID)
		assert.Equal(t, "formbricks", result.SourceType)
	})

	t.Run("Get non-existent experience", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/experiences/00000000-0000-0000-0000-000000000000", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestUpdateExperience(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create a test experience
	reqBody := map[string]interface{}{
		"source_type": "formbricks",
		"field_id":    "comment",
		"field_type":  "text",
		"value_text":  "Initial comment",
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", server.URL+"/v1/experiences", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(req)
	require.NoError(t, err)
	defer createResp.Body.Close()

	var created models.ExperienceData
	decodeData(createResp, &created)

	// Test updating the experience
	t.Run("Update experience", func(t *testing.T) {
		updateBody := map[string]interface{}{
			"value_text": "Updated comment",
		}
		body, _ := json.Marshal(updateBody)

		req, _ := http.NewRequest("PATCH", fmt.Sprintf("%s/v1/experiences/%s", server.URL, created.ID), bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.ExperienceData
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.Equal(t, created.ID, result.ID)
		assert.NotNil(t, result.ValueText)
		assert.Equal(t, "Updated comment", *result.ValueText)
	})
}

func TestDeleteExperience(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create a test experience
	reqBody := map[string]interface{}{
		"source_type": "formbricks",
		"field_id":    "temp",
		"field_type":  "text",
		"value_text":  "To be deleted",
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", server.URL+"/v1/experiences", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")

	createResp, err := client.Do(req)
	require.NoError(t, err)
	defer createResp.Body.Close()

	var created models.ExperienceData
	decodeData(createResp, &created)

	// Test deleting the experience
	t.Run("Delete experience", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/v1/experiences/%s", server.URL, created.ID), nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})

	// Verify it's deleted
	t.Run("Verify deletion", func(t *testing.T) {
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/v1/experiences/%s", server.URL, created.ID), nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestSearchExperiences(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	t.Run("Search with query parameters", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/experiences/search?source_type=formbricks&pageSize=5", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.SearchExperiencesResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		// Should return pagination metadata
		assert.Equal(t, 0, result.Page)
		assert.Equal(t, 5, result.PageSize)
		assert.GreaterOrEqual(t, result.TotalCount, 0)
		assert.NotNil(t, result.Data)
	})

	t.Run("Search with invalid date format", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/experiences/search?start_date=invalid", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}
