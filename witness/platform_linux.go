//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

func defaultDataHome() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".local", "share"), err
}

func fallbackLanguage() string { return "" }

// A separate process group leaves terminal signals to the witness; parent death still requests clean shutdown.
func configureNode(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}

// The inherited read-only descriptor makes the kernel execute the file that was hashed, even after a rename.
func runCheckedCopy(cmd *exec.Cmd, f *os.File) error {
	path := "/proc/self/fd/" + strconv.FormatUint(uint64(f.Fd()), 10)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot start the checked node: /proc/self/fd is unavailable; mount procfs: %w", err)
	}
	checked, err := f.Stat()
	if err != nil || !os.SameFile(info, checked) {
		return fmt.Errorf("cannot start the checked node: /proc/self/fd does not identify its file")
	}
	cmd.ExtraFiles = []*os.File{f}
	cmd.Path = "/proc/self/fd/3"
	return nil
}

func processExecutable(pid int) (string, error) {
	path, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if os.IsNotExist(err) {
		return "", nil
	}
	return path, err
}

func openBrowser(url string) error { return exec.Command("xdg-open", url).Start() }
