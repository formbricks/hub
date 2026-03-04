package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/hashicorp/golang-lru/v2"
	"golang.org/x/sync/singleflight"

	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/observability"
	"github.com/formbricks/hub/internal/repository"
)

const searchQueryEmbeddingCacheName = "search_query_embedding"

// Sentinel errors for search (used by handlers for status mapping).
var (
	ErrMissingTenantID   = errors.New("tenant_id is required")
	ErrEmptyQuery        = errors.New("query is required and must be non-empty")
	ErrEmbeddingNotFound = repository.ErrEmbeddingNotFound
)

// EmbeddingsRepositoryForSearch provides the embedding read operations needed for semantic search.
// HasMore is true when there may be additional results (full page returned or full fetch limit consumed).
type EmbeddingsRepositoryForSearch interface {
	GetEmbeddingByFeedbackRecordAndModelAndTenant(
		ctx context.Context, feedbackRecordID uuid.UUID, model, tenantID string,
	) ([]float32, error)
	NearestFeedbackRecordsByEmbedding(
		ctx context.Context, model string, queryEmbedding []float32, tenantID string, limit, offset int, excludeID *uuid.UUID, minScore float64,
	) ([]models.FeedbackRecordWithScore, bool, error)
	NearestFeedbackRecordsByEmbeddingAfterCursor(
		ctx context.Context, model string, queryEmbedding []float32, tenantID string, limit int,
		lastDistance float64, lastFeedbackRecordID uuid.UUID, excludeID *uuid.UUID, minScore float64,
	) ([]models.FeedbackRecordWithScore, bool, error)
}

// SearchService performs semantic search and similar-feedback lookups using embeddings.
type SearchService struct {
	embeddingClient EmbeddingClient
	embeddingsRepo  EmbeddingsRepositoryForSearch
	model           string
	queryCache      *lru.Cache[string, []float32]
	queryLoadGroup  singleflight.Group
	cacheMetrics    observability.CacheMetrics
	logger          *slog.Logger
}

// SearchServiceParams configures SearchService. QueryCache and CacheMetrics may be nil (no caching).
type SearchServiceParams struct {
	EmbeddingClient EmbeddingClient
	EmbeddingsRepo  EmbeddingsRepositoryForSearch
	Model           string
	QueryCache      *lru.Cache[string, []float32]
	CacheMetrics    observability.CacheMetrics
	Logger          *slog.Logger
}

// NewSearchService creates a SearchService.
func NewSearchService(p SearchServiceParams) *SearchService {
	logger := p.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &SearchService{
		embeddingClient: p.EmbeddingClient,
		embeddingsRepo:  p.EmbeddingsRepo,
		model:           p.Model,
		queryCache:      p.QueryCache,
		cacheMetrics:    p.CacheMetrics,
		logger:          logger,
	}
}

// SemanticSearch returns feedback record IDs and similarity scores for the given query, scoped to tenantID.
// Requires non-empty tenantID and non-empty (after trim) query. If cursor is non-empty it is used for
// keyset paging (offset is ignored); otherwise offset is used. minScore is the minimum similarity score (0..1).
// NextCursor is set when there may be a next page (full page returned).
func (s *SearchService) SemanticSearch(
	ctx context.Context, query, tenantID string, limit, offset int, minScore float64, cursor string,
) (SearchResult, error) {
	out := SearchResult{}
	if tenantID == "" {
		return out, ErrMissingTenantID
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return out, ErrEmptyQuery
	}

	var (
		embedding []float32
		err       error
	)

	if s.queryCache != nil {
		embedding, err = s.getQueryEmbeddingCached(ctx, query)
	} else {
		embedding, err = s.embeddingClient.CreateEmbeddingForQuery(ctx, query)
	}

	if err != nil {
		s.logger.Error("semantic search: create embedding failed", "error", err, "model", s.model, "limit", limit)

		return out, fmt.Errorf("create embedding: %w", err)
	}

	var results []models.FeedbackRecordWithScore

	var hasMore bool

	if cursor != "" {
		lastDistance, lastID, decErr := DecodeSearchCursor(cursor)
		if decErr != nil {
			return out, ErrInvalidCursor
		}

		results, hasMore, err = s.embeddingsRepo.NearestFeedbackRecordsByEmbeddingAfterCursor(
			ctx, s.model, embedding, tenantID, limit, lastDistance, lastID, nil, minScore)
	} else {
		results, hasMore, err = s.embeddingsRepo.NearestFeedbackRecordsByEmbedding(
			ctx, s.model, embedding, tenantID, limit, offset, nil, minScore)
	}

	if err != nil {
		s.logger.Error("semantic search: nearest failed", "error", err, "model", s.model)

		return out, fmt.Errorf("nearest feedback records: %w", err)
	}

	out.Results = results
	if hasMore && len(results) > 0 {
		last := results[len(results)-1]

		nextCursor, err := EncodeSearchCursor(1-last.Score, last.FeedbackRecordID)
		if err != nil {
			return out, fmt.Errorf("encode next cursor: %w", err)
		}

		out.NextCursor = nextCursor
	}

	return out, nil
}

// SimilarFeedback returns feedback record IDs and similarity scores for records similar to the given one, scoped to tenantID.
// Requires non-empty tenantID. Returns ErrEmbeddingNotFound when the record has no embedding for the current model.
// If cursor is non-empty it is used for keyset paging (offset is ignored); otherwise offset is used.
func (s *SearchService) SimilarFeedback(
	ctx context.Context, feedbackRecordID uuid.UUID, tenantID string, limit, offset int, minScore float64, cursor string,
) (SearchResult, error) {
	out := SearchResult{}
	if tenantID == "" {
		return out, ErrMissingTenantID
	}

	// Load source embedding only if the record belongs to this tenant (tenant isolation).
	embedding, err := s.embeddingsRepo.GetEmbeddingByFeedbackRecordAndModelAndTenant(ctx, feedbackRecordID, s.model, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrEmbeddingNotFound) {
			s.logger.Debug("similar feedback: no embedding or tenant mismatch",
				"feedbackRecordId", feedbackRecordID.String(), "model", s.model)
			//nolint:wrapcheck // return as-is so handler can map to 404
			return out, err
		}

		s.logger.Error("similar feedback: get embedding failed", "error", err, "feedbackRecordId", feedbackRecordID.String())

		return out, fmt.Errorf("get embedding: %w", err)
	}

	var (
		results []models.FeedbackRecordWithScore
		hasMore bool
	)

	if cursor != "" {
		lastDistance, lastID, decErr := DecodeSearchCursor(cursor)
		if decErr != nil {
			return out, ErrInvalidCursor
		}

		results, hasMore, err = s.embeddingsRepo.NearestFeedbackRecordsByEmbeddingAfterCursor(
			ctx, s.model, embedding, tenantID, limit, lastDistance, lastID, &feedbackRecordID, minScore)
	} else {
		results, hasMore, err = s.embeddingsRepo.NearestFeedbackRecordsByEmbedding(
			ctx, s.model, embedding, tenantID, limit, offset, &feedbackRecordID, minScore)
	}

	if err != nil {
		s.logger.Error("similar feedback: nearest failed", "error", err, "feedbackRecordId", feedbackRecordID.String())

		return out, fmt.Errorf("nearest feedback records: %w", err)
	}

	out.Results = results
	if hasMore && len(results) > 0 {
		last := results[len(results)-1]

		nextCursor, err := EncodeSearchCursor(1-last.Score, last.FeedbackRecordID)
		if err != nil {
			return out, fmt.Errorf("encode next cursor: %w", err)
		}

		out.NextCursor = nextCursor
	}

	return out, nil
}

func (s *SearchService) getQueryEmbeddingCached(ctx context.Context, query string) ([]float32, error) {
	if vec, ok := s.queryCache.Get(query); ok {
		if s.cacheMetrics != nil {
			s.cacheMetrics.RecordHit(ctx, searchQueryEmbeddingCacheName)
		}

		return vec, nil
	}

	val, err, shared := s.queryLoadGroup.Do(query, func() (any, error) {
		vec, loadErr := s.embeddingClient.CreateEmbeddingForQuery(ctx, query)
		if loadErr != nil {
			return nil, fmt.Errorf("create embedding: %w", loadErr)
		}

		s.queryCache.Add(query, vec)

		return vec, nil
	})
	if err != nil {
		return nil, fmt.Errorf("query embedding: %w", err)
	}

	if s.cacheMetrics != nil {
		// shared == true: we waited on another caller and got their result (deduplicated = effective cache hit).
		// shared == false: we ran the load ourselves (cache miss).
		if shared {
			s.cacheMetrics.RecordHit(ctx, searchQueryEmbeddingCacheName)
		} else {
			s.cacheMetrics.RecordMiss(ctx, searchQueryEmbeddingCacheName)
		}
	}

	return val.([]float32), nil
}
