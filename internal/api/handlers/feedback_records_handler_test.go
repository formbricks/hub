package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/models"
)

// mockFeedbackRecordsService mocks FeedbackRecordsService for handler tests.
type mockFeedbackRecordsService struct {
	countFunc        func(ctx context.Context, filters *models.ListFeedbackRecordsFilters) (int, error)
	createFunc       func(ctx context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error)
	deleteByUserFunc func(ctx context.Context, filters *models.DeleteFeedbackRecordsByUserFilters) (int, error)
}

func (m *mockFeedbackRecordsService) CreateFeedbackRecord(
	ctx context.Context, req *models.CreateFeedbackRecordRequest,
) (*models.FeedbackRecord, error) {
	if m.createFunc != nil {
		return m.createFunc(ctx, req)
	}

	return nil, nil
}

func (m *mockFeedbackRecordsService) GetFeedbackRecord(context.Context, uuid.UUID) (*models.FeedbackRecord, error) {
	return nil, nil
}

func (m *mockFeedbackRecordsService) ListFeedbackRecords(
	context.Context, *models.ListFeedbackRecordsFilters,
) (*models.ListFeedbackRecordsResponse, error) {
	return nil, nil
}

func (m *mockFeedbackRecordsService) UpdateFeedbackRecord(
	context.Context, uuid.UUID, *models.UpdateFeedbackRecordRequest,
) (*models.FeedbackRecord, error) {
	return nil, nil
}

func (m *mockFeedbackRecordsService) DeleteFeedbackRecord(context.Context, uuid.UUID) error {
	return nil
}

func (m *mockFeedbackRecordsService) CountFeedbackRecords(
	ctx context.Context, filters *models.ListFeedbackRecordsFilters,
) (int, error) {
	if m.countFunc != nil {
		return m.countFunc(ctx, filters)
	}

	return 0, nil
}

func (m *mockFeedbackRecordsService) DeleteFeedbackRecordsByUser(
	ctx context.Context, filters *models.DeleteFeedbackRecordsByUserFilters,
) (int, error) {
	if m.deleteByUserFunc != nil {
		return m.deleteByUserFunc(ctx, filters)
	}

	return 0, nil
}

func TestFeedbackRecordsHandler_List(t *testing.T) {
	t.Run("missing tenant_id returns 400", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://test/v1/feedback-records", http.NoBody)
		rec := httptest.NewRecorder()

		handler.List(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("invalid since returns validation problem", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
			"http://test/v1/feedback-records?tenant_id=org-123&since=not-a-date", http.NoBody)
		rec := httptest.NewRecorder()

		handler.List(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)

		var problem response.ProblemDetails

		err := json.Unmarshal(rec.Body.Bytes(), &problem)
		require.NoError(t, err)
		assert.Equal(t, response.CodeValidation, problem.Code)
		require.Len(t, problem.InvalidParams, 1)
		assert.Equal(t, "since", problem.InvalidParams[0].Name)
		assert.Equal(t, "must be in RFC3339 (ISO 8601) format", problem.InvalidParams[0].Reason)
	})
}

func TestFeedbackRecordsHandler_Create(t *testing.T) {
	t.Run("success returns created record", func(t *testing.T) {
		recordID := uuid.Must(uuid.NewV7())
		mock := &mockFeedbackRecordsService{
			createFunc: func(_ context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
				assert.Equal(t, "org-123", req.TenantID)

				return &models.FeedbackRecord{
					ID:           recordID,
					SourceType:   req.SourceType,
					FieldID:      req.FieldID,
					FieldType:    req.FieldType,
					TenantID:     req.TenantID,
					SubmissionID: req.SubmissionID,
				}, nil
			},
		}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodPost, "http://test/v1/feedback-records", feedbackRecordCreateBody(t, "org-123"),
		)
		rec := httptest.NewRecorder()

		handler.Create(rec, req)

		assert.Equal(t, http.StatusCreated, rec.Code)

		var got models.FeedbackRecord

		err := json.Unmarshal(rec.Body.Bytes(), &got)
		require.NoError(t, err)
		assert.Equal(t, recordID, got.ID)
		assert.Equal(t, "org-123", got.TenantID)
	})

	t.Run("invalid field_type returns field-level problem details", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{}
		handler := NewFeedbackRecordsHandler(mock)

		body := []byte(`{
			"source_type": "survey",
			"field_id": "q1",
			"field_type": "textt",
			"tenant_id": "tenant-123",
			"submission_id": "submission-123"
		}`)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://test/v1/feedback-records", bytes.NewReader(body))
		rec := httptest.NewRecorder()

		handler.Create(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")

		var problem response.ProblemDetails

		err := json.Unmarshal(rec.Body.Bytes(), &problem)
		require.NoError(t, err)

		assert.Equal(t, response.ProblemTypeValidation, problem.Type)
		assert.NotEqual(t, "about:blank", problem.Type)
		assert.Equal(t, "Validation Error", problem.Title)
		assert.Equal(t, response.CodeValidation, problem.Code)
		require.Len(t, problem.InvalidParams, 1)
		assert.Equal(t, "field_type", problem.InvalidParams[0].Name)
		assert.Contains(t, problem.InvalidParams[0].Reason, "textt")
		assert.Contains(t, problem.InvalidParams[0].Reason, "text")
		assert.Contains(t, problem.InvalidParams[0].Reason, "date")
	})

	t.Run("service validation error returns bad request", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			createFunc: func(_ context.Context, _ *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
				return nil, huberrors.NewValidationError("tenant_id", "tenant_id is required and cannot be empty")
			},
		}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodPost, "http://test/v1/feedback-records", feedbackRecordCreateBody(t, "   "),
		)
		rec := httptest.NewRecorder()

		handler.Create(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")
	})

	t.Run("service conflict returns conflict", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			createFunc: func(_ context.Context, _ *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
				return nil, huberrors.NewConflictError("duplicate feedback record")
			},
		}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodPost, "http://test/v1/feedback-records", feedbackRecordCreateBody(t, "org-123"),
		)
		rec := httptest.NewRecorder()

		handler.Create(rec, req)

		assert.Equal(t, http.StatusConflict, rec.Code)
	})
}

func feedbackRecordCreateBody(t *testing.T, tenantID string) *bytes.Reader {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"source_type":   "formbricks",
		"submission_id": "submission-1",
		"tenant_id":     tenantID,
		"field_id":      "feedback",
		"field_type":    "text",
	})
	require.NoError(t, err)

	return bytes.NewReader(body)
}

func TestFeedbackRecordsHandler_DeleteByUser(t *testing.T) {
	t.Run("success returns 200 with deleted_count and message", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			deleteByUserFunc: func(_ context.Context, filters *models.DeleteFeedbackRecordsByUserFilters) (int, error) {
				assert.Equal(t, "user-123", filters.UserID)
				assert.Nil(t, filters.TenantID)

				return 3, nil
			},
		}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodDelete, "http://test/v1/feedback-records?user_id=user-123", http.NoBody)
		rec := httptest.NewRecorder()

		handler.DeleteByUser(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var resp models.DeleteFeedbackRecordsByUserResponse

		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Equal(t, int64(3), resp.DeletedCount)
		assert.Equal(t, "Successfully deleted 3 feedback records", resp.Message)
	})

	t.Run("optional tenant_id query parameter is passed to service", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			deleteByUserFunc: func(_ context.Context, filters *models.DeleteFeedbackRecordsByUserFilters) (int, error) {
				assert.Equal(t, "user-456", filters.UserID)
				require.NotNil(t, filters.TenantID)
				assert.Equal(t, "tenant-a", *filters.TenantID)

				return 1, nil
			},
		}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodDelete, "http://test/v1/feedback-records?user_id=user-456&tenant_id=tenant-a", http.NoBody)
		rec := httptest.NewRecorder()

		handler.DeleteByUser(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("empty tenant_id returns bad request", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodDelete, "http://test/v1/feedback-records?user_id=user-123&tenant_id=", http.NoBody)
		rec := httptest.NewRecorder()

		handler.DeleteByUser(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("overlength tenant_id returns bad request", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{}
		handler := NewFeedbackRecordsHandler(mock)
		tenantID := strings.Repeat("a", 256)

		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodDelete, "http://test/v1/feedback-records?user_id=user-123&tenant_id="+tenantID, http.NoBody)
		rec := httptest.NewRecorder()

		handler.DeleteByUser(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("missing user_id returns bad request", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "http://test/v1/feedback-records", http.NoBody)
		rec := httptest.NewRecorder()

		handler.DeleteByUser(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("empty user_id returns bad request", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodDelete, "http://test/v1/feedback-records?user_id=", http.NoBody)
		rec := httptest.NewRecorder()

		handler.DeleteByUser(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("overlength user_id returns bad request", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{}
		handler := NewFeedbackRecordsHandler(mock)
		userID := strings.Repeat("a", 256)

		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodDelete, "http://test/v1/feedback-records?user_id="+userID, http.NoBody)
		rec := httptest.NewRecorder()

		handler.DeleteByUser(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("service error returns 500", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			deleteByUserFunc: func(_ context.Context, _ *models.DeleteFeedbackRecordsByUserFilters) (int, error) {
				return 0, assert.AnError
			},
		}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodDelete, "http://test/v1/feedback-records?user_id=user-789", http.NoBody)
		rec := httptest.NewRecorder()

		handler.DeleteByUser(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("zero deleted returns 200 with deleted_count 0", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			deleteByUserFunc: func(_ context.Context, _ *models.DeleteFeedbackRecordsByUserFilters) (int, error) {
				return 0, nil
			},
		}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodDelete, "http://test/v1/feedback-records?user_id=nonexistent", http.NoBody)
		rec := httptest.NewRecorder()

		handler.DeleteByUser(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var resp models.DeleteFeedbackRecordsByUserResponse

		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Equal(t, int64(0), resp.DeletedCount)
		assert.Equal(t, "Successfully deleted 0 feedback records", resp.Message)
	})
}

// TestFeedbackRecordsHandler_BodyBounds locks the ingest cost bounds: an oversized body is
// rejected with 413 before being read into memory, and an over-long value_text with 400 — both
// before any service call, since every accepted value_text byte is later re-sent to the LLM and
// embedding providers by up to four enrichment pipelines.
func TestFeedbackRecordsHandler_BodyBounds(t *testing.T) {
	t.Run("oversized body returns 413", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{}
		handler := NewFeedbackRecordsHandler(mock)

		huge := strings.Repeat("a", maxFeedbackRecordBodyBytes+1024)
		body := []byte(`{"source_type":"survey","field_id":"q1","field_type":"text",` +
			`"tenant_id":"t","submission_id":"s","value_text":"` + huge + `"}`)

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodPost, "http://test/v1/feedback-records", bytes.NewReader(body))
		rec := httptest.NewRecorder()

		handler.Create(rec, req)

		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	})

	t.Run("over-long value_text returns 400", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{}
		handler := NewFeedbackRecordsHandler(mock)

		tooLong := strings.Repeat("a", 30001)
		body := []byte(`{"source_type":"survey","field_id":"q1","field_type":"text",` +
			`"tenant_id":"t","submission_id":"s","value_text":"` + tooLong + `"}`)

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodPost, "http://test/v1/feedback-records", bytes.NewReader(body))
		rec := httptest.NewRecorder()

		handler.Create(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("value_text at the cap is accepted", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			createFunc: func(_ context.Context, req *models.CreateFeedbackRecordRequest) (*models.FeedbackRecord, error) {
				return &models.FeedbackRecord{TenantID: req.TenantID}, nil
			},
		}
		handler := NewFeedbackRecordsHandler(mock)

		atCap := strings.Repeat("a", 30000)
		body := []byte(`{"source_type":"survey","field_id":"q1","field_type":"text",` +
			`"tenant_id":"t","submission_id":"s","value_text":"` + atCap + `"}`)

		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodPost, "http://test/v1/feedback-records", bytes.NewReader(body))
		rec := httptest.NewRecorder()

		handler.Create(rec, req)

		assert.Equal(t, http.StatusCreated, rec.Code)
	})

	t.Run("over-long optional string fields return 400", func(t *testing.T) {
		base := `"source_type":"survey","field_id":"q1","field_type":"text","tenant_id":"t","submission_id":"s"`
		over256 := strings.Repeat("a", 256)
		over2049 := strings.Repeat("a", 2049)

		cases := map[string]string{
			"source_id":         `"source_id":"` + over256 + `"`,
			"source_name":       `"source_name":"` + over256 + `"`,
			"user_id":           `"user_id":"` + over256 + `"`,
			"value_id":          `"value_id":"` + over256 + `"`,
			"field_label":       `"field_label":"` + over2049 + `"`,
			"field_group_label": `"field_group_label":"` + over2049 + `"`,
		}

		for field, fragment := range cases {
			t.Run(field, func(t *testing.T) {
				handler := NewFeedbackRecordsHandler(&mockFeedbackRecordsService{})
				body := []byte("{" + base + "," + fragment + "}")

				req := httptest.NewRequestWithContext(
					context.Background(), http.MethodPost, "http://test/v1/feedback-records", bytes.NewReader(body))
				rec := httptest.NewRecorder()

				handler.Create(rec, req)

				assert.Equal(t, http.StatusBadRequest, rec.Code, "%s over its cap must be rejected", field)
			})
		}
	})

	t.Run("update body is bounded too", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{}
		handler := NewFeedbackRecordsHandler(mock)

		huge := strings.Repeat("a", maxFeedbackRecordBodyBytes+1024)
		req := httptest.NewRequestWithContext(
			context.Background(), http.MethodPatch,
			"http://test/v1/feedback-records/"+uuid.Must(uuid.NewV7()).String(),
			bytes.NewReader([]byte(`{"value_text":"`+huge+`"}`)))
		req.SetPathValue("id", uuid.Must(uuid.NewV7()).String())

		rec := httptest.NewRecorder()

		handler.Update(rec, req)

		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	})
}

func TestFeedbackRecordsHandler_Count(t *testing.T) {
	t.Run("success returns count", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			countFunc: func(_ context.Context, filters *models.ListFeedbackRecordsFilters) (int, error) {
				assert.Equal(t, "org-123", *filters.TenantID)

				return 42, nil
			},
		}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodGet, "http://test/v1/feedback-records/count?tenant_id=org-123", http.NoBody)
		rec := httptest.NewRecorder()

		handler.Count(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var resp models.CountFeedbackRecordsResponse

		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Equal(t, int64(42), resp.Count)
	})

	t.Run("missing tenant_id returns 400", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://test/v1/feedback-records/count", http.NoBody)
		rec := httptest.NewRecorder()

		handler.Count(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("service error returns 500", func(t *testing.T) {
		mock := &mockFeedbackRecordsService{
			countFunc: func(_ context.Context, _ *models.ListFeedbackRecordsFilters) (int, error) {
				return 0, assert.AnError
			},
		}
		handler := NewFeedbackRecordsHandler(mock)

		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodGet, "http://test/v1/feedback-records/count?tenant_id=org-123", http.NoBody)
		rec := httptest.NewRecorder()

		handler.Count(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}
