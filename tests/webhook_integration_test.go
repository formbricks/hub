package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/formbricks/hub/internal/api/handlers"
	"github.com/formbricks/hub/internal/api/middleware"
	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/connector"
	formbricksconnector "github.com/formbricks/hub/internal/connector/formbricks"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/repository"
	"github.com/formbricks/hub/internal/service"
	"github.com/formbricks/hub/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testWebhookAPIKey       = "test-webhook-key-12345"
	contentTypeHeader       = "Content-Type"
	contentTypeJSON         = "application/json"
	formbricksWebhookPath   = "/webhooks/formbricks?apiKey="
)

// Sample Formbricks webhook payload (responseCreated event with data)
var sampleFormbricksWebhookPayload = `{
	"webhookId": "cmkv56wms4f0mad01uow8gvoj",
	"event": "responseCreated",
	"data": {
		"id": "cmkv5a5x14ogjad01kcjfgb6v",
		"createdAt": "2026-01-26T12:29:40.597Z",
		"updatedAt": "2026-01-26T12:29:40.597Z",
		"surveyId": "cmkv114zm8gspad01m5fowk9u",
		"displayId": "cmkv59utk4nj8ad01y4y2l0ux",
		"contact": null,
		"contactAttributes": null,
		"finished": true,
		"endingId": null,
		"data": {
			"satisfaction_rating": 9,
			"feedback_text": "Great product, love the features!",
			"would_recommend": true
		},
		"variables": {},
		"ttc": {"cxq5whbvyucban6tsht14d6j": 14125},
		"tags": [],
		"meta": {
			"url": "https://app.formbricks.com/s/cmkv114zm8gspad01m5fowk9u",
			"userAgent": {"browser": "Firefox", "os": "macOS", "device": "desktop"},
			"country": "AE"
		},
		"singleUseId": null,
		"language": "en",
		"survey": {
			"title": "Product Market Fit (Superhuman)",
			"type": "link",
			"status": "inProgress",
			"createdAt": "2026-01-26T10:30:41.026Z",
			"updatedAt": "2026-01-26T10:31:38.302Z"
		}
	}
}`

// setupWebhookTestServer creates a test HTTP server with webhook routes configured
func setupWebhookTestServer(t *testing.T) (*httptest.Server, *service.FeedbackRecordsService, func()) {
	ctx := context.Background()

	// Set test API key in environment for authentication
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

	// Initialize webhook router and register Formbricks connector
	webhookRouter := connector.NewWebhookRouter()
	fbWebhookConnector := formbricksconnector.NewWebhookConnector(formbricksconnector.WebhookConfig{
		FeedbackService: feedbackRecordsService,
	})
	err = webhookRouter.Register("formbricks", fbWebhookConnector, testWebhookAPIKey)
	require.NoError(t, err, "Failed to register Formbricks webhook connector")

	webhookHandler := handlers.NewWebhookHandler(webhookRouter)

	// Set up public endpoints (including webhooks)
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("GET /health", healthHandler.Check)
	publicMux.HandleFunc("POST /webhooks/{connector}", webhookHandler.Handle)

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

	return server, feedbackRecordsService, cleanup
}

func TestWebhookEndpoint(t *testing.T) {
	server, _, cleanup := setupWebhookTestServer(t)
	defer cleanup()

	client := &http.Client{}

	t.Run("Missing connector name returns 404", func(t *testing.T) {
		// Note: This will match the pattern but with empty connector
		req, _ := http.NewRequest("POST", server.URL+"/webhooks/", bytes.NewBufferString("{}"))
		req.Header.Set(contentTypeHeader, contentTypeJSON)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		// Empty connector name should return 404 (not found)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("Missing API key returns 401", func(t *testing.T) {
		req, _ := http.NewRequest("POST", server.URL+"/webhooks/formbricks", bytes.NewBufferString("{}"))
		req.Header.Set(contentTypeHeader, contentTypeJSON)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("Invalid API key returns 401", func(t *testing.T) {
		req, _ := http.NewRequest("POST", server.URL+"/webhooks/formbricks?apiKey=wrong-key", bytes.NewBufferString("{}"))
		req.Header.Set(contentTypeHeader, contentTypeJSON)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("Unknown connector returns 404", func(t *testing.T) {
		req, _ := http.NewRequest("POST", server.URL+"/webhooks/unknown?apiKey="+testWebhookAPIKey, bytes.NewBufferString("{}"))
		req.Header.Set(contentTypeHeader, contentTypeJSON)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("Invalid JSON payload returns 500", func(t *testing.T) {
		req, _ := http.NewRequest("POST", server.URL+formbricksWebhookPath+testWebhookAPIKey, bytes.NewBufferString("not valid json"))
		req.Header.Set(contentTypeHeader, contentTypeJSON)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		// Invalid JSON will cause parsing error in connector
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})
}

func TestFormbricksWebhookCreatesFeedbackRecords(t *testing.T) {
	server, feedbackService, cleanup := setupWebhookTestServer(t)
	defer cleanup()

	client := &http.Client{}
	ctx := context.Background()

	t.Run("Formbricks webhook creates feedback records", func(t *testing.T) {
		// Send webhook request
		req, _ := http.NewRequest("POST", server.URL+formbricksWebhookPath+testWebhookAPIKey, bytes.NewBufferString(sampleFormbricksWebhookPayload))
		req.Header.Set(contentTypeHeader, contentTypeJSON)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify response
		var webhookResp map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&webhookResp)
		require.NoError(t, err)
		assert.Equal(t, true, webhookResp["success"])

		// Verify feedback records were created by listing them
		// The sample payload has 3 data fields: satisfaction_rating, feedback_text, would_recommend
		records, err := feedbackService.ListFeedbackRecords(ctx, &models.ListFeedbackRecordsFilters{
			ResponseID: stringPtr("cmkv5a5x14ogjad01kcjfgb6v"),
		})
		require.NoError(t, err)

		// Should have created 3 feedback records (one per field in data)
		assert.Equal(t, int64(3), records.Total, "Expected 3 feedback records to be created")

		// Verify the records have correct source type
		for _, record := range records.Data {
			assert.Equal(t, "formbricks", record.SourceType)
			assert.Equal(t, "cmkv114zm8gspad01m5fowk9u", *record.SourceID) // surveyId
			assert.NotNil(t, record.ResponseID)
			assert.Equal(t, "cmkv5a5x14ogjad01kcjfgb6v", *record.ResponseID)
		}

		// Verify specific field values
		fieldValues := make(map[string]models.FeedbackRecord)
		for _, record := range records.Data {
			fieldValues[record.FieldID] = record
		}

		// Check satisfaction_rating (number)
		if rating, ok := fieldValues["satisfaction_rating"]; ok {
			assert.Equal(t, "number", rating.FieldType)
			assert.NotNil(t, rating.ValueNumber)
			assert.Equal(t, float64(9), *rating.ValueNumber)
		} else {
			t.Error("Expected satisfaction_rating field not found")
		}

		// Check feedback_text (text)
		if feedback, ok := fieldValues["feedback_text"]; ok {
			assert.Equal(t, "text", feedback.FieldType)
			assert.NotNil(t, feedback.ValueText)
			assert.Equal(t, "Great product, love the features!", *feedback.ValueText)
		} else {
			t.Error("Expected feedback_text field not found")
		}

		// Check would_recommend (boolean)
		if recommend, ok := fieldValues["would_recommend"]; ok {
			assert.Equal(t, "boolean", recommend.FieldType)
			assert.NotNil(t, recommend.ValueBoolean)
			assert.Equal(t, true, *recommend.ValueBoolean)
		} else {
			t.Error("Expected would_recommend field not found")
		}
	})
}

func TestFormbricksWebhookWithEmptyData(t *testing.T) {
	server, feedbackService, cleanup := setupWebhookTestServer(t)
	defer cleanup()

	client := &http.Client{}
	ctx := context.Background()

	// Webhook with empty data (no response fields yet)
	emptyDataPayload := `{
		"webhookId": "test-webhook-id",
		"event": "responseCreated",
		"data": {
			"id": "test-response-empty-data",
			"createdAt": "2026-01-26T12:29:40.597Z",
			"updatedAt": "2026-01-26T12:29:40.597Z",
			"surveyId": "test-survey-id",
			"displayId": "test-display-id",
			"finished": false,
			"data": {},
			"meta": {
				"url": "https://example.com",
				"userAgent": {"browser": "Chrome", "os": "Windows", "device": "desktop"},
				"country": "US"
			}
		}
	}`

	t.Run("Webhook with empty data succeeds but creates no records", func(t *testing.T) {
		req, _ := http.NewRequest("POST", server.URL+formbricksWebhookPath+testWebhookAPIKey, bytes.NewBufferString(emptyDataPayload))
		req.Header.Set(contentTypeHeader, contentTypeJSON)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		// Should succeed (acknowledge the webhook)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify no feedback records were created for this response
		records, err := feedbackService.ListFeedbackRecords(ctx, &models.ListFeedbackRecordsFilters{
			ResponseID: stringPtr("test-response-empty-data"),
		})
		require.NoError(t, err)
		assert.Equal(t, int64(0), records.Total, "Expected no feedback records for empty data")
	})
}

func TestFormbricksWebhookDifferentEventTypes(t *testing.T) {
	server, _, cleanup := setupWebhookTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Test responseUpdated event
	t.Run("responseUpdated event is processed", func(t *testing.T) {
		payload := `{
			"webhookId": "test-webhook-id",
			"event": "responseUpdated",
			"data": {
				"id": "test-response-updated",
				"createdAt": "2026-01-26T12:29:40.597Z",
				"updatedAt": "2026-01-26T12:30:40.597Z",
				"surveyId": "test-survey-id",
				"displayId": "test-display-id",
				"finished": false,
				"data": {"nps_score": 8},
				"meta": {
					"url": "https://example.com",
					"userAgent": {"browser": "Chrome", "os": "Windows", "device": "desktop"},
					"country": "US"
				}
			}
		}`

		req, _ := http.NewRequest("POST", server.URL+formbricksWebhookPath+testWebhookAPIKey, bytes.NewBufferString(payload))
		req.Header.Set(contentTypeHeader, contentTypeJSON)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	// Test responseFinished event
	t.Run("responseFinished event is processed", func(t *testing.T) {
		payload := `{
			"webhookId": "test-webhook-id",
			"event": "responseFinished",
			"data": {
				"id": "test-response-finished",
				"createdAt": "2026-01-26T12:29:40.597Z",
				"updatedAt": "2026-01-26T12:31:40.597Z",
				"surveyId": "test-survey-id",
				"displayId": "test-display-id",
				"finished": true,
				"data": {"final_comment": "All done!"},
				"meta": {
					"url": "https://example.com",
					"userAgent": {"browser": "Chrome", "os": "Windows", "device": "desktop"},
					"country": "US"
				}
			}
		}`

		req, _ := http.NewRequest("POST", server.URL+formbricksWebhookPath+testWebhookAPIKey, bytes.NewBufferString(payload))
		req.Header.Set(contentTypeHeader, contentTypeJSON)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	// Test unknown event type (should still succeed, just log warning)
	t.Run("Unknown event type succeeds", func(t *testing.T) {
		payload := `{
			"webhookId": "test-webhook-id",
			"event": "unknownEvent",
			"data": {
				"id": "test-response-unknown",
				"createdAt": "2026-01-26T12:29:40.597Z",
				"updatedAt": "2026-01-26T12:29:40.597Z",
				"surveyId": "test-survey-id",
				"displayId": "test-display-id",
				"finished": false,
				"data": {},
				"meta": {
					"url": "https://example.com",
					"userAgent": {"browser": "Chrome", "os": "Windows", "device": "desktop"},
					"country": "US"
				}
			}
		}`

		req, _ := http.NewRequest("POST", server.URL+formbricksWebhookPath+testWebhookAPIKey, bytes.NewBufferString(payload))
		req.Header.Set(contentTypeHeader, contentTypeJSON)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		// Unknown events should still be acknowledged
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

// stringPtr returns a pointer to the string value
func stringPtr(s string) *string {
	return &s
}
