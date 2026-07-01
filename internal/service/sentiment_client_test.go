package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/llm"
	"github.com/formbricks/hub/internal/models"
)

// fakeStructuredCompleter is a rawStructuredCompleter stub: it records what it was called with
// and returns a canned response/error.
type fakeStructuredCompleter struct {
	response  string
	err       error
	gotSystem string
	gotUser   string
	gotSchema llm.Schema
}

func (f *fakeStructuredCompleter) CompleteJSON(
	_ context.Context, systemPrompt, userText string, schema llm.Schema,
) (string, error) {
	f.gotSystem = systemPrompt
	f.gotUser = userText
	f.gotSchema = schema

	return f.response, f.err
}

func TestPromptSentimentClient_Classify_ParsesLabelAndScore(t *testing.T) {
	fake := &fakeStructuredCompleter{response: `{"sentiment":"positive","score":0.5}`}
	client := promptSentimentClient{raw: fake}

	result, err := client.Classify(context.Background(), "great product", "en-US")
	require.NoError(t, err)
	assert.Equal(t, models.SentimentPositive, result.Label)
	assert.InDelta(t, 0.5, result.Score, 1e-9)

	// The record's text is the user message; the schema is the sentiment contract.
	assert.Equal(t, "great product", fake.gotUser)
	assert.Equal(t, "sentiment", fake.gotSchema.Name)
}

func TestPromptSentimentClient_Classify_AcceptsEveryLabel(t *testing.T) {
	for _, label := range models.SentimentValues() {
		t.Run(string(label), func(t *testing.T) {
			fake := &fakeStructuredCompleter{response: `{"sentiment":"` + string(label) + `","score":0}`}
			client := promptSentimentClient{raw: fake}

			result, err := client.Classify(context.Background(), "text", "")
			require.NoError(t, err)
			assert.Equal(t, label, result.Label)
		})
	}
}

func TestPromptSentimentClient_Classify_ClampsScoreToRange(t *testing.T) {
	tests := map[string]struct {
		response string
		want     float64
	}{
		"above max is clamped": {response: `{"sentiment":"neutral","score":9}`, want: models.SentimentScoreMax},
		"below min is clamped": {response: `{"sentiment":"neutral","score":-9}`, want: models.SentimentScoreMin},
		"in range is kept":     {response: `{"sentiment":"neutral","score":0.75}`, want: 0.75},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			client := promptSentimentClient{raw: &fakeStructuredCompleter{response: testCase.response}}

			result, err := client.Classify(context.Background(), "text", "")
			require.NoError(t, err)
			assert.InDelta(t, testCase.want, result.Score, 1e-9)
		})
	}
}

func TestPromptSentimentClient_Classify_RejectsInvalidResponse(t *testing.T) {
	tests := map[string]string{
		"malformed json": `not json`,
		"unknown label":  `{"sentiment":"furious","score":-1}`,
		"empty label":    `{"sentiment":"","score":0}`,
		"missing score":  `{"sentiment":"positive"}`,
	}

	for name, response := range tests {
		t.Run(name, func(t *testing.T) {
			client := promptSentimentClient{raw: &fakeStructuredCompleter{response: response}}

			_, err := client.Classify(context.Background(), "text", "")
			require.ErrorIs(t, err, ErrSentimentResponseInvalid)
		})
	}
}

func TestPromptSentimentClient_Classify_PropagatesProviderError(t *testing.T) {
	sentinel := errors.New("provider boom")
	client := promptSentimentClient{raw: &fakeStructuredCompleter{err: sentinel}}

	_, err := client.Classify(context.Background(), "text", "")
	require.ErrorIs(t, err, sentinel)
}

func TestPromptSentimentClient_Classify_PreservesRateLimitError(t *testing.T) {
	// The worker's snooze relies on errors.As finding the RateLimitError through Classify's wrap.
	rateLimited := huberrors.NewRateLimitError(5*time.Second, errors.New("429"))
	client := promptSentimentClient{raw: &fakeStructuredCompleter{err: rateLimited}}

	_, err := client.Classify(context.Background(), "text", "")

	var got *huberrors.RateLimitError
	require.ErrorAs(t, err, &got)
	assert.Equal(t, 5*time.Second, got.RetryAfter)
}

func TestSentimentResponseSchema_EnumDerivesFromModelLabels(t *testing.T) {
	var sentiment *llm.Property

	for i := range sentimentResponseSchema.Properties {
		if sentimentResponseSchema.Properties[i].Name == "sentiment" {
			sentiment = &sentimentResponseSchema.Properties[i]
		}
	}

	require.NotNil(t, sentiment, "the schema must have a sentiment property")
	assert.Equal(t, sentimentLabelStrings(), sentiment.Enum, "the enum is the model label set, in order")
	assert.Len(t, sentiment.Enum, len(models.SentimentValues()))
}

// TestParseSentimentResult_ConsumesSchemaPropertyNames guards the implicit coupling between the
// structured-output schema (the property names the model is told to return) and
// sentimentResponse's json tags (the names the parser reads). It builds a response keyed by the
// schema's own property names: a renamed schema property without a matching json tag would bind
// to a zero value here — an empty label (rejected) or a 0 score (caught by the assertion).
func TestParseSentimentResult_ConsumesSchemaPropertyNames(t *testing.T) {
	values := make(map[string]any, len(sentimentResponseSchema.Properties))

	for _, property := range sentimentResponseSchema.Properties {
		switch property.Type {
		case llm.TypeString:
			values[property.Name] = string(models.SentimentPositive)
		case llm.TypeNumber:
			values[property.Name] = 0.5
		case llm.TypeInteger, llm.TypeBoolean, llm.TypeArray:
			t.Fatalf("unexpected schema property type %q for %q", property.Type, property.Name)
		default:
			t.Fatalf("unhandled schema property type %q for %q", property.Type, property.Name)
		}
	}

	raw, err := json.Marshal(values)
	require.NoError(t, err)

	result, err := parseSentimentResult(string(raw))
	require.NoError(t, err)
	assert.Equal(t, models.SentimentPositive, result.Label)
	assert.InDelta(t, 0.5, result.Score, 1e-9)
}

func TestBuildSentimentPrompt_LanguageHint(t *testing.T) {
	withHint, userText := buildSentimentPrompt("ich liebe es", "de-DE")
	assert.Contains(t, withHint, "German", "a known source language is named as a hint")
	assert.Equal(t, "ich liebe es", userText, "the feedback text is the user message")

	noHint, _ := buildSentimentPrompt("text", "")
	assert.NotContains(t, noHint, "written in", "no hint sentence when the source language is unknown")
}
