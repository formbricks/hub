package formbricks

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hashicorp/go-retryablehttp"
)

// ClientOptions configures the Formbricks API client
type ClientOptions struct {
	// BaseURL is the base URL for Formbricks (default: "https://app.formbricks.com")
	// Do not include /api/v1 or /api/v2 - these are added automatically
	BaseURL string
	// APIKey is the Formbricks API key
	APIKey string
	// RetryMax is the maximum number of retries (default: 3)
	RetryMax int
	// Timeout is the HTTP client timeout (default: 30 seconds)
	Timeout time.Duration
}

// Client is the Formbricks API client
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *retryablehttp.Client
}

// NewClient creates a new Formbricks API client with default settings
func NewClient(apiKey string) *Client {
	return NewClientWithOptions(ClientOptions{
		APIKey:  apiKey,
		BaseURL: "https://app.formbricks.com",
	})
}

// NewClientWithBaseURL creates a new Formbricks API client with a custom base URL
func NewClientWithBaseURL(baseURL, apiKey string) *Client {
	return NewClientWithOptions(ClientOptions{
		APIKey:  apiKey,
		BaseURL: baseURL,
	})
}

// NewClientWithOptions creates a new Formbricks API client with custom options
func NewClientWithOptions(opts ClientOptions) *Client {
	if opts.BaseURL == "" {
		opts.BaseURL = "https://app.formbricks.com"
	}

	// Normalize base URL - remove trailing slash and any /api/v* suffix
	opts.BaseURL = strings.TrimSuffix(opts.BaseURL, "/")
	opts.BaseURL = strings.TrimSuffix(opts.BaseURL, "/api/v1")
	opts.BaseURL = strings.TrimSuffix(opts.BaseURL, "/api/v2")

	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.RetryMax == 0 {
		opts.RetryMax = 3
	}

	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = opts.RetryMax
	retryClient.HTTPClient.Timeout = opts.Timeout
	retryClient.Logger = nil // Disable logging by default

	return &Client{
		baseURL:    opts.BaseURL,
		apiKey:     opts.APIKey,
		httpClient: retryClient,
	}
}

// v1URL returns the v1 API base URL
func (c *Client) v1URL() string {
	return c.baseURL + "/api/v1"
}

// v2URL returns the v2 API base URL
func (c *Client) v2URL() string {
	return c.baseURL + "/api/v2"
}

// GetResponsesOptions contains options for getting responses
type GetResponsesOptions struct {
	SurveyID string
}

// GetResponses retrieves survey responses (v2 API)
func (c *Client) GetResponses(opts GetResponsesOptions) (*ResponsesResponse, error) {
	reqURL := fmt.Sprintf("%s/management/responses", c.v2URL())

	// Build query parameters
	params := url.Values{}
	if opts.SurveyID != "" {
		params.Add("surveyId", opts.SurveyID)
	}

	if len(params) > 0 {
		reqURL += "?" + params.Encode()
	}

	req, err := retryablehttp.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("Failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			slog.Error("Failed to read error response body", "error", err)
		}
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var responsesResponse ResponsesResponse
	if err := json.Unmarshal(body, &responsesResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &responsesResponse, nil
}

// SurveyResponse wraps the survey details API response
type SurveyResponse struct {
	Data SurveyDetails `json:"data"`
}

// GetSurvey retrieves survey details including questions (v1 API)
// See: https://formbricks.com/docs/api-reference/management-api--survey/get-survey-by-id
func (c *Client) GetSurvey(surveyID string) (*SurveyDetails, error) {
	if surveyID == "" {
		return nil, fmt.Errorf("survey_id is required")
	}

	reqURL := fmt.Sprintf("%s/management/surveys/%s", c.v1URL(), surveyID)

	req, err := retryablehttp.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("Failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			slog.Error("Failed to read error response body", "error", err)
		}
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var surveyResponse SurveyResponse
	if err := json.Unmarshal(body, &surveyResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal survey: %w", err)
	}

	return &surveyResponse.Data, nil
}
