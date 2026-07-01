package service

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// hashContent returns a stable hex-encoded SHA-256 of s for use as a dedupe key, or the "empty"
// sentinel when s is blank. The enrichment providers share it so a record maps to one dedupe key
// per type regardless of which pipeline computes it.
func hashContent(s string) string {
	if s == "" {
		return "empty"
	}

	sum := sha256.Sum256([]byte(s))

	return hex.EncodeToString(sum[:])
}

// normalizedText trims and NFC-normalizes an optional value_text, returning "" for a nil or blank
// pointer. NFC gives a stable byte representation without changing meaning, so equal text hashes
// equally regardless of its Unicode composition.
func normalizedText(valueText *string) string {
	if valueText == nil {
		return ""
	}

	trimmed := strings.TrimSpace(*valueText)
	if trimmed == "" {
		return ""
	}

	return norm.NFC.String(trimmed)
}
