package models

import (
	"encoding/json"
	"testing"
)

// The event-types error is a json/v2 SemanticError. A v1 decode converts it and reads its GoType,
// so it must carry one: without it, v1 dereferenced a nil type and panicked.
func TestWebhookRequestInvalidEventTypeUnderV1Decode(t *testing.T) {
	body := []byte(`{"url":"https://example.com/hook","tenant_id":"t","event_types":["not.an.event"]}`)

	var create CreateWebhookRequest

	err := json.Unmarshal(body, &create)
	if err == nil {
		t.Fatal("want an error for an unknown event type")
	}

	_ = err.Error() // must not panic

	var update UpdateWebhookRequest

	err = json.Unmarshal(body, &update)
	if err == nil {
		t.Fatal("want an error for an unknown event type")
	}

	_ = err.Error()
}
