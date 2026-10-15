//go:build darwin

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
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
			if err := pathACLProblem(c.output, 'd'); (err == nil) != c.safe {
				t.Fatalf("safe=%v: %v", c.safe, err)
			}
		})
	}
}

func TestFileACLParsing(t *testing.T) {
	const header = "-rw-------+ 1 alice staff 64 Oct 14 12:00 /private/data/witness.lock\n"
	for _, c := range []struct {
		name, output string
		safe         bool
	}{
		{"no ACL", strings.Replace(header, "+", "", 1), true},
		{"deny only", header + " 0: group:everyone deny delete\n", true},
		{"multiple denies", header + " 0: group:everyone deny delete\n 1: user:bob deny read,write\n", true},
		{"allow", header + " 0: group:everyone allow read,write\n", false},
		{"inherited allow", header + " 0: user:bob inherited allow write\n", false},
		{"allow after deny", header + " 0: group:everyone deny delete\n 1: user:bob allow write\n", false},
		{"empty", "", false},
		{"garbage", "unparseable\n", false},
		{"folder line", "drwx------ 2 alice staff 64 Oct 14 12:00 /private/data\n", false},
		{"missing entries", header, false},
		{"bad size", strings.Replace(header, " 64 ", " invalid ", 1), false},
		{"bad index", header + " 1: group:everyone deny delete\n", false},
		{"bad entry", header + " 0: unexpected\n", false},
		{"extra output", header + " 0: group:everyone deny delete\nerror\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := pathACLProblem(c.output, '-'); (err == nil) != c.safe {
				t.Fatalf("safe=%v: %v", c.safe, err)
			}
		})
	}
}

func TestDarwinInstanceFileACL(t *testing.T) {
	for _, file := range []string{lockFile, pageFile} {
		t.Run(file, func(t *testing.T) {
			path := filepath.Join(privateTempDir(t), file)
			writeTestFile(t, path, "saved page", 0o600)
			if file == lockFile {
				exe, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, exe, "-test.run=^TestDarwinInstanceLockHelper$")
				cmd.Env = append(os.Environ(), "WITNESS_ACL_LOCK_HELPER="+path)
				cmd.Stderr = os.Stderr
				input, err := cmd.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				defer input.Close()
				output, err := cmd.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				defer output.Close()
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() {
					_ = input.Close()
					if err := cmd.Wait(); err != nil {
						t.Errorf("lock helper: %v", err)
					}
				}()
				if ready, err := bufio.NewReader(output).ReadString('\n'); err != nil || ready != "locked\n" {
					t.Fatalf("lock helper readiness: %q %v", ready, err)
				}
				if release, err := lockInstance(path); !errors.Is(err, errInstanceRunning) {
					if release != nil {
						release()
					}
					t.Fatalf("safe busy lock: %v", err)
				}
			} else if data, err := readPageFile(path); err != nil || string(data) != "saved page" {
				t.Fatalf("safe page file: %q %v", data, err)
			}
			if output, err := exec.Command("/bin/chmod", "+a", "everyone allow read,write", path).CombinedOutput(); err != nil {
				t.Fatalf("add file ACL: %v: %s", err, output)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			stat := info.Sys().(*syscall.Stat_t)
			if info.Mode().Perm() != 0o600 || !ownedByUser(info) || stat.Nlink != 1 {
				t.Fatalf("ACL fixture must pass descriptor checks: %v", info)
			}
			if file == lockFile {
				release, err := lockInstance(path)
				if release != nil {
					release()
				}
				if err == nil || errors.Is(err, errInstanceRunning) {
					t.Fatalf("unsafe busy lock must return an error with advice: %v", err)
				}
				for _, text := range []string{path, "allow access control entry", "delete this file", "retry"} {
					if !strings.Contains(err.Error(), text) {
						t.Errorf("missing %q in lock error: %v", text, err)
					}
				}
			} else {
				data, err := readPageFile(path)
				if err == nil || data != nil || !strings.Contains(err.Error(), path) ||
					!strings.Contains(err.Error(), "allow access control entry") || !strings.Contains(err.Error(), "delete") {
					t.Fatalf("unsafe page file must be refused with advice: %q %v", data, err)
				}
			}
		})
	}
}

func TestDarwinInstanceLockHelper(t *testing.T) {
	path := os.Getenv("WITNESS_ACL_LOCK_HELPER")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, "locked")
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

func TestDataTreeACLParsing(t *testing.T) {
	const dir = "/private/data"
	const listing = "/private/data:\ntotal 0\ndrwx------+ 2 alice staff 64 Oct 14 12:00 signet\n"
	const child = "\n/private/data/signet:\ntotal 8\n-rw-------+ 1 alice staff 10 Oct 14 12:00 debug log\n"
	const hiddenFile = "\n/private/data/signet:\ntotal 8\n-rw-------+ 1 alice staff 10 Oct 14 12:00 .cookie\n"
	const hiddenFolder = "\n/private/data/signet:\ntotal 0\ndrwx------+ 2 alice staff 64 Oct 14 12:00 .hidden\n"
	const hiddenChild = "\n/private/data/signet/.hidden:\ntotal 8\n-rw-------+ 1 alice staff 10 Oct 14 12:00 .lock\n"
	for _, c := range []struct {
		name, output, entry string
	}{
		{"no ACL", "total 0\n", ""},
		{"deny only", listing + " 0: group:everyone deny delete\n" + child + " 0: user:bob inherited deny write\n", ""},
		{"allow in filename", "-rw------- 1 alice staff 10 Oct 14 12:00 allow\n", ""},
		{"directory allow", listing + " 0: user:bob allow add_file\n", dir + "/signet"},
		{"file allow after deny", listing + " 0: group:everyone deny delete\n" + child + " 0: group:everyone deny delete\n 1: user:bob allow write\n", dir + "/signet/debug log"},
		{"inherited allow", listing + child + " 0: user:bob inherited allow write\n", dir + "/signet/debug log"},
		{"allow field anywhere", listing + child + " 23: user:bob allow inherited write\n", dir + "/signet/debug log"},
		{"hidden file allow", listing + hiddenFile + " 0: user:bob allow write\n", dir + "/signet/.cookie"},
		{"hidden folder allow", listing + hiddenFolder + " 0: user:bob allow add_file\n", dir + "/signet/.hidden"},
		{"hidden folder contents allow", listing + hiddenFolder + hiddenChild + " 0: user:bob allow write\n", dir + "/signet/.hidden/.lock"},
		{"hidden deny only", listing + hiddenFile + " 0: group:everyone deny delete\n" + hiddenFolder + " 0: group:everyone deny delete\n" + hiddenChild + " 0: user:bob deny write\n", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := dataTreeACLProblem(dir, c.output)
			if c.entry == "" {
				if err != nil {
					t.Fatalf("safe recursive ACL output: %v", err)
				}
				return
			}
			checkDataTreeAdvice(t, dir, c.entry, err)
			if !strings.Contains(err.Error(), "allow access control entry") {
				t.Fatalf("allow entry was not identified: %v", err)
			}
		})
	}
}

func TestDarwinDataTreeACLInspection(t *testing.T) {
	dir := privateTempDir(t)
	missing := filepath.Join(dir, "missing")
	checkDataTreeAdvice(t, missing, missing, checkDataTreeACL(missing))
	for _, name := range []string{"file", "signet/.cookie", "signet/.hidden", "signet/.hidden/.lock"} {
		t.Run(name, func(t *testing.T) {
			dir := privateTempDir(t)
			path := filepath.Join(dir, name)
			if name == "signet/.hidden" {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, path, "safe file", 0o600)
			}
			if err := checkDataTree(dir); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command("/bin/chmod", "+a", "everyone allow write", path).CombinedOutput(); err != nil {
				t.Fatalf("add write ACL: %v: %s", err, output)
			}
			checkDataTreeAdvice(t, dir, path, checkDataTree(dir))
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
