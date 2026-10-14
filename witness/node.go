package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
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
	portFile = "witness-rpcport.txt"
	pageFile = "witness-page.txt"
)

// maxStarts bounds the attempts to start the node while an earlier one still holds the data folder.
const maxStarts = 40

// nodeArgs are the node's arguments: Ark-0, outgoing connections only, RPC on 127.0.0.1 with the cookie the
// node writes into the data folder, no wallet.
func nodeArgs(dataDir string, rpcPort int) []string {
	return []string{
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
	exe     string
	dataDir string
	done    chan struct{} // closed once the node has stopped for good

	mu       sync.Mutex
	rpc      *rpcClient
	phase    string // "starting", "running" or "stopped"
	message  string // why the node stopped, when it stopped by itself
	stopping bool
}

func newNode(exe, dataDir string) *node {
	return &node{exe: exe, dataDir: dataDir, done: make(chan struct{}), phase: "starting"}
}

// start runs the node in the background.
func (n *node) start() {
	go n.supervise()
}

func (n *node) client() *rpcClient {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.rpc
}

func (n *node) state() (string, string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.phase, n.message
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

// stop asks the node to shut down. The node finishes that on its own, after this program has exited if need be.
func (n *node) stop() {
	n.mu.Lock()
	n.stopping = true
	rpc := n.rpc
	n.mu.Unlock()
	if rpc != nil {
		_ = rpc.callWithin(2*time.Second, "stop", nil, nil)
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
	// A node left running from this data folder, by a window that was closed without stopping it, is used as it
	// is, for as long as it answers.
	if port, ok := readPort(n.dataDir); ok {
		rpc := newRPCClient(port, cookiePath(n.dataDir))
		if rpc.alive() {
			n.mu.Lock()
			n.rpc = rpc
			n.mu.Unlock()
			n.follow(rpc)
		}
	}
	port := 0
	for attempt := 0; attempt < maxStarts && !n.isStopping(); attempt++ {
		if port == 0 {
			p, err := freePort()
			if err == nil {
				err = writePort(n.dataDir, p)
			}
			if err != nil {
				n.set("stopped", err.Error())
				return
			}
			port = p
		}
		n.mu.Lock()
		n.rpc = newRPCClient(port, cookiePath(n.dataDir))
		n.phase = "starting"
		n.mu.Unlock()
		began := time.Now()
		output, err := n.runOnce(port)
		if err != nil {
			n.set("stopped", err.Error())
			return
		}
		if n.isStopping() {
			break
		}
		early := time.Since(began) < time.Minute
		switch {
		case early && strings.Contains(output, "Cannot obtain a lock"):
			// The node of an earlier window is still shutting down and holds the data folder: try again shortly.
			time.Sleep(3 * time.Second)
		case early && strings.Contains(output, "Unable to start HTTP server"):
			port = 0 // the RPC port was taken in the meantime: pick another
		default:
			n.set("stopped", lastError(output))
			return
		}
	}
	if n.isStopping() {
		n.set("stopped", "")
	} else {
		n.set("stopped", "the node did not start")
	}
}

// runOnce runs the node until it exits and returns the end of what it wrote to its error output.
func (n *node) runOnce(port int) (string, error) {
	cmd := exec.Command(n.exe, nodeArgs(n.dataDir, port)...)
	cmd.Dir = n.dataDir
	stderr := &tail{limit: 8 << 10}
	cmd.Stderr = stderr
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	_ = cmd.Wait()
	return stderr.String(), nil
}

// follow watches a node this program did not start until it stops answering, or until this program stops it.
func (n *node) follow(rpc *rpcClient) {
	for misses := 0; misses < 3 && !n.isStopping(); {
		time.Sleep(5 * time.Second)
		if rpc.alive() {
			misses = 0
		} else {
			misses++
		}
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func readPort(dataDir string) (int, bool) {
	data, err := os.ReadFile(filepath.Join(dataDir, portFile))
	if err != nil {
		return 0, false
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	return port, err == nil && port > 0 && port < 65536
}

func writePort(dataDir string, port int) error {
	return os.WriteFile(filepath.Join(dataDir, portFile), []byte(strconv.Itoa(port)+"\n"), 0o600)
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

// lastError picks the node's last error line from its error output, or its last line.
func lastError(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); strings.Contains(line, "Error") {
			return clip(line, 300)
		}
	}
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return clip(last, 300)
	}
	return "the node stopped"
}

// clip shortens s to n characters.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
