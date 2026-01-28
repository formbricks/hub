package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// TaxonomyClient is an HTTP client for the taxonomy-generator Python microservice.
type TaxonomyClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewTaxonomyClient creates a new taxonomy client.
func NewTaxonomyClient(baseURL string) *TaxonomyClient {
	return &TaxonomyClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute, // Clustering can take a while
		},
	}
}

// ClusterConfig contains optional configuration for clustering.
type ClusterConfig struct {
	UMAPNComponents       *int     `json:"umap_n_components,omitempty"`
	UMAPNNeighbors        *int     `json:"umap_n_neighbors,omitempty"`
	UMAPMinDist           *float64 `json:"umap_min_dist,omitempty"`
	HDBSCANMinClusterSize *int     `json:"hdbscan_min_cluster_size,omitempty"`
	HDBSCANMinSamples     *int     `json:"hdbscan_min_samples,omitempty"`
	MaxEmbeddings         *int     `json:"max_embeddings,omitempty"`
	GenerateLevel2        *bool    `json:"generate_level2,omitempty"`
	Level2MinClusterSize  *int     `json:"level2_min_cluster_size,omitempty"`
}

// ClusteringJobStatus represents the status of a clustering job.
type ClusteringJobStatus string

const (
	ClusteringStatusPending   ClusteringJobStatus = "pending"
	ClusteringStatusRunning   ClusteringJobStatus = "running"
	ClusteringStatusCompleted ClusteringJobStatus = "completed"
	ClusteringStatusFailed    ClusteringJobStatus = "failed"
)

// TopicResult represents a generated topic from clustering.
type TopicResult struct {
	ID                    uuid.UUID  `json:"id"`
	Title                 string     `json:"title"`
	Description           string     `json:"description"`
	Level                 int        `json:"level"`
	ParentID              *uuid.UUID `json:"parent_id,omitempty"`
	ClusterSize           int        `json:"cluster_size"`
	AvgDistanceToCentroid float64    `json:"avg_distance_to_centroid"`
}

// TaxonomyResult contains the result of a completed clustering job.
type TaxonomyResult struct {
	TenantID         string        `json:"tenant_id"`
	JobID            uuid.UUID     `json:"job_id"`
	Status           string        `json:"status"`
	TotalRecords     int           `json:"total_records"`
	ClusteredRecords int           `json:"clustered_records"`
	NoiseRecords     int           `json:"noise_records"`
	NumClusters      int           `json:"num_clusters"`
	Topics           []TopicResult `json:"topics"`
	StartedAt        time.Time     `json:"started_at"`
	CompletedAt      *time.Time    `json:"completed_at,omitempty"`
	ErrorMessage     *string       `json:"error_message,omitempty"`
}

// ClusteringJobResponse is the response from the taxonomy service.
type ClusteringJobResponse struct {
	JobID    uuid.UUID           `json:"job_id"`
	TenantID string              `json:"tenant_id"`
	Status   ClusteringJobStatus `json:"status"`
	Progress float64             `json:"progress"`
	Message  *string             `json:"message,omitempty"`
	Result   *TaxonomyResult     `json:"result,omitempty"`
}

// TriggerClustering starts an async taxonomy generation job for a tenant.
func (c *TaxonomyClient) TriggerClustering(ctx context.Context, tenantID string, config *ClusterConfig) (*ClusteringJobResponse, error) {
	url := fmt.Sprintf("%s/cluster/%s", c.baseURL, tenantID)

	var body io.Reader
	if config != nil {
		jsonBody, err := json.Marshal(config)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal config: %w", err)
		}
		body = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	slog.Info("triggering taxonomy generation", "tenant_id", tenantID, "url", url)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("taxonomy service returned %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result ClusteringJobResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// GetClusteringStatus retrieves the status of a clustering job.
func (c *TaxonomyClient) GetClusteringStatus(ctx context.Context, tenantID string, jobID *uuid.UUID) (*ClusteringJobResponse, error) {
	url := fmt.Sprintf("%s/cluster/%s/status", c.baseURL, tenantID)
	if jobID != nil {
		url = fmt.Sprintf("%s?job_id=%s", url, jobID.String())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("job not found")
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("taxonomy service returned %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result ClusteringJobResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// GenerateTaxonomySync synchronously generates taxonomy for a tenant (blocking).
// Use this for testing or when you need to wait for results.
func (c *TaxonomyClient) GenerateTaxonomySync(ctx context.Context, tenantID string, config *ClusterConfig) (*ClusteringJobResponse, error) {
	url := fmt.Sprintf("%s/cluster/%s/sync", c.baseURL, tenantID)

	var body io.Reader
	if config != nil {
		jsonBody, err := json.Marshal(config)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal config: %w", err)
		}
		body = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	slog.Info("generating taxonomy synchronously", "tenant_id", tenantID, "url", url)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("taxonomy service returned %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result ClusteringJobResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// HealthCheck checks if the taxonomy service is healthy.
func (c *TaxonomyClient) HealthCheck(ctx context.Context) error {
	url := fmt.Sprintf("%s/health", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("taxonomy service unhealthy: status %d", resp.StatusCode)
	}

	return nil
}
