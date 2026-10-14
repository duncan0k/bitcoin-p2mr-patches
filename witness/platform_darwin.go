//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func defaultDataHome() (string, error) {
	home, err := absoluteHome()
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
	// 同整个设计一样，此检查信任同一用户的进程；仅去掉 ps 的输出换行，完整路径必须精确相等。
	return strings.TrimSuffix(string(data), "\n"), err
}

func openBrowser(url string) error { return exec.Command("/usr/bin/open", url).Start() }

func stopRecordedNode(pid int, expected, message string) error {
	image, err := processExecutable(pid)
	if err != nil || image != expected {
		return err
	}
	fmt.Println(message)
	// macOS 没有 pidfd：发信号前再次检查，仍有在这一瞬间 PID 被重用的风险。
	image, err = processExecutable(pid)
	if err != nil || image != expected {
		return err
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return waitLeftoverNode(pid, expected, shutdownTimeout)
}

func waitLeftoverNode(pid int, expected string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		image, err := processExecutable(pid)
		if err != nil || image != expected {
			return err
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the earlier node (PID %d) is still shutting down; wait for it to exit and retry", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
