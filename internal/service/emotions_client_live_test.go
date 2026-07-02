package service

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmotionsClient_Live_Gemini exercises the emotions array-of-enum structured-output schema
// against a real Gemini model. It closes ENG-1613 task #14: the schema builder is unit-tested, but
// runtime acceptance of {"type":"array","items":{"type":"string","enum":[…]}} by a live provider
// was deferred at P4/P5. It is gated on EMOTIONS_LIVE_GEMINI_KEY so it never runs in CI; run it
// with a Gemini API key (reuse the configured SENTIMENT key):
//
//	EMOTIONS_LIVE_GEMINI_KEY=$(grep '^SENTIMENT_PROVIDER_API_KEY=' .env | cut -d= -f2- | tr -d '"') \
//	  go test -run TestEmotionsClient_Live_Gemini -count=1 -v ./internal/service/
//
// The must-pass assertion is that Classify returns no error — i.e. Gemini's ResponseJsonSchema
// accepts the array-of-enum — and that every returned label is a valid Ekman-6 value. Each
// classification is logged for an eyeball sanity check.
func TestEmotionsClient_Live_Gemini(t *testing.T) {
	key := os.Getenv("EMOTIONS_LIVE_GEMINI_KEY")
	if key == "" {
		t.Skip("set EMOTIONS_LIVE_GEMINI_KEY to run the live Gemini array-of-enum validation")
	}

	model := os.Getenv("EMOTIONS_LIVE_MODEL")
	if model == "" {
		model = "gemini-2.5-flash"
	}

	client, err := NewEmotionsClient(context.Background(), EmotionsClientConfig{
		Provider:       EmotionsProviderGoogle,
		ProviderAPIKey: key,
		Model:          model,
	})
	require.NoError(t, err)

	cases := []struct {
		name         string
		text         string
		wantNonEmpty bool
	}{
		{"joy + surprise", "Wow, I absolutely love this — it works perfectly and it was such a delightful surprise!", true},
		{"anger + disgust", "This is disgusting and infuriating. I am furious that it broke yet again.", true},
		{"sadness", "I'm really disappointed and heartbroken; this let me down badly.", true},
		{"neutral factual", "The package arrived on Tuesday and the box contained three items.", false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			res, classifyErr := client.Classify(context.Background(), testCase.text, "en")
			require.NoError(t, classifyErr,
				"Gemini must accept the array-of-enum schema and return parseable structured output")

			for _, label := range res.Labels {
				assert.Truef(t, label.IsValid(), "returned label %q is not a valid Ekman-6 emotion", label)
			}

			t.Logf("text=%q -> emotions=%v", testCase.text, res.Labels)

			if testCase.wantNonEmpty {
				assert.NotEmpty(t, res.Labels, "clearly emotional text should yield at least one emotion")
			}
		})
	}
}
