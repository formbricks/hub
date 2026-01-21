package repository

import (
	"strings"
	"testing"
	"time"

	"github.com/formbricks/hub/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSearchQuery(t *testing.T) {
	t.Run("requires query parameter", func(t *testing.T) {
		req := &models.SearchFeedbackRecordsRequest{
			Query: nil,
		}

		query, args, err := buildSearchQuery(req)

		assert.Error(t, err)
		assert.Nil(t, args)
		assert.Empty(t, query)
		assert.Contains(t, err.Error(), "query parameter is required")
	})

	t.Run("requires non-empty query parameter", func(t *testing.T) {
		emptyQuery := ""
		req := &models.SearchFeedbackRecordsRequest{
			Query: &emptyQuery,
		}

		query, args, err := buildSearchQuery(req)

		assert.Error(t, err)
		assert.Nil(t, args)
		assert.Empty(t, query)
		assert.Contains(t, err.Error(), "query parameter is required")
	})

	t.Run("builds basic query with only query parameter", func(t *testing.T) {
		queryStr := "test"
		limit := 10
		req := &models.SearchFeedbackRecordsRequest{
			Query: &queryStr,
			Limit: limit,
		}

		query, args, err := buildSearchQuery(req)

		require.NoError(t, err)
		require.NotNil(t, args)
		assert.Len(t, args, 2) // query pattern + limit

		// Check that query contains the search pattern
		assert.Contains(t, query, "value_text ILIKE $1")
		assert.Contains(t, query, "field_label ILIKE $1")
		assert.Contains(t, query, "source_name ILIKE $1")
		assert.Contains(t, query, "field_id ILIKE $1")
		assert.Contains(t, query, "ORDER BY collected_at DESC")
		assert.Contains(t, query, "LIMIT $2")

		// Check args
		assert.Equal(t, "%test%", args[0])
		assert.Equal(t, limit, args[1])
	})

	t.Run("includes source_type filter", func(t *testing.T) {
		queryStr := "test"
		sourceType := "formbricks"
		req := &models.SearchFeedbackRecordsRequest{
			Query:      &queryStr,
			SourceType: &sourceType,
		}

		query, args, err := buildSearchQuery(req)

		require.NoError(t, err)
		require.NotNil(t, args)
		assert.Len(t, args, 3) // query pattern + source_type + limit

		// Check that query contains source_type filter
		assert.Contains(t, query, "source_type = $2")
		assert.Equal(t, "formbricks", args[1])
	})

	t.Run("includes source_id filter", func(t *testing.T) {
		queryStr := "test"
		sourceID := "survey_123"
		req := &models.SearchFeedbackRecordsRequest{
			Query:    &queryStr,
			SourceID: &sourceID,
		}

		query, args, err := buildSearchQuery(req)

		require.NoError(t, err)
		require.NotNil(t, args)
		assert.Len(t, args, 3) // query pattern + source_id + limit

		// Check that query contains source_id filter
		assert.Contains(t, query, "source_id = $2")
		assert.Equal(t, "survey_123", args[1])
	})

	t.Run("includes field_type filter", func(t *testing.T) {
		queryStr := "test"
		fieldType := "text"
		req := &models.SearchFeedbackRecordsRequest{
			Query:     &queryStr,
			FieldType: &fieldType,
		}

		query, args, err := buildSearchQuery(req)

		require.NoError(t, err)
		require.NotNil(t, args)
		assert.Len(t, args, 3) // query pattern + field_type + limit

		// Check that query contains field_type filter
		assert.Contains(t, query, "field_type = $2")
		assert.Equal(t, "text", args[1])
	})

	t.Run("includes user_identifier filter", func(t *testing.T) {
		queryStr := "test"
		userIdentifier := "user_123"
		req := &models.SearchFeedbackRecordsRequest{
			Query:          &queryStr,
			UserIdentifier: &userIdentifier,
		}

		query, args, err := buildSearchQuery(req)

		require.NoError(t, err)
		require.NotNil(t, args)
		assert.Len(t, args, 3) // query pattern + user_identifier + limit

		// Check that query contains user_identifier filter
		assert.Contains(t, query, "user_identifier = $2")
		assert.Equal(t, "user_123", args[1])
	})

	t.Run("includes since date filter", func(t *testing.T) {
		queryStr := "test"
		since := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		req := &models.SearchFeedbackRecordsRequest{
			Query: &queryStr,
			Since: &since,
		}

		query, args, err := buildSearchQuery(req)

		require.NoError(t, err)
		require.NotNil(t, args)
		assert.Len(t, args, 3) // query pattern + since + limit

		// Check that query contains since filter
		assert.Contains(t, query, "collected_at >= $2")
		assert.Equal(t, since, args[1])
	})

	t.Run("includes until date filter", func(t *testing.T) {
		queryStr := "test"
		until := time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)
		req := &models.SearchFeedbackRecordsRequest{
			Query: &queryStr,
			Until: &until,
		}

		query, args, err := buildSearchQuery(req)

		require.NoError(t, err)
		require.NotNil(t, args)
		assert.Len(t, args, 3) // query pattern + until + limit

		// Check that query contains until filter
		assert.Contains(t, query, "collected_at <= $2")
		assert.Equal(t, until, args[1])
	})

	t.Run("includes all filters together", func(t *testing.T) {
		queryStr := "test"
		sourceType := "formbricks"
		sourceID := "survey_123"
		fieldType := "text"
		userIdentifier := "user_123"
		since := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		until := time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)
		limit := 10
		req := &models.SearchFeedbackRecordsRequest{
			Query:          &queryStr,
			SourceType:     &sourceType,
			SourceID:       &sourceID,
			FieldType:     &fieldType,
			UserIdentifier: &userIdentifier,
			Since:          &since,
			Until:          &until,
			Limit:          limit,
		}

		query, args, err := buildSearchQuery(req)

		require.NoError(t, err)
		require.NotNil(t, args)
		assert.Len(t, args, 8) // query pattern + 6 filters + limit

		// Check that all filters are present
		assert.Contains(t, query, "source_type = $2")
		assert.Contains(t, query, "source_id = $3")
		assert.Contains(t, query, "field_type = $4")
		assert.Contains(t, query, "user_identifier = $5")
		assert.Contains(t, query, "collected_at >= $6")
		assert.Contains(t, query, "collected_at <= $7")
		assert.Contains(t, query, "LIMIT $8")

		// Check args order
		assert.Equal(t, "%test%", args[0])
		assert.Equal(t, "formbricks", args[1])
		assert.Equal(t, "survey_123", args[2])
		assert.Equal(t, "text", args[3])
		assert.Equal(t, "user_123", args[4])
		assert.Equal(t, since, args[5])
		assert.Equal(t, until, args[6])
		assert.Equal(t, limit, args[7])
	})

	t.Run("uses provided limit value", func(t *testing.T) {
		queryStr := "test"
		limit := 25
		req := &models.SearchFeedbackRecordsRequest{
			Query: &queryStr,
			Limit: limit,
		}

		query, args, err := buildSearchQuery(req)

		require.NoError(t, err)
		assert.Contains(t, query, "LIMIT $2")
		assert.Equal(t, limit, args[1])
	})

	t.Run("uses limit exactly as provided (no defaults)", func(t *testing.T) {
		queryStr := "test"
		req := &models.SearchFeedbackRecordsRequest{
			Query: &queryStr,
			Limit: 0, // Repository uses whatever is provided - defaults handled by service
		}

		query, args, err := buildSearchQuery(req)

		require.NoError(t, err)
		assert.Contains(t, query, "LIMIT $2")
		assert.Equal(t, 0, args[1]) // Repository uses value as-is
	})

	t.Run("uses custom limit when provided", func(t *testing.T) {
		queryStr := "test"
		req := &models.SearchFeedbackRecordsRequest{
			Query: &queryStr,
			Limit: 25,
		}

		query, args, err := buildSearchQuery(req)

		require.NoError(t, err)
		assert.Contains(t, query, "LIMIT $2")
		assert.Equal(t, 25, args[1])
	})

	t.Run("uses limit exactly as provided (no capping)", func(t *testing.T) {
		queryStr := "test"
		req := &models.SearchFeedbackRecordsRequest{
			Query: &queryStr,
			Limit: 200, // Repository uses whatever is provided - capping handled by service
		}

		query, args, err := buildSearchQuery(req)

		require.NoError(t, err)
		assert.Contains(t, query, "LIMIT $2")
		assert.Equal(t, 200, args[1]) // Repository uses value as-is
	})

	t.Run("handles query with special characters", func(t *testing.T) {
		queryStr := "test%_query"
		req := &models.SearchFeedbackRecordsRequest{
			Query: &queryStr,
		}

		query, args, err := buildSearchQuery(req)

		require.NoError(t, err)
		// Special characters should be escaped to prevent ILIKE wildcard injection
		assert.Equal(t, "%test\\%\\_query%", args[0])
		// Query should include ESCAPE clause for proper ILIKE escaping
		assert.Contains(t, query, "ESCAPE '\\'")
	})

	t.Run("query structure is correct", func(t *testing.T) {
		queryStr := "test"
		req := &models.SearchFeedbackRecordsRequest{
			Query: &queryStr,
		}

		query, _, err := buildSearchQuery(req)

		require.NoError(t, err)

		// Trim whitespace for checking prefix
		trimmedQuery := strings.TrimSpace(query)

		// Check that query has correct structure
		assert.True(t, strings.HasPrefix(trimmedQuery, "SELECT"), "Query should start with SELECT")
		assert.Contains(t, query, "FROM feedback_records", "Query should contain FROM clause")
		assert.Contains(t, query, "WHERE", "Query should contain WHERE clause")
		assert.Contains(t, query, "ORDER BY collected_at DESC", "Query should contain ORDER BY clause")
		assert.Contains(t, query, "LIMIT", "Query should contain LIMIT clause")

		// Check that WHERE comes before ORDER BY
		whereIndex := strings.Index(query, "WHERE")
		orderByIndex := strings.Index(query, "ORDER BY")
		assert.True(t, whereIndex < orderByIndex, "WHERE should come before ORDER BY")

		// Check that ORDER BY comes before LIMIT
		limitIndex := strings.Index(query, "LIMIT")
		assert.True(t, orderByIndex < limitIndex, "ORDER BY should come before LIMIT")
	})

	t.Run("filters are combined with AND", func(t *testing.T) {
		queryStr := "test"
		sourceType := "formbricks"
		fieldType := "text"
		req := &models.SearchFeedbackRecordsRequest{
			Query:      &queryStr,
			SourceType: &sourceType,
			FieldType: &fieldType,
		}

		query, _, err := buildSearchQuery(req)

		require.NoError(t, err)

		// Count occurrences of "AND" in WHERE clause
		whereIndex := strings.Index(query, "WHERE")
		orderByIndex := strings.Index(query, "ORDER BY")
		whereClause := query[whereIndex:orderByIndex]

		// Should have at least one AND (between search condition and filters)
		assert.Contains(t, whereClause, " AND ", "Filters should be combined with AND")
	})

	t.Run("search pattern is properly escaped in ILIKE", func(t *testing.T) {
		queryStr := "test"
		req := &models.SearchFeedbackRecordsRequest{
			Query: &queryStr,
		}

		query, args, err := buildSearchQuery(req)

		require.NoError(t, err)

		// The search pattern should be wrapped with %
		assert.Equal(t, "%test%", args[0])
		// The query should use ILIKE (case-insensitive)
		assert.Contains(t, query, "ILIKE")
		// The query should include ESCAPE clause for proper ILIKE escaping
		assert.Contains(t, query, "ESCAPE '\\'")
	})
}

func TestEscapeILIKE(t *testing.T) {
	t.Run("escapes percent sign", func(t *testing.T) {
		result := escapeILIKE("test%query")
		assert.Equal(t, "test\\%query", result)
	})

	t.Run("escapes underscore", func(t *testing.T) {
		result := escapeILIKE("test_query")
		assert.Equal(t, "test\\_query", result)
	})

	t.Run("escapes backslash first", func(t *testing.T) {
		result := escapeILIKE("test\\query")
		assert.Equal(t, "test\\\\query", result)
	})

	t.Run("escapes multiple special characters", func(t *testing.T) {
		result := escapeILIKE("test%_query\\value")
		assert.Equal(t, "test\\%\\_query\\\\value", result)
	})

	t.Run("handles empty string", func(t *testing.T) {
		result := escapeILIKE("")
		assert.Equal(t, "", result)
	})

	t.Run("handles string with no special characters", func(t *testing.T) {
		result := escapeILIKE("normal text")
		assert.Equal(t, "normal text", result)
	})

	t.Run("handles only special characters", func(t *testing.T) {
		result := escapeILIKE("%_\\")
		assert.Equal(t, "\\%\\_\\\\", result)
	})

	t.Run("handles backslash before wildcards", func(t *testing.T) {
		// Backslash must be escaped first, then wildcards
		result := escapeILIKE("\\%\\_")
		assert.Equal(t, "\\\\\\%\\\\\\_", result)
	})
}
