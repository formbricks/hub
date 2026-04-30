package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestNewOpenAPIHandlerFailsForMissingSpec(t *testing.T) {
	_, err := NewOpenAPIHandler(filepath.Join(t.TempDir(), "missing-openapi.yaml"), "")
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestResolveOpenAPISpecPathFindsRepoLocalSpec(t *testing.T) {
	tempDir := t.TempDir()
	specPath := filepath.Join(tempDir, "openapi.yaml")
	err := os.WriteFile(specPath, []byte("openapi: 3.1.0\npaths: {}\n"), 0o600)
	require.NoError(t, err)

	t.Chdir(tempDir)

	assert.Equal(t, "openapi.yaml", ResolveOpenAPISpecPath())
}

func TestNewOpenAPIHandlerFailsForInvalidSpec(t *testing.T) {
	specPath := filepath.Join(t.TempDir(), "openapi.yaml")
	err := os.WriteFile(specPath, []byte(":\n - invalid"), 0o600)
	require.NoError(t, err)

	_, err = NewOpenAPIHandler(specPath, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal openapi spec")
}

func TestLoadOpenAPISpecFailsForEmptyDocument(t *testing.T) {
	specPath := filepath.Join(t.TempDir(), "openapi.yaml")
	err := os.WriteFile(specPath, []byte("{}\n"), 0o600)
	require.NoError(t, err)

	_, err = loadOpenAPISpec(specPath)
	require.Error(t, err)
	assert.ErrorIs(t, err, errOpenAPISpecUnavailable)
}

func TestOpenAPIHandlerYAMLUsesConfiguredPublicBaseURL(t *testing.T) {
	handler := newTestOpenAPIHandler(t, "https://hub.example.com/root")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal/openapi.yaml", nil)
	rec := httptest.NewRecorder()

	handler.YAML(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/yaml; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, cacheControlNoStore, rec.Header().Get("Cache-Control"))
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))

	var spec map[string]any

	err := yaml.Unmarshal(rec.Body.Bytes(), &spec)
	require.NoError(t, err)

	servers := mustServers(t, spec)
	assert.Equal(t, "https://hub.example.com/root", servers[0]["url"])
}

func TestOpenAPIHandlerYAMLUsesRequestHostWithoutForwardedHeaders(t *testing.T) {
	handler := newTestOpenAPIHandler(t, "")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal/openapi.yaml", nil)
	req.Host = "hub.dynamic.test"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "forwarded.example.com")

	rec := httptest.NewRecorder()

	handler.YAML(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var spec map[string]any

	err := yaml.Unmarshal(rec.Body.Bytes(), &spec)
	require.NoError(t, err)

	servers := mustServers(t, spec)
	assert.Equal(t, "http://hub.dynamic.test", servers[0]["url"])
}

func TestOpenAPIHandlerJSONUsesRequestHostWithoutForwardedHeaders(t *testing.T) {
	handler := newTestOpenAPIHandler(t, "")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal/openapi.json", nil)
	req.Host = "hub.acme.test"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "forwarded.example.com")

	rec := httptest.NewRecorder()

	handler.JSON(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, cacheControlNoStore, rec.Header().Get("Cache-Control"))

	var spec map[string]any

	err := json.Unmarshal(rec.Body.Bytes(), &spec)
	require.NoError(t, err)

	servers := mustServers(t, spec)
	assert.Equal(t, "http://hub.acme.test", servers[0]["url"])
}

func TestOpenAPIHandlerJSONUsesConfiguredPublicBaseURL(t *testing.T) {
	handler := newTestOpenAPIHandler(t, "https://hub.example.com")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal/openapi.json", nil)
	rec := httptest.NewRecorder()

	handler.JSON(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var spec map[string]any

	err := json.Unmarshal(rec.Body.Bytes(), &spec)
	require.NoError(t, err)

	servers := mustServers(t, spec)
	assert.Equal(t, "https://hub.example.com", servers[0]["url"])
}

func TestOpenAPIHandlerYAMLReturns500WhenRenderFails(t *testing.T) {
	handler := newTestOpenAPIHandler(t, "")
	handler.baseSpec["bad"] = make(chan int)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal/openapi.yaml", nil)
	rec := httptest.NewRecorder()

	handler.YAML(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestOpenAPIHandlerJSONReturns500WhenRenderFails(t *testing.T) {
	handler := newTestOpenAPIHandler(t, "")
	handler.baseSpec["bad"] = make(chan int)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal/openapi.json", nil)
	rec := httptest.NewRecorder()

	handler.JSON(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestOpenAPIHandlerJSONUsesHTTPSWhenTLSPresent(t *testing.T) {
	handler := newTestOpenAPIHandler(t, "")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "https://hub.secure.test/openapi.json", nil)
	rec := httptest.NewRecorder()

	handler.JSON(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var spec map[string]any

	err := json.Unmarshal(rec.Body.Bytes(), &spec)
	require.NoError(t, err)

	servers := mustServers(t, spec)
	assert.Equal(t, "https://hub.secure.test", servers[0]["url"])
}

func TestOpenAPIHandlerCachesConfiguredOutput(t *testing.T) {
	handler := newTestOpenAPIHandler(t, "https://hub.example.com")

	req1 := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal/openapi.json", nil)
	req2 := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://different/openapi.json", nil)
	rec1 := httptest.NewRecorder()
	rec2 := httptest.NewRecorder()

	handler.JSON(rec1, req1)
	handler.JSON(rec2, req2)

	assert.Equal(t, rec1.Body.Bytes(), rec2.Body.Bytes())
}

func TestMarshalJSONAddsTrailingNewline(t *testing.T) {
	body, err := marshalJSON(map[string]any{"openapi": "3.1.0"})
	require.NoError(t, err)
	assert.True(t, bytes.HasSuffix(body, []byte("\n")))
}

func TestMarshalJSONReturnsErrorForUnsupportedValue(t *testing.T) {
	_, err := marshalJSON(map[string]any{"bad": make(chan int)})
	require.Error(t, err)
}

func TestMarshalYAMLReturnsErrorForUnsupportedValue(t *testing.T) {
	_, err := marshalYAML(map[string]any{"bad": make(chan int)})
	require.Error(t, err)
}

func TestRequestBaseURLFallsBackToLocalHostWhenRequestHostMissing(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal/openapi.yaml", nil)
	req.Host = ""

	assert.Equal(t, localDevelopmentBaseURL, requestBaseURL(req))
}

func newTestOpenAPIHandler(t *testing.T, publicBaseURL string) *OpenAPIHandler {
	t.Helper()

	specPath := filepath.Join(t.TempDir(), "openapi.yaml")
	spec := []byte("openapi: 3.1.0\ninfo:\n  title: Test Hub API\n  version: 1.0.0\nservers:\n  - url: http://localhost:8080\npaths: {}\n")

	err := os.WriteFile(specPath, spec, 0o600)
	require.NoError(t, err)

	handler, err := NewOpenAPIHandler(specPath, publicBaseURL)
	require.NoError(t, err)

	return handler
}

func mustServers(t *testing.T, spec map[string]any) []map[string]any {
	t.Helper()

	rawServers, ok := spec["servers"].([]any)
	require.True(t, ok)
	require.Len(t, rawServers, 1)

	servers := make([]map[string]any, 0, len(rawServers))
	for _, raw := range rawServers {
		server, ok := raw.(map[string]any)
		require.True(t, ok)

		servers = append(servers, server)
	}

	return servers
}
