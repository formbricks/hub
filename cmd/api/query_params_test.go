package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/formbricks/hub/internal/api/response"
	"github.com/formbricks/hub/internal/api/validation"
)

// specParameter is the part of an OpenAPI parameter this test reads.
type specParameter struct {
	Ref    string `yaml:"$ref"`
	Name   string `yaml:"name"`
	In     string `yaml:"in"`
	Schema struct {
		Type   string `yaml:"type"`
		Format string `yaml:"format"`
		Enum   []any  `yaml:"enum"`
		Items  struct {
			Enum []any `yaml:"enum"`
		} `yaml:"items"`
	} `yaml:"schema"`
}

type specOperation struct {
	Parameters []specParameter `yaml:"parameters"`
}

type specDocument struct {
	Paths      map[string]map[string]yaml.Node `yaml:"paths"`
	Components struct {
		Parameters map[string]specParameter `yaml:"parameters"`
	} `yaml:"components"`
}

// TestQueryParamsMatchOpenAPI walks every operation openapi.yaml gives query parameters, against
// the real router. Each documented parameter must get past parameter validation, and an
// undocumented one must be a 400 naming it. The handlers that read their parameters by hand keep
// their own allowlists, so this is what keeps those lists and the spec from drifting apart.
//
// The router runs with nil services: a request that passes validation reaches a nil service and
// panics or answers 503, which here means only "validation let it through".
func TestQueryParamsMatchOpenAPI(t *testing.T) {
	server := newTestHTTPServer(t)
	document := loadSpec(t)

	const unknownParam = "zz_not_a_parameter"

	checked := 0

	for path, pathItem := range document.Paths {
		var common []specParameter
		if node, ok := pathItem["parameters"]; ok {
			if err := node.Decode(&common); err != nil {
				t.Fatalf("decode %s parameters: %v", path, err)
			}
		}

		for method, node := range pathItem {
			if !slices.Contains([]string{"get", "post", "put", "patch", "delete"}, method) {
				continue
			}

			var operation specOperation
			if err := node.Decode(&operation); err != nil {
				t.Fatalf("decode %s %s: %v", method, path, err)
			}

			params := resolveParams(t, document, append(slices.Clone(common), operation.Parameters...))

			query := url.Values{}
			target := path

			for _, param := range params {
				switch param.In {
				case "path":
					target = strings.ReplaceAll(target, "{"+param.Name+"}", "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b")
				case "query":
					query.Set(param.Name, sampleValue(param))
				}
			}

			if len(query) == 0 {
				continue
			}

			checked++
			name := strings.ToUpper(method) + " " + path

			t.Run(name+" accepts every documented parameter", func(t *testing.T) {
				status, problem := serve(t, server.Handler, strings.ToUpper(method), target, query)
				if status == http.StatusBadRequest {
					for _, param := range problem.InvalidParams {
						if param.Reason == validation.ReasonUnknownQueryParam {
							t.Errorf("documented parameter %q rejected as unknown", param.Name)
						}
					}
				}
			})

			t.Run(name+" rejects an undocumented parameter", func(t *testing.T) {
				withUnknown := maps.Clone(query)

				withUnknown.Set(unknownParam, "1")

				status, problem := serve(t, server.Handler, strings.ToUpper(method), target, withUnknown)
				if status != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400", status)
				}

				if !slices.ContainsFunc(problem.InvalidParams, func(param response.InvalidParam) bool {
					return param.Name == unknownParam && param.Reason == validation.ReasonUnknownQueryParam
				}) {
					t.Errorf("invalid_params = %+v, want %q named as unknown", problem.InvalidParams, unknownParam)
				}
			})
		}
	}

	// A spec that stopped parsing, or lost its query parameters, must not pass by checking nothing.
	if checked < 16 {
		t.Fatalf("checked %d operations with query parameters, want at least 16", checked)
	}
}

// serve runs one authenticated request, treating a panic as "past validation" (see the test).
func serve(t *testing.T, handler http.Handler, method, target string, query url.Values) (int, response.ProblemDetails) {
	t.Helper()

	var body *strings.Reader
	if method == http.MethodPost {
		body = strings.NewReader(`{"query":"q","tenant_id":"t"}`)
	} else {
		body = strings.NewReader("")
	}

	request := httptest.NewRequestWithContext(context.Background(), method, target+"?"+query.Encode(), body)
	request.Header.Set("Authorization", "Bearer test-api-key")
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()

	panicked := func() (panicked bool) {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()

		handler.ServeHTTP(recorder, request)

		return false
	}()
	if panicked {
		return http.StatusServiceUnavailable, response.ProblemDetails{}
	}

	var problem response.ProblemDetails
	if recorder.Code >= http.StatusBadRequest && strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/problem+json") {
		if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
			t.Fatalf("decode problem: %v", err)
		}
	}

	return recorder.Code, problem
}

// sampleValue picks a value the parameter's schema accepts, so a rejection can only come from the
// parameter's name. Values may still fail other validation (an unknown cursor, a scope mismatch);
// the test only looks for the unknown-parameter reason.
func sampleValue(param specParameter) string {
	switch {
	case len(param.Schema.Enum) > 0:
		return toString(param.Schema.Enum[0])
	case len(param.Schema.Items.Enum) > 0:
		return toString(param.Schema.Items.Enum[0])
	case param.Name == "order":
		return "asc"
	case param.Schema.Format == "date-time":
		return "2026-01-01T00:00:00Z"
	case param.Schema.Type == "integer":
		return "1"
	case param.Schema.Type == "number":
		return "0.5"
	case param.Schema.Type == "boolean":
		return "true"
	default:
		return "x"
	}
}

func toString(value any) string {
	return fmt.Sprint(value)
}

func resolveParams(t *testing.T, document specDocument, params []specParameter) []specParameter {
	t.Helper()

	resolved := make([]specParameter, 0, len(params))

	for _, param := range params {
		if param.Ref != "" {
			component, ok := document.Components.Parameters[strings.TrimPrefix(param.Ref, "#/components/parameters/")]
			if !ok {
				t.Fatalf("unresolved parameter %s", param.Ref)
			}

			param = component
		}

		resolved = append(resolved, param)
	}

	return resolved
}

func loadSpec(t *testing.T) specDocument {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}

	// #nosec G304 -- the repository-local spec path is derived from this test file's location.
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "..", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}

	var document specDocument
	if err := yaml.Unmarshal(contents, &document); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}

	return document
}
