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
