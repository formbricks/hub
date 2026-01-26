package formbricks

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateRandomID generates a random alphanumeric ID similar to Formbricks format
func generateRandomID(t *testing.T) string {
	b := make([]byte, 20) // Generate enough bytes to ensure we have at least 26 chars after encoding
	_, err := rand.Read(b)
	require.NoError(t, err)
	// Use base64 URL encoding and remove padding, then take first 26 chars to match Formbricks format
	encoded := base64.URLEncoding.EncodeToString(b)
	// Remove any padding characters
	encoded = encoded[:len(encoded)-len(encoded)%4]
	// Take first 26 chars, or all if shorter (shouldn't happen with 20 bytes)
	if len(encoded) > 26 {
		return encoded[:26]
	}
	return encoded
}

func TestClient_GetResponses(t *testing.T) {
	// Generate random IDs for the test
	responseID := generateRandomID(t)
	surveyID := generateRandomID(t)
	displayID := generateRandomID(t)
	questionID1 := generateRandomID(t)
	questionID2 := generateRandomID(t)
	questionID3 := generateRandomID(t)

	// Mock response data matching the Formbricks API format
	mockResponse := ResponsesResponse{
		Data: []Response{
			{
				ID:        responseID,
				CreatedAt: time.Date(2026, 1, 26, 10, 31, 49, 219000000, time.UTC),
				UpdatedAt: time.Date(2026, 1, 26, 10, 32, 7, 104000000, time.UTC),
				Finished:  true,
				SurveyID:  surveyID,
				Data: map[string]interface{}{
					questionID1: "nothing",
					questionID2: "noone",
					questionID3: "Very disappointed",
				},
				Variables: map[string]interface{}{},
				TTC: map[string]interface{}{
					"_total": 21805,
				},
				Meta: Meta{
					URL:     "https://app.formbricks.com/s/" + surveyID,
					Country: "GB",
					UserAgent: UserAgent{
						OS:      "macOS",
						Device:  "desktop",
						Browser: "Firefox",
					},
				},
				DisplayID: displayID,
			},
		},
	}

	// Create a mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/api/v2/management/responses", r.URL.Path)
		assert.Equal(t, "test-api-key", r.Header.Get("x-api-key"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, surveyID, r.URL.Query().Get("surveyId"))

		// Return mock response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(mockResponse); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	// Create client with the mock server URL
	client := NewClientWithBaseURL(server.URL+"/api/v2", "test-api-key")

	// Test GetResponses
	responses, err := client.GetResponses(GetResponsesOptions{
		SurveyID: surveyID,
	})

	// Assertions
	require.NoError(t, err)
	require.NotNil(t, responses)
	assert.Len(t, responses.Data, 1)

	response := responses.Data[0]
	assert.Equal(t, responseID, response.ID)
	assert.Equal(t, surveyID, response.SurveyID)
	assert.True(t, response.Finished)
	assert.Equal(t, "GB", response.Meta.Country)
	assert.Equal(t, "Firefox", response.Meta.UserAgent.Browser)
	assert.Equal(t, "macOS", response.Meta.UserAgent.OS)
	assert.Equal(t, "desktop", response.Meta.UserAgent.Device)
	assert.Equal(t, "nothing", response.Data[questionID1])
}

func TestClient_GetResponses_ErrorHandling(t *testing.T) {
	t.Run("HTTP error status", func(t *testing.T) {
		// Create a mock server that returns an error
		// Note: retryablehttp will retry on 5xx errors, so we use a 4xx error instead
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			if _, err := w.Write([]byte(`{"error": "Unauthorized"}`)); err != nil {
				slog.Error("Failed to write error response", "error", err)
			}
		}))
		defer server.Close()

		surveyID := generateRandomID(t)
		client := NewClientWithBaseURL(server.URL+"/api/v2", "test-api-key")
		responses, err := client.GetResponses(GetResponsesOptions{
			SurveyID: surveyID,
		})

		assert.Error(t, err)
		assert.Nil(t, responses)
		// The error should mention the status code
		assert.Contains(t, err.Error(), "401")
	})

	t.Run("Invalid JSON response", func(t *testing.T) {
		// Create a mock server that returns invalid JSON
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`invalid json`)); err != nil {
				slog.Error("Failed to write invalid JSON response", "error", err)
			}
		}))
		defer server.Close()

		surveyID := generateRandomID(t)
		client := NewClientWithBaseURL(server.URL+"/api/v2", "test-api-key")
		responses, err := client.GetResponses(GetResponsesOptions{
			SurveyID: surveyID,
		})

		assert.Error(t, err)
		assert.Nil(t, responses)
		assert.Contains(t, err.Error(), "unmarshal")
	})

	t.Run("Empty responses", func(t *testing.T) {
		// Create a mock server that returns empty data
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if err := json.NewEncoder(w).Encode(ResponsesResponse{Data: []Response{}}); err != nil {
				t.Errorf("Failed to encode response: %v", err)
			}
		}))
		defer server.Close()

		surveyID := generateRandomID(t)
		client := NewClientWithBaseURL(server.URL+"/api/v2", "test-api-key")
		responses, err := client.GetResponses(GetResponsesOptions{
			SurveyID: surveyID,
		})

		require.NoError(t, err)
		assert.NotNil(t, responses)
		assert.Len(t, responses.Data, 0)
	})
}

func TestClient_GetResponses_WithoutSurveyID(t *testing.T) {
	// Mock response with no survey ID filter
	mockResponse := ResponsesResponse{
		Data: []Response{},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify no surveyId query parameter
		assert.Empty(t, r.URL.Query().Get("surveyId"))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(mockResponse); err != nil {
			t.Errorf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.URL+"/api/v2", "test-api-key")
	responses, err := client.GetResponses(GetResponsesOptions{})

	require.NoError(t, err)
	assert.NotNil(t, responses)
}

func TestNewClient(t *testing.T) {
	client := NewClient("test-api-key")
	assert.NotNil(t, client)
	assert.Equal(t, "https://app.formbricks.com/api/v2", client.baseURL)
	assert.Equal(t, "test-api-key", client.apiKey)
}

func TestNewClientWithBaseURL(t *testing.T) {
	customURL := "https://custom.formbricks.com/api/v2"
	client := NewClientWithBaseURL(customURL, "test-api-key")
	assert.NotNil(t, client)
	assert.Equal(t, customURL, client.baseURL)
	assert.Equal(t, "test-api-key", client.apiKey)
}

func TestNewClientWithOptions(t *testing.T) {
	t.Run("With all options", func(t *testing.T) {
		client := NewClientWithOptions(ClientOptions{
			BaseURL:  "https://custom.formbricks.com/api/v2",
			APIKey:   "test-api-key",
			RetryMax: 5,
			Timeout:  60 * time.Second,
		})

		assert.NotNil(t, client)
		assert.Equal(t, "https://custom.formbricks.com/api/v2", client.baseURL)
		assert.Equal(t, "test-api-key", client.apiKey)
		assert.Equal(t, 5, client.httpClient.RetryMax)
		assert.Equal(t, 60*time.Second, client.httpClient.HTTPClient.Timeout)
	})

	t.Run("With defaults", func(t *testing.T) {
		client := NewClientWithOptions(ClientOptions{
			APIKey: "test-api-key",
		})

		assert.NotNil(t, client)
		assert.Equal(t, "https://app.formbricks.com/api/v2", client.baseURL)
		assert.Equal(t, "test-api-key", client.apiKey)
		assert.Equal(t, 3, client.httpClient.RetryMax)
		assert.Equal(t, 30*time.Second, client.httpClient.HTTPClient.Timeout)
	})
}
