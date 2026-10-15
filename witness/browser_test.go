package main

import (
	"errors"
	"testing"
)

func TestBrowserAppleScript(t *testing.T) {
	url := "http://127.0.0.1:12345/0123456789abcdefABCDEF-/"
	got, err := browserAppleScript(url)
	if want := "open location \"" + url + "\"\n"; err != nil || got != want {
		t.Fatalf("script = %q, %v; want %q", got, err, want)
	}
	for _, c := range []struct{ name, url string }{
		{"empty", ""}, {"quote", "http://localhost/\""}, {"backslash", "http://localhost/\\"},
		{"newline", "http://localhost/\n"}, {"carriage return", "http://localhost/\r"},
		{"space", "http://localhost/ "}, {"query", "http://localhost/?"}, {"fragment", "http://localhost/#"},
		{"percent escape", "http://localhost/%22"}, {"underscore", "http://localhost/_"},
		{"non ASCII letter", "http://localhost/\u00e9"}, {"null byte", "http://localhost/\x00"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got, err := browserAppleScript(c.url); err == nil || got != "" {
				t.Fatalf("unsafe URL produced script %q, %v", got, err)
			}
		})
	}
}

func TestBrowserDecision(t *testing.T) {
	for _, platform := range []string{"windows", "darwin", "linux"} {
		for _, noBrowser := range []bool{false, true} {
			for _, display := range []string{"", ":0"} {
				for _, wayland := range []string{"", "wayland-0"} {
					want := !noBrowser
					if platform == "linux" && display == "" && wayland == "" {
						want = false
					}
					if got := shouldOpenBrowser(platform, noBrowser, display, wayland); got != want {
						t.Errorf("%s noBrowser=%v DISPLAY=%q WAYLAND_DISPLAY=%q: %v", platform, noBrowser, display, wayland, got)
					}
				}
			}
		}
	}
	for _, attempted := range []bool{false, true} {
		for _, err := range []error{nil, errors.New("browser failed")} {
			text := consoleTexts["en"]
			want := text.RunningNoBrowser
			if attempted && err == nil {
				want = text.Running
			}
			if got := runningText(text, attempted, err); got != want {
				t.Errorf("attempted=%v err=%v: %q", attempted, err, got)
			}
		}
	}
}

func TestNewConsoleTexts(t *testing.T) {
	for _, lang := range []string{"en", "zh", "ja", "ko", "ru", "es"} {
		text, ok := consoleTexts[lang]
		if !ok || text.RunningNoBrowser == "" || text.Leftover == "" {
			t.Errorf("%s: missing RunningNoBrowser or Leftover", lang)
		}
	}
}
