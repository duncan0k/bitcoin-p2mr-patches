package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestProgramNames(t *testing.T) {
	data, err := os.ReadFile("ui/strings.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), ".exe") {
		t.Error("the page translations contain a Windows program name")
	}
	var page map[string]map[string]string
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatal(err)
	}
	for _, platform := range []struct{ os, program, node string }{
		{"windows", "Ark0Witness.exe", "bitcoind.exe"},
		{"linux", "ark0-witness", "bitcoind"},
		{"darwin", "Ark0Witness", "bitcoind"},
	} {
		t.Run(platform.os, func(t *testing.T) {
			if got := fillProgramNames("{program} {node} {h3}", platform.os); got != platform.program+" "+platform.node+" {h3}" {
				t.Fatalf("names: %q", got)
			}
			assets, err := loadAssetsForPlatform(platform.os)
			if err != nil {
				t.Fatal(err)
			}
			body := strings.TrimSuffix(strings.TrimPrefix(string(assets["strings.js"].body), "window.STRINGS = "), ";\n")
			var filled map[string]map[string]string
			if err := json.Unmarshal([]byte(body), &filled); err != nil {
				t.Fatal(err)
			}
			for lang, texts := range page {
				for key, text := range texts {
					want := strings.ReplaceAll(strings.ReplaceAll(text, "{program}", platform.program), "{node}", platform.node)
					if filled[lang][key] != want {
						t.Errorf("%s.%s: names not filled", lang, key)
					}
				}
			}
			for lang, text := range consoleTexts {
				raw, filled := reflect.ValueOf(text), reflect.ValueOf(fillConsole(text, platform.os))
				for i := 0; i < raw.NumField(); i++ {
					s := raw.Field(i).String()
					if strings.Contains(s, ".exe") {
						t.Errorf("%s: a console translation contains a Windows program name", lang)
					}
					want := strings.ReplaceAll(strings.ReplaceAll(s, "{program}", platform.program), "{node}", platform.node)
					if filled.Field(i).String() != want {
						t.Errorf("%s.%s: names not filled", lang, raw.Type().Field(i).Name)
					}
				}
			}
		})
	}
}
