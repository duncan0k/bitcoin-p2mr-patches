package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
