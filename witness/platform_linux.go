package main

import (
	"os"
	"os/exec"
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

// configureNode puts the node in a process group of its own, so that Ctrl+C in this terminal reaches only this
// program, which then stops the node over RPC, as on Windows; and has Linux send the node SIGTERM, on which it
// shuts down cleanly, should this program die first.
func configureNode(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}

// openNodeProgram opens the node program. Linux has no sharing mode that keeps it from being replaced while it
// is checked and started; the Linux build serves tests.
func openNodeProgram(exe string) (*os.File, error) {
	return os.Open(exe)
}

// admit has nothing to do on Linux: the node runs from its start, and the parent-death signal ends it with this
// program.
func admit(*os.Process, *os.File) error {
	return nil
}

// terminate asks the node to stop with SIGTERM, which it obeys even while it loads.
func terminate(p *os.Process) error {
	return p.Signal(syscall.SIGTERM)
}

// openBrowser opens the page in the default browser.
func openBrowser(url string) error {
	return exec.Command("xdg-open", url).Start()
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
