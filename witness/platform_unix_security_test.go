//go:build linux || darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

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
			if problem := folderProblem(c.mode, c.owner, uid, c.data, false); (problem == "") != c.safe {
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
			if reason := folderProblem(os.ModeDir|c.mode, stat.Uid, 1000, false, false); (reason == "") != c.safe {
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

	for _, shield := range []bool{false, true} {
		walk := folderWalk{uid: uid, shield: shield}
		err := walk.check("/foreign", os.ModeDir|0o700, uid+1, false)
		if err == nil || !strings.Contains(err.Error(), "/foreign") {
			t.Fatalf("foreign owner must fail immediately, shield=%v: %v", shield, err)
		}
	}
	walk = folderWalk{uid: uid}
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

func TestShieldFolderPermissions(t *testing.T) {
	for _, c := range []struct {
		name       string
		mode       os.FileMode
		owner      uint32
		data, safe bool
	}{
		{"group writable below shield", os.ModeDir | 0o770, 1000, false, true},
		{"other writable below shield", os.ModeDir | 0o777, 1000, false, true},
		{"root group writable below shield", os.ModeDir | 0o775, 0, false, true},
		{"foreign owner below shield", os.ModeDir | 0o700, 1001, false, false},
		{"data group writable below shield", os.ModeDir | 0o770, 1000, true, false},
		{"data other writable below shield", os.ModeDir | 0o707, 1000, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if reason := folderProblem(c.mode, c.owner, 1000, c.data, true); (reason == "") != c.safe {
				t.Fatalf("safe=%v: %s", c.safe, reason)
			}
		})
	}
}

func TestFolderWalkShield(t *testing.T) {
	for _, stickyMode := range []os.FileMode{0o770, 0o777} {
		walk := folderWalk{uid: 1000}
		if err := walk.check("/tmp", os.ModeDir|os.ModeSticky|stickyMode, 0, false); err != nil {
			t.Fatalf("sticky parent above shield: %v", err)
		}
		for _, c := range []struct {
			path string
			mode os.FileMode
		}{
			{"/tmp/home", 0o750},
			{"/tmp/home/private", 0o700},
			{"/tmp/home/private/group", 0o775},
			{"/tmp/home/private/group/other", 0o777},
		} {
			if err := walk.check(c.path, os.ModeDir|c.mode, 1000, false); err != nil {
				t.Fatal(err)
			}
		}
		if err := walk.check("/tmp/home/private/group/other/data", os.ModeDir|0o700, 1000, true); err != nil {
			t.Fatalf("writable parents below shield: %v", err)
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
	if reason := folderProblem(os.ModeDir|0o700, 1001, 1000, true, false); reason == "" {
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
	// The outer 0700 folder is a shield: the write permission of the folders inside it is not open to other users.
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
	if got, err := dataFolder(alias); err != nil || got != want {
		t.Fatalf("other writable parent below shield: %q %v", got, err)
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
	if got, err := dataFolder(alias); err != nil || got != want {
		t.Fatalf("group writable parent below shield: %q %v", got, err)
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
