package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	localDevelopmentBaseURL = "http://localhost:8080"
	cacheControlNoStore     = "no-store"
)

var (
	errOpenAPISpecUnavailable = errors.New("openapi spec is unavailable")
	errMarshalYAMLPanic       = errors.New("panic marshalling yaml")
)

// OpenAPIHandler serves the Hub OpenAPI document in YAML or JSON form.
type OpenAPIHandler struct {
	baseSpec          map[string]any
	publicBaseURL     string
	cachedJSON        []byte
	cachedYAML        []byte
	usesConfiguredURL bool
}

// NewOpenAPIHandler loads and validates the OpenAPI spec at startup.
func NewOpenAPIHandler(specPath, publicBaseURL string) (*OpenAPIHandler, error) {
	spec, err := loadOpenAPISpec(specPath)
	if err != nil {
		return nil, err
	}

	handler := &OpenAPIHandler{
		baseSpec:          spec,
		publicBaseURL:     strings.TrimRight(publicBaseURL, "/"),
		usesConfiguredURL: strings.TrimSpace(publicBaseURL) != "",
	}

	if handler.usesConfiguredURL {
		handler.cachedYAML, err = marshalYAML(handler.specForBaseURL(handler.publicBaseURL))
		if err != nil {
			return nil, fmt.Errorf("marshal cached openapi yaml: %w", err)
		}

		handler.cachedJSON, err = marshalJSON(handler.specForBaseURL(handler.publicBaseURL))
		if err != nil {
			return nil, fmt.Errorf("marshal cached openapi json: %w", err)
		}
	}

	return handler, nil
}

// ResolveOpenAPISpecPath finds the OpenAPI spec in common repo and runtime locations.
func ResolveOpenAPISpecPath() string {
	candidates := []string{"openapi.yaml"}

	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			filepath.Join(exeDir, "openapi.yaml"),
			filepath.Join(exeDir, "..", "openapi.yaml"),
		)
	}

	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate
		}
	}

	return ""
}

// YAML serves the runtime OpenAPI document as YAML.
func (h *OpenAPIHandler) YAML(w http.ResponseWriter, r *http.Request) {
	if h.usesConfiguredURL {
		h.writeHeaders(w, "application/yaml; charset=utf-8")

		if _, err := w.Write(h.cachedYAML); err != nil {
			slog.Error("failed to write openapi yaml response", "error", err)
		}

		return
	}

	rendered, err := marshalYAML(h.specForBaseURL(requestBaseURL(r)))
	if err != nil {
		slog.Error("failed to marshal openapi yaml", "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)

		return
	}

	h.writeHeaders(w, "application/yaml; charset=utf-8")

	if _, err := w.Write(rendered); err != nil {
		slog.Error("failed to write openapi yaml response", "error", err)
	}
}

// JSON serves the runtime OpenAPI document as JSON.
func (h *OpenAPIHandler) JSON(w http.ResponseWriter, r *http.Request) {
	if h.usesConfiguredURL {
		h.writeHeaders(w, "application/json; charset=utf-8")

		if _, err := w.Write(h.cachedJSON); err != nil {
			slog.Error("failed to write openapi json response", "error", err)
		}

		return
	}

	rendered, err := marshalJSON(h.specForBaseURL(requestBaseURL(r)))
	if err != nil {
		slog.Error("failed to marshal openapi json", "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)

		return
	}

	h.writeHeaders(w, "application/json; charset=utf-8")

	if _, err := w.Write(rendered); err != nil {
		slog.Error("failed to write openapi json response", "error", err)
	}
}

func (h *OpenAPIHandler) specForBaseURL(baseURL string) map[string]any {
	spec := maps.Clone(h.baseSpec)
	spec["servers"] = []map[string]string{
		{
			"url":         baseURL,
			"description": "Current Hub instance",
		},
	}

	return spec
}

func (h *OpenAPIHandler) writeHeaders(w http.ResponseWriter, contentType string) {
	w.Header().Set("Cache-Control", cacheControlNoStore)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
}

func loadOpenAPISpec(specPath string) (map[string]any, error) {
	if specPath == "" {
		return nil, errOpenAPISpecUnavailable
	}

	//nolint:gosec // specPath is process configuration resolved at startup, never request input.
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("read openapi spec: %w", err)
	}

	var spec map[string]any

	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("unmarshal openapi spec: %w", err)
	}

	if len(spec) == 0 {
		return nil, fmt.Errorf("%w: empty document", errOpenAPISpecUnavailable)
	}

	return spec, nil
}

func marshalJSON(spec map[string]any) ([]byte, error) {
	body, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal openapi json: %w", err)
	}

	return append(body, '\n'), nil
}

func marshalYAML(spec map[string]any) (body []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			panicErr, ok := recovered.(error)
			if ok {
				err = panicErr
			} else {
				err = fmt.Errorf("%w: %v", errMarshalYAMLPanic, recovered)
			}
		}
	}()

	body, err = yaml.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal openapi yaml: %w", err)
	}

	return body, nil
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	host := r.Host
	if host == "" {
		host = strings.TrimPrefix(localDevelopmentBaseURL, "http://")
	}

	return fmt.Sprintf("%s://%s", scheme, host)
}
