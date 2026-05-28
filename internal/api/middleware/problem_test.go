package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/api/response"
)

func TestProblemErrorsRewritesStdlibNotFound(t *testing.T) {
	handler := ProblemErrors(http.NotFoundHandler())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/missing", http.NoBody))

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))

	var problem response.ProblemDetails

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &problem))
	assert.Equal(t, response.CodeNotFound, problem.Code)
	assert.Equal(t, "/v1/missing", problem.Instance)
}

func TestProblemErrorsRewritesStdlibMethodNotAllowed(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Allow", "GET, DELETE")
		http.Error(w, "405 method not allowed", http.StatusMethodNotAllowed)
	})
	handler := ProblemErrors(inner)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/feedback-records/1", http.NoBody))

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "GET, DELETE", rec.Header().Get("Allow"))

	var problem response.ProblemDetails

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &problem))
	assert.Equal(t, response.CodeMethodNotAllowed, problem.Code)
}

func TestProblemErrorsPassesThroughExistingProblemJSON(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response.RespondNotFound(w, r, "feedback record not found")
	})
	handler := ProblemErrors(inner)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/feedback-records/1", http.NoBody))

	assert.Equal(t, http.StatusNotFound, rec.Code)

	var problem response.ProblemDetails

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &problem))
	// The handler's own detail is preserved, not replaced by the middleware's generic message.
	assert.Equal(t, "feedback record not found", problem.Detail)
}

func TestProblemErrorsPassesThroughSuccess(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	handler := ProblemErrors(inner)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/x", http.NoBody))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"ok":true}`, rec.Body.String())
}
