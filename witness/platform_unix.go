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
	"os/user"
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

// 先以非阻塞方式打开并检查描述符，避免 FIFO 或设备在读取前阻塞。
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
	data, err := io.ReadAll(io.LimitReader(f, 1024))
	if err != nil {
		return nil, fmt.Errorf("cannot read cookie %q; remove it and let the node recreate it: %w", path, err)
	}
	// 不多读第 1025 字节；读取后再次检查大小也能发现读取期间增长的文件。
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
		return nil, err
	}
	defer f.Close()
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
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		if reason := privateFileProblem(info.Mode(), fileOwner(info), uint32(os.Geteuid())); reason != "" {
			err = fmt.Errorf("unsafe lock file %q: %s; remove it before retrying", path, reason)
		}
	}
	if err != nil {
		_ = f.Close()
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
	if reason := folderProblem(info.Mode(), stat.Uid, stat.Gid, uint32(os.Geteuid()), uint32(os.Getegid()), true, false, false, false); reason != "" {
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
	// macOS 的文件 ACL 不受组权限位限制，旧副本即使通过 mode/hash 检查也可能被别人改写。
	// 每次用无 allow ACL 的 node 目录内 O_EXCL 创建的新 inode 替换它，避免继承授权。
	// Linux 的组权限位包含 ACL mask，且从已检查的描述符执行，因此可以复用。
	if runtime.GOOS == "linux" {
		if f, err := checkedPrivateCopy(path, want); err == nil {
			return f, nil
		}
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
func folderProblem(mode os.FileMode, owner, group, uid, gid uint32, data, privateGroup, accessACL, shield bool) string {
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
	if shield {
		return ""
	}
	if mode.Perm()&0o002 != 0 && mode&os.ModeSticky == 0 {
		return "could be changed by other users because it is writable by others without the sticky bit"
	}
	// sticky 公共目录（例如 /tmp）允许组和其他用户写入。
	if mode.Perm()&0o002 != 0 && mode&os.ModeSticky != 0 {
		return ""
	}
	if mode.Perm()&0o020 != 0 && (owner != uid || group != gid || !privateGroup || accessACL) {
		return "could be changed by other users because it is group-writable without a verified user-private group and no POSIX access ACL"
	}
	return ""
}

func privateGroupMembers(username, groupname string, members []string) bool {
	if username == "" || groupname != username {
		return false
	}
	for _, member := range members {
		if member != username {
			return false
		}
	}
	return true
}

func userPrivateGroup() bool {
	u, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil || u.Gid != strconv.Itoa(os.Getegid()) {
		return false
	}
	return userPrivateGroupFromFiles(u.Username, uint32(os.Geteuid()), uint32(os.Getegid()), readAccountFile)
}

func readAccountFile(path string) ([]byte, error) {
	f, err := openRegularFile(path, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func userPrivateGroupFromFiles(username string, uid, gid uint32, readFile func(string) ([]byte, error)) bool {
	if username == "" {
		return false
	}
	nsswitch, err := readFile("/etc/nsswitch.conf")
	if err != nil || !localAccountSources(string(nsswitch)) {
		return false
	}
	for _, database := range []string{"passwd", "group"} {
		data, err := readFile("/etc/" + database)
		if err != nil {
			return false
		}
		found := false
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Split(line, ":")
			count, groupIndex := 4, 2
			if database == "passwd" {
				count, groupIndex = 7, 3
			}
			if len(fields) != count || fields[0] == "" || strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
				return false
			}
			entryGroup, err := strconv.ParseUint(fields[groupIndex], 10, 32)
			if err != nil {
				return false
			}
			var entryUID uint64
			if database == "passwd" {
				entryUID, err = strconv.ParseUint(fields[2], 10, 32)
				if err != nil {
					return false
				}
			}
			if uint32(entryGroup) != gid {
				continue
			}
			if database == "passwd" {
				if fields[0] != username || uint32(entryUID) != uid {
					return false
				}
			} else {
				var members []string
				if fields[3] != "" {
					members = strings.Split(fields[3], ",")
				}
				if !privateGroupMembers(username, fields[0], members) {
					return false
				}
			}
			found = true
		}
		if !found {
			return false
		}
	}
	return true
}

func localAccountSources(text string) bool {
	found := make(map[string]bool)
	for _, line := range strings.Split(text, "\n") {
		line, _, _ = strings.Cut(line, "#")
		name, sources, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if name != "passwd" && name != "group" {
			continue
		}
		if !ok || found[name] {
			return false
		}
		// NSS 的 [action] 可以含空格；只忽略完整、非嵌套的方括号项。
		var cleaned strings.Builder
		for len(sources) > 0 {
			before, after, bracket := strings.Cut(sources, "[")
			if strings.Contains(before, "]") {
				return false
			}
			cleaned.WriteString(before)
			if !bracket {
				break
			}
			action, rest, closed := strings.Cut(after, "]")
			if !closed || strings.TrimSpace(action) == "" || strings.Contains(action, "[") {
				return false
			}
			cleaned.WriteByte(' ')
			sources = rest
		}
		tokens := strings.Fields(cleaned.String())
		if len(tokens) == 0 {
			return false
		}
		for _, source := range tokens {
			if source != "files" && source != "systemd" {
				return false
			}
		}
		found[name] = true
	}
	return found["passwd"] && found["group"]
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
	shield := false
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return "", fmt.Errorf("cannot inspect folder %q; choose another folder with -datadir: %w", path, err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		privateGroup, accessACL := false, false
		if !shield && path != resolved && info.Mode().Perm()&0o020 != 0 {
			privateGroup = userPrivateGroup()
			accessACL, err = folderHasAccessACL(path)
			if err != nil {
				return "", fmt.Errorf("cannot inspect access ACL on folder %q; other users could change it; run chmod g-w %s or choose another folder with -datadir: %w", path, shellFolder(path), err)
			}
		}
		if reason := folderProblem(info.Mode(), stat.Uid, stat.Gid, uint32(os.Geteuid()), uint32(os.Getegid()), path == resolved, privateGroup, accessACL, shield); reason != "" {
			if path == resolved {
				ownership := ""
				if stat.Uid != uint32(os.Geteuid()) {
					ownership = "fix its ownership and "
				}
				return "", fmt.Errorf("data folder %q %s; %srun chmod 700 %s, or choose another folder with -datadir", path, reason, ownership, shellFolder(path))
			}
			if stat.Uid != 0 && stat.Uid != uint32(os.Geteuid()) {
				return "", fmt.Errorf("parent folder %q %s; other users could change it; fix its ownership or choose another folder with -datadir", path, reason)
			}
			if info.Mode().Perm()&0o002 != 0 {
				return "", fmt.Errorf("parent folder %q %s; run chmod o-w %s or choose another folder with -datadir", path, reason, shellFolder(path))
			}
			return "", fmt.Errorf("parent folder %q %s; run chmod g-w %s or choose another folder with -datadir", path, reason, shellFolder(path))
		}
		if err := checkFolderACL(path); err != nil {
			return "", err
		}
		// 自己拥有且组/其他用户无 search 权限的目录阻断其他用户访问下面的路径。
		// Linux 的 POSIX ACL 组权限位是 mask，也限制所有 named 条目，故不能绕过此屏障。
		// macOS ACL 独立于 mode，必须先通过上面的检查，确认没有 allow 条目。
		if stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0o011 == 0 {
			shield = true
		}
	}
	return resolved, nil
}

func shellFolder(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\"'\"'") + "'"
}
