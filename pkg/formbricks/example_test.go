package formbricks_test

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"

	"github.com/formbricks/hub/pkg/formbricks"
)

// generateRandomExampleID generates a random alphanumeric ID for examples
func generateRandomExampleID() string {
	b := make([]byte, 20) // Generate enough bytes to ensure we have at least 26 chars after encoding
	_, _ = rand.Read(b)
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

func ExampleClient_GetResponses() {
	// Create a mock HTTP server that simulates the Formbricks API
	// Generate random IDs for the example
	responseID := "response-" + generateRandomExampleID()
	surveyID := generateRandomExampleID()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock response matching the Formbricks API format
		mockResponse := formbricks.ResponsesResponse{
			Data: []formbricks.Response{
				{
					ID:       responseID,
					SurveyID: surveyID,
					Finished: true,
					Data: map[string]interface{}{
						"question1": "Answer 1",
						"question2": "Answer 2",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(mockResponse); err != nil {
			slog.Error("Failed to encode mock response", "error", err)
		}
	}))
	defer server.Close()

	// Create a client pointing to the mock server
	client := formbricks.NewClientWithBaseURL(server.URL+"/api/v2", "test-api-key")

	// Get responses for a specific survey
	responses, err := client.GetResponses(formbricks.GetResponsesOptions{
		SurveyID: surveyID,
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// Print the responses
	for _, response := range responses.Data {
		fmt.Printf("Finished: %v\n", response.Finished)
		fmt.Printf("Data: %v\n", response.Data)
	}

	// Output:
	// Finished: true
	// Data: map[question1:Answer 1 question2:Answer 2]
}
