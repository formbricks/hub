package api

import (
	"context"
	"log/slog"
	"math"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/danielgtaylor/huma/v2"
	"github.com/formbricks/hub/internal/config"
	"github.com/formbricks/hub/internal/embedding"
	"github.com/formbricks/hub/internal/ent"
	"github.com/formbricks/hub/internal/ent/feedbackrecord"
	entvec "github.com/pgvector/pgvector-go/ent"
)

// SearchInput defines the input for semantic search
type SearchInput struct {
	Query string `query:"query" required:"true" minLength:"1" maxLength:"1000" doc:"Natural language search query" example:"pricing feedback"`
	Limit int    `query:"limit" default:"10" minimum:"1" maximum:"100" doc:"Maximum number of results to return"`

	// Optional filters
	SourceType string `query:"source_type" doc:"Filter by source type (e.g., survey, review)" example:"survey"`
	Since      string `query:"since" doc:"Filter by collection date (ISO 8601)" example:"2024-01-01T00:00:00Z"`
	Until      string `query:"until" doc:"Filter by collection date (ISO 8601)" example:"2024-12-31T23:59:59Z"`
}

// SearchResultItem represents a single search result with similarity score
type SearchResultItem struct {
	FeedbackRecordData
	SimilarityScore float64 `json:"similarity_score" doc:"Cosine similarity score (0-1, higher is more similar)"`
}

// SearchOutput defines the output for semantic search
type SearchOutput struct {
	Body struct {
		Results []SearchResultItem `json:"results" doc:"Search results ordered by relevance"`
		Query   string             `json:"query" doc:"The search query that was executed"`
		Count   int                `json:"count" doc:"Number of results returned"`
	}
}

// RegisterSearchRoutes registers semantic search routes
func RegisterSearchRoutes(api huma.API, cfg *config.Config, client *ent.Client, logger *slog.Logger) {
	huma.Register(api, huma.Operation{
		OperationID: "search-feedback-records",
		Method:      "GET",
		Path:        "/v1/feedback-records/search",
		Summary:     "Search feedback records using semantic search",
		Description: "Performs vector similarity search on feedback record data using OpenAI embeddings. Only returns text records that have been embedded.",
		Tags:        []string{"Feedback Records"},
	}, func(ctx context.Context, input *SearchInput) (*SearchOutput, error) {
		// Check if embeddings are enabled
		if !cfg.IsEmbeddingEnabled() {
			return nil, huma.Error400BadRequest("Semantic search is not enabled. Configure SERVICE_OPENAI_EMBEDDING_MODEL to enable.")
		}

		// Create embedding service
		embeddingService := embedding.NewService(
			cfg.OpenAIKey,
			cfg.OpenAIEmbeddingModel,
			cfg.EnrichmentTimeout,
			logger,
		)

		// Generate embedding for the search query
		queryVector, err := embeddingService.GenerateEmbedding(ctx, input.Query)
		if err != nil {
			// Use sanitized error handling for service errors
			return nil, handleServiceError(logger, err, "embedding", "generate query embedding")
		}

		// Build query with filters and ordering by cosine distance
		query := client.FeedbackRecord.Query()

		// Apply optional filters
		if input.SourceType != "" {
			query = query.Where(feedbackrecord.SourceTypeEQ(input.SourceType))
		}
		if input.Since != "" {
			sinceTime, err := time.Parse(time.RFC3339, input.Since)
			if err != nil {
				return nil, huma.Error400BadRequest("Invalid 'since' timestamp format. Expected ISO 8601 (RFC3339) format, e.g., 2024-01-01T00:00:00Z")
			}
			query = query.Where(feedbackrecord.CollectedAtGTE(sinceTime))
		}
		if input.Until != "" {
			untilTime, err := time.Parse(time.RFC3339, input.Until)
			if err != nil {
				return nil, huma.Error400BadRequest("Invalid 'until' timestamp format. Expected ISO 8601 (RFC3339) format, e.g., 2024-12-31T23:59:59Z")
			}
			query = query.Where(feedbackrecord.CollectedAtLTE(untilTime))
		}

		// Execute the query
		records, err := query.
			Order(func(s *sql.Selector) {
				s.OrderExpr(entvec.CosineDistance(feedbackrecord.FieldEmbedding, queryVector))
			}).
			Limit(input.Limit).
			All(ctx)

		if err != nil {
			return nil, handleDatabaseError(logger, err, "semantic search", "query")
		}

		// For each record, compute the actual similarity
		// Since we can't easily extract distance from Ent query, we recalculate it
		var results []SearchResultItem
		for _, fr := range records {
			// Calculate cosine distance between query vector and feedback record embedding
			var distance float64
			if fr.Embedding != nil && queryVector.Slice() != nil {
				distance = cosineDist(queryVector.Slice(), fr.Embedding.Slice())
			} else {
				distance = 1.0 // Maximum distance if no embedding
			}

			// Convert distance to similarity: similarity = 1 - distance
			// Cosine distance ranges from 0 (identical) to 2 (opposite)
			// Clamp to [0, 1] range
			similarity := 1.0 - distance
			if similarity < 0 {
				similarity = 0
			}
			if similarity > 1 {
				similarity = 1
			}

			results = append(results, SearchResultItem{
				FeedbackRecordData: entityToOutput(fr),
				SimilarityScore:    similarity,
			})
		}

		return &SearchOutput{
			Body: struct {
				Results []SearchResultItem `json:"results" doc:"Search results ordered by relevance"`
				Query   string             `json:"query" doc:"The search query that was executed"`
				Count   int                `json:"count" doc:"Number of results returned"`
			}{
				Results: results,
				Query:   input.Query,
				Count:   len(results),
			},
		}, nil
	})
}

// cosineDist calculates the cosine distance between two vectors
// Cosine distance = 1 - cosine similarity
// Returns 0 for identical vectors, up to 2 for opposite vectors
func cosineDist(a, b []float32) float64 {
	if len(a) != len(b) {
		return 2.0 // Maximum distance for incompatible vectors
	}
	if len(a) == 0 {
		return 2.0
	}

	var dotProduct float64
	var magnitudeA float64
	var magnitudeB float64

	for i := 0; i < len(a); i++ {
		dotProduct += float64(a[i]) * float64(b[i])
		magnitudeA += float64(a[i]) * float64(a[i])
		magnitudeB += float64(b[i]) * float64(b[i])
	}

	magnitudeA = math.Sqrt(magnitudeA)
	magnitudeB = math.Sqrt(magnitudeB)

	if magnitudeA == 0 || magnitudeB == 0 {
		return 2.0 // Avoid division by zero
	}

	cosineSim := dotProduct / (magnitudeA * magnitudeB)

	// Clamp to [-1, 1] to handle floating point errors
	if cosineSim > 1.0 {
		cosineSim = 1.0
	}
	if cosineSim < -1.0 {
		cosineSim = -1.0
	}

	// Distance = 1 - similarity
	return 1.0 - cosineSim
}
