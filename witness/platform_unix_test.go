//go:build linux || darwin

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func fileHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func writeTestFile(t *testing.T, path, text string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateNodeCopy(t *testing.T) {
	dataDir, shipped := t.TempDir(), filepath.Join(t.TempDir(), nodeName)
	writeTestFile(t, shipped, "the shipped node", 0o700)
	want := fileHash(t, shipped)
	copyPath := filepath.Join(dataDir, "node", nodeName)
	prepare := func() os.FileInfo {
		t.Helper()
		f, err := prepareNodeProgram(shipped, want, dataDir)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		resolved, err := filepath.EvalSymlinks(copyPath)
		if err != nil {
			t.Fatal(err)
		}
		if f.Name() != resolved {
			t.Fatalf("running %s instead of the private copy", f.Name())
		}
		info, err := f.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 || !ownedByUser(info) || fileHash(t, copyPath) != want {
			t.Fatalf("unsafe copy: %v", info)
		}
		return info
	}
	first := prepare()
	if !os.SameFile(first, prepare()) {
		t.Fatal("a good copy was not reused")
	}
	for _, kind := range []string{"wrong hash", "symlink", "group writable", "others writable", "not executable"} {
		t.Run(kind, func(t *testing.T) {
			if err := os.Remove(copyPath); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "wrong hash":
				writeTestFile(t, copyPath, "another node", 0o700)
			case "symlink":
				if err := os.Symlink(shipped, copyPath); err != nil {
					t.Fatal(err)
				}
			default:
				mode := os.FileMode(0o720)
				if kind == "others writable" {
					mode = 0o702
				} else if kind == "not executable" {
					mode = 0o600
				}
				writeTestFile(t, copyPath, "the shipped node", mode)
			}
			prepare()
			if info, err := os.Lstat(copyPath); err != nil || !info.Mode().IsRegular() {
				t.Fatalf("the unsafe copy was not replaced: %v %v", info, err)
			}
		})
	}
	// Even a reusable copy must not bypass the check of the program supplied in this release.
	writeTestFile(t, shipped, "changed shipped node", 0o700)
	if f, err := prepareNodeProgram(shipped, want, dataDir); err == nil {
		f.Close()
		t.Fatal("a changed shipped file was accepted")
	}
}

func TestPrivateNodeFolder(t *testing.T) {
	for _, kind := range []string{"symlink", "file", "group writable", "others writable"} {
		t.Run(kind, func(t *testing.T) {
			dataDir := t.TempDir()
			path := filepath.Join(dataDir, "node")
			switch kind {
			case "symlink":
				if err := os.Symlink(t.TempDir(), path); err != nil {
					t.Fatal(err)
				}
			case "file":
				writeTestFile(t, path, "node", 0o700)
			default:
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				mode := os.FileMode(0o720)
				if kind == "others writable" {
					mode = 0o702
				}
				if err := os.Chmod(path, mode); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := privateNodeFolder(dataDir); err == nil {
				t.Fatal("an unsafe node folder was accepted")
			}
		})
	}
}

func TestShippedNodeChangesDuringCopy(t *testing.T) {
	shipped := filepath.Join(t.TempDir(), nodeName)
	writeTestFile(t, shipped, "original", 0o700)
	want := fileHash(t, shipped)
	f, err := openNodeProgram(shipped)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := checkSHA256(f, want); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, shipped, "modified", 0o700)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := copyNodeProgram(f, dir, want); err == nil || !strings.Contains(err.Error(), "changed while copying") {
		t.Fatalf("copy: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("a failed copy left files behind: %v %v", entries, err)
	}
}

func TestDevelopmentNodeSkipsCopy(t *testing.T) {
	shipped := filepath.Join(t.TempDir(), nodeName)
	writeTestFile(t, shipped, "node", 0o700)
	dataDir := t.TempDir()
	f, err := prepareNodeProgram(shipped, "", dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Name() != shipped {
		t.Error("a development build uses a copy")
	}
	if _, err := os.Lstat(filepath.Join(dataDir, "node")); !os.IsNotExist(err) {
		t.Errorf("development build created a node folder: %v", err)
	}
}

func TestLeftoverNode(t *testing.T) {
	// Data folder aliases must still identify the private executable reported by the operating system.
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	n := fakeNodeProcess(t, "silent", dir, fileHash(t, exe))
	n.start()
	if !eventually(10*time.Second, func() bool { return starts(dir) == 1 && n.child() != nil }) {
		t.Fatal("no private fake node started")
	}
	if err := writeNodePID(dir, n.child().Pid); err != nil {
		t.Fatal(err)
	}
	release, err := lockInstance(filepath.Join(dir, lockFile))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := stopLeftoverNode(dir); err != nil {
		t.Fatal(err)
	}
	if !stoppedWithin(n, 5*time.Second) {
		t.Fatal("the earlier node still runs")
	}
	if _, err := os.Lstat(filepath.Join(dir, nodePIDFile)); !os.IsNotExist(err) {
		t.Errorf("the old PID remains: %v", err)
	}
	next := fakeNodeProcess(t, "ready", dir, fileHash(t, exe))
	next.start()
	if !eventually(10*time.Second, func() bool { return starts(dir) == 2 && next.client() != nil }) {
		t.Fatal("a new node did not start after recovery")
	}
}

func TestUnrelatedPIDIsNotSignalled(t *testing.T) {
	t.Setenv("WITNESS_FAKE_NODE", "silent")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cmd := exec.Command(exe, nodeArgs(dir, 1)...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	defer func() { _ = cmd.Process.Kill(); <-exited }()
	if !eventually(5*time.Second, func() bool { return starts(dir) == 1 }) {
		t.Fatal("the unrelated process did not start")
	}
	// A symlink at the private copy's name must not make an unrelated executable eligible for SIGTERM.
	if err := os.Mkdir(filepath.Join(dir, "node"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(dir, "node", nodeName)); err != nil {
		t.Fatal(err)
	}
	if err := writeNodePID(dir, cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if err := stopLeftoverNode(dir); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
		t.Fatalf("an unrelated process was stopped: %v", err)
	}
	select {
	case <-exited:
		t.Fatal("an unrelated process exited during recovery")
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := os.Lstat(filepath.Join(dir, nodePIDFile)); !os.IsNotExist(err) {
		t.Errorf("a stale PID was not removed: %v", err)
	}
}

func TestUnixDataFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, xdg := range []string{"", "relative", t.TempDir()} {
		t.Setenv("XDG_DATA_HOME", xdg)
		if xdg == "" {
			if err := os.Unsetenv("XDG_DATA_HOME"); err != nil {
				t.Fatal(err)
			}
		}
		want := filepath.Join(home, "Library", "Application Support", "Ark0Witness")
		if runtime.GOOS == "linux" {
			base := filepath.Join(home, ".local", "share")
			if filepath.IsAbs(xdg) {
				base = xdg
			}
			want = filepath.Join(base, "Ark0Witness")
		}
		got, err := dataFolder("")
		if err != nil || got != want {
			t.Fatalf("XDG_DATA_HOME=%q: %s %v, want %s", xdg, got, err, want)
		}
		info, err := os.Stat(got)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("data folder mode: %v %v", info, err)
		}
	}
	override := filepath.Join(t.TempDir(), "explicit")
	if got, err := dataFolder(override); err != nil || got != override {
		t.Fatalf("override: %s %v", got, err)
	}
}

func TestUnixStoppingSignals(t *testing.T) {
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP} {
		if !slices.Contains(stoppingSignals(), sig) {
			t.Errorf("%v does not take the stopping path", sig)
		}
	}
	if shutdownTimeout != 60*time.Second {
		t.Error("Unix does not allow sixty seconds for shutdown")
	}
	cmd := exec.Command("unused")
	configureNode(cmd)
	if !cmd.SysProcAttr.Setpgid {
		t.Error("terminal signals can reach the node's process group")
	}
}
