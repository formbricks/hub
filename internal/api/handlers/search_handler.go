package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/models"
	"github.com/formbricks/hub/internal/service"
)

// SearchService defines the interface for semantic search and similar feedback.
type SearchService interface {
	SemanticSearch(ctx context.Context, query, tenantID string, topK, offset int, minScore float64, cursor string) (
		service.SearchResult, error)
	SimilarFeedback(ctx context.Context, feedbackRecordID uuid.UUID, tenantID string, limit, offset int,
		minScore float64, cursor string) (service.SearchResult, error)
}

// SearchHandler handles HTTP requests for semantic search and similar feedback.
type SearchHandler struct {
	service SearchService
}

// NewSearchHandler creates a new search handler.
func NewSearchHandler(service SearchService) *SearchHandler {
	return &SearchHandler{service: service}
}

// SemanticSearchRequest is the body for POST /v1/feedback-records/search/semantic.
// API contract uses camelCase (topK, tenantId).
type SemanticSearchRequest struct {
	Query    string `json:"query"`
	TopK     int    `json:"topK"`     //nolint:tagliatelle // API contract
	TenantID string `json:"tenantId"` //nolint:tagliatelle // API contract
}

// SemanticSearchResponse is the response for semantic search and similar feedback.
type SemanticSearchResponse struct {
	Results    []SemanticSearchResultItem `json:"results"`
	NextCursor string                     `json:"nextCursor,omitempty"` //nolint:tagliatelle // API contract camelCase
}

// SemanticSearchResultItem is one result with feedbackRecordId, score, and the record's value_text as value.
type SemanticSearchResultItem struct {
	FeedbackRecordID uuid.UUID `json:"feedbackRecordId"` //nolint:tagliatelle // API contract
	Score            float64   `json:"score"`
	Value            string    `json:"value"` // value_text of the feedback record (the text that was embedded)
}

// maxSearchOffset caps how far paging can go. With OFFSET-based paging the database
// still computes and discards all rows before the offset, so large offsets (e.g. 5000)
// make queries slow. Clamping keeps latency predictable and discourages deep paging.
// To support deeper paging without this limit, switch to cursor-based (keyset) pagination:
// return a cursor (e.g. last score + last feedback_record_id), and query WHERE (score, id) after cursor
// instead of OFFSET.
const maxSearchOffset = 1000

// SemanticSearch handles POST /v1/feedback-records/search/semantic.
func (h *SearchHandler) SemanticSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.RespondError(w, http.StatusMethodNotAllowed, "Method Not Allowed", "POST required")

		return
	}

	var req SemanticSearchRequest

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&req); err != nil {
		response.RespondBadRequest(w, "Invalid request body")

		return
	}

	if req.TenantID == "" {
		response.RespondBadRequest(w, "tenantId is required")

		return
	}

	topK := req.TopK
	if topK <= 0 {
		topK = 10
	}

	const maxTopK = 100
	if topK > maxTopK {
		topK = maxTopK
	}

	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))

	offset := 0
	if cursor == "" {
		offset = min(parseOffset(r.URL.Query().Get("offset")), maxSearchOffset)
	}

	minScore := parseMinScore(r.URL.Query().Get("minScore"))

	res, err := h.service.SemanticSearch(r.Context(), req.Query, req.TenantID, topK, offset, minScore, cursor)
	if err != nil {
		if errors.Is(err, service.ErrMissingTenantID) {
			response.RespondBadRequest(w, "tenantId is required")

			return
		}

		if errors.Is(err, service.ErrEmptyQuery) {
			response.RespondBadRequest(w, "query is required and must be non-empty")

			return
		}

		if errors.Is(err, service.ErrInvalidCursor) {
			response.RespondBadRequest(w, "Invalid cursor")

			return
		}

		response.RespondInternalServerError(w, "Search failed")

		return
	}

	response.RespondJSON(w, http.StatusOK, SemanticSearchResponse{
		Results:    toResultItems(res.Results),
		NextCursor: res.NextCursor,
	})
}

// SimilarFeedback handles GET /v1/feedback-records/{id}/similar.
func (h *SearchHandler) SimilarFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.RespondError(w, http.StatusMethodNotAllowed, "Method Not Allowed", "GET required")

		return
	}

	idStr := r.PathValue("id")
	if idStr == "" {
		response.RespondBadRequest(w, "Feedback record ID is required")

		return
	}

	id, err := uuid.Parse(idStr)
	if err != nil {
		response.RespondBadRequest(w, "Invalid feedback record ID")

		return
	}

	tenantID := r.URL.Query().Get("tenantId")
	if tenantID == "" {
		response.RespondBadRequest(w, "tenantId query parameter is required")

		return
	}

	topKStr := r.URL.Query().Get("topK")
	limit := 10

	const maxSimilarLimit = 100

	if topKStr != "" {
		if l, err := strconv.Atoi(topKStr); err == nil && l > 0 {
			limit = min(l, maxSimilarLimit)
		}
	}

	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))

	offset := 0
	if cursor == "" {
		offset = min(parseOffset(r.URL.Query().Get("offset")), maxSearchOffset)
	}

	minScore := parseMinScore(r.URL.Query().Get("minScore"))

	res, err := h.service.SimilarFeedback(r.Context(), id, tenantID, limit, offset, minScore, cursor)
	if err != nil {
		if errors.Is(err, service.ErrEmbeddingNotFound) {
			response.RespondNotFound(w, "Feedback record has no embedding for the current model")

			return
		}

		if errors.Is(err, service.ErrMissingTenantID) {
			response.RespondBadRequest(w, "tenantId is required")

			return
		}

		if errors.Is(err, service.ErrInvalidCursor) {
			response.RespondBadRequest(w, "Invalid cursor")

			return
		}

		response.RespondInternalServerError(w, "Similar feedback failed")

		return
	}

	response.RespondJSON(w, http.StatusOK, SemanticSearchResponse{
		Results:    toResultItems(res.Results),
		NextCursor: res.NextCursor,
	})
}

// parseOffset returns the query param "offset" as a non-negative int; default 0.
func parseOffset(s string) int {
	if s == "" {
		return 0
	}

	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}

	return n
}

// parseMinScore returns the query param "minScore" as a float in [0,1]; default 0.
func parseMinScore(s string) float64 {
	if s == "" {
		return 0
	}

	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}

	if val < 0 {
		return 0
	}

	return math.Min(val, 1)
}

func toResultItems(results []models.FeedbackRecordWithScore) []SemanticSearchResultItem {
	items := make([]SemanticSearchResultItem, len(results))
	for i := range results {
		items[i] = SemanticSearchResultItem{
			FeedbackRecordID: results[i].FeedbackRecordID,
			Score:            results[i].Score,
			Value:            results[i].ValueText, // always set: we only have embeddings of text
		}
	}

	return items
}
