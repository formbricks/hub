package service

import "unicode/utf8"

// TextCap bounds the size of the text Hub sends to an enrichment provider. Zero means unlimited.
//
// BYTES, not runes and not tokens. Tokens are what providers bill and limit on, but no tokenizer is
// right for every deployment: OpenAI, Gemini and a self-hosted TEI endpoint each tokenize
// differently, neither SDK counts locally, and counting provider-side costs a request on Gemini and
// does not exist on OpenAI. Bytes are the cheapest proxy that holds ACROSS SCRIPTS: a token costs
// roughly 4 bytes of Latin text and 1.5-3 bytes of CJK/Korean, so a byte budget bounds token counts
// to within about 2x whichever script arrives, where the same budget in runes bounds them to within
// about 4x — 30k CJK runes is ~30k tokens against ~7.5k for 30k Latin characters, which is exactly
// the divergence that makes a rune cap unsafe for a multilingual deployment.
//
// It is a bound on cost and a way to keep long records embeddable, not a guarantee: the provider's
// own length rejection is still classified as a terminal failure (huberrors.TerminalReasonLength),
// so whatever the approximation misses degrades to today's behaviour rather than to silence.
type TextCap struct {
	MaxBytes int
}

// Apply returns text unchanged when it is empty or fits the cap, or the longest prefix of it that
// fits, plus whether anything was cut. The cut never splits a UTF-8 rune, so the result is always
// valid text to hand a provider.
func (c TextCap) Apply(text string) (string, bool) {
	if c.MaxBytes <= 0 || len(text) <= c.MaxBytes {
		return text, false
	}

	cut := c.MaxBytes
	// At most three steps: a rune is at most 4 bytes, so one of the next 3 positions is a boundary.
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}

	return text[:cut], true
}

// Exceeds reports whether text is over the cap. False for an unlimited cap.
func (c TextCap) Exceeds(text string) bool {
	return c.MaxBytes > 0 && len(text) > c.MaxBytes
}
