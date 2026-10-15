package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

func checkCookieOwnerAndLinks(t *testing.T, path string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &info); err != nil {
		t.Fatal(err)
	}
	if info.NumberOfLinks != 1 {
		t.Fatalf("cookie has %d links, want one", info.NumberOfLinks)
	}
	getSecurityInfo := syscall.NewLazyDLL("advapi32.dll").NewProc("GetSecurityInfo")
	ownerSID := func(file *os.File) string {
		t.Helper()
		var owner *syscall.SID
		var descriptor syscall.Handle
		// SE_FILE_OBJECT and OWNER_SECURITY_INFORMATION both have value 1.
		result, _, _ := getSecurityInfo.Call(file.Fd(), 1, 1, uintptr(unsafe.Pointer(&owner)), 0, 0, 0, uintptr(unsafe.Pointer(&descriptor)))
		if result != 0 {
			t.Fatal(syscall.Errno(result))
		}
		defer syscall.LocalFree(descriptor)
		sid, err := owner.String()
		if err != nil {
			t.Fatal(err)
		}
		return sid
	}
	// Elevated administrators can create files owned by the token's default owner, BUILTIN\Administrators.
	reference, err := os.CreateTemp(filepath.Dir(path), ".cookie-owner-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(reference.Name())
	defer reference.Close()
	if got, want := ownerSID(f), ownerSID(reference); got != want {
		t.Fatalf("cookie owner=%s, want newly created file owner=%s", got, want)
	}
}

// A file id left empty by the file system is no id, even on a volume with a serial number.
func TestFileIDCheck(t *testing.T) {
	if (fileID{VolumeSerialNumber: 7}).check() == nil {
		t.Error("an empty file id is accepted")
	}
	if (fileID{VolumeSerialNumber: 7, ID: [16]byte{15: 1}}).check() != nil {
		t.Error("a file id is refused")
	}
}

// A node whose image is not the file that was checked is not let run.
func TestAdmitRefusesAnotherFile(t *testing.T) {
	t.Setenv("WITNESS_FAKE_NODE", "silent")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other.exe")
	if err := os.WriteFile(other, []byte("not the node"), 0o600); err != nil {
		t.Fatal(err)
	}
	checked, err := openNodeProgram(other)
	if err != nil {
		t.Fatal(err)
	}
	defer checked.Close()
	cmd := exec.Command(exe)
	configureNode(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	if err := admit(cmd.Process, checked); err == nil || !strings.Contains(err.Error(), "not the node program") {
		t.Fatalf("admit: %v", err)
	}
}
