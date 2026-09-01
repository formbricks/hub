package validation

import (
	"reflect"
	"unicode/utf8"

	"github.com/go-playground/validator/v10"
)

// Postgres refuses two character-level things when text is written into a jsonb column, and both
// arrive as escapes inside otherwise-valid JSON:
//
//	SELECT '{"note":"bad\u0000value"}'::jsonb  -- ERROR: unsupported Unicode escape sequence
//	SELECT '{"note":"bad\ud83dvalue"}'::jsonb  -- ERROR: invalid input syntax for type json
//
// A raw-JSON request field (json.RawMessage) reaches the driver byte-for-byte, so neither is caught
// by anything upstream: the request parses, the struct validates, and the failure lands as an
// unmapped driver error — a 500 on input the API documents as invalid. `no_null_bytes` cannot cover
// it either, because it skips any field that is not a string kind and json.RawMessage is a []byte.
//
// (Postgres also refuses a third, non-character thing: numbers that do not fit numeric, e.g.
// 1e1000000. That is a range property of the parsed value, invisible to a byte scan, so it is
// handled where it surfaces — the feedback-records repository maps SQLSTATE 22003 to a metadata
// validation error.)
//
// The check runs over the raw bytes rather than the unmarshalled value on purpose. Unmarshalling
// rewrites both cases — encoding/json turns a \u0000 escape into a real NUL and replaces an
// unpaired surrogate with U+FFFD — so a value read back through a Go string looks storable while
// the bytes actually sent to Postgres are not.
const storableJSONTag = "storable_json"

const (
	highSurrogateStart = 0xd800
	highSurrogateEnd   = 0xdbff
	lowSurrogateStart  = 0xdc00
	lowSurrogateEnd    = 0xdfff

	escapeLen   = 6  // \uXXXX
	decimalBase = 10 // first hex value a letter digit stands for
	hexDigits   = 4
	pairLen     = escapeLen * 2
	nibbleBits  = 4
)

// validateStorableJSON is the `storable_json` tag: it accepts anything that is not a raw JSON field
// so the tag is inert if applied to the wrong kind, matching how no_null_bytes behaves.
func validateStorableJSON(fl validator.FieldLevel) bool {
	field := fl.Field()

	if field.Kind() == reflect.Ptr {
		if field.IsNil() {
			return true // nil pointer is valid (handled by omitempty)
		}

		field = field.Elem()
	}

	if field.Kind() != reflect.Slice || field.Type().Elem().Kind() != reflect.Uint8 {
		return true // Not raw JSON, skip validation
	}

	return IsStorableJSON(field.Bytes())
}

// IsStorableJSON reports whether raw JSON can be written to a Postgres jsonb column.
//
// It assumes the bytes are already syntactically valid JSON, which the request decoder has
// established by the time a struct is validated. That is what makes a plain scan sound: a backslash
// escape can only appear inside a JSON string, so keys and values are both covered without tracking
// string context — and a NULL byte in a *key* is rejected by Postgres just as one in a value is.
func IsStorableJSON(raw []byte) bool {
	if len(raw) == 0 {
		return true
	}

	// Catches raw invalid bytes, including a surrogate smuggled in as CESU-8, which Postgres
	// rejects as an encoding error rather than a JSON one.
	if !utf8.Valid(raw) {
		return false
	}

	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}

		if i+1 >= len(raw) {
			return false // trailing backslash: not valid JSON, and not storable
		}

		// Any other escape (\\, \", \n, ...) consumes its own second byte, which is what keeps a
		// literal "\\u0000" — an escaped backslash followed by text — from reading as an escape.
		if raw[i+1] != 'u' {
			i++

			continue
		}

		code, ok := parseHex4(raw[i+2:])
		if !ok {
			return false // malformed \u escape; Postgres would reject it too
		}

		if code == 0 {
			return false // NULL byte
		}

		if code >= lowSurrogateStart && code <= lowSurrogateEnd {
			return false // low surrogate with no high surrogate before it
		}

		if code >= highSurrogateStart && code <= highSurrogateEnd {
			if !hasLowSurrogateAt(raw, i+escapeLen) {
				return false // high surrogate never completed into a pair
			}

			i += pairLen - 1 // consume both halves

			continue
		}

		i += escapeLen - 1
	}

	return true
}

// hasLowSurrogateAt reports whether a \uDC00-\uDFFF escape starts exactly at offset i.
func hasLowSurrogateAt(raw []byte, i int) bool {
	if i+escapeLen > len(raw) || raw[i] != '\\' || raw[i+1] != 'u' {
		return false
	}

	code, ok := parseHex4(raw[i+2:])

	return ok && code >= lowSurrogateStart && code <= lowSurrogateEnd
}

// parseHex4 reads the four hex digits of a \uXXXX escape.
func parseHex4(digits []byte) (int, bool) {
	if len(digits) < hexDigits {
		return 0, false
	}

	code := 0

	for _, digit := range digits[:hexDigits] {
		var nibble int

		switch {
		case digit >= '0' && digit <= '9':
			nibble = int(digit - '0')
		case digit >= 'a' && digit <= 'f':
			nibble = int(digit-'a') + decimalBase
		case digit >= 'A' && digit <= 'F':
			nibble = int(digit-'A') + decimalBase
		default:
			return 0, false
		}

		code = code<<nibbleBits | nibble
	}

	return code, true
}
