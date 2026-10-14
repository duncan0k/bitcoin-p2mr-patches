//go:build linux || darwin

package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
)

const nodeName = "bitcoind"
const nodePIDFile = "node.pid"
const shutdownTimeout = 60 * time.Second
const endUnresponsiveNode = false

func setupConsole(string) {}

func systemLanguage() string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if lang := languageOfLocale(os.Getenv(name)); lang != "" {
			return lang
		}
	}
	return fallbackLanguage()
}

func stoppingSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}
}

// A closed terminal must not kill the witness before it can stop its node.
func ignoreBrokenPipe() { signal.Ignore(syscall.SIGPIPE) }

func openNodeProgram(exe string) (*os.File, error) { return os.Open(exe) }
func terminate(p *os.Process) error                { return p.Signal(syscall.SIGTERM) }

func shutdownNode(n *node, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	n.stop()
	n.wait(time.Until(deadline))
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.proc != nil {
		_ = terminate(n.proc)
	}
}

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

func ownedByUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func privateNodeFolder(dataDir string) (string, error) {
	dir := filepath.Join(dataDir, "node")
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || !ownedByUser(info) || info.Mode().Perm()&0o022 != 0 {
		return "", fmt.Errorf("node folder %s must be a real directory owned by you and not writable by group or others; fix its ownership and permissions or choose another -datadir", dir)
	}
	return dir, nil
}

// Lstat rejects symlinks, and the descriptor check also rejects a file replaced between Lstat and open.
func checkedPrivateCopy(path, want string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !ownedByUser(info) || info.Mode().Perm()&0o022 != 0 || info.Mode().Perm()&0o100 == 0 {
		return nil, errors.New("the private node copy has unsafe ownership or permissions")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err == nil && (!os.SameFile(info, opened) || !opened.Mode().IsRegular() || !ownedByUser(opened) || opened.Mode().Perm()&0o022 != 0) {
		err = errors.New("the private node copy changed while opening it")
	}
	if err == nil {
		err = checkSHA256(f, want)
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func exclusiveTemporary(dir string, mode os.FileMode) (*os.File, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, ".witness-"+hex.EncodeToString(token[:]))
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, mode)
}

func copyNodeProgram(source io.Reader, dir, want string) error {
	f, err := exclusiveTemporary(dir, 0o700)
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), source); err != nil {
		return err
	}
	if err := f.Chmod(0o700); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), want) {
		return errors.New("the shipped node changed while copying it; download the witness again")
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(dir, nodeName)); err != nil {
		return fmt.Errorf("cannot replace the private node copy in %s; remove the old copy and retry: %w", dir, err)
	}
	return nil
}

func prepareNodeProgram(exe, want, dataDir string) (*os.File, error) {
	source, err := openNodeProgram(exe)
	if err != nil {
		return nil, err
	}
	if want == "" {
		return source, nil
	}
	defer source.Close()
	if err := checkSHA256(source, want); err != nil {
		return nil, err
	}
	dir, err := privateNodeFolder(dataDir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, nodeName)
	if f, err := checkedPrivateCopy(path, want); err == nil {
		return f, nil
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	// On macOS a file written here has no quarantine attribute, so Gatekeeper asks once for the witness,
	// not again for the node. Only this user can write the copy's folder, so the checked file is the one started.
	if err := copyNodeProgram(source, dir, want); err != nil {
		return nil, err
	}
	return checkedPrivateCopy(path, want)
}

func (n *node) startNode(cmd *exec.Cmd) error {
	f, err := prepareNodeProgram(n.exe, n.exeSHA256, n.dataDir)
	if err != nil {
		return err
	}
	defer f.Close()
	if n.exeSHA256 != "" {
		if err := runCheckedCopy(cmd, f); err != nil {
			return err
		}
		cmd.Args[0] = f.Name()
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// macOS 在启动和记录 PID 之间被 SIGKILL 时，未记录的节点只能手动停止。
	if err := writeNodePID(n.dataDir, cmd.Process.Pid); err != nil {
		// Without a recovery record, an untracked child could survive this witness on macOS.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("cannot record the node PID in %s: %w", n.dataDir, err)
	}
	return nil
}

func writeNodePID(dataDir string, pid int) error {
	f, err := exclusiveTemporary(dataDir, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := fmt.Fprintf(f, "%d\n", pid); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dataDir, nodePIDFile))
}

func removeNodePID(dataDir string) error {
	err := os.Remove(filepath.Join(dataDir, nodePIDFile))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func readNodePID(path string) (int, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("cannot safely open node PID file %q: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, fmt.Errorf("cannot inspect node PID file %q: %w", path, err)
	}
	if reason := privateFileProblem(info.Mode(), fileOwner(info), uint32(os.Geteuid())); reason != "" {
		return 0, fmt.Errorf("unsafe node PID file %q: %s; remove it before retrying", path, reason)
	}
	data, err := io.ReadAll(io.LimitReader(f, 32))
	if err != nil {
		return 0, fmt.Errorf("cannot read node PID file %q: %w", path, err)
	}
	if info.Size() > 32 {
		return 0, nil
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" || strings.IndexFunc(text, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return 0, nil
	}
	pid, err := strconv.Atoi(text)
	if err != nil || pid <= 1 || pid >= 1<<22 {
		return 0, nil
	}
	return pid, nil
}

func fileOwner(info os.FileInfo) uint32 {
	return info.Sys().(*syscall.Stat_t).Uid
}

func privateFileProblem(mode os.FileMode, owner, uid uint32) string {
	if !mode.IsRegular() {
		return "must be a regular file"
	}
	if owner != uid {
		return "must be owned by the effective user"
	}
	if mode.Perm()&0o022 != 0 {
		return "must not be writable by group or others"
	}
	return ""
}

// 仅恢复解析后的数据目录中的私有程序。
func stopLeftoverNode(dataDir, message string) error {
	path := filepath.Join(dataDir, nodePIDFile)
	pid, err := readNodePID(path)
	if err != nil {
		return err
	}
	if pid != 0 {
		if err := stopRecordedNode(pid, filepath.Join(dataDir, "node", nodeName), message); err != nil {
			return fmt.Errorf("cannot recover node recorded in %q: %w", path, err)
		}
	}
	return removeNodePID(dataDir)
}

func absoluteHome() (string, error) {
	home := os.Getenv("HOME")
	if !filepath.IsAbs(home) {
		return "", errors.New("HOME must be set to an absolute path; set HOME or use -datadir")
	}
	return home, nil
}

func dataPathProblem(path string) error {
	if strings.ContainsFunc(path, unicode.IsControl) {
		return fmt.Errorf("data folder %q contains a control character; choose another folder with -datadir", path)
	}
	return nil
}

// 数据目录更严格；父目录允许 sticky 公共目录和 user-private-group 的组写权限。
func folderProblem(mode os.FileMode, owner, group, uid, gid uint32, data bool) string {
	if !mode.IsDir() {
		return "must be a directory"
	}
	if data {
		if owner != uid {
			return "must be owned by the effective user"
		}
		if mode.Perm()&0o022 != 0 {
			return "must not be writable by group or others"
		}
		return ""
	}
	if owner != 0 && owner != uid {
		return "must be owned by root or the effective user"
	}
	if mode.Perm()&0o002 != 0 && mode&os.ModeSticky == 0 {
		return "is writable by others without the sticky bit"
	}
	// sticky 公共目录（例如 /tmp）允许组和其他用户写入。
	if mode.Perm()&0o002 != 0 && mode&os.ModeSticky != 0 {
		return ""
	}
	if mode.Perm()&0o020 != 0 && (owner != uid || group != gid) {
		return "is group-writable without ownership by the effective user and their primary group"
	}
	return ""
}

func prepareDataFolder(dir string) (string, error) {
	if err := dataPathProblem(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("cannot create data folder %q; choose another folder with -datadir: %w", dir, err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("cannot resolve data folder %q; choose another folder with -datadir: %w", dir, err)
	}
	if err := dataPathProblem(resolved); err != nil {
		return "", err
	}
	for path := resolved; ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil {
			return "", fmt.Errorf("cannot inspect folder %q; choose another folder with -datadir: %w", path, err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		if reason := folderProblem(info.Mode(), stat.Uid, stat.Gid, uint32(os.Geteuid()), uint32(os.Getegid()), path == resolved); reason != "" {
			if path == resolved {
				return "", fmt.Errorf("data folder %q %s; fix its ownership and run chmod 700 on it, or choose another folder with -datadir", path, reason)
			}
			return "", fmt.Errorf("parent folder %q %s; choose another folder with -datadir", path, reason)
		}
		if filepath.Dir(path) == path {
			break
		}
	}
	return resolved, nil
}
