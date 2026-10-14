//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLinuxAbsoluteXDGWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", base)
	if got, err := defaultDataHome(); err != nil || got != base {
		t.Fatalf("absolute XDG without HOME: %q %v", got, err)
	}
}

func TestLinuxBrowserWithoutDesktop(t *testing.T) {
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("PATH", t.TempDir())
	if err := openBrowser("http://127.0.0.1/"); err == nil || !strings.Contains(err.Error(), "no graphical desktop") {
		t.Fatalf("headless browser: %v", err)
	}
}

func TestLinuxExecutesCheckedDescriptor(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	t.Setenv("WITNESS_FAKE_NODE", "silent")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	f, err := prepareNodeProgram(exe, fileHash(t, exe), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	checked, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.Name(), f.Name()+".saved"); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, f.Name(), "a replacement that cannot run", 0o700)
	cmd := exec.Command(f.Name(), nodeArgs(dir, 1)...)
	configureNode(cmd)
	if cmd.SysProcAttr.Pdeathsig != syscall.SIGTERM {
		t.Error("parent death does not request node shutdown")
	}
	if err := runCheckedCopy(cmd, f); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	if !eventually(5*time.Second, func() bool { return starts(dir) == 1 }) {
		t.Fatal("the checked executable did not start")
	}
	image, err := os.Stat(filepath.Join("/proc", strconv.Itoa(cmd.Process.Pid), "exe"))
	if err != nil || !os.SameFile(checked, image) {
		t.Fatalf("the kernel ran another file: %v %v", image, err)
	}
}
