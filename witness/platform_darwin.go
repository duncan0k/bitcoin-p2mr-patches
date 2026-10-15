//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func checkFolderACL(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/ls", "-lde", "--", path)
	cmd.Env = []string{"LC_ALL=C"}
	data, err := cmd.Output()
	if err == nil {
		err = folderACLProblem(string(data))
	}
	if err != nil {
		return fmt.Errorf("unsafe or unreadable access list on folder %q; remove the access list with chmod -N or choose another folder with -datadir: %w", path, err)
	}
	return nil
}

var folderACLHeader = regexp.MustCompile(`^d[rwxstST-]{9}[+@]?\s+[0-9]+\s+\S.+$`)
var folderACLEntry = regexp.MustCompile(`^\s+([0-9]+): (user|group):.+ (allow|deny) [a-z_]+(,[a-z_]+)*$`)

// Only the folder's own line followed by deny entries numbered in order is accepted; any other output is refused.
func folderACLProblem(output string) error {
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(lines) == 0 || !folderACLHeader.MatchString(lines[0]) {
		return errors.New("cannot parse ls access list header")
	}
	header := strings.Fields(lines[0])
	if len(header) < 9 {
		return errors.New("incomplete ls access list header")
	}
	if _, err := strconv.ParseUint(header[4], 10, 64); err != nil {
		return errors.New("cannot parse ls folder size")
	}
	for i, line := range lines[1:] {
		// ls prints inheritance flags before allow or deny, so every field is checked: no name can hide an allow entry.
		for _, field := range strings.Fields(line) {
			if field == "allow" {
				return errors.New("contains an allow access control entry")
			}
		}
		entry := folderACLEntry.FindStringSubmatch(line)
		if entry == nil || entry[1] != strconv.Itoa(i) || entry[3] != "deny" {
			return errors.New("cannot parse ls access control entry")
		}
	}
	if strings.Contains(header[0], "+") && len(lines) == 1 {
		return errors.New("missing ls access control entries")
	}
	return nil
}

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
	cmd := exec.CommandContext(ctx, "/bin/ps", "-o", "comm=", "-p", strconv.Itoa(pid))
	cmd.Env = []string{"LC_ALL=en_US.UTF-8"}
	data, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && ctx.Err() == nil {
		return "", nil
	}
	// Like the whole design, this check trusts processes of the same user. Only the newline that ps adds is removed:
	// the full path must match exactly.
	return strings.TrimSuffix(string(data), "\n"), err
}

func openBrowser(url string) error {
	script, err := browserAppleScript(url)
	if err != nil {
		return err
	}
	cmd := exec.Command("/usr/bin/osascript", "-")
	cmd.Stdin = strings.NewReader(script)
	return cmd.Start()
}

func stopRecordedNode(pid int, expected, message string) error {
	image, err := processExecutable(pid)
	if err != nil || image != expected {
		return err
	}
	fmt.Println(message)
	// macOS has no pidfd: the check is repeated just before the signal, which still leaves that instant for the PID to
	// be reused.
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
