// Package llmtest provides shared test helpers for the LLM client wrappers
// (internal/openai, internal/googleai), which both assert on the JSON request
// bodies they send to their provider SDKs. It is imported only from _test.go
// files, so it stays out of the production import graph.
package llmtest

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// MustMap asserts v is a JSON object and returns it, failing the test (naming v
// in the message) otherwise. Use it to navigate decoded request/response bodies.
func MustMap(t *testing.T, v any, name string) map[string]any {
	t.Helper()

	asMap, isMap := v.(map[string]any)
	require.True(t, isMap, "%s must be a JSON object", name)

	return asMap
}
