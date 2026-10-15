//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

func checkDataTreeACL(string) error { return nil }

func checkFolderACL(path string) error {
	for _, name := range []string{"system.posix_acl_access", "system.posix_acl_default"} {
		_, err := syscall.Getxattr(path, name, nil)
		if errors.Is(err, syscall.ENODATA) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP) {
			continue
		}
		advice := fmt.Sprintf("folder %q has an unsafe or unreadable POSIX ACL; other users could be given access to what the node writes there; run setfacl -b -k %s or choose another folder with -datadir", path, shellFolder(path))
		if err != nil {
			return fmt.Errorf("%s: %w", advice, err)
		}
		return errors.New(advice)
	}
	return nil
}

func defaultDataHome() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
		return dir, nil
	}
	home, err := absoluteHome()
	return filepath.Join(home, ".local", "share"), err
}

func fallbackLanguage() string { return "" }

// A separate process group leaves terminal signals to the witness; parent death still requests clean shutdown.
func configureNode(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}

// The inherited read-only descriptor makes the node run the file that was checked; that the node keeps a
// descriptor of its own program is harmless.
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

func openBrowser(url string) error {
	if !shouldOpenBrowser("linux", false, os.Getenv("DISPLAY"), os.Getenv("WAYLAND_DISPLAY")) {
		return errors.New("no graphical desktop is available")
	}
	return exec.Command("xdg-open", url).Start()
}

func stopRecordedNode(pid int, expected, message string) error {
	// Take a pidfd first, then check the program path; from then on, signal and wait only through that handle, so that
	// a reused PID cannot be hit.
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	defer p.Release()
	image, err := processExecutable(pid)
	if err != nil || image != expected {
		return err
	}
	if err := p.WithHandle(func(uintptr) {}); err != nil {
		return fmt.Errorf("cannot safely stop PID %d without a pidfd: %w", pid, err)
	}
	fmt.Println(message)
	if err := p.Signal(syscall.SIGTERM); errors.Is(err, os.ErrProcessDone) {
		return nil
	} else if err != nil {
		return err
	}
	return waitLeftoverNode(p, shutdownTimeout)
}

func waitLeftoverNode(p *os.Process, timeout time.Duration) error {
	// The orphan is not a child of this process, so Wait would return ECHILD; polling the pidfd waits for its actual
	// exit (a zombie included).
	var waitErr error
	err := p.WithHandle(func(handle uintptr) {
		poll := struct {
			fd      int32
			events  int16
			revents int16
		}{fd: int32(handle), events: 1}
		deadline := time.Now().Add(timeout)
		for {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				waitErr = fmt.Errorf("the earlier node (PID %d) is still shutting down; wait for it to exit and retry", p.Pid)
				return
			}
			ts := syscall.NsecToTimespec(remaining.Nanoseconds())
			ready, _, errno := syscall.Syscall6(syscall.SYS_PPOLL, uintptr(unsafe.Pointer(&poll)), 1, uintptr(unsafe.Pointer(&ts)), 0, 0, 0)
			if errno == syscall.EINTR {
				continue
			}
			if errno != 0 {
				waitErr = errno
				return
			}
			if ready != 0 {
				if poll.revents&1 == 0 {
					waitErr = fmt.Errorf("cannot wait for earlier node PID %d: poll events %d", p.Pid, poll.revents)
				}
				return
			}
		}
	})
	if err != nil {
		return err
	}
	return waitErr
}
