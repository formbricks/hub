package service

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestTextCapApply(t *testing.T) {
	tests := []struct {
		name string
		cap  TextCap
		in   string
		want string
		cut  bool
	}{
		{"a zero cap is unlimited", TextCap{}, strings.Repeat("a", 500), strings.Repeat("a", 500), false},
		{"under the cap", TextCap{MaxBytes: 10}, "abc", "abc", false},
		{"exactly at the cap", TextCap{MaxBytes: 3}, "abc", "abc", false},
		{"over the cap keeps the head", TextCap{MaxBytes: 3}, "abcdef", "abc", true},
		{"empty text is never cut", TextCap{MaxBytes: 3}, "", "", false},
		// 2-byte and 4-byte runes: the byte at the cap is a continuation byte, so the cut has to
		// step back rather than emit invalid UTF-8 to a provider.
		{"a 2-byte rune cut lands on its boundary", TextCap{MaxBytes: 3}, "ééé", "é", true},
		{"a 4-byte rune cut lands on its boundary", TextCap{MaxBytes: 5}, "😀😀", "😀", true},
		{"a cut that lands exactly on a boundary keeps it", TextCap{MaxBytes: 4}, "😀😀", "😀", true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, cut := testCase.cap.Apply(testCase.in)

			assert.Equal(t, testCase.want, got)
			assert.Equal(t, testCase.cut, cut)
			assert.True(t, utf8.ValidString(got), "a cut must never leave invalid UTF-8")
			assert.LessOrEqual(t, len(got), len(testCase.in))
		})
	}
}

func TestTextCapExceeds(t *testing.T) {
	// The zero cap means unlimited, so it must never report an over-cap input: Exceeds drives the
	// translation skip, where a false positive abandons a record that had nothing wrong with it.
	assert.False(t, TextCap{}.Exceeds(strings.Repeat("a", 100)))
	assert.False(t, TextCap{MaxBytes: 3}.Exceeds("abc"))
	assert.True(t, TextCap{MaxBytes: 3}.Exceeds("abcd"))
	assert.False(t, TextCap{MaxBytes: 3}.Exceeds(""))
}
