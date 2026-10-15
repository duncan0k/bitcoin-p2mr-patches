//go:build linux

package main

import (
	"encoding/binary"
	"errors"
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

func setTestFolderACL(t *testing.T, path, name string, mask uint16) {
	t.Helper()
	entries := []struct {
		tag, perm uint16
		id        uint32
	}{
		{0x01, 7, 0xffffffff},               // USER_OBJ
		{0x02, 7, uint32(os.Geteuid()) + 1}, // USER
		{0x04, 0, 0xffffffff},               // GROUP_OBJ
		{0x10, mask, 0xffffffff},            // MASK
		{0x20, 0, 0xffffffff},               // OTHER
	}
	acl := make([]byte, 4+8*len(entries))
	binary.LittleEndian.PutUint32(acl, 2)
	for i, entry := range entries {
		offset := 4 + 8*i
		binary.LittleEndian.PutUint16(acl[offset:], entry.tag)
		binary.LittleEndian.PutUint16(acl[offset+2:], entry.perm)
		binary.LittleEndian.PutUint32(acl[offset+4:], entry.id)
	}
	if err := syscall.Setxattr(path, name, acl, 0); errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP) {
		t.Skipf("file system does not support POSIX ACLs: %v", err)
	} else if err != nil {
		t.Fatalf("set %s on %q: %v", name, path, err)
	}
}

func TestLinuxPrivateFolderACLs(t *testing.T) {
	for _, folder := range []string{"data", "node", "signet"} {
		for _, kind := range []string{"inherited", "access only", "default only"} {
			t.Run(folder+"/"+kind, func(t *testing.T) {
				base := privateTempDir(t)
				dataDir := filepath.Join(base, "data")
				path := dataDir
				if folder != "data" {
					if err := os.Mkdir(dataDir, 0o700); err != nil {
						t.Fatal(err)
					}
					path = filepath.Join(dataDir, folder)
				}
				if kind == "inherited" {
					setTestFolderACL(t, filepath.Dir(path), "system.posix_acl_default", 7)
				}
				// In the inherited case, prepareDataFolder creates the data folder itself; the other cases start from an
				// existing folder.
				if folder != "data" || kind != "inherited" {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				switch kind {
				case "access only":
					// A zero mask lets the mode check pass, so the ACL itself must cause the refusal.
					setTestFolderACL(t, path, "system.posix_acl_access", 0)
				case "default only":
					setTestFolderACL(t, path, "system.posix_acl_default", 7)
				}
				var err error
				switch folder {
				case "data":
					_, err = prepareDataFolder(dataDir)
				case "node":
					_, err = privateNodeFolder(dataDir)
				case "signet":
					err = checkSignetFolder(dataDir)
				}
				advice := "setfacl -b -k " + shellFolder(path)
				if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), advice) ||
					!strings.Contains(err.Error(), "other users could be given access to what the node writes there") ||
					!strings.Contains(err.Error(), "-datadir") {
					t.Fatalf("unsafe %s ACL: %v; want %s", folder, err, advice)
				}
				if kind == "inherited" {
					for _, name := range []string{"system.posix_acl_access", "system.posix_acl_default"} {
						if size, err := syscall.Getxattr(path, name, nil); err != nil || size == 0 {
							t.Fatalf("%s was not inherited: size=%d err=%v", name, size, err)
						}
					}
				}
				if folder == "signet" {
					writeTestFile(t, cookiePath(dataDir), "keep", 0o600)
					n := newNode(filepath.Join(dataDir, "missing-node"), "", dataDir)
					if err := n.startNode(exec.Command(n.exe)); err == nil || !strings.Contains(err.Error(), advice) {
						t.Fatalf("unsafe signet before node start: %v", err)
					}
					if _, err := n.runOnce(1); err == nil || !strings.Contains(err.Error(), advice) {
						t.Fatalf("unsafe signet before cookie removal: %v", err)
					}
					if data, err := os.ReadFile(cookiePath(dataDir)); err != nil || string(data) != "keep" {
						t.Fatalf("old cookie was touched: %q %v", data, err)
					}
				}
			})
		}
	}
}

func TestLinuxFolderACLInspectionFailure(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "missing")
	err := checkFolderACL(path)
	if !errors.Is(err, syscall.ENOENT) || !strings.Contains(err.Error(), "setfacl -b -k "+shellFolder(path)) {
		t.Fatalf("ACL inspection failure must refuse the folder: %v", err)
	}
}

func TestLinuxAbsoluteXDGWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	base := privateTempDir(t)
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
	dir := privateTempDir(t)
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
