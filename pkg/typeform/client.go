package typeform

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/hashicorp/go-retryablehttp"
)

// ClientOptions configures the Typeform API client
type ClientOptions struct {
	// BaseURL is the base URL for the Typeform API (default: "https://api.typeform.com")
	BaseURL string
	// AccessToken is the Typeform personal access token
	AccessToken string
	// RetryMax is the maximum number of retries (default: 3)
	RetryMax int
	// Timeout is the HTTP client timeout (default: 30 seconds)
	Timeout time.Duration
}

// Client is the Typeform API client
type Client struct {
	baseURL     string
	accessToken string
	httpClient  *retryablehttp.Client
}

// NewClient creates a new Typeform API client with default settings
func NewClient(accessToken string) *Client {
	return NewClientWithOptions(ClientOptions{
		AccessToken: accessToken,
		BaseURL:     "https://api.typeform.com",
	})
}

// NewClientWithBaseURL creates a new Typeform API client with a custom base URL
func NewClientWithBaseURL(baseURL, accessToken string) *Client {
	return NewClientWithOptions(ClientOptions{
		AccessToken: accessToken,
		BaseURL:     baseURL,
	})
}

// NewClientWithOptions creates a new Typeform API client with custom options
func NewClientWithOptions(opts ClientOptions) *Client {
	if opts.BaseURL == "" {
		opts.BaseURL = "https://api.typeform.com"
	}
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
		baseURL:     opts.BaseURL,
		accessToken: opts.AccessToken,
		httpClient:  retryClient,
	}
}

// GetResponsesOptions contains options for getting responses
type GetResponsesOptions struct {
	// FormID is the Typeform form ID (required)
	FormID string
	// PageSize is the number of responses per page (default: 25, max: 1000)
	PageSize int
	// Since filters responses submitted since this date (RFC3339 format)
	Since string
	// Until filters responses submitted until this date (RFC3339 format)
	Until string
	// After is a cursor for pagination (response token to start after)
	After string
	// Before is a cursor for pagination (response token to end before)
	Before string
	// Completed filters by completion status (true = only completed)
	Completed *bool
	// Sort specifies the sort order (submitted_at for oldest first, or leave empty for newest first)
	Sort string
	// Query searches responses for a specific string
	Query string
	// Fields filters for specific field IDs (comma-separated)
	Fields string
	// AnsweredFields filters for responses with specific fields answered (comma-separated)
	AnsweredFields string
}

// GetResponses retrieves form responses
func (c *Client) GetResponses(opts GetResponsesOptions) (*ResponsesResponse, error) {
	if opts.FormID == "" {
		return nil, fmt.Errorf("form_id is required")
	}

	reqURL := fmt.Sprintf("%s/forms/%s/responses", c.baseURL, opts.FormID)

	// Build query parameters
	params := url.Values{}
	if opts.PageSize > 0 {
		params.Add("page_size", fmt.Sprintf("%d", opts.PageSize))
	}
	if opts.Since != "" {
		params.Add("since", opts.Since)
	}
	if opts.Until != "" {
		params.Add("until", opts.Until)
	}
	if opts.After != "" {
		params.Add("after", opts.After)
	}
	if opts.Before != "" {
		params.Add("before", opts.Before)
	}
	if opts.Completed != nil {
		params.Add("completed", fmt.Sprintf("%t", *opts.Completed))
	}
	if opts.Sort != "" {
		params.Add("sort", opts.Sort)
	}
	if opts.Query != "" {
		params.Add("query", opts.Query)
	}
	if opts.Fields != "" {
		params.Add("fields", opts.Fields)
	}
	if opts.AnsweredFields != "" {
		params.Add("answered_fields", opts.AnsweredFields)
	}

	if len(params) > 0 {
		reqURL += "?" + params.Encode()
	}

	req, err := retryablehttp.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Accept", "application/json")

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

// BoolPtr returns a pointer to the bool value
func BoolPtr(b bool) *bool {
	return &b
}
