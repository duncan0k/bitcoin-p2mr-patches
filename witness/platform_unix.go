//go:build linux || darwin

package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
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

// Opens without blocking and checks the descriptor first, so that a FIFO or a device cannot block the program.
func openRegularFile(path string, flags int) (*os.File, error) {
	f, err := os.OpenFile(path, flags|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("file %q must be a regular file; replace it with the expected file", path)
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func openNodeProgram(exe string) (*os.File, error) { return openRegularFile(exe, os.O_RDONLY) }
func terminate(p *os.Process) error                { return p.Signal(syscall.SIGTERM) }

func readCookie(path string) ([]byte, error) {
	f, err := openRegularFile(path, os.O_RDONLY|syscall.O_NOFOLLOW)
	if os.IsNotExist(err) {
		return nil, errNotReady
	}
	if err != nil {
		return nil, fmt.Errorf("cannot safely open cookie %q; remove the unsafe file and let the node recreate it: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("cannot inspect cookie %q: %w", path, err)
	}
	if !ownedByUser(info) {
		return nil, fmt.Errorf("cookie %q must be owned by the effective user; remove it and let the node recreate it", path)
	}
	// On Linux, the group bits of a file with an ACL are its mask, so this also rules out every named entry.
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("cookie %q could be read by other users; remove it and let the node recreate it", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, 1024))
	if err != nil {
		return nil, fmt.Errorf("cannot read cookie %q; remove it and let the node recreate it: %w", path, err)
	}
	// Nothing past 1 KiB is read; checking the size again after reading also catches a file that grew meanwhile.
	info, err = f.Stat()
	if err != nil {
		return nil, fmt.Errorf("cannot inspect cookie %q: %w", path, err)
	}
	if info.Size() > 1024 {
		return nil, fmt.Errorf("cookie %q exceeds 1 KiB; remove it and let the node recreate it", path)
	}
	return data, nil
}

func readPageFile(path string) ([]byte, error) {
	f, err := openRegularFile(path, os.O_RDONLY|syscall.O_NOFOLLOW)
	if err != nil {
		return nil, fmt.Errorf("cannot safely open page file %q: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("cannot inspect page file %q: %w", path, err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if reason := instanceFileProblem(info.Mode(), stat.Uid, uint32(os.Geteuid()), uint64(stat.Nlink)); reason != "" {
		return nil, fmt.Errorf("unsafe page file %q: %s; delete it or the data folder and retry", path, reason)
	}
	if err := checkInstanceFileACL(path); err != nil {
		return nil, fmt.Errorf("unsafe page file %q: %w; delete it or the data folder and retry", path, err)
	}
	return io.ReadAll(io.LimitReader(f, 1024))
}

func writePageFile(path string, data []byte) error {
	f, err := exclusiveTemporary(filepath.Dir(path), 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

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
	unsafe := func(err error) (func(), error) {
		return nil, fmt.Errorf("cannot safely lock file %q: %w; delete this file or the data folder %s and retry", path, err, shellFolder(filepath.Dir(path)))
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return unsafe(err)
	}
	info, err := f.Stat()
	if err == nil {
		stat := info.Sys().(*syscall.Stat_t)
		if reason := instanceFileProblem(info.Mode(), stat.Uid, uint32(os.Geteuid()), uint64(stat.Nlink)); reason != "" {
			err = errors.New(reason)
		}
	}
	if err == nil {
		err = checkInstanceFileACL(path)
	}
	if err != nil {
		_ = f.Close()
		return unsafe(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errInstanceRunning
		}
		return unsafe(err)
	}
	return func() { _ = f.Close() }, nil
}

func instanceFileProblem(mode os.FileMode, owner, uid uint32, links uint64) string {
	if reason := privateFileProblem(mode, owner, uid); reason != "" {
		return reason
	}
	if links != 1 {
		return "must have exactly one hard link"
	}
	if mode.Perm()&0o077 != 0 {
		return "must not grant any permissions to group or others"
	}
	return ""
}

func dataEntryProblem(mode os.FileMode, owner, uid uint32, links uint64) string {
	if !mode.IsDir() && !mode.IsRegular() {
		return "must be a directory or a regular file (no symlinks or special files)"
	}
	if owner != uid {
		return "must be owned by the effective user"
	}
	if mode.Perm()&0o022 != 0 {
		return "must not be writable by group or others"
	}
	if mode.IsRegular() && links != 1 {
		return "must have exactly one hard link"
	}
	return ""
}

func dataTreeError(dataDir, path string, err error) error {
	return fmt.Errorf("unsafe data folder entry %q: %w; another user may have placed or changed it; delete the data folder %s and start again (the node will sync again), or choose another folder with -datadir", path, err, shellFolder(dataDir))
}

// Call only while holding the instance lock, after stopping any leftover node and before preparing a new one.
// Fixing the folder permissions does not remove old entries. Every entry another user created is owned by that
// user, and a hard link they made to one of our own files has a link count above one. These checks reject both.
func checkDataTree(dataDir string) error {
	err := filepath.WalkDir(dataDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return dataTreeError(dataDir, path, err)
		}
		// DirEntry.Info uses Lstat semantics, so neither the inspection nor WalkDir follows symlinks.
		info, err := entry.Info()
		if err != nil {
			return dataTreeError(dataDir, path, err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		if reason := dataEntryProblem(info.Mode(), stat.Uid, uint32(os.Geteuid()), uint64(stat.Nlink)); reason != "" {
			return dataTreeError(dataDir, path, errors.New(reason))
		}
		if runtime.GOOS == "linux" && info.IsDir() {
			if err := checkFolderACL(path); err != nil {
				return dataTreeError(dataDir, path, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return checkDataTreeACL(dataDir)
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
	if err := checkFolderACL(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func checkSignetFolder(dataDir string) error {
	path := filepath.Join(dataDir, "signet")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot inspect signet folder %q; fix it or choose another folder with -datadir: %w", path, err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if reason := folderProblem(info.Mode(), stat.Uid, uint32(os.Geteuid()), true); reason != "" {
		return fmt.Errorf("signet folder %q %s; use a real directory owned by you and run chmod 700 on it, or choose another folder with -datadir", path, reason)
	}
	return checkFolderACL(path)
}

// Lstat rejects symlinks, and the descriptor check also rejects a file replaced between Lstat and open.
func checkedPrivateCopy(path, want string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !ownedByUser(info) || info.Mode().Perm()&0o022 != 0 || info.Mode().Perm()&0o100 == 0 {
		return nil, fmt.Errorf("private node copy %q has unsafe ownership or permissions", path)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err == nil && (!os.SameFile(info, opened) || !opened.Mode().IsRegular() || !ownedByUser(opened) || opened.Mode().Perm()&0o022 != 0) {
		err = fmt.Errorf("private node copy %q changed while opening it", path)
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
		return nil, fmt.Errorf("cannot verify shipped node %q: %w", exe, err)
	}
	dir, err := privateNodeFolder(dataDir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, nodeName)
	// On both Linux and macOS, tightening permissions does not revoke write descriptors held by other users.
	// Before each start, write a fresh inode with O_EXCL in the private node folder so those descriptors cannot
	// change the new copy. On macOS the folder has no allow entry to inherit.
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
	if err := checkSignetFolder(n.dataDir); err != nil {
		return err
	}
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
	// On macOS, if the program is killed between starting the node and recording its PID, that node must be stopped
	// by hand.
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

// Stops a node left from an earlier run, but only one that runs the private copy in the resolved data folder.
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

// The data folder has the stricter rule. Every parent folder must be owned by root or the user, and any parent
// writable by group or others needs the sticky bit to prevent other users from renaming or removing entries
// they do not own. A private ancestor does not revoke access through an existing working directory or descriptor.
func folderProblem(mode os.FileMode, owner, uid uint32, data bool) string {
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
	if mode.Perm()&0o022 != 0 && mode&os.ModeSticky == 0 {
		return "is writable by group or others without the sticky bit"
	}
	return ""
}

type folderWalk struct {
	uid             uint32
	writableParents []string
}

func (w *folderWalk) check(path string, mode os.FileMode, owner uint32, data bool) error {
	if reason := folderProblem(mode, owner, w.uid, data); reason != "" {
		if data {
			ownership := ""
			if owner != w.uid {
				ownership = "fix its ownership and "
			}
			return fmt.Errorf("data folder %q %s; %srun chmod 700 %s, or choose another folder with -datadir", path, reason, ownership, shellFolder(path))
		}
		if owner != 0 && owner != w.uid {
			return fmt.Errorf("parent folder %q %s; other users could change it; fix its ownership or choose another folder with -datadir", path, reason)
		}
		if (owner == 0 && w.uid != 0) || !mode.IsDir() {
			return fmt.Errorf("parent folder %q %s; other users could change it; choose another folder with -datadir", path, reason)
		}
		w.writableParents = append(w.writableParents, path)
	}
	if data && len(w.writableParents) != 0 {
		quoted := make([]string, len(w.writableParents))
		for i, parent := range w.writableParents {
			quoted[i] = shellFolder(parent)
		}
		if len(quoted) == 1 {
			return fmt.Errorf("parent folder %s could be changed by other users because it is writable by group or others; run chmod go-w %s or choose another folder with -datadir", quoted[0], quoted[0])
		}
		return fmt.Errorf("parent folders %s could be changed by other users because they are writable by group or others; run chmod go-w %s or choose another folder with -datadir", strings.Join(quoted, ", "), strings.Join(quoted, " "))
	}
	return nil
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
	var paths []string
	for path := resolved; ; path = filepath.Dir(path) {
		paths = append(paths, path)
		if filepath.Dir(path) == path {
			break
		}
	}
	slices.Reverse(paths)
	walk := folderWalk{uid: uint32(os.Geteuid())}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return "", fmt.Errorf("cannot inspect folder %q; choose another folder with -datadir: %w", path, err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		if err := walk.check(path, info.Mode(), stat.Uid, path == resolved); err != nil {
			return "", err
		}
		// On Linux only the data folder, where the node writes, must have no ACL; on macOS no folder on the path may
		// have an allow entry.
		if runtime.GOOS == "darwin" || path == resolved {
			if err := checkFolderACL(path); err != nil {
				return "", err
			}
		}
	}
	return resolved, nil
}

func shellFolder(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\"'\"'") + "'"
}
