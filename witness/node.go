package main

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The Ark-0 network, exactly as ark0/JOIN.md configures a node. The program passes them as arguments, so the
// data folder needs no bitcoin.conf.
const (
	signetChallenge = "5121026fc5d8e79a9fbc8bc3dea07d82641d16717ce0b000c13c512d8e8c1788c6e5da51ae"
	powSpacing      = "90@8064"
	publicNode      = "node.ark0.si:38433"
)

// Files of this program in the data folder, next to the node's own.
const (
	lockFile = "witness.lock"
	pageFile = "witness-page.txt"
)

// maxStarts bounds the attempts to start the node while an earlier one still holds the data folder.
const maxStarts = 40

// nodeArgs are the node's arguments: Ark-0, outgoing connections only, RPC on 127.0.0.1 with the cookie the
// node writes into the data folder, no wallet. -noconf and -nosettings keep a bitcoin.conf or settings.json
// found in the data folder from adding anything, such as another RPC address.
func nodeArgs(dataDir string, rpcPort int) []string {
	return []string{
		"-noconf",
		"-nosettings",
		"-datadir=" + dataDir,
		"-signet=1",
		"-signetchallenge=" + signetChallenge,
		"-signetpowtargetspacing=" + powSpacing,
		"-addnode=" + publicNode,
		"-listen=0",
		"-server=1",
		"-rpcbind=127.0.0.1",
		"-rpcallowip=127.0.0.1",
		"-rpcport=" + strconv.Itoa(rpcPort),
		"-disablewallet=1",
		"-printtoconsole=0",
	}
}

// cookiePath is where the node writes its RPC credentials; every signet keeps its files in signet/.
func cookiePath(dataDir string) string {
	return filepath.Join(dataDir, "signet", ".cookie")
}

// node runs the Ark-0 node in the background and keeps track of it.
type node struct {
	exe       string
	exeSHA256 string // what the node program must hash to; empty in development builds
	dataDir   string
	done      chan struct{} // closed once no node runs and none will be started

	mu       sync.Mutex
	proc     *os.Process // the node, while it runs
	rpc      *rpcClient  // its RPC, once the node is known to listen on its port
	phase    string      // "starting", "running" or "stopped"
	message  string      // the node's last error, when it stopped by itself
	stopping bool
}

func newNode(exe, exeSHA256, dataDir string) *node {
	return &node{exe: exe, exeSHA256: exeSHA256, dataDir: dataDir, done: make(chan struct{}), phase: "starting"}
}

// start runs the node in the background.
func (n *node) start() {
	go n.supervise()
}

// client is the node's RPC, or nil while the node is not known to listen on its port.
func (n *node) client() *rpcClient {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.rpc
}

func (n *node) child() *os.Process {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.proc
}

func (n *node) state() (string, string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.phase, n.message
}

// snapshot is the node's phase, message and RPC, read together.
func (n *node) snapshot() (string, string, *rpcClient) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.phase, n.message, n.rpc
}

func (n *node) set(phase, message string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.phase, n.message = phase, message
}

// markRunning records that the node has answered; the status page calls it.
func (n *node) markRunning() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.phase == "starting" {
		n.phase = "running"
	}
}

func (n *node) isStopping() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.stopping
}

// stop keeps any further node from starting and asks the running one to shut down. A node still loading
// refuses and a node not yet listening cannot be asked, so the request is repeated for two seconds; a node that
// has not agreed by then is ended. One still shutting down when this program exits is ended with it (the job
// object on Windows; SIGTERM again on Linux).
func (n *node) stop() {
	n.mu.Lock()
	n.stopping = true
	n.mu.Unlock()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		n.mu.Lock()
		p, rpc := n.proc, n.rpc
		n.mu.Unlock()
		if p == nil {
			return // no node runs, and none will start
		}
		if rpc != nil && rpc.callWithin(time.Second, "stop", nil, nil) == nil {
			return
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.proc != nil {
		_ = terminate(n.proc) // it may have exited in the meantime
	}
}

// wait waits until the node has stopped, at most d.
func (n *node) wait(d time.Duration) {
	select {
	case <-n.done:
	case <-time.After(d):
	}
}

func (n *node) supervise() {
	defer close(n.done)
	output := ""
	for attempt := 0; attempt < maxStarts && !n.isStopping(); attempt++ {
		port, err := freePort()
		if err == nil {
			began := time.Now()
			output, err = n.runOnce(port)
			early := time.Since(began) < time.Minute
			if err == nil && early && strings.Contains(output, "Cannot obtain a lock") {
				// The node of an earlier window is still shutting down and holds the data folder: try again shortly.
				time.Sleep(3 * time.Second)
				continue
			}
			if err == nil && early && strings.Contains(output, "Unable to start HTTP server") {
				continue // the RPC port was taken in the meantime: the next attempt picks another
			}
		}
		if err != nil {
			output = err.Error()
		}
		break
	}
	if n.isStopping() {
		output = ""
	}
	n.set("stopped", lastError(output))
}

// runOnce starts the node, unless the program is stopping, and waits until it exits; it returns the end of what
// the node wrote to its error output.
func (n *node) runOnce(port int) (string, error) {
	// On Linux the node's parent-death signal (see configureNode) follows the thread that started it, so this
	// goroutine keeps its thread until the node has exited.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// A cookie left by an earlier node goes first, so that a cookie found later is this node's.
	if err := os.Remove(cookiePath(n.dataDir)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	stderr := &tail{limit: 8 << 10}
	cmd := exec.Command(n.exe, nodeArgs(n.dataDir, port)...)
	cmd.Dir = n.dataDir
	cmd.Stderr = stderr
	configureNode(cmd)

	// The start happens under the lock that stop() takes: a node is either never started or seen by stop().
	n.mu.Lock()
	if n.stopping {
		n.mu.Unlock()
		return "", nil
	}
	if err := n.startNode(cmd); err != nil {
		n.mu.Unlock()
		return "", err
	}
	n.proc, n.rpc, n.phase = cmd.Process, nil, "starting"
	n.mu.Unlock()

	go n.trustWhenReady(cmd.Process, port)
	_ = cmd.Wait()
	n.mu.Lock()
	n.proc, n.rpc = nil, nil
	n.mu.Unlock()
	return stderr.String(), nil
}

// trustWhenReady hands out the node's RPC once the node has written its cookie, which it does only after it has
// bound its RPC port. Until then the port may belong to another program, which must not receive the cookie or
// a transaction pasted into the page.
func (n *node) trustWhenReady(p *os.Process, port int) {
	cookie := cookiePath(n.dataDir)
	for {
		_, err := os.Stat(cookie)
		n.mu.Lock()
		if n.proc != p {
			n.mu.Unlock()
			return // that node has exited
		}
		if err == nil {
			n.rpc = newRPCClient(port, cookie)
			n.mu.Unlock()
			return
		}
		n.mu.Unlock()
		time.Sleep(200 * time.Millisecond)
	}
}

// startNode starts cmd while the node program is held open against changes: it checks the program against the
// SHA-256 this program was built with, if any, starts it, and has admit tie it to this program, check that it is
// that very file and let it run.
func (n *node) startNode(cmd *exec.Cmd) error {
	f, err := openNodeProgram(n.exe)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := checkSHA256(f, n.exeSHA256); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := admit(cmd.Process, f); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	return nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// tail keeps the last bytes written to it.
type tail struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = t.buf[len(t.buf)-t.limit:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// lastError picks the node's last error line from its error output, or its last line, or "".
func lastError(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); strings.Contains(line, "Error") {
			return clip(line, 300)
		}
	}
	return clip(strings.TrimSpace(lines[len(lines)-1]), 300)
}

// clip shortens s to n characters.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
