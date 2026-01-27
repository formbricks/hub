package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"sort"

	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
)

// ClusteringRepository defines the interface for data access needed by clustering.
type ClusteringRepository interface {
	GetFeedbackEmbeddings(ctx context.Context, tenantID *string, limit int) ([]models.EmbeddingRecord, error)
}

// ClusteringService handles K-means clustering operations for feedback analysis.
type ClusteringService struct {
	repo ClusteringRepository
}

// NewClusteringService creates a new clustering service.
func NewClusteringService(repo ClusteringRepository) *ClusteringService {
	return &ClusteringService{repo: repo}
}

// ClusterResult represents the result of clustering operation.
type ClusterResult struct {
	Clusters      []Cluster      `json:"clusters"`
	NumClusters   int            `json:"num_clusters"`
	TotalRecords  int            `json:"total_records"`
	ElbowAnalysis *ElbowAnalysis `json:"elbow_analysis,omitempty"`
	Silhouette    float64        `json:"silhouette_score"`
}

// Cluster represents a single cluster with its centroid and members.
type Cluster struct {
	ID              int             `json:"id"`
	Centroid        []float32       `json:"-"` // Not exposed in JSON
	Members         []ClusterMember `json:"members"`
	Size            int             `json:"size"`
	SuggestedTitle  string          `json:"suggested_title,omitempty"`
	TopKeywords     []string        `json:"top_keywords,omitempty"`
	AverageDistance float64         `json:"average_distance"`
}

// ClusterMember represents a feedback record within a cluster.
type ClusterMember struct {
	ID       uuid.UUID `json:"id"`
	Text     string    `json:"text,omitempty"`
	Distance float64   `json:"distance"` // Distance from centroid
}

// ElbowAnalysis provides data for determining optimal number of clusters.
type ElbowAnalysis struct {
	KValues  []int     `json:"k_values"`
	Inertias []float64 `json:"inertias"` // Within-cluster sum of squares
	OptimalK int       `json:"optimal_k"`
}

// ClusterFeedback performs K-means clustering on feedback embeddings.
func (s *ClusteringService) ClusterFeedback(ctx context.Context, tenantID *string, k int, maxIterations int) (*ClusterResult, error) {
	if k < 2 {
		return nil, fmt.Errorf("k must be at least 2")
	}
	if maxIterations < 1 {
		maxIterations = 100
	}

	// Fetch embeddings
	embeddings, err := s.repo.GetFeedbackEmbeddings(ctx, tenantID, 10000) // Limit to 10k for performance
	if err != nil {
		return nil, fmt.Errorf("failed to fetch embeddings: %w", err)
	}

	if len(embeddings) < k {
		return nil, fmt.Errorf("not enough records (%d) for %d clusters", len(embeddings), k)
	}

	slog.Info("starting k-means clustering", "k", k, "records", len(embeddings))

	// Run K-means
	clusters := kMeans(embeddings, k, maxIterations)

	// Calculate silhouette score
	silhouette := calculateSilhouetteScore(embeddings, clusters)

	// Build result
	result := &ClusterResult{
		Clusters:     clusters,
		NumClusters:  k,
		TotalRecords: len(embeddings),
		Silhouette:   silhouette,
	}

	return result, nil
}

// FindOptimalClusters runs elbow method to find optimal number of clusters.
func (s *ClusteringService) FindOptimalClusters(ctx context.Context, tenantID *string, minK, maxK int) (*ElbowAnalysis, error) {
	if minK < 2 {
		minK = 2
	}
	if maxK > 20 {
		maxK = 20 // Cap at 20 clusters for performance
	}
	if minK >= maxK {
		return nil, fmt.Errorf("minK must be less than maxK")
	}

	// Fetch embeddings
	embeddings, err := s.repo.GetFeedbackEmbeddings(ctx, tenantID, 10000)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch embeddings: %w", err)
	}

	if len(embeddings) < maxK {
		maxK = len(embeddings)
	}

	slog.Info("running elbow analysis", "minK", minK, "maxK", maxK, "records", len(embeddings))

	kValues := make([]int, 0, maxK-minK+1)
	inertias := make([]float64, 0, maxK-minK+1)

	for k := minK; k <= maxK; k++ {
		clusters := kMeans(embeddings, k, 50) // Fewer iterations for speed
		inertia := calculateInertia(embeddings, clusters)
		kValues = append(kValues, k)
		inertias = append(inertias, inertia)
	}

	// Find elbow point using the "knee" detection algorithm
	optimalK := findElbowPoint(kValues, inertias)

	return &ElbowAnalysis{
		KValues:  kValues,
		Inertias: inertias,
		OptimalK: optimalK,
	}, nil
}

// kMeans performs the K-means clustering algorithm.
func kMeans(embeddings []models.EmbeddingRecord, k, maxIterations int) []Cluster {
	if len(embeddings) == 0 || k == 0 {
		return nil
	}

	dim := len(embeddings[0].Embedding)

	// Initialize centroids using K-means++ algorithm
	centroids := initializeCentroidsKMeansPlusPlus(embeddings, k)

	assignments := make([]int, len(embeddings))

	for iter := 0; iter < maxIterations; iter++ {
		// Assignment step: assign each point to nearest centroid
		changed := false
		for i, emb := range embeddings {
			nearest := findNearestCentroid(emb.Embedding, centroids)
			if assignments[i] != nearest {
				assignments[i] = nearest
				changed = true
			}
		}

		// If no assignments changed, we've converged
		if !changed && iter > 0 {
			slog.Debug("k-means converged", "iterations", iter+1)
			break
		}

		// Update step: recalculate centroids
		newCentroids := make([][]float32, k)
		counts := make([]int, k)

		for i := 0; i < k; i++ {
			newCentroids[i] = make([]float32, dim)
		}

		for i, emb := range embeddings {
			cluster := assignments[i]
			counts[cluster]++
			for d := 0; d < dim; d++ {
				newCentroids[cluster][d] += emb.Embedding[d]
			}
		}

		for i := 0; i < k; i++ {
			if counts[i] > 0 {
				for d := 0; d < dim; d++ {
					newCentroids[i][d] /= float32(counts[i])
				}
				centroids[i] = newCentroids[i]
			}
		}
	}

	// Build cluster results
	clusters := make([]Cluster, k)
	for i := 0; i < k; i++ {
		clusters[i] = Cluster{
			ID:       i,
			Centroid: centroids[i],
			Members:  make([]ClusterMember, 0),
		}
	}

	for i, emb := range embeddings {
		cluster := assignments[i]
		dist := cosineDistance(emb.Embedding, centroids[cluster])
		clusters[cluster].Members = append(clusters[cluster].Members, ClusterMember{
			ID:       emb.ID,
			Text:     emb.Text,
			Distance: dist,
		})
	}

	// Calculate cluster stats and sort members by distance
	for i := range clusters {
		clusters[i].Size = len(clusters[i].Members)

		// Sort members by distance (closest first)
		sort.Slice(clusters[i].Members, func(a, b int) bool {
			return clusters[i].Members[a].Distance < clusters[i].Members[b].Distance
		})

		// Calculate average distance
		var totalDist float64
		for _, m := range clusters[i].Members {
			totalDist += m.Distance
		}
		if clusters[i].Size > 0 {
			clusters[i].AverageDistance = totalDist / float64(clusters[i].Size)
		}

		// Suggest a title based on the closest member (representative)
		if clusters[i].Size > 0 && clusters[i].Members[0].Text != "" {
			// Truncate for title
			text := clusters[i].Members[0].Text
			if len(text) > 50 {
				text = text[:50] + "..."
			}
			clusters[i].SuggestedTitle = fmt.Sprintf("Cluster %d: %s", i+1, text)
		} else {
			clusters[i].SuggestedTitle = fmt.Sprintf("Cluster %d", i+1)
		}
	}

	return clusters
}

// initializeCentroidsKMeansPlusPlus uses K-means++ initialization for better starting centroids.
func initializeCentroidsKMeansPlusPlus(embeddings []models.EmbeddingRecord, k int) [][]float32 {
	n := len(embeddings)
	centroids := make([][]float32, 0, k)

	// Pick first centroid randomly
	firstIdx := rand.Intn(n)
	centroids = append(centroids, embeddings[firstIdx].Embedding)

	// Pick remaining centroids with probability proportional to distance squared
	for len(centroids) < k {
		distances := make([]float64, n)
		var totalDist float64

		for i, emb := range embeddings {
			minDist := math.MaxFloat64
			for _, centroid := range centroids {
				dist := cosineDistance(emb.Embedding, centroid)
				if dist < minDist {
					minDist = dist
				}
			}
			distances[i] = minDist * minDist // Square the distance
			totalDist += distances[i]
		}

		// Weighted random selection
		target := rand.Float64() * totalDist
		var cumDist float64
		selectedIdx := 0
		for i, d := range distances {
			cumDist += d
			if cumDist >= target {
				selectedIdx = i
				break
			}
		}

		centroids = append(centroids, embeddings[selectedIdx].Embedding)
	}

	return centroids
}

// findNearestCentroid finds the index of the nearest centroid to the given embedding.
func findNearestCentroid(embedding []float32, centroids [][]float32) int {
	minDist := math.MaxFloat64
	nearest := 0
	for i, centroid := range centroids {
		dist := cosineDistance(embedding, centroid)
		if dist < minDist {
			minDist = dist
			nearest = i
		}
	}
	return nearest
}

// cosineDistance calculates 1 - cosine_similarity (so smaller is more similar).
func cosineDistance(a, b []float32) float64 {
	if len(a) != len(b) {
		return 1.0 // Maximum distance for mismatched dimensions
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 1.0
	}

	similarity := dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
	return 1.0 - similarity
}

// calculateInertia calculates the within-cluster sum of squares.
func calculateInertia(embeddings []models.EmbeddingRecord, clusters []Cluster) float64 {
	var inertia float64
	for _, cluster := range clusters {
		for _, member := range cluster.Members {
			// Find the embedding for this member
			for _, emb := range embeddings {
				if emb.ID == member.ID {
					dist := cosineDistance(emb.Embedding, cluster.Centroid)
					inertia += dist * dist
					break
				}
			}
		}
	}
	return inertia
}

// calculateSilhouetteScore calculates the silhouette score for clustering quality.
// Score ranges from -1 to 1, where higher is better.
func calculateSilhouetteScore(embeddings []models.EmbeddingRecord, clusters []Cluster) float64 {
	if len(clusters) < 2 {
		return 0
	}

	// Create a map of ID to embedding for quick lookup
	embMap := make(map[uuid.UUID][]float32)
	for _, emb := range embeddings {
		embMap[emb.ID] = emb.Embedding
	}

	// Create cluster assignment map
	clusterMap := make(map[uuid.UUID]int)
	for i, cluster := range clusters {
		for _, member := range cluster.Members {
			clusterMap[member.ID] = i
		}
	}

	var totalScore float64
	var count int

	for _, emb := range embeddings {
		clusterIdx := clusterMap[emb.ID]

		// Calculate a(i): average distance to other points in same cluster
		var aSum float64
		var aCount int
		for _, member := range clusters[clusterIdx].Members {
			if member.ID != emb.ID {
				aSum += cosineDistance(emb.Embedding, embMap[member.ID])
				aCount++
			}
		}
		var a float64
		if aCount > 0 {
			a = aSum / float64(aCount)
		}

		// Calculate b(i): minimum average distance to points in other clusters
		b := math.MaxFloat64
		for i, cluster := range clusters {
			if i == clusterIdx {
				continue
			}
			var bSum float64
			for _, member := range cluster.Members {
				bSum += cosineDistance(emb.Embedding, embMap[member.ID])
			}
			if len(cluster.Members) > 0 {
				avgDist := bSum / float64(len(cluster.Members))
				if avgDist < b {
					b = avgDist
				}
			}
		}

		// Calculate silhouette for this point
		maxAB := math.Max(a, b)
		if maxAB > 0 {
			s := (b - a) / maxAB
			totalScore += s
			count++
		}
	}

	if count == 0 {
		return 0
	}
	return totalScore / float64(count)
}

// findElbowPoint finds the optimal K using the elbow method.
func findElbowPoint(kValues []int, inertias []float64) int {
	if len(kValues) < 3 {
		return kValues[0]
	}

	// Use the "kneedle" algorithm: find the point with maximum distance
	// from the line connecting the first and last points

	n := len(kValues)
	x1, y1 := float64(kValues[0]), inertias[0]
	x2, y2 := float64(kValues[n-1]), inertias[n-1]

	maxDist := 0.0
	elbowIdx := 0

	for i := 1; i < n-1; i++ {
		x0, y0 := float64(kValues[i]), inertias[i]

		// Distance from point to line
		num := math.Abs((y2-y1)*x0 - (x2-x1)*y0 + x2*y1 - y2*x1)
		den := math.Sqrt((y2-y1)*(y2-y1) + (x2-x1)*(x2-x1))

		dist := num / den
		if dist > maxDist {
			maxDist = dist
			elbowIdx = i
		}
	}

	return kValues[elbowIdx]
}
