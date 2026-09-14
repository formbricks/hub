// Package jsonutil contains small helpers for comparing and reading JSON payloads.
package jsonutil

import (
	"bytes"
	"encoding/json"
	"slices"
)

// ObjectString returns a string property from a JSON object, or an empty string
// when the payload is invalid, is not an object, or the property is not a string.
func ObjectString(raw json.RawMessage, key string) string {
	values := make(map[string]any)
	if err := json.Unmarshal(normalizeEmptyObject(raw), &values); err != nil {
		return ""
	}

	value, _ := values[key].(string)

	return value
}

// CanonicalEqual compares JSON values independent of object-key ordering. Empty
// and null payloads are normalized to an empty object because JSONB object
// columns persist an omitted metrics payload as {}.
func CanonicalEqual(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	if json.Unmarshal(normalizeEmptyObject(left), &leftValue) != nil ||
		json.Unmarshal(normalizeEmptyObject(right), &rightValue) != nil {
		return false
	}

	leftRaw, leftErr := json.Marshal(leftValue)
	rightRaw, rightErr := json.Marshal(rightValue)

	return leftErr == nil && rightErr == nil && slices.Equal(leftRaw, rightRaw)
}

func normalizeEmptyObject(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return json.RawMessage(`{}`)
	}

	return raw
}
