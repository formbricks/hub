package llm_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/formbricks/hub/internal/llm"
	"github.com/formbricks/hub/internal/llm/llmtest"
)

func TestSchema_JSONSchema_ClosedObjectWithEnum(t *testing.T) {
	schema := llm.Schema{
		Name: "sentiment",
		Properties: []llm.Property{
			{Name: "label", Type: llm.TypeString, Description: "polarity", Enum: []string{"negative", "neutral", "positive"}},
			{Name: "score", Type: llm.TypeNumber, Description: "polarity score"},
		},
	}

	jsonSchema := schema.JSONSchema()

	assert.Equal(t, "object", jsonSchema["type"])
	assert.Equal(t, false, jsonSchema["additionalProperties"], "the object is closed")
	assert.ElementsMatch(t, []string{"label", "score"}, jsonSchema["required"], "every property is required")

	properties := llmtest.MustMap(t, jsonSchema["properties"], "properties")

	label := llmtest.MustMap(t, properties["label"], "label")
	assert.Equal(t, "string", label["type"])
	assert.Equal(t, []string{"negative", "neutral", "positive"}, label["enum"])
	assert.Equal(t, "polarity", label["description"])

	score := llmtest.MustMap(t, properties["score"], "score")
	assert.Equal(t, "number", score["type"])
	assert.NotContains(t, score, "enum", "a non-enum property carries no enum")
}

func TestSchema_JSONSchema_ArrayOfEnum(t *testing.T) {
	schema := llm.Schema{
		Name: "emotions",
		Properties: []llm.Property{
			{
				Name:        "emotions",
				Type:        llm.TypeArray,
				Description: "applicable emotions",
				Items:       &llm.Property{Type: llm.TypeString, Enum: []string{"joy", "anger", "sadness"}},
			},
		},
	}

	jsonSchema := schema.JSONSchema()

	assert.Equal(t, "object", jsonSchema["type"])
	assert.Equal(t, false, jsonSchema["additionalProperties"], "the object is closed")
	assert.ElementsMatch(t, []string{"emotions"}, jsonSchema["required"], "the array field is required")

	properties := llmtest.MustMap(t, jsonSchema["properties"], "properties")
	emotions := llmtest.MustMap(t, properties["emotions"], "emotions")
	assert.Equal(t, "array", emotions["type"])
	assert.Equal(t, "applicable emotions", emotions["description"])
	assert.NotContains(t, emotions, "enum", "the array itself carries no enum; the constraint lives on its items")

	items := llmtest.MustMap(t, emotions["items"], "items")
	assert.Equal(t, "string", items["type"])
	assert.Equal(t, []string{"joy", "anger", "sadness"}, items["enum"])
	assert.NotContains(t, items, "items", "a scalar element has no nested items")
}

// A TypeArray property with no Items is a construction bug — an underspecified array schema the
// OpenAI/Gemini strict decoders reject — so JSONSchema fails fast instead of emitting it.
func TestSchema_JSONSchema_ArrayWithoutItems(t *testing.T) {
	schema := llm.Schema{
		Name:       "tags",
		Properties: []llm.Property{{Name: "tags", Type: llm.TypeArray}},
	}

	assert.Panics(t, func() { _ = schema.JSONSchema() },
		"a TypeArray without Items must fail fast at schema construction")
}
