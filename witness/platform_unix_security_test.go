//go:build linux || darwin

package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDataEntryDecision(t *testing.T) {
	const uid = uint32(1000)
	for _, c := range []struct {
		name  string
		mode  os.FileMode
		owner uint32
		links uint64
		safe  bool
	}{
		{"regular", 0o644, uid, 1, true},
		{"directory", os.ModeDir | 0o755, uid, 3, true},
		{"foreign file", 0o600, uid + 1, 1, false},
		{"foreign directory", os.ModeDir | 0o700, uid + 1, 2, false},
		{"hard link", 0o600, uid, 2, false},
		{"unlinked file", 0o600, uid, 0, false},
		{"symlink", os.ModeSymlink | 0o777, uid, 1, false},
		{"fifo", os.ModeNamedPipe | 0o600, uid, 1, false},
		{"socket", os.ModeSocket | 0o600, uid, 1, false},
		{"device", os.ModeDevice | 0o600, uid, 1, false},
		{"group writable file", 0o620, uid, 1, false},
		{"other writable file", 0o602, uid, 1, false},
		{"group writable directory", os.ModeDir | 0o770, uid, 2, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if reason := dataEntryProblem(c.mode, c.owner, uid, c.links); (reason == "") != c.safe {
				t.Fatalf("safe=%v: %s", c.safe, reason)
			}
		})
	}
}

func checkDataTreeAdvice(t *testing.T, dataDir, path string, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("unsafe data tree was accepted")
	}
	for _, text := range []string{path, "another user may have placed or changed it", "delete the data folder " + shellFolder(dataDir), "sync again", "-datadir"} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("missing %q in error: %v", text, err)
		}
	}
}

func TestDataTreeRejectsUnsafeEntries(t *testing.T) {
	for _, kind := range []string{"symlink", "hard link", "fifo", "group writable file", "group writable directory"} {
		t.Run(kind, func(t *testing.T) {
			dir := filepath.Join(privateTempDir(t), "data's folder")
			if err := os.MkdirAll(filepath.Join(dir, "signet"), 0o700); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(privateTempDir(t), "outside")
			writeTestFile(t, outside, "keep this private key", 0o600)
			path := filepath.Join(dir, "signet", "bitcoind.pid")
			switch kind {
			case "symlink":
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			case "hard link":
				if err := os.Link(outside, path); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			case "group writable file":
				writeTestFile(t, path, "unsafe", 0o620)
			case "group writable directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0o770); err != nil {
					t.Fatal(err)
				}
			}
			checkDataTreeAdvice(t, dir, path, checkDataTree(dir))
			output, err := runWitnessStartup(t, dir)
			if err == nil || !strings.Contains(output, path) || !strings.Contains(output, "another user may have placed or changed it") {
				t.Fatalf("unsafe tree did not stop startup: %v\n%s", err, output)
			}
			if data, err := os.ReadFile(outside); err != nil || string(data) != "keep this private key" {
				t.Fatalf("outside file changed: %q %v", data, err)
			}
			if starts(dir) != 0 {
				t.Fatal("a node was started with an unsafe data tree")
			}
		})
	}
}

func TestDataTreeAfterFakeNodeRun(t *testing.T) {
	dir := privateTempDir(t)
	release, err := lockInstance(filepath.Join(dir, lockFile))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := stopLeftoverNode(dir, "unused"); err != nil {
		t.Fatal(err)
	}
	if err := checkDataTree(dir); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	n := fakeNodeProcess(t, "ready", dir, fileHash(t, exe))
	n.start()
	if !eventually(10*time.Second, func() bool { return n.client() != nil }) {
		t.Fatal("fake node did not become ready")
	}
	// Keep the real cookie after shutdown, as Core may leave one after an interrupted run.
	cookie, err := readCookie(cookiePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	shutdownNode(n, 3*time.Second)
	if !stoppedWithin(n, 5*time.Second) {
		t.Fatal("fake node did not stop")
	}
	// A page file left by an older version is still part of the data folder scan.
	writeTestFile(t, filepath.Join(dir, pageFile), "saved page", 0o600)
	if err := writeNodePID(dir, 123); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, cookiePath(dir), string(cookie), 0o600)
	for _, path := range []string{"node", filepath.Join("node", nodeName), nodePIDFile, pageFile, "signet", filepath.Join("signet", ".cookie")} {
		if _, err := os.Lstat(filepath.Join(dir, path)); err != nil {
			t.Fatal(err)
		}
	}
	if err := checkDataTree(dir); err != nil {
		t.Fatalf("safe tree after a full fake node run: %v", err)
	}
}

func TestLockFileDecision(t *testing.T) {
	for _, c := range []struct {
		name  string
		mode  os.FileMode
		owner uint32
		links uint64
		safe  bool
	}{
		{"private", 0o600, 1000, 1, true},
		{"foreign owner", 0o600, 1001, 1, false},
		{"two links", 0o600, 1000, 2, false},
		{"group read", 0o640, 1000, 1, false},
		{"group write", 0o620, 1000, 1, false},
		{"group execute", 0o610, 1000, 1, false},
		{"other read", 0o604, 1000, 1, false},
		{"directory", os.ModeDir | 0o700, 1000, 2, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if reason := instanceFileProblem(c.mode, c.owner, 1000, c.links); (reason == "") != c.safe {
				t.Fatalf("safe=%v: %s", c.safe, reason)
			}
		})
	}
}

func TestUnsafeLockFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "hard link", "group read", "group write", "group execute", "fifo", "directory"} {
		t.Run(kind, func(t *testing.T) {
			dir := privateTempDir(t)
			path := filepath.Join(dir, lockFile)
			target := filepath.Join(privateTempDir(t), "target")
			url := "http://127.0.0.1:40123/" + strings.Repeat("ab", 32) + "/"
			writeTestFile(t, target, url, 0o600)
			switch kind {
			case "symlink":
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "hard link":
				if err := os.Link(target, path); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			default:
				mode := os.FileMode(0o640)
				if kind == "group write" {
					mode = 0o620
				} else if kind == "group execute" {
					mode = 0o610
				}
				writeTestFile(t, path, url, mode)
			}
			release, err := lockInstance(path)
			if release != nil {
				release()
			}
			if err == nil || errors.Is(err, errInstanceRunning) || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "delete") {
				t.Fatalf("unsafe lock must be an error, not a busy instance: %v", err)
			}
			writeTestFile(t, filepath.Join(dir, pageFile), url, 0o600)
			output, err := runWitnessStartup(t, dir)
			if err == nil || !strings.Contains(output, path) || strings.Contains(output, url) {
				t.Fatalf("unsafe instance file reached the page: %v\n%s", err, output)
			}
		})
	}
}

func checkBusyInstanceOutput(t *testing.T, output string, err error) {
	t.Helper()
	if err != nil || !strings.Contains(output, consoleTexts["en"].AlreadyElsewhere) ||
		strings.Contains(output, "http://") || strings.Contains(output, "https://") || strings.Contains(output, "unexpected browser launch") {
		t.Fatalf("busy instance must show only the other-window notice: %v\n%s", err, output)
	}
}

func TestBusyInstanceDoesNotReadPageFile(t *testing.T) {
	for _, kind := range []string{"retained descriptors", "fifo", "missing"} {
		t.Run(kind, func(t *testing.T) {
			dir := privateTempDir(t)
			lockPath := filepath.Join(dir, lockFile)
			writeTestFile(t, lockPath, "", 0o666)
			lock, err := os.OpenFile(lockPath, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if err := os.Chmod(lockPath, 0o600); err != nil {
				t.Fatal(err)
			}
			// Tightened permissions do not revoke the descriptor from an earlier exposure.
			if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, pageFile)
			url := "http://127.0.0.1:40123/" + strings.Repeat("ab", 32) + "/\n"
			var pipe *os.File
			switch kind {
			case "retained descriptors":
				writeTestFile(t, path, "", 0o666)
				writer, err := os.OpenFile(path, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer writer.Close()
				if err := os.Chmod(path, 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := writer.WriteString(url); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
				pipe, err = os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer pipe.Close()
				if _, err := pipe.WriteString(url); err != nil {
					t.Fatal(err)
				}
			}
			output, err := runWitnessStartup(t, dir)
			checkBusyInstanceOutput(t, output, err)
			if pipe != nil {
				// A read would consume these bytes, even if the URL were then discarded.
				fd := int(pipe.Fd())
				if err := syscall.SetNonblock(fd, true); err != nil {
					t.Fatal(err)
				}
				data := make([]byte, len(url))
				if n, err := syscall.Read(fd, data); err != nil || n != len(url) || string(data) != url {
					t.Fatalf("page FIFO was read: %q %v", data, err)
				}
			}
			if starts(dir) != 0 {
				t.Fatal("a busy instance started a node")
			}
		})
	}
}

func TestBusyInstanceDoesNotScanDataTree(t *testing.T) {
	dir := privateTempDir(t)
	release, err := lockInstance(filepath.Join(dir, lockFile))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if again, err := lockInstance(filepath.Join(dir, lockFile)); !errors.Is(err, errInstanceRunning) {
		if again != nil {
			again()
		}
		t.Fatalf("busy flock did not identify a second instance: %v", err)
	}
	url := "http://127.0.0.1:40123/" + strings.Repeat("ab", 32) + "/"
	writeTestFile(t, filepath.Join(dir, pageFile), url, 0o600)
	if err := syscall.Mkfifo(filepath.Join(dir, "unsafe-entry"), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := runWitnessStartup(t, dir)
	checkBusyInstanceOutput(t, output, err)
	if strings.Contains(output, "unsafe data folder entry") {
		t.Fatalf("second instance scanned the data tree: %s", output)
	}
}

func runWitnessStartup(t *testing.T, dir string) (string, error) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestWitnessStartupHelper$")
	cmd.Env = append(os.Environ(), "WITNESS_FAKE_NODE=", "WITNESS_STARTUP_HELPER="+dir)
	cmd.Stdin = strings.NewReader("\n")
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("witness startup did not finish: %v\n%s", ctx.Err(), output)
	}
	return string(output), err
}

func TestWitnessStartupHelper(t *testing.T) {
	dir := os.Getenv("WITNESS_STARTUP_HELPER")
	if dir == "" {
		return
	}
	flag.CommandLine = flag.NewFlagSet("witness", flag.ExitOnError)
	os.Args = []string{os.Args[0], "-datadir", dir, "-bitcoind", os.Args[0], "-lang", "en"}
	if kind := os.Getenv("WITNESS_STARTUP_NODE"); kind != "" {
		t.Setenv("WITNESS_FAKE_NODE", kind)
		os.Args = append(os.Args, "-no-browser")
	}
	if code := runWithBrowser(func(string) error { panic("unexpected browser launch") }); code != 0 {
		os.Exit(code)
	}
}

func TestStartupDoesNotWritePageFile(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(strconv.FormatBool(saved), func(t *testing.T) {
			dir := privateTempDir(t)
			path := filepath.Join(dir, pageFile)
			if saved {
				writeTestFile(t, path, "page from an older version", 0o600)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestWitnessStartupHelper$")
			cmd.Env = append(os.Environ(), "WITNESS_FAKE_NODE=", "WITNESS_STARTUP_HELPER="+dir, "WITNESS_STARTUP_NODE=ready")
			cmd.Stdin = strings.NewReader("\n")
			cmd.Stderr = os.Stderr
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = cmd.Process.Signal(syscall.SIGTERM)
				if err := cmd.Wait(); err != nil {
					t.Errorf("startup helper: %v", err)
				}
				if saved {
					if data, err := os.ReadFile(path); err != nil || string(data) != "page from an older version" {
						t.Errorf("saved page was changed or removed: %q %v", data, err)
					}
				} else if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Errorf("shutdown left a page file: %v", err)
				}
			}()
			scanner := bufio.NewScanner(output)
			for scanner.Scan() {
				if scanner.Text() != consoleTexts["en"].KeepOpen {
					continue
				}
				if saved {
					if data, err := os.ReadFile(path); err != nil || string(data) != "page from an older version" {
						t.Fatalf("startup changed the saved page: %q %v", data, err)
					}
				} else if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("startup wrote a page file: %v", err)
				}
				return
			}
			t.Fatalf("startup did not reach the running notice: %v; %v", scanner.Err(), ctx.Err())
		})
	}
}

func checkCookieOwnerAndLinks(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if !ownedByUser(info) || stat.Nlink != 1 {
		t.Fatalf("cookie owner=%d links=%d, want effective uid=%d and one link", stat.Uid, stat.Nlink, os.Geteuid())
	}
}

func TestFolderPermissions(t *testing.T) {
	const uid = uint32(1000)
	for _, c := range []struct {
		name       string
		mode       os.FileMode
		owner      uint32
		data, safe bool
	}{
		{"private data", os.ModeDir | 0o700, uid, true, true},
		{"readable data", os.ModeDir | 0o755, uid, true, true},
		{"root data", os.ModeDir | 0o700, 0, true, false},
		{"foreign data", os.ModeDir | 0o700, uid + 1, true, false},
		{"group writable data", os.ModeDir | 0o770, uid, true, false},
		{"sticky data", os.ModeDir | os.ModeSticky | 0o777, uid, true, false},
		{"other writable data", os.ModeDir | 0o702, uid, true, false},
		{"data file", 0o700, uid, true, false},
		{"root parent", os.ModeDir | 0o755, 0, false, true},
		{"private parent", os.ModeDir | 0o700, uid, false, true},
		{"foreign parent", os.ModeDir | 0o755, uid + 1, false, false},
		{"group writable parent", os.ModeDir | 0o775, uid, false, false},
		{"root group writable", os.ModeDir | 0o775, 0, false, false},
		{"other writable parent", os.ModeDir | 0o777, uid, false, false},
		{"sticky root parent", os.ModeDir | os.ModeSticky | 0o777, 0, false, true},
		{"sticky user parent", os.ModeDir | os.ModeSticky | 0o777, uid, false, true},
		{"sticky foreign parent", os.ModeDir | os.ModeSticky | 0o777, uid + 1, false, false},
		{"sticky group writable parent", os.ModeDir | os.ModeSticky | 0o770, uid, false, true},
		{"parent file", 0o700, uid, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if problem := folderProblem(c.mode, c.owner, uid, c.data); (problem == "") != c.safe {
				t.Fatalf("safe=%v: %s", c.safe, problem)
			}
		})
	}
}

func TestFolderGroupDoesNotMatter(t *testing.T) {
	for _, gid := range []uint32{0, 1000, 1001} {
		stat := syscall.Stat_t{Uid: 1000, Gid: gid}
		for _, c := range []struct {
			mode os.FileMode
			safe bool
		}{
			{0o755, true},
			{0o775, false},
			{os.ModeSticky | 0o775, true},
		} {
			if reason := folderProblem(os.ModeDir|c.mode, stat.Uid, 1000, false); (reason == "") != c.safe {
				t.Fatalf("gid=%d mode=%v safe=%v: %s", gid, c.mode, c.safe, reason)
			}
		}
	}
}

func TestFolderWalk(t *testing.T) {
	const uid = uint32(1000)
	local := "/home/alice/.local"
	share := local + "/share"
	walk := folderWalk{uid: uid}
	for _, c := range []struct {
		path string
		mode os.FileMode
	}{
		{"/home/alice", 0o750},
		{local, 0o775},
		{share, 0o775},
	} {
		if err := walk.check(c.path, os.ModeDir|c.mode, uid, false); err != nil {
			t.Fatalf("must collect unsafe parents until the data folder: %v", err)
		}
	}
	err := walk.check(share+"/data", os.ModeDir|0o700, uid, true)
	command := "chmod go-w " + shellFolder(local) + " " + shellFolder(share)
	if err == nil || !strings.Contains(err.Error(), command) || strings.Count(err.Error(), "chmod") != 1 ||
		!strings.Contains(err.Error(), "could be changed by other users because they are writable by group or others") ||
		!strings.Contains(err.Error(), "-datadir") {
		t.Fatalf("unsafe parents: %v; want one command %s", err, command)
	}
	if !strings.HasPrefix(err.Error(), "parent folders ") {
		t.Fatalf("multiple parents need a plural error: %v", err)
	}
	walk = folderWalk{uid: uid}
	if err := walk.check(local, os.ModeDir|0o775, uid, false); err != nil {
		t.Fatal(err)
	}
	err = walk.check(local+"/data", os.ModeDir|0o700, uid, true)
	want := "parent folder '" + local + "' could be changed by other users because it is writable by group or others; run chmod go-w '" + local + "' or choose another folder with -datadir"
	if err == nil || err.Error() != want {
		t.Fatalf("single parent: %v; want %s", err, want)
	}

	walk = folderWalk{uid: uid}
	if err := walk.check("/private", os.ModeDir|0o700, uid, false); err != nil {
		t.Fatal(err)
	}
	err = walk.check("/private/foreign", os.ModeDir|0o700, uid+1, false)
	if err == nil || !strings.Contains(err.Error(), "/private/foreign") {
		t.Fatalf("foreign owner must fail immediately: %v", err)
	}
	walk = folderWalk{uid: uid}
	if err := walk.check("/home/alice", os.ModeDir|0o700, uid, false); err != nil {
		t.Fatal(err)
	}
	if err := walk.check(local, os.ModeDir|0o775, uid, false); err != nil {
		t.Fatal(err)
	}
	err = walk.check("/root-parent", os.ModeDir|0o775, 0, false)
	if err == nil || !strings.Contains(err.Error(), "/root-parent") ||
		!strings.Contains(err.Error(), "other users could change it") ||
		!strings.Contains(err.Error(), "-datadir") || strings.Contains(err.Error(), "chmod") {
		t.Fatalf("root parent must fail immediately without chmod advice: %v", err)
	}
}

func TestFolderWalkRootOwnedParent(t *testing.T) {
	const parent = "/root-parent"
	for _, uid := range []uint32{0, 1000} {
		t.Run("uid "+strconv.Itoa(int(uid)), func(t *testing.T) {
			walk := folderWalk{uid: uid}
			err := walk.check(parent, os.ModeDir|0o775, 0, false)
			if uid != 0 {
				if err == nil || !strings.Contains(err.Error(), parent) ||
					!strings.Contains(err.Error(), "-datadir") || strings.Contains(err.Error(), "chmod") {
					t.Fatalf("root-owned parent must fail immediately without chmod advice: %v", err)
				}
				if len(walk.writableParents) != 0 {
					t.Fatalf("must not collect another owner's parent: %v", walk.writableParents)
				}
				return
			}
			if err != nil {
				t.Fatalf("must collect the effective user's writable parent: %v", err)
			}
			if len(walk.writableParents) != 1 || walk.writableParents[0] != parent {
				t.Fatalf("writable parents: %v; want [%s]", walk.writableParents, parent)
			}
			err = walk.check(parent+"/data", os.ModeDir|0o700, uid, true)
			command := "chmod go-w " + shellFolder(parent)
			if err == nil || !strings.Contains(err.Error(), command) || !strings.Contains(err.Error(), "-datadir") {
				t.Fatalf("root-owned parent must have chmod advice for root: %v; want %s", err, command)
			}
		})
	}
}

func TestFolderWalkBelowPrivateParent(t *testing.T) {
	for _, stickyMode := range []os.FileMode{0o770, 0o777} {
		walk := folderWalk{uid: 1000}
		if err := walk.check("/tmp", os.ModeDir|os.ModeSticky|stickyMode, 0, false); err != nil {
			t.Fatalf("sticky parent: %v", err)
		}
		for _, c := range []struct {
			path string
			mode os.FileMode
		}{
			{"/tmp/home", 0o750},
			{"/tmp/home/private", 0o700},
			{"/tmp/home/private/group", 0o770},
			{"/tmp/home/private/group/other", 0o707},
		} {
			if err := walk.check(c.path, os.ModeDir|c.mode, 1000, false); err != nil {
				t.Fatal(err)
			}
		}
		err := walk.check("/tmp/home/private/group/other/data", os.ModeDir|0o700, 1000, true)
		command := "chmod go-w '/tmp/home/private/group' '/tmp/home/private/group/other'"
		if err == nil || !strings.Contains(err.Error(), command) || strings.Count(err.Error(), "chmod") != 1 {
			t.Fatalf("writable parents below private parent: %v; want one command %s", err, command)
		}
		walk = folderWalk{uid: 1000}
		if err := walk.check("/private", os.ModeDir|0o700, 1000, false); err != nil {
			t.Fatal(err)
		}
		if err := walk.check("/private/sticky", os.ModeDir|os.ModeSticky|stickyMode, 1000, false); err != nil {
			t.Fatal(err)
		}
		if err := walk.check("/private/sticky/data", os.ModeDir|0o700, 1000, true); err != nil {
			t.Fatalf("sticky parent below private parent: %v", err)
		}
	}
}

func TestShellFolder(t *testing.T) {
	for path, want := range map[string]string{
		"/home/alice/data": "'/home/alice/data'",
		"/home/alice/a b":  "'/home/alice/a b'",
		"/home/alice/a'b":  "'/home/alice/a'\"'\"'b'",
		"/home/alice/$()":  "'/home/alice/$()'",
	} {
		if got := shellFolder(path); got != want {
			t.Errorf("%s: %s, want %s", strconv.Quote(path), got, want)
		}
	}
}

func TestSignetFolder(t *testing.T) {
	if reason := folderProblem(os.ModeDir|0o700, 1001, 1000, true); reason == "" {
		t.Fatal("accepted a signet folder owned by another uid")
	}
	for _, kind := range []string{"symlink", "group writable", "other writable", "file", "safe", "missing"} {
		t.Run(kind, func(t *testing.T) {
			dir := privateTempDir(t)
			path := filepath.Join(dir, "signet")
			target := privateTempDir(t)
			switch kind {
			case "symlink":
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "file":
				writeTestFile(t, path, "", 0o600)
			case "missing":
			default:
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				mode := os.FileMode(0o700)
				if kind == "group writable" {
					mode = 0o770
				} else if kind == "other writable" {
					mode = 0o702
				}
				if err := os.Chmod(path, mode); err != nil {
					t.Fatal(err)
				}
			}
			err := checkSignetFolder(dir)
			if kind == "safe" || kind == "missing" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "chmod 700") {
				t.Fatalf("unsafe signet: %v", err)
			}
			if kind == "symlink" || kind == "group writable" || kind == "other writable" {
				writeTestFile(t, cookiePath(dir), "keep", 0o600)
				n := newNode(filepath.Join(dir, "missing-node"), "", dir)
				if err := n.startNode(exec.Command(n.exe)); err == nil || !strings.Contains(err.Error(), path) {
					t.Fatalf("unsafe signet before node start: %v", err)
				}
				if _, err := n.runOnce(1); err == nil || !strings.Contains(err.Error(), path) {
					t.Fatalf("unsafe signet before cookie removal: %v", err)
				}
				if data, err := os.ReadFile(cookiePath(dir)); err != nil || string(data) != "keep" {
					t.Fatalf("old cookie was touched: %q %v", data, err)
				}
			}
		})
	}
}

func TestCookieFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "fifo", "oversized", "directory", "valid", "limit"} {
		t.Run(kind, func(t *testing.T) {
			dir := privateTempDir(t)
			path := filepath.Join(dir, ".cookie")
			switch kind {
			case "symlink":
				target := filepath.Join(dir, "target")
				writeTestFile(t, target, "cookie", 0o600)
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			default:
				content := "cookie"
				if kind == "oversized" {
					content = strings.Repeat("x", 1025)
				} else if kind == "limit" {
					content = strings.Repeat("x", 1024)
				}
				writeTestFile(t, path, content, 0o600)
			}
			done := make(chan error, 1)
			go func() { _, err := readCookie(path); done <- err }()
			select {
			case err := <-done:
				if kind == "valid" || kind == "limit" {
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "remove") {
					t.Fatalf("unsafe cookie: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cookie open or read blocked")
			}
		})
	}
}

func TestCookiePermissions(t *testing.T) {
	for _, mode := range []os.FileMode{0o640, 0o604, 0o600} {
		t.Run(strconv.FormatUint(uint64(mode), 8), func(t *testing.T) {
			path := filepath.Join(privateTempDir(t), ".cookie")
			writeTestFile(t, path, "cookie", 0o600)
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			data, err := readCookie(path)
			if mode == 0o600 {
				if err != nil || string(data) != "cookie" {
					t.Fatalf("private cookie: %q %v", data, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), path) ||
				!strings.Contains(err.Error(), "could be read by other users") ||
				!strings.Contains(err.Error(), "remove it and let the node recreate it") {
				t.Fatalf("readable cookie: %q %v", data, err)
			}
		})
	}
}

func TestShippedNodeFIFO(t *testing.T) {
	path := filepath.Join(privateTempDir(t), nodeName)
	if err := syscall.Mkfifo(path, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := privateTempDir(t)
	done := make(chan error, 1)
	go func() {
		if err := checkNodeProgram(path, "hash"); err == nil || !strings.Contains(err.Error(), path) {
			done <- err
			return
		}
		f, err := prepareNodeProgram(path, "hash", dir)
		if f != nil {
			_ = f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), path) {
			t.Fatalf("FIFO node: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("opening a shipped FIFO node blocked")
	}
}

func TestDataFolderBoundary(t *testing.T) {
	root := filepath.Join(privateTempDir(t), "parent")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := dataFolder(alias); err != nil || got != want {
		t.Fatalf("resolved folder: %q %v; want %q", got, err, want)
	}
	for _, mode := range []os.FileMode{0o770, 0o707} {
		if err := os.Chmod(target, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := dataFolder(alias); err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "chmod 700") {
			t.Fatalf("unsafe data: %v", err)
		}
	}
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := dataFolder(alias); err == nil || !strings.Contains(err.Error(), "chmod go-w "+shellFolder(resolvedRoot)) {
		t.Fatalf("other writable parent below private parent: %v", err)
	}
	if err := os.Chmod(root, os.ModeSticky|0o777); err != nil {
		t.Fatal(err)
	}
	if got, err := dataFolder(alias); err != nil || got != want {
		t.Fatalf("sticky parent: %q %v", got, err)
	}
	if err := os.Chmod(root, 0o770); err != nil {
		t.Fatal(err)
	}
	if _, err := dataFolder(alias); err == nil || !strings.Contains(err.Error(), "chmod go-w "+shellFolder(resolvedRoot)) {
		t.Fatalf("group writable parent below private parent: %v", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "file")
	writeTestFile(t, file, "", 0o600)
	if _, err := dataFolder(file); err == nil {
		t.Fatal("accepted a file as data folder")
	}
	for _, char := range []string{"\n", "\t", "\r", "\x00", "\x1b", "\x7f", "\u0085"} {
		path := root + "/control" + char
		if _, err := dataFolder(path); err == nil || !strings.Contains(err.Error(), "control character") || !strings.Contains(err.Error(), "-datadir") {
			t.Fatalf("control character %q: %v", char, err)
		}
		if _, err := dataFolder(path + "/../clean"); err == nil {
			t.Fatal("control character disappeared during cleaning")
		}
	}
	controlTarget := filepath.Join(root, "target\t")
	if err := os.Mkdir(controlTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(controlTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	controlAlias := filepath.Join(root, "control-alias")
	if err := os.Symlink(controlTarget, controlAlias); err != nil {
		t.Fatal(err)
	}
	if _, err := dataFolder(controlAlias); err == nil || !strings.Contains(err.Error(), "control character") {
		t.Fatalf("resolved control character: %v", err)
	}
}

func TestUnixDefaultHome(t *testing.T) {
	for _, home := range []string{"", "relative"} {
		for _, xdg := range []string{"", "relative"} {
			t.Setenv("HOME", home)
			t.Setenv("XDG_DATA_HOME", xdg)
			if _, err := defaultDataHome(); err == nil || !strings.Contains(err.Error(), "set HOME or use -datadir") {
				t.Fatalf("HOME=%q XDG=%q: %v", home, xdg, err)
			}
		}
	}
	t.Setenv("HOME", privateTempDir(t))
	if _, err := defaultDataHome(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", "")
	if _, err := dataFolder(filepath.Join(privateTempDir(t), "explicit")); err != nil {
		t.Fatalf("explicit datadir without HOME: %v", err)
	}
}

func TestNodePIDFiles(t *testing.T) {
	for _, content := range []string{"", "junk", "0\n", "1\n", "-2\n", "4194304\n", "999999999999999999999999999999999", strings.Repeat("2", 33), "123\nextra", "+123", " 123 ", strings.Repeat("0", 33) + "2"} {
		t.Run(content, func(t *testing.T) {
			dir := privateTempDir(t)
			path := filepath.Join(dir, nodePIDFile)
			writeTestFile(t, path, content, 0o600)
			if pid, err := readNodePID(path); err != nil || pid != 0 {
				t.Fatalf("malformed: %d %v", pid, err)
			}
			if err := stopLeftoverNode(dir, "unused"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("stale PID remains: %v", err)
			}
		})
	}
	for _, content := range []string{"2", "123\n", "4194303\n"} {
		path := filepath.Join(privateTempDir(t), nodePIDFile)
		writeTestFile(t, path, content, 0o600)
		if pid, err := readNodePID(path); err != nil || pid <= 1 {
			t.Fatalf("valid: %d %v", pid, err)
		}
	}
	for _, kind := range []string{"symlink", "dangling symlink", "fifo", "directory", "group writable", "other writable"} {
		t.Run(kind, func(t *testing.T) {
			dir := privateTempDir(t)
			path := filepath.Join(dir, nodePIDFile)
			switch kind {
			case "symlink", "dangling symlink":
				target := filepath.Join(dir, "target")
				if kind == "symlink" {
					writeTestFile(t, target, "123", 0o600)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			default:
				mode := os.FileMode(0o620)
				if kind == "other writable" {
					mode = 0o602
				}
				writeTestFile(t, path, "123", mode)
			}
			if err := stopLeftoverNode(dir, "unused"); err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("unsafe PID: %v", err)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("unsafe PID was removed: %v", err)
			}
		})
	}
	if problem := privateFileProblem(0o600, 1001, 1000); problem == "" {
		t.Fatal("foreign PID owner accepted")
	}
	dir := privateTempDir(t)
	if err := removeNodePID(dir); err != nil {
		t.Fatalf("absent PID removal: %v", err)
	}
	writeTestFile(t, filepath.Join(dir, nodePIDFile), "junk", 0o600)
	if err := removeNodePID(dir); err != nil {
		t.Fatal(err)
	}
	if err := removeNodePID(dir); err != nil {
		t.Fatalf("second removal: %v", err)
	}
}
