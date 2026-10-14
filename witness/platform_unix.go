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
	return ok && stat.Uid == uint32(os.Getuid())
}

func privateNodeFolder(dataDir string) (string, error) {
	// A canonical data folder keeps the executed path and the recovery path identical, including /var on macOS.
	dataDir, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		return "", err
	}
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

func removeNodePID(dataDir string) { _ = os.Remove(filepath.Join(dataDir, nodePIDFile)) }

// Only an executable in this data folder may be stopped; a reused PID must never target another program.
func stopLeftoverNode(dataDir string) error {
	path := filepath.Join(dataDir, nodePIDFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
	// Resolve the data folder's aliases, but never let a symlink at node/bitcoind identify an unrelated process.
	if resolved, err := filepath.EvalSymlinks(dataDir); err == nil {
		dataDir = resolved
	}
	expected := filepath.Join(dataDir, "node", nodeName)
	if parseErr == nil && pid > 1 {
		image, err := processExecutable(pid)
		if err != nil {
			return fmt.Errorf("cannot identify the node recorded in %s: %w", path, err)
		}
		if image == expected {
			fmt.Println("Stopping a node left from an earlier run...")
			// Check again immediately before signalling, because the earlier process may already have exited.
			image, err = processExecutable(pid)
			if err != nil {
				return err
			}
			if image == expected {
				if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
					return fmt.Errorf("cannot stop the earlier node (PID %d); stop it before retrying: %w", pid, err)
				}
				if err := waitLeftoverNode(pid, expected, shutdownTimeout); err != nil {
					return err
				}
			}
		}
	}
	return os.Remove(path)
}

func waitLeftoverNode(pid int, expected string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		image, err := processExecutable(pid)
		if err != nil {
			return err
		}
		if image != expected {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the earlier node (PID %d) is still shutting down; wait for it to exit and retry", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
