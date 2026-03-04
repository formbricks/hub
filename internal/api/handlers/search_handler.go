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
	SemanticSearch(ctx context.Context, query, tenantID string, limit, offset int, minScore float64, cursor string) (
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

// SemanticSearchRequest is the body for POST /v1/feedback-records/search/semantic (snake_case for consistency with data model).
type SemanticSearchRequest struct {
	Query    string `json:"query"`
	TenantID string `json:"tenant_id"`
}

// SemanticSearchResponse is the response for semantic search and similar feedback (consistent with list endpoints: data, limit).
type SemanticSearchResponse struct {
	Data       []SemanticSearchResultItem `json:"data"`
	Limit      int                        `json:"limit"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}

// SemanticSearchResultItem is one result: feedback_record_id, score, field_label, value_text (snake_case).
type SemanticSearchResultItem struct {
	FeedbackRecordID uuid.UUID `json:"feedback_record_id"`
	Score            float64   `json:"score"`
	FieldLabel       string    `json:"field_label"`
	ValueText        string    `json:"value_text"` // value_text of the feedback record (the text that was embedded)
}

// maxSearchOffset caps how far paging can go. With OFFSET-based paging the database
// still computes and discards all rows before the offset, so large offsets (e.g. 5000)
// make queries slow. Clamping keeps latency predictable and discourages deep paging.
// To support deeper paging without this limit, switch to cursor-based (keyset) pagination:
// return a cursor (e.g. last score + last feedback_record_id), and query WHERE (score, id) after cursor
// instead of OFFSET.
const (
	maxSearchOffset    = 1000
	defaultSearchLimit = 10
	maxSearchLimit     = 100
)

// SemanticSearch handles POST /v1/feedback-records/search/semantic.
func (h *SearchHandler) SemanticSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.RespondError(w, http.StatusMethodNotAllowed, "Method Not Allowed", "POST required")

		return
	}

	if h.service == nil {
		response.RespondServiceUnavailable(w, "Semantic search is not available: embeddings are not configured.")

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
		response.RespondBadRequest(w, "tenant_id is required")

		return
	}

	limit := parseLimit(r.URL.Query().Get("limit"), defaultSearchLimit, maxSearchLimit)

	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))

	offset := 0
	if cursor == "" {
		offset = min(parseOffset(r.URL.Query().Get("offset")), maxSearchOffset)
	}

	minScore := parseMinScore(r.URL.Query().Get("min_score"))

	res, err := h.service.SemanticSearch(r.Context(), req.Query, req.TenantID, limit, offset, minScore, cursor)
	if err != nil {
		if errors.Is(err, service.ErrMissingTenantID) {
			response.RespondBadRequest(w, "tenant_id is required")

			return
		}

		if errors.Is(err, service.ErrEmptyQuery) {
			response.RespondBadRequest(w, "query is required and must be non-empty")

			return
		}

		if errors.Is(err, service.ErrInvalidCursor) {
			response.RespondBadRequest(w, "Invalid cursor: omit for first page, or use the exact next_cursor value from the previous response")

			return
		}

		response.RespondInternalServerError(w, "Search failed")

		return
	}

	response.RespondJSON(w, http.StatusOK, SemanticSearchResponse{
		Data:       toResultItems(res.Results),
		Limit:      limit,
		NextCursor: res.NextCursor,
	})
}

// SimilarFeedback handles GET /v1/feedback-records/{id}/similar.
func (h *SearchHandler) SimilarFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.RespondError(w, http.StatusMethodNotAllowed, "Method Not Allowed", "GET required")

		return
	}

	if h.service == nil {
		response.RespondServiceUnavailable(w, "Similar feedback is not available: embeddings are not configured.")

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

	tenantID := r.URL.Query().Get("tenant_id")
	if tenantID == "" {
		response.RespondBadRequest(w, "tenant_id query parameter is required")

		return
	}

	limit := parseLimit(r.URL.Query().Get("limit"), defaultSearchLimit, maxSearchLimit)

	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))

	offset := 0
	if cursor == "" {
		offset = min(parseOffset(r.URL.Query().Get("offset")), maxSearchOffset)
	}

	minScore := parseMinScore(r.URL.Query().Get("min_score"))

	res, err := h.service.SimilarFeedback(r.Context(), id, tenantID, limit, offset, minScore, cursor)
	if err != nil {
		if errors.Is(err, service.ErrEmbeddingNotFound) {
			response.RespondNotFound(w, "Feedback record has no embedding for the current model")

			return
		}

		if errors.Is(err, service.ErrMissingTenantID) {
			response.RespondBadRequest(w, "tenant_id is required")

			return
		}

		if errors.Is(err, service.ErrInvalidCursor) {
			response.RespondBadRequest(w, "Invalid cursor: omit for first page, or use the exact next_cursor value from the previous response")

			return
		}

		response.RespondInternalServerError(w, "Similar feedback failed")

		return
	}

	response.RespondJSON(w, http.StatusOK, SemanticSearchResponse{
		Data:       toResultItems(res.Results),
		Limit:      limit,
		NextCursor: res.NextCursor,
	})
}

// parseLimit returns the query param as int clamped to [1, upperBound]; default def when param is missing or invalid.
func parseLimit(s string, def, upperBound int) int {
	if s == "" {
		return def
	}

	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}

	return min(n, upperBound)
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

// defaultMinScore is the default minimum similarity score when the query param is omitted (reduces noise).
const defaultMinScore = 0.7

// parseMinScore returns the query param "min_score" as a float in [0,1]; default defaultMinScore.
func parseMinScore(s string) float64 {
	if s == "" {
		return defaultMinScore
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
			FieldLabel:       results[i].FieldLabel,
			ValueText:        results[i].ValueText,
		}
	}

	return items
}
