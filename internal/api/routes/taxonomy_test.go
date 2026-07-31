package routes

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type openAPIDocument struct {
	Paths map[string]map[string]openAPIOperation `yaml:"paths"`
}

type openAPIOperation struct {
	OperationID string         `yaml:"operationId"`
	Responses   map[string]any `yaml:"responses"`
}

func TestPublicTaxonomyRoutesMatchOpenAPI(t *testing.T) {
	t.Parallel()

	document := loadOpenAPI(t)
	registered := make(map[string]publicTaxonomyRoute, len(publicTaxonomyRoutes))

	for _, route := range publicTaxonomyRoutes {
		key := strings.ToLower(route.method) + " " + route.path
		registered[key] = route

		path, ok := document.Paths[route.path]
		require.Truef(t, ok, "OpenAPI is missing taxonomy path %s", route.path)

		operation, ok := path[strings.ToLower(route.method)]
		require.Truef(t, ok, "OpenAPI is missing %s %s", route.method, route.path)
		assert.Equal(t, route.operationID, operation.OperationID)

		actualStatuses := make([]string, 0, len(operation.Responses))
		for status := range operation.Responses {
			actualStatuses = append(actualStatuses, status)
		}

		slices.Sort(actualStatuses)

		expectedStatuses := slices.Clone(route.responseStatuses)
		slices.Sort(expectedStatuses)
		assert.Equal(t, expectedStatuses, actualStatuses, "%s %s response statuses", route.method, route.path)
	}

	openAPIRoutes := make(map[string]struct{})

	for path, pathItem := range document.Paths {
		if !strings.HasPrefix(path, "/v1/taxonomy") {
			continue
		}

		for method := range pathItem {
			upperMethod := strings.ToUpper(method)
			if !isHTTPMethod(upperMethod) {
				continue
			}

			key := method + " " + path
			openAPIRoutes[key] = struct{}{}
			_, ok := registered[key]
			assert.Truef(t, ok, "OpenAPI taxonomy operation is not registered by Hub: %s %s", upperMethod, path)
		}
	}

	assert.Len(t, openAPIRoutes, len(publicTaxonomyRoutes))
}

func TestInternalTaxonomyRoutesStayOutOfPublicOpenAPI(t *testing.T) {
	t.Parallel()

	document := loadOpenAPI(t)

	for _, route := range internalTaxonomyRoutes {
		_, ok := document.Paths[route.path]
		assert.Falsef(t, ok, "internal route must not be published in OpenAPI: %s %s", route.method, route.path)
	}
}

func loadOpenAPI(t *testing.T) openAPIDocument {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)

	specPath := filepath.Join(filepath.Dir(filename), "..", "..", "..", "openapi.yaml")
	// #nosec G304 -- the repository-local spec path is derived from this test file's location.
	contents, err := os.ReadFile(specPath)
	require.NoError(t, err)

	var document openAPIDocument
	require.NoError(t, yaml.Unmarshal(contents, &document))

	return document
}

func isHTTPMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
