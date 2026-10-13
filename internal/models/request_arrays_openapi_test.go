package models

import (
	"testing"

	"github.com/formbricks/hub/internal/datatypes"
)

type openAPIRequestSchemas struct {
	Components struct {
		Schemas map[string]struct {
			Properties map[string]struct {
				MaxItems *int `yaml:"maxItems"`
				Items    struct {
					Enum []string `yaml:"enum"`
				} `yaml:"items"`
			} `yaml:"properties"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

// TestOpenAPIRequestArrayCapsMatchTheirValues pins each request array whose values come from a fixed
// set to a maxItems equal to that set's size. The server refuses a repeated or unknown value, so the
// set's size is the real bound; a spec that falls behind it (a seventh event type added, maxItems left
// at six) would make generated clients refuse a list the server accepts.
func TestOpenAPIRequestArrayCapsMatchTheirValues(t *testing.T) {
	t.Parallel()

	var spec openAPIRequestSchemas
	loadOpenAPISpec(t, &spec)

	retry := spec.Components.Schemas["EnrichmentRetryInputBody"].Properties["enrichments"]

	cases := map[string]struct {
		maxItems *int
		want     int
	}{
		"CreateWebhookInputBody.event_types": {
			spec.Components.Schemas["CreateWebhookInputBody"].Properties["event_types"].MaxItems,
			len(datatypes.GetAllEventTypes()),
		},
		"UpdateWebhookInputBody.event_types": {
			spec.Components.Schemas["UpdateWebhookInputBody"].Properties["event_types"].MaxItems,
			len(datatypes.GetAllEventTypes()),
		},
		"EnrichmentRetryInputBody.enrichments": {retry.MaxItems, len(retry.Items.Enum)},
	}

	for name, testCase := range cases {
		if testCase.maxItems == nil {
			t.Errorf("%s has no maxItems in openapi.yaml", name)

			continue
		}

		if testCase.want == 0 {
			t.Errorf("%s: found no allowed values to compare against; this check would assert nothing", name)

			continue
		}

		if *testCase.maxItems != testCase.want {
			t.Errorf("%s: maxItems is %d, but there are %d allowed values", name, *testCase.maxItems, testCase.want)
		}
	}
}
