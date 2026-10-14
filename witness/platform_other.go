//go:build !windows

package main

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

const nodeName = "bitcoind"

func setupConsole(string) {}

// systemLanguage is the language of the locale, if the program has it.
func systemLanguage() string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if lang := languageOfLocale(os.Getenv(name)); lang != "" {
			return lang
		}
	}
	return ""
}

// hideWindow puts the node in a process group of its own, so that Ctrl+C in this terminal reaches only this
// program, which then stops the node over RPC, as on Windows.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// openBrowser opens the page in the default browser.
func openBrowser(url string) error {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	return exec.Command(opener, url).Start()
}

// lockInstance takes an exclusive lock on the lock file, so that a second program on the same data folder
// cannot; the lock goes with the process.
func lockInstance(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}
