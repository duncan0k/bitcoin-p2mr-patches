//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"
)

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
