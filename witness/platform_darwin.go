//go:build darwin

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func defaultDataHome() (string, error) {
	home, err := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support"), err
}

func fallbackLanguage() string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "/usr/bin/defaults", "read", "-g", "AppleLanguages").Output()
	if err != nil {
		return ""
	}
	return languageOfAppleLanguages(string(data))
}

func languageOfAppleLanguages(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "(") {
		return ""
	}
	first, _, _ := strings.Cut(strings.TrimSpace(text[1:]), ",")
	return languageOfLocale(strings.Trim(strings.TrimSpace(strings.TrimSuffix(first, ")")), `"`))
}

// macOS has no parent-death signal, so node.pid supplies recovery on the next start.
func configureNode(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func runCheckedCopy(cmd *exec.Cmd, f *os.File) error {
	cmd.Path = f.Name()
	return nil
}

func processExecutable(pid int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "/bin/ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && ctx.Err() == nil {
		return "", nil
	}
	return strings.TrimSpace(string(data)), err
}

func openBrowser(url string) error { return exec.Command("/usr/bin/open", url).Start() }
