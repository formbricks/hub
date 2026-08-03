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
	Paths map[string]map[string]yaml.Node `yaml:"paths"`
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

		operation, ok := decodeOpenAPIOperation(t, path, strings.ToLower(route.method))
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

func TestOpenAPIPathItemMetadataIsIgnored(t *testing.T) {
	t.Parallel()

	document := decodeOpenAPI(t, []byte(`
paths:
  /v1/taxonomy/fields:
    summary: Taxonomy field discovery
    parameters:
      - name: tenant_id
        in: query
    get:
      operationId: list-taxonomy-fields
      responses:
        "200": {}
`))

	path, ok := document.Paths["/v1/taxonomy/fields"]
	require.True(t, ok)

	operation, ok := decodeOpenAPIOperation(t, path, "get")
	require.True(t, ok)
	assert.Equal(t, "list-taxonomy-fields", operation.OperationID)
	assert.Contains(t, operation.Responses, "200")
}

func loadOpenAPI(t *testing.T) openAPIDocument {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)

	specPath := filepath.Join(filepath.Dir(filename), "..", "..", "..", "openapi.yaml")
	// #nosec G304 -- the repository-local spec path is derived from this test file's location.
	contents, err := os.ReadFile(specPath)
	require.NoError(t, err)

	return decodeOpenAPI(t, contents)
}

func decodeOpenAPI(t *testing.T, contents []byte) openAPIDocument {
	t.Helper()

	var document openAPIDocument
	require.NoError(t, yaml.Unmarshal(contents, &document))

	return document
}

func decodeOpenAPIOperation(
	t *testing.T,
	pathItem map[string]yaml.Node,
	method string,
) (openAPIOperation, bool) {
	t.Helper()

	node, ok := pathItem[method]
	if !ok {
		return openAPIOperation{}, false
	}

	var operation openAPIOperation
	require.NoErrorf(t, node.Decode(&operation), "decode OpenAPI %s operation", strings.ToUpper(method))

	return operation, true
}

func isHTTPMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
