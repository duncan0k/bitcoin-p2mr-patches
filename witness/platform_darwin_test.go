//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLeftoverNodeUnicode(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	t.Setenv("COLUMNS", "1")
	dir := filepath.Join(privateTempDir(t), "节点-é")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	testLeftoverNode(t, dir)
}

func TestFolderACLParsing(t *testing.T) {
	const header = "drwx------+ 2 alice staff 64 Oct 14 12:00 /Users/alice\n"
	for _, c := range []struct {
		name, output string
		safe         bool
	}{
		{"no ACL", "drwx------ 2 alice staff 64 Oct 14 12:00 /private/data\n", true},
		{"home default deny", header + " 0: group:everyone deny delete\n", true},
		{"inherited deny", header + " 0: group:everyone inherited deny delete\n", true},
		{"multiple denies", header + " 0: group:everyone deny delete\n 1: user:bob deny add_file,delete_child\n", true},
		{"allow", header + " 0: user:bob allow add_file\n", false},
		{"allow after deny", header + " 0: group:everyone deny delete\n 1: user:bob allow delete_child\n", false},
		{"inheritable allow", header + " 0: group:everyone allow write,file_inherit,directory_inherit\n", false},
		{"inherited allow", header + " 0: user:bob inherited allow add_file\n", false},
		{"empty output", "", false},
		{"garbage", "unparseable\n", false},
		{"missing entries", header, false},
		{"bad entry", header + " 0: unexpected\n", false},
		{"bad index", header + " 1: group:everyone deny delete\n", false},
		{"unknown action", header + " 0: group:everyone audit delete\n", false},
		{"extra output", header + " 0: group:everyone deny delete\nerror\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := folderACLProblem(c.output); (err == nil) != c.safe {
				t.Fatalf("safe=%v: %v", c.safe, err)
			}
		})
	}
}

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
