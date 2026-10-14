//go:build darwin

package main

import "testing"

func TestAppleLanguages(t *testing.T) {
	for text, want := range map[string]string{
		"(\n    \"zh-Hans-CN\",\n    en\n)": "zh",
		"(\n    es\n)":                      "es",
		"(de-DE, en)":                       "",
		"()":                                "",
		"not a language list":               "",
	} {
		if got := languageOfAppleLanguages(text); got != want {
			t.Errorf("%q: %s, want %s", text, got, want)
		}
	}
}
