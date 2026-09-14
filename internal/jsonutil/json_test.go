package jsonutil

import (
	"encoding/json"
	"testing"
)

func TestCanonicalEqual(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		left, right json.RawMessage
		want        bool
	}{
		{name: "key order", left: json.RawMessage(`{"a":1,"b":2}`), right: json.RawMessage(`{"b":2,"a":1}`), want: true},
		{name: "empty and object", left: nil, right: json.RawMessage(`{}`), want: true},
		{name: "null and object", left: json.RawMessage(`null`), right: json.RawMessage(`{}`), want: true},
		{name: "different values", left: json.RawMessage(`{"a":1}`), right: json.RawMessage(`{"a":2}`), want: false},
		{name: "invalid value", left: json.RawMessage(`{`), right: json.RawMessage(`{}`), want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := CanonicalEqual(test.left, test.right); got != test.want {
				t.Fatalf("CanonicalEqual(%q, %q) = %v, want %v", test.left, test.right, got, test.want)
			}
		})
	}
}

func TestObjectString(t *testing.T) {
	t.Parallel()

	if got := ObjectString(json.RawMessage(`{"result_digest":"sha256:abc"}`), "result_digest"); got != "sha256:abc" {
		t.Fatalf("ObjectString() = %q, want %q", got, "sha256:abc")
	}

	for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`{`), json.RawMessage(`{"result_digest":1}`)} {
		if got := ObjectString(raw, "result_digest"); got != "" {
			t.Fatalf("ObjectString(%q) = %q, want empty", raw, got)
		}
	}
}
