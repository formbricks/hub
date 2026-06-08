package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/formbricks/hub/internal/observability"
)

var (
	ErrTaxonomyServiceURLRequired   = errors.New("TAXONOMY_SERVICE_URL is required")
	ErrTaxonomyServiceTokenRequired = errors.New("TAXONOMY_SERVICE_TOKEN is required")
)

const defaultTaxonomyClientTimeout = 30 * time.Second

// TaxonomyClientConfig configures the outbound client Hub uses to call the taxonomy service.
type TaxonomyClientConfig struct {
	ServiceURL   string
	ServiceToken string
}

// TaxonomyClient calls the standalone taxonomy service.
type TaxonomyClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewTaxonomyClient creates a Hub-to-taxonomy-service client.
func NewTaxonomyClient(cfg TaxonomyClientConfig, httpClient *http.Client) (*TaxonomyClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.ServiceURL), "/")
	if baseURL == "" {
		return nil, ErrTaxonomyServiceURLRequired
	}

	token := strings.TrimSpace(cfg.ServiceToken)
	if token == "" {
		return nil, ErrTaxonomyServiceTokenRequired
	}

	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTaxonomyClientTimeout}
	}

	return &TaxonomyClient{
		baseURL:    baseURL,
		token:      token,
		httpClient: httpClient,
	}, nil
}

// StartRun asks the taxonomy service to start compute for a Hub-created run.
func (c *TaxonomyClient) StartRun(ctx context.Context, runID string) error {
	endpoint, err := url.JoinPath(c.baseURL, "/v1/runs", runID, "start")
	if err != nil {
		return fmt.Errorf("build taxonomy start URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, http.NoBody)
	if err != nil {
		return fmt.Errorf("create taxonomy start request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	if requestID := observability.RequestIDFromContext(ctx); requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("start taxonomy run: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("start taxonomy run: taxonomy service returned status %d", resp.StatusCode)
	}

	return nil
}
