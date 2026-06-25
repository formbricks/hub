package service

import "testing"

func TestLanguageDisplayName(t *testing.T) {
	tests := []struct {
		name string
		tag  string
		want string
	}{
		{name: "locale maps to language name", tag: "de-DE", want: "German"},
		{name: "language only", tag: "fr", want: "French"},
		{name: "another language", tag: "ja", want: "Japanese"},
		{name: "empty is empty (unknown source language)", tag: "", want: ""},
		{name: "whitespace is empty", tag: "   ", want: ""},
		{name: "unparseable falls back to the raw tag", tag: "@@@bogus", want: "@@@bogus"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := languageDisplayName(testCase.tag); got != testCase.want {
				t.Fatalf("languageDisplayName(%q) = %q, want %q", testCase.tag, got, testCase.want)
			}
		})
	}
}
