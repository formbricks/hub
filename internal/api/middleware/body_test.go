package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaxBodyBytesRejectsOversizedBody(t *testing.T) {
	var readErr error

	handler := MaxBodyBytes(8)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/x", strings.NewReader("0123456789abcdef"))
	handler.ServeHTTP(rec, req)

	var maxErr *http.MaxBytesError
	require.ErrorAs(t, readErr, &maxErr)
}

func TestMaxBodyBytesAllowsBodyWithinLimit(t *testing.T) {
	var (
		body    []byte
		readErr error
	)

	handler := MaxBodyBytes(1024)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body, readErr = io.ReadAll(r.Body)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/x", strings.NewReader("hello"))
	handler.ServeHTTP(rec, req)

	require.NoError(t, readErr)
	assert.Equal(t, "hello", string(body))
}
