package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/formbricks/hub/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchPagination(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create multiple test feedback records
	for i := 0; i < 25; i++ {
		reqBody := map[string]interface{}{
			"source_type": "formbricks",
			"field_id":    fmt.Sprintf("test_field_%d", i),
			"field_type":  "text",
			"value_text":  fmt.Sprintf("Test value %d", i),
		}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")
		_, _ = client.Do(req)
	}

	t.Run("Default limit (10)", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records/search?query=test", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.SearchFeedbackRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.Equal(t, "test", result.Query)
		assert.LessOrEqual(t, len(result.Results), 10) // Default limit is 10
		assert.Equal(t, int64(len(result.Results)), result.Count)
	})

	t.Run("Custom limit within max", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records/search?query=test&limit=5", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.SearchFeedbackRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.LessOrEqual(t, len(result.Results), 5)
		assert.Equal(t, int64(len(result.Results)), result.Count)
	})

	t.Run("Limit exceeds maximum (100)", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records/search?query=test&limit=200", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.SearchFeedbackRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		// Should be capped at 100
		assert.LessOrEqual(t, len(result.Results), 100)
	})

	t.Run("Missing query parameter", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records/search", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("Invalid limit parameter", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records/search?query=test&limit=invalid", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("Negative limit parameter", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records/search?query=test&limit=-1", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestSearchFilters(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create test feedback records with different attributes
	testData := []map[string]interface{}{
		{
			"source_type":     "formbricks",
			"source_id":       "survey_1",
			"field_id":        "question_1",
			"field_type":      "text",
			"value_text":      "Great product!",
			"user_identifier": "user_123",
		},
		{
			"source_type":     "formbricks",
			"source_id":       "survey_2",
			"field_id":        "question_2",
			"field_type":      "number",
			"value_number":    5,
			"user_identifier": "user_456",
		},
		{
			"source_type":     "typeform",
			"source_id":       "form_1",
			"field_id":        "rating",
			"field_type":      "number",
			"value_number":    4,
			"user_identifier": "user_123",
		},
	}

	for _, data := range testData {
		body, _ := json.Marshal(data)
		req, _ := http.NewRequest("POST", server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")
		_, _ = client.Do(req)
	}

	t.Run("Filter by source_type", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records/search?query=product&source_type=typeform", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.SearchFeedbackRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.Greater(t, len(result.Results), 0)
		for _, item := range result.Results {
			assert.Equal(t, "typeform", item.SourceType)
		}
	})

	t.Run("Filter by source_id", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records/search?query=Great&source_id=survey_1", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.SearchFeedbackRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.Greater(t, len(result.Results), 0)
		for _, item := range result.Results {
			assert.Equal(t, "survey_1", *item.SourceID)
		}
	})

	t.Run("Filter by field_type", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records/search?query=5&field_type=number", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.SearchFeedbackRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.Greater(t, len(result.Results), 0)
		for _, item := range result.Results {
			assert.Equal(t, "number", item.FieldType)
		}
	})

	t.Run("Filter by user_identifier", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records/search?query=Great&user_identifier=user_123", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.SearchFeedbackRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.Greater(t, len(result.Results), 0)
		for _, item := range result.Results {
			assert.Equal(t, "user_123", *item.UserIdentifier)
		}
	})

	t.Run("Multiple filters combined", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records/search?query=Great&source_type=formbricks&field_type=text", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.SearchFeedbackRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		for _, item := range result.Results {
			assert.Equal(t, "formbricks", item.SourceType)
			assert.Equal(t, "text", item.FieldType)
		}
	})
}

func TestSearchFullText(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create test feedback records with searchable text
	testData := []map[string]interface{}{
		{
			"source_type": "formbricks",
			"field_id":    "feedback",
			"field_type":  "text",
			"value_text":  "This product is amazing!",
		},
		{
			"source_type": "formbricks",
			"field_id":    "comment",
			"field_type":  "text",
			"value_text":  "Could be better, needs improvement",
		},
		{
			"source_type": "formbricks",
			"field_id":    "review",
			"field_type":  "text",
			"value_text":  "Amazing features and great support",
		},
	}

	for _, data := range testData {
		body, _ := json.Marshal(data)
		req, _ := http.NewRequest("POST", server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")
		_, _ = client.Do(req)
	}

	t.Run("Search with query parameter", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records/search?query=amazing", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.SearchFeedbackRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		// Should find at least the records with "amazing" in them
		assert.GreaterOrEqual(t, len(result.Results), 2)
		assert.Equal(t, "amazing", result.Query)
		assert.Equal(t, int64(len(result.Results)), result.Count)
	})

	t.Run("Search with no results", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records/search?query=nonexistent123xyz", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.SearchFeedbackRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.Equal(t, 0, len(result.Results))
		assert.Equal(t, int64(0), result.Count)
	})
}

func TestSearchDateRange(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create test feedback records with different dates
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)
	tomorrow := now.Add(24 * time.Hour)

	testData := []struct {
		collectedAt time.Time
		text        string
	}{
		{yesterday, "Yesterday's feedback"},
		{now, "Today's feedback"},
		{tomorrow, "Tomorrow's feedback"},
	}

	for _, data := range testData {
		reqBody := map[string]interface{}{
			"source_type":  "formbricks",
			"field_id":     "feedback",
			"field_type":   "text",
			"value_text":   data.text,
			"collected_at": data.collectedAt.Format(time.RFC3339),
		}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")
		_, _ = client.Do(req)
	}

	t.Run("Filter by since date", func(t *testing.T) {
		sinceDate := now.Add(-1 * time.Hour).Format(time.RFC3339)
		url := fmt.Sprintf("%s/v1/feedback-records/search?query=feedback&since=%s", server.URL, sinceDate)
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.SearchFeedbackRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		// Should find records from today and tomorrow
		assert.GreaterOrEqual(t, len(result.Results), 2)
		for _, item := range result.Results {
			assert.True(t, item.CollectedAt.After(now.Add(-2*time.Hour)) || item.CollectedAt.Equal(now.Add(-1*time.Hour)))
		}
	})

	t.Run("Filter by until date", func(t *testing.T) {
		untilDate := now.Add(1 * time.Hour).Format(time.RFC3339)
		url := fmt.Sprintf("%s/v1/feedback-records/search?query=feedback&until=%s", server.URL, untilDate)
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.SearchFeedbackRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		// Should exclude tomorrow's record
		for _, item := range result.Results {
			assert.True(t, item.CollectedAt.Before(now.Add(2*time.Hour)) || item.CollectedAt.Equal(now.Add(1*time.Hour)))
		}
	})

	t.Run("Filter by date range", func(t *testing.T) {
		sinceDate := yesterday.Add(-1 * time.Hour).Format(time.RFC3339)
		untilDate := now.Add(1 * time.Hour).Format(time.RFC3339)
		url := fmt.Sprintf("%s/v1/feedback-records/search?query=feedback&since=%s&until=%s", server.URL, sinceDate, untilDate)
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.SearchFeedbackRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		// Should find yesterday's and today's records
		assert.GreaterOrEqual(t, len(result.Results), 2)
	})
}

func TestSearchPaginationMetadata(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	client := &http.Client{}

	// Create exactly 45 test feedback records
	for i := 0; i < 45; i++ {
		reqBody := map[string]interface{}{
			"source_type": "pagination_test",
			"field_id":    fmt.Sprintf("field_%d", i),
			"field_type":  "text",
			"value_text":  fmt.Sprintf("Value %d", i),
		}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", server.URL+"/v1/feedback-records", bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")
		_, _ = client.Do(req)
	}

	t.Run("Verify limit and count", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records/search?query=Value&source_type=pagination_test&limit=10", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.SearchFeedbackRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.Equal(t, "Value", result.Query)
		assert.LessOrEqual(t, len(result.Results), 10) // Max 10 results per limit
		assert.Equal(t, int64(len(result.Results)), result.Count)
	})

	t.Run("Verify limit enforcement", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/feedback-records/search?query=Value&source_type=pagination_test&limit=5", nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result models.SearchFeedbackRecordsResponse
		err = decodeData(resp, &result)
		require.NoError(t, err)

		assert.LessOrEqual(t, len(result.Results), 5) // Max 5 results per limit
	})
}
