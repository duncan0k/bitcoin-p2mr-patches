//go:build darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDarwinStartReplacesPrivateCopy(t *testing.T) {
	t.Setenv("WITNESS_FAKE_NODE", "silent")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := privateTempDir(t)
	want := fileHash(t, exe)
	f, err := prepareNodeProgram(exe, want, dir)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := f.Stat()
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "node", nodeName)
	// A write ACL on the old inode must not reach the next start through a reused copy.
	if output, err := exec.Command("/bin/chmod", "+a", "everyone allow write", path).CombinedOutput(); err != nil {
		t.Fatalf("add write ACL: %v: %s", err, output)
	}
	n := newNode(exe, want, dir)
	for i := 0; i < 3; i++ {
		func() {
			cmd := exec.Command(exe, nodeArgs(dir, 1)...)
			configureNode(cmd)
			if err := n.startNode(cmd); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			current, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if os.SameFile(previous, current) {
				t.Fatalf("start %d reused the old inode", i+1)
			}
			previous = current
		}()
	}
}

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
