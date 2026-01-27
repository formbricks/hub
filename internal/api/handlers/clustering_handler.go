package handlers

import (
	"context"
	"net/http"
	"strconv"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/service"
)

// ClusteringService defines the interface for clustering business logic.
type ClusteringService interface {
	ClusterFeedback(ctx context.Context, tenantID *string, k int, maxIterations int) (*service.ClusterResult, error)
	FindOptimalClusters(ctx context.Context, tenantID *string, minK, maxK int) (*service.ElbowAnalysis, error)
}

// ClusteringHandler handles HTTP requests for clustering operations.
type ClusteringHandler struct {
	service ClusteringService
}

// NewClusteringHandler creates a new clustering handler.
func NewClusteringHandler(service ClusteringService) *ClusteringHandler {
	return &ClusteringHandler{service: service}
}

// Cluster handles POST /v1/clustering/run
// Performs K-means clustering on feedback embeddings.
func (h *ClusteringHandler) Cluster(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	kStr := r.URL.Query().Get("k")
	if kStr == "" {
		kStr = "5" // Default to 5 clusters
	}
	k, err := strconv.Atoi(kStr)
	if err != nil || k < 2 {
		response.RespondBadRequest(w, "k must be an integer >= 2")
		return
	}

	maxIterStr := r.URL.Query().Get("max_iterations")
	maxIterations := 100 // Default
	if maxIterStr != "" {
		maxIterations, err = strconv.Atoi(maxIterStr)
		if err != nil || maxIterations < 1 {
			response.RespondBadRequest(w, "max_iterations must be a positive integer")
			return
		}
	}

	var tenantID *string
	if tid := r.URL.Query().Get("tenant_id"); tid != "" {
		tenantID = &tid
	}

	result, err := h.service.ClusterFeedback(r.Context(), tenantID, k, maxIterations)
	if err != nil {
		response.RespondBadRequest(w, err.Error())
		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}

// Elbow handles GET /v1/clustering/elbow
// Runs elbow analysis to find optimal number of clusters.
func (h *ClusteringHandler) Elbow(w http.ResponseWriter, r *http.Request) {
	minKStr := r.URL.Query().Get("min_k")
	minK := 2 // Default
	if minKStr != "" {
		var err error
		minK, err = strconv.Atoi(minKStr)
		if err != nil || minK < 2 {
			response.RespondBadRequest(w, "min_k must be an integer >= 2")
			return
		}
	}

	maxKStr := r.URL.Query().Get("max_k")
	maxK := 10 // Default
	if maxKStr != "" {
		var err error
		maxK, err = strconv.Atoi(maxKStr)
		if err != nil || maxK < minK {
			response.RespondBadRequest(w, "max_k must be an integer >= min_k")
			return
		}
	}

	var tenantID *string
	if tid := r.URL.Query().Get("tenant_id"); tid != "" {
		tenantID = &tid
	}

	result, err := h.service.FindOptimalClusters(r.Context(), tenantID, minK, maxK)
	if err != nil {
		response.RespondBadRequest(w, err.Error())
		return
	}

	response.RespondJSON(w, http.StatusOK, result)
}
