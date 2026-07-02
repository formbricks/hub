package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/llm"
	"github.com/formbricks/hub/internal/llm/llmtest"
)

type embeddingRequest struct {
	Input      string `json:"input"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
}

func newEmbeddingServer(t *testing.T, embedding []float64) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var hitCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount.Add(1)
		assert.Equal(t, "/v1/embeddings", r.URL.Path)
		assert.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))

		var req embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request body: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)

			return
		}

		assert.Equal(t, "hello world", req.Input)
		assert.Equal(t, "test-model", req.Model)
		assert.Equal(t, 2, req.Dimensions)

		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"model":  req.Model,
			"data": []map[string]any{
				{
					"object":    "embedding",
					"index":     0,
					"embedding": embedding,
				},
			},
			"usage": map[string]any{
				"prompt_tokens": 1,
				"total_tokens":  1,
			},
		}); err != nil {
			t.Errorf("encode response body: %v", err)
		}
	}))

	t.Cleanup(server.Close)

	return server, &hitCount
}

func TestCreateEmbedding_UsesExplicitBaseURLOverEnvironment(t *testing.T) {
	envServer, envHits := newEmbeddingServer(t, []float64{9, 9})
	explicitServer, explicitHits := newEmbeddingServer(t, []float64{1, 2})

	t.Setenv("OPENAI_BASE_URL", envServer.URL+"/v1")

	client := NewClient("sk-test",
		WithBaseURL(explicitServer.URL+"/v1"),
		WithDimensions(2),
		WithModel("test-model"),
	)

	embedding, err := client.CreateEmbedding(context.Background(), "hello world")
	require.NoError(t, err)
	assert.Equal(t, []float32{1, 2}, embedding)
	assert.Equal(t, int32(0), envHits.Load())
	assert.Equal(t, int32(1), explicitHits.Load())
}

func TestCreateEmbedding_UsesEnvironmentBaseURLWhenExplicitBaseURLIsUnset(t *testing.T) {
	envServer, envHits := newEmbeddingServer(t, []float64{3, 4})

	t.Setenv("OPENAI_BASE_URL", envServer.URL+"/v1")

	client := NewClient("sk-test",
		WithDimensions(2),
		WithModel("test-model"),
	)

	embedding, err := client.CreateEmbedding(context.Background(), "hello world")
	require.NoError(t, err)
	assert.Equal(t, []float32{3, 4}, embedding)
	assert.Equal(t, int32(1), envHits.Load())
}

// newChatCompletionServer drives the real SDK against a stub /v1/chat/completions endpoint so
// the translation error paths exercise the SDK's own response decoding, not a hand-built error.
func newChatCompletionServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	return server
}

func TestTranslate_RateLimitReturnsRateLimitError(t *testing.T) {
	// No x-should-retry masking: the client disables the SDK's internal retries
	// (WithMaxRetries(0)), so a real 429 must surface immediately from a single call.
	var calls atomic.Int32

	server := newChatCompletionServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "12")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	client := NewClient("sk-test", WithBaseURL(server.URL+"/v1"), WithModel("test-model"))

	_, err := client.Translate(context.Background(), "system prompt", "hello")
	require.Error(t, err)

	var rateLimited *huberrors.RateLimitError
	require.ErrorAs(t, err, &rateLimited)
	assert.Equal(t, 12*time.Second, rateLimited.RetryAfter, "the Retry-After header is honored")
	assert.Equal(t, int32(1), calls.Load(),
		"the SDK must not retry internally — River and the snooze own retry policy")
}

func TestTranslate_RateLimitWithoutRetryAfterHeader(t *testing.T) {
	server := newChatCompletionServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})

	client := NewClient("sk-test", WithBaseURL(server.URL+"/v1"), WithModel("test-model"))

	_, err := client.Translate(context.Background(), "system prompt", "hello")

	var rateLimited *huberrors.RateLimitError
	require.ErrorAs(t, err, &rateLimited)
	assert.Equal(t, time.Duration(0), rateLimited.RetryAfter, "no header means no hint, not a parse error")
}

func TestTranslate_RateLimitRetryAfterVariants(t *testing.T) {
	tests := map[string]struct {
		header string // header name
		value  func() string
		check  func(t *testing.T, got time.Duration)
	}{
		"fractional seconds": {
			header: "Retry-After",
			value:  func() string { return "1.5" },
			check: func(t *testing.T, got time.Duration) {
				t.Helper()
				assert.Equal(t, 1500*time.Millisecond, got)
			},
		},
		"http-date": {
			header: "Retry-After",
			value:  func() string { return time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat) },
			check: func(t *testing.T, got time.Duration) {
				t.Helper()
				assert.InDelta(t, float64(90*time.Second), float64(got), float64(5*time.Second),
					"an HTTP-date Retry-After is converted to a wait duration")
			},
		},
		"retry-after-ms": {
			header: "Retry-After-Ms",
			value:  func() string { return "250" },
			check: func(t *testing.T, got time.Duration) {
				t.Helper()
				assert.Equal(t, 250*time.Millisecond, got)
			},
		},
		"negative seconds ignored": {
			header: "Retry-After",
			value:  func() string { return "-3" },
			check: func(t *testing.T, got time.Duration) {
				t.Helper()
				assert.Equal(t, time.Duration(0), got)
			},
		},
		"past http-date ignored": {
			header: "Retry-After",
			value:  func() string { return time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat) },
			check: func(t *testing.T, got time.Duration) {
				t.Helper()
				assert.Equal(t, time.Duration(0), got)
			},
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			server := newChatCompletionServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(testCase.header, testCase.value())
				w.WriteHeader(http.StatusTooManyRequests)
			})

			client := NewClient("sk-test", WithBaseURL(server.URL+"/v1"), WithModel("test-model"))

			_, err := client.Translate(context.Background(), "system prompt", "hello")

			var rateLimited *huberrors.RateLimitError
			require.ErrorAs(t, err, &rateLimited)
			testCase.check(t, rateLimited.RetryAfter)
		})
	}
}

func TestTranslate_NonRateLimitErrorIsNotWrapped(t *testing.T) {
	server := newChatCompletionServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

	client := NewClient("sk-test", WithBaseURL(server.URL+"/v1"), WithModel("test-model"))

	_, err := client.Translate(context.Background(), "system prompt", "hello")
	require.Error(t, err)

	var rateLimited *huberrors.RateLimitError
	assert.NotErrorAs(t, err, &rateLimited, "a non-429 error must not be classified as rate-limited")
}

func TestTranslate_Success(t *testing.T) {
	server := newChatCompletionServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-test",
			"object": "chat.completion",
			"model":  "test-model",
			"choices": []map[string]any{{
				"index":         0,
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": "  Hola mundo  "},
			}},
		}); err != nil {
			t.Errorf("encode response body: %v", err)
		}
	})

	client := NewClient("sk-test", WithBaseURL(server.URL+"/v1"), WithModel("test-model"))

	out, err := client.Translate(context.Background(), "system prompt", "hello")
	require.NoError(t, err)
	assert.Equal(t, "Hola mundo", out, "the assistant message is returned trimmed")
}

var sentimentTestSchema = llm.Schema{
	Name: "sentiment",
	Properties: []llm.Property{
		{Name: "label", Type: llm.TypeString, Description: "polarity", Enum: []string{"negative", "neutral", "positive"}},
		{Name: "score", Type: llm.TypeNumber, Description: "polarity score"},
	},
}

func TestCompleteJSON_SendsStrictSchemaAndReturnsJSON(t *testing.T) {
	var body map[string]any

	server := newChatCompletionServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-test",
			"object": "chat.completion",
			"model":  "test-model",
			"choices": []map[string]any{{
				"index":         0,
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": `  {"label":"positive","score":1.5}  `},
			}},
		}); err != nil {
			t.Errorf("encode response body: %v", err)
		}
	})

	client := NewClient("sk-test", WithBaseURL(server.URL+"/v1"), WithModel("test-model"))

	out, err := client.CompleteJSON(context.Background(), "classify", "great product", sentimentTestSchema)
	require.NoError(t, err)
	assert.JSONEq(t, `{"label":"positive","score":1.5}`, out, "the JSON content is returned trimmed")

	// The request carries response_format: a strict json_schema named after the schema.
	responseFormat := llmtest.MustMap(t, body["response_format"], "response_format")
	assert.Equal(t, "json_schema", responseFormat["type"])

	jsonSchema := llmtest.MustMap(t, responseFormat["json_schema"], "json_schema")
	assert.Equal(t, "sentiment", jsonSchema["name"])
	assert.Equal(t, true, jsonSchema["strict"])

	schema := llmtest.MustMap(t, jsonSchema["schema"], "schema")
	assert.Equal(t, "object", schema["type"])
	assert.Equal(t, false, schema["additionalProperties"], "strict mode requires a closed object")
	assert.ElementsMatch(t, []any{"label", "score"}, schema["required"], "every property is required")
}

func TestCompleteJSON_RateLimitReturnsRateLimitError(t *testing.T) {
	server := newChatCompletionServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	client := NewClient("sk-test", WithBaseURL(server.URL+"/v1"), WithModel("test-model"))

	_, err := client.CompleteJSON(context.Background(), "classify", "hello", sentimentTestSchema)

	var rateLimited *huberrors.RateLimitError
	require.ErrorAs(t, err, &rateLimited, "a 429 must surface as a rate-limit error so the worker can snooze")
	assert.Equal(t, 7*time.Second, rateLimited.RetryAfter)
}

func TestCompleteJSON_EmptyInputReturnsErrEmptyInput(t *testing.T) {
	client := NewClient("sk-test", WithModel("test-model"))

	_, err := client.CompleteJSON(context.Background(), "classify", "   ", sentimentTestSchema)
	require.ErrorIs(t, err, ErrEmptyInput)
}

func TestCompleteJSON_RefusalAndFinishReasonAreSurfaced(t *testing.T) {
	tests := map[string]struct {
		message      map[string]any
		finishReason string
		wantInError  string
	}{
		"refusal": {
			message:      map[string]any{"role": "assistant", "content": "", "refusal": "I can't help with that."},
			finishReason: "stop",
			wantInError:  "model refused: I can't help with that.",
		},
		"content filter": {
			message:      map[string]any{"role": "assistant", "content": ""},
			finishReason: "content_filter",
			wantInError:  `finish reason "content_filter"`,
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			server := newChatCompletionServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")

				if err := json.NewEncoder(w).Encode(map[string]any{
					"id":     "chatcmpl-test",
					"object": "chat.completion",
					"model":  "test-model",
					"choices": []map[string]any{{
						"index":         0,
						"finish_reason": testCase.finishReason,
						"message":       testCase.message,
					}},
				}); err != nil {
					t.Errorf("encode response body: %v", err)
				}
			})

			client := NewClient("sk-test", WithBaseURL(server.URL+"/v1"), WithModel("test-model"))

			_, err := client.CompleteJSON(context.Background(), "classify", "hello", sentimentTestSchema)
			require.ErrorIs(t, err, ErrNoCompletionInResponse)
			assert.ErrorContains(t, err, testCase.wantInError,
				"the block/refusal reason must be diagnosable in logs")
		})
	}
}

// TestCompleteJSON_TemperatureFallbackForReasoningModels: a model that rejects the temperature
// parameter (invalid_request_error, param "temperature" — reasoning-model behavior) triggers one
// retry without it, and the client latches so later calls omit it up front.
func TestCompleteJSON_TemperatureFallbackForReasoningModels(t *testing.T) {
	var bodies []map[string]any

	server := newChatCompletionServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any

		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		bodies = append(bodies, body)

		if _, hasTemperature := body["temperature"]; hasTemperature {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{` +
				`"message":"Unsupported parameter: 'temperature' is not supported with this model.",` +
				`"type":"invalid_request_error","param":"temperature","code":"unsupported_parameter"}}`))

			return
		}

		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-test",
			"object": "chat.completion",
			"model":  "test-model",
			"choices": []map[string]any{{
				"index":         0,
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": `{"label":"neutral","score":0}`},
			}},
		}); err != nil {
			t.Errorf("encode response body: %v", err)
		}
	})

	client := NewClient("sk-test", WithBaseURL(server.URL+"/v1"), WithModel("o-test"))

	// First call: temperature rejected -> retried once without it, succeeding.
	out, err := client.CompleteJSON(context.Background(), "classify", "hello", sentimentTestSchema)
	require.NoError(t, err)
	assert.JSONEq(t, `{"label":"neutral","score":0}`, out)

	// Second call: the latch skips temperature up front (no wasted 400).
	_, err = client.CompleteJSON(context.Background(), "classify", "hello again", sentimentTestSchema)
	require.NoError(t, err)

	require.Len(t, bodies, 3, "reject + fallback retry + latched second call")
	assert.Contains(t, bodies[0], "temperature", "first attempt sends temperature 0")
	assert.NotContains(t, bodies[1], "temperature", "the fallback retry omits it")
	assert.NotContains(t, bodies[2], "temperature", "later calls skip it up front")
}
