package main

import (
	"errors"
	"testing"
)

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
