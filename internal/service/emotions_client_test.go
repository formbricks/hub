package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/huberrors"
	"github.com/formbricks/hub/internal/llm"
	"github.com/formbricks/hub/internal/models"
)

func TestPromptEmotionsClient_Classify_ParsesLabels(t *testing.T) {
	fake := &fakeStructuredCompleter{response: `{"emotions":["joy","surprise"]}`}
	client := promptEmotionsClient{raw: fake}

	result, err := client.Classify(context.Background(), "what a delightful surprise", "en-US")
	require.NoError(t, err)
	assert.Equal(t, []models.EmotionValue{models.EmotionJoy, models.EmotionSurprise}, result.Labels)

	// The record's text is the user message; the schema is the emotions contract.
	assert.Equal(t, "what a delightful surprise", fake.gotUser)
	assert.Equal(t, "emotions", fake.gotSchema.Name)
}

func TestPromptEmotionsClient_Classify_AcceptsEveryLabel(t *testing.T) {
	for _, label := range models.EmotionValues() {
		t.Run(string(label), func(t *testing.T) {
			fake := &fakeStructuredCompleter{response: `{"emotions":["` + string(label) + `"]}`}
			client := promptEmotionsClient{raw: fake}

			result, err := client.Classify(context.Background(), "text", "")
			require.NoError(t, err)
			assert.Equal(t, []models.EmotionValue{label}, result.Labels)
		})
	}
}

func TestPromptEmotionsClient_Classify_DedupesAndDropsUnknown(t *testing.T) {
	// Duplicates are collapsed (first-seen order preserved) and out-of-pool labels dropped, so a
	// slightly out-of-contract response still yields a clean, valid set rather than failing.
	fake := &fakeStructuredCompleter{response: `{"emotions":["anger","anger","ecstatic","sadness"]}`}
	client := promptEmotionsClient{raw: fake}

	result, err := client.Classify(context.Background(), "text", "")
	require.NoError(t, err)
	assert.Equal(t, []models.EmotionValue{models.EmotionAnger, models.EmotionSadness}, result.Labels)
}

func TestPromptEmotionsClient_Classify_EmptyIsAllowed(t *testing.T) {
	// An empty array (no emotion applies) and a response with only unknown labels both yield an
	// empty set — valid, and the worker clears the column rather than erroring.
	for name, response := range map[string]string{
		"empty array":   `{"emotions":[]}`,
		"absent field":  `{}`,
		"only unknowns": `{"emotions":["ecstatic","bored"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			client := promptEmotionsClient{raw: &fakeStructuredCompleter{response: response}}

			result, err := client.Classify(context.Background(), "text", "")
			require.NoError(t, err)
			assert.Empty(t, result.Labels)
		})
	}
}

func TestPromptEmotionsClient_Classify_RejectsMalformedJSON(t *testing.T) {
	client := promptEmotionsClient{raw: &fakeStructuredCompleter{response: `not json`}}

	_, err := client.Classify(context.Background(), "text", "")
	require.ErrorIs(t, err, ErrEmotionsResponseInvalid)
}

func TestPromptEmotionsClient_Classify_PropagatesProviderError(t *testing.T) {
	sentinel := errors.New("provider boom")
	client := promptEmotionsClient{raw: &fakeStructuredCompleter{err: sentinel}}

	_, err := client.Classify(context.Background(), "text", "")
	require.ErrorIs(t, err, sentinel)
}

func TestPromptEmotionsClient_Classify_PreservesRateLimitError(t *testing.T) {
	// The worker's snooze relies on errors.As finding the RateLimitError through Classify's wrap.
	rateLimited := huberrors.NewRateLimitError(5*time.Second, errors.New("429"))
	client := promptEmotionsClient{raw: &fakeStructuredCompleter{err: rateLimited}}

	_, err := client.Classify(context.Background(), "text", "")

	var got *huberrors.RateLimitError
	require.ErrorAs(t, err, &got)
	assert.Equal(t, 5*time.Second, got.RetryAfter)
}

func TestEmotionsResponseSchema_EnumDerivesFromModelLabels(t *testing.T) {
	var emotions *llm.Property

	for i := range emotionsResponseSchema.Properties {
		if emotionsResponseSchema.Properties[i].Name == "emotions" {
			emotions = &emotionsResponseSchema.Properties[i]
		}
	}

	require.NotNil(t, emotions, "the schema must have an emotions property")
	assert.Equal(t, llm.TypeArray, emotions.Type, "emotions is an array")
	require.NotNil(t, emotions.Items, "the array must declare its element schema")
	assert.Equal(t, labelStrings(models.EmotionValues()), emotions.Items.Enum, "the element enum is the model label set, in order")
	assert.Len(t, emotions.Items.Enum, len(models.EmotionValues()))
}

func TestBuildEmotionsPrompt_LanguageHint(t *testing.T) {
	withHint, userText := buildEmotionsPrompt("ich bin so wütend", "de-DE")
	assert.Contains(t, withHint, "German", "a known source language is named as a hint")
	assert.Equal(t, "ich bin so wütend", userText, "the feedback text is the user message")

	noHint, _ := buildEmotionsPrompt("text", "")
	assert.NotContains(t, noHint, "written in", "no hint sentence when the source language is unknown")
}
