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
	const uid, gid = uint32(1000), uint32(1000)
	for _, c := range []struct {
		name         string
		mode         os.FileMode
		owner, group uint32
		data, safe   bool
	}{
		{"private data", os.ModeDir | 0o700, uid, gid, true, true},
		{"readable data", os.ModeDir | 0o755, uid, gid, true, true},
		{"root data", os.ModeDir | 0o700, 0, gid, true, false},
		{"foreign data", os.ModeDir | 0o700, uid + 1, gid, true, false},
		{"group writable data", os.ModeDir | 0o770, uid, gid, true, false},
		{"sticky data", os.ModeDir | os.ModeSticky | 0o777, uid, gid, true, false},
		{"other writable data", os.ModeDir | 0o702, uid, gid, true, false},
		{"data file", 0o700, uid, gid, true, false},
		{"root parent", os.ModeDir | 0o755, 0, 0, false, true},
		{"private parent", os.ModeDir | 0o700, uid, gid, false, true},
		{"foreign parent", os.ModeDir | 0o755, uid + 1, gid, false, false},
		{"user private group", os.ModeDir | 0o775, uid, gid, false, true},
		{"foreign group", os.ModeDir | 0o775, uid, gid + 1, false, false},
		{"root group writable", os.ModeDir | 0o775, 0, gid, false, false},
		{"other writable parent", os.ModeDir | 0o777, uid, gid, false, false},
		{"sticky root parent", os.ModeDir | os.ModeSticky | 0o777, 0, 0, false, true},
		{"sticky user parent", os.ModeDir | os.ModeSticky | 0o777, uid, gid, false, true},
		{"sticky foreign parent", os.ModeDir | os.ModeSticky | 0o777, uid + 1, gid, false, false},
		{"sticky foreign group only", os.ModeDir | os.ModeSticky | 0o770, uid, gid + 1, false, false},
		{"parent file", 0o700, uid, gid, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if problem := folderProblem(c.mode, c.owner, c.group, uid, gid, c.data, true, false, false); (problem == "") != c.safe {
				t.Fatalf("safe=%v: %s", c.safe, problem)
			}
		})
	}
}

func TestUserPrivateGroup(t *testing.T) {
	for _, c := range []struct {
		name, username, groupname string
		members                   []string
		acl, safe                 bool
	}{
		{"empty private group", "alice", "alice", nil, false, true},
		{"self member", "alice", "alice", []string{"alice"}, false, true},
		{"shared staff", "alice", "staff", nil, false, false},
		{"shared users", "alice", "users", []string{"alice", "bob"}, false, false},
		{"extra member", "alice", "alice", []string{"alice", "bob"}, false, false},
		{"different name", "alice", "other", nil, false, false},
		{"lookup failed", "", "", nil, false, false},
		{"Linux access ACL", "alice", "alice", nil, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			private := privateGroupMembers(c.username, c.groupname, c.members)
			problem := folderProblem(os.ModeDir|0o770, 1000, 1000, 1000, 1000, false, private, c.acl, false)
			if (problem == "") != c.safe {
				t.Fatalf("safe=%v: %s", c.safe, problem)
			}
		})
	}
}

func TestPrivateGroupAccountFiles(t *testing.T) {
	const passwd = "alice:x:1000:1000::/home/alice:/bin/sh\nbob:x:1001:1001::/home/bob:/bin/sh\n"
	const group = "alice:x:1000:alice\nbob:x:1001:\n"
	for _, c := range []struct {
		name, nsswitch, passwd, group, missing string
		safe                                   bool
	}{
		{"private", "passwd: files systemd\ngroup: files systemd\n", passwd, group, "", true},
		{"empty members", "passwd: files\ngroup: files\n", passwd, "alice:x:1000:\n", "", true},
		{"actions and comments", "passwd: files [NOTFOUND=return SUCCESS=continue] systemd # local\ngroup: files [SUCCESS=return]\n", passwd, group, "", true},
		{"shared primary GID", "passwd: files\ngroup: files\n", passwd + "eve:x:1002:1000::/home/eve:/bin/sh\n", group, "", false},
		{"same name different UID", "passwd: files\ngroup: files\n", passwd + "alice:x:1002:1000::/home/eve:/bin/sh\n", group, "", false},
		{"same UID different name", "passwd: files\ngroup: files\n", passwd + "eve:x:1000:1000::/home/eve:/bin/sh\n", group, "", false},
		{"group alias", "passwd: files\ngroup: files\n", passwd, group + "alias:x:1000:\n", "", false},
		{"extra member", "passwd: files\ngroup: files\n", passwd, "alice:x:1000:alice,eve\n", "", false},
		{"ldap passwd", "passwd: files ldap\ngroup: files\n", passwd, group, "", false},
		{"sss group", "passwd: files\ngroup: files sss\n", passwd, group, "", false},
		{"missing nsswitch", "", passwd, group, "/etc/nsswitch.conf", false},
		{"missing passwd", "passwd: files\ngroup: files\n", "", group, "/etc/passwd", false},
		{"missing group", "passwd: files\ngroup: files\n", passwd, "", "/etc/group", false},
		{"NIS passwd include", "passwd: files\ngroup: files\n", passwd + "+::::::\n", group, "", false},
		{"NIS passwd exclude", "passwd: files\ngroup: files\n", passwd + "-eve::::::\n", group, "", false},
		{"NIS group include", "passwd: files\ngroup: files\n", passwd, group + "+:::\n", "", false},
		{"NIS group exclude", "passwd: files\ngroup: files\n", passwd, group + "-eve:::\n", "", false},
		{"bad passwd fields", "passwd: files\ngroup: files\n", passwd + "bad\n", group, "", false},
		{"bad passwd UID", "passwd: files\ngroup: files\n", passwd + "eve:x:bad:1001::/:/bin/sh\n", group, "", false},
		{"bad passwd GID", "passwd: files\ngroup: files\n", passwd + "eve:x:1002:bad::/:/bin/sh\n", group, "", false},
		{"bad group fields", "passwd: files\ngroup: files\n", passwd, group + "bad\n", "", false},
		{"bad group GID", "passwd: files\ngroup: files\n", passwd, group + "eve:x:bad:\n", "", false},
		{"missing own passwd", "passwd: files\ngroup: files\n", "bob:x:1001:1001::/:/bin/sh\n", group, "", false},
		{"missing own group", "passwd: files\ngroup: files\n", passwd, "bob:x:1001:\n", "", false},
		{"missing group sources", "passwd: files\n", passwd, group, "", false},
		{"empty passwd sources", "passwd:\ngroup: files\n", passwd, group, "", false},
		{"bad action", "passwd: files [NOTFOUND=return\ngroup: files\n", passwd, group, "", false},
		{"duplicate sources", "passwd: files\npasswd: ldap\ngroup: files\n", passwd, group, "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{"/etc/nsswitch.conf": c.nsswitch, "/etc/passwd": c.passwd, "/etc/group": c.group}
			read := func(path string) ([]byte, error) {
				if path == c.missing {
					return nil, os.ErrNotExist
				}
				text, ok := files[path]
				if !ok {
					t.Fatalf("unexpected account file %q", path)
				}
				return []byte(text), nil
			}
			private := userPrivateGroupFromFiles("alice", 1000, 1000, read)
			if private != c.safe {
				t.Fatalf("private=%v, want %v", private, c.safe)
			}
			// 位于屏障上方的组可写目录必须通过完整的私有组证明。
			if problem := folderProblem(os.ModeDir|0o770, 1000, 1000, 1000, 1000, false, private, false, false); (problem == "") != c.safe {
				t.Fatalf("group writable above shield: %s", problem)
			}
		})
	}
	for _, path := range []string{"/etc/nsswitch.conf", "/etc/passwd", "/etc/group"} {
		t.Run("unreadable "+path, func(t *testing.T) {
			read := func(name string) ([]byte, error) {
				if name == path {
					return nil, os.ErrPermission
				}
				return []byte(map[string]string{"/etc/nsswitch.conf": "passwd: files\ngroup: files\n", "/etc/passwd": passwd, "/etc/group": group}[name]), nil
			}
			if userPrivateGroupFromFiles("alice", 1000, 1000, read) {
				t.Fatal("unreadable account database was accepted")
			}
		})
	}
}

func TestShieldFolderPermissions(t *testing.T) {
	for _, c := range []struct {
		name         string
		mode         os.FileMode
		owner, group uint32
		data, safe   bool
	}{
		{"group writable below shield", os.ModeDir | 0o770, 1000, 1001, false, true},
		{"other writable below shield", os.ModeDir | 0o777, 1000, 1001, false, true},
		{"root group writable below shield", os.ModeDir | 0o775, 0, 0, false, true},
		{"foreign owner below shield", os.ModeDir | 0o700, 1001, 1000, false, false},
		{"data group writable below shield", os.ModeDir | 0o770, 1000, 1000, true, false},
		{"data other writable below shield", os.ModeDir | 0o707, 1000, 1000, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if reason := folderProblem(c.mode, c.owner, c.group, 1000, 1000, c.data, false, true, true); (reason == "") != c.safe {
				t.Fatalf("safe=%v: %s", c.safe, reason)
			}
		})
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
	if reason := folderProblem(os.ModeDir|0o700, 1001, 1000, 1000, 1000, true, false, false, false); reason == "" {
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
	// 外层 0700 目录提供屏障，内层父目录的写权限不会再暴露给其他用户。
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
