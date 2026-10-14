package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for bitcoind: with WITNESS_FAKE_NODE set, it acts as a node of that
// kind instead of running the tests.
func TestMain(m *testing.M) {
	if kind := os.Getenv("WITNESS_FAKE_NODE"); kind != "" {
		os.Exit(fakeBitcoind(kind, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeBitcoind acts as bitcoind started with nodeArgs, as far as the program can tell. Every kind notes its start
// in starts.txt in the data folder.
//   - "ready" binds its RPC port, then writes its cookie, answers every call and exits on stop;
//   - "loading" binds its port and writes its cookie, but answers every call, stop included, with the warmup
//     error until a signal ends it;
//   - "silent" never binds its port or writes a cookie;
//   - "locked" finds the data folder locked, as when an earlier node still holds it, and exits.
func fakeBitcoind(kind string, args []string) int {
	var dataDir, port string
	for _, arg := range args {
		if v, ok := strings.CutPrefix(arg, "-datadir="); ok {
			dataDir = v
		}
		if v, ok := strings.CutPrefix(arg, "-rpcport="); ok {
			port = v
		}
	}
	if f, err := os.OpenFile(filepath.Join(dataDir, "starts.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_, _ = f.WriteString(kind + "\n")
		_ = f.Close()
	}
	switch kind {
	case "silent":
		time.Sleep(time.Hour)
		return 0
	case "locked":
		fmt.Fprintln(os.Stderr, "Error: Cannot obtain a lock on directory "+dataDir+". Bitcoin Core is probably already running.")
		return 1
	}
	l, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: Unable to start HTTP server. See debug log for details.")
		return 1
	}
	if os.MkdirAll(filepath.Join(dataDir, "signet"), 0o700) != nil || os.WriteFile(cookiePath(dataDir), []byte("__cookie__:fake"), 0o600) != nil {
		return 1
	}
	stopped := make(chan struct{})
	var once sync.Once
	go func() {
		_ = http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			reply := map[string]any{"id": req.ID, "result": "ok", "error": nil}
			switch {
			case kind == "loading":
				reply["result"], reply["error"] = nil, rpcError{Code: rpcInWarmup, Message: "Loading block index…"}
			case req.Method == "stop":
				defer once.Do(func() { close(stopped) })
			}
			_ = json.NewEncoder(w).Encode(reply)
		}))
	}()
	<-stopped
	time.Sleep(100 * time.Millisecond) // the answer to stop goes out first
	_ = os.Remove(cookiePath(dataDir))
	return 0
}

// fakeNodeProcess is a node supervisor that runs the test binary as a bitcoind of the given kind.
func fakeNodeProcess(t *testing.T, kind, dataDir, exeSHA256 string) *node {
	t.Setenv("WITNESS_FAKE_NODE", kind)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	n := newNode(exe, exeSHA256, dataDir)
	t.Cleanup(func() {
		if runtime.GOOS == "windows" {
			n.stop()
		} else {
			shutdownNode(n, 3*time.Second)
		}
		n.wait(10 * time.Second)
	})
	return n
}

func eventually(d time.Duration, cond func() bool) bool {
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

func stoppedWithin(n *node, d time.Duration) bool {
	select {
	case <-n.done:
		return true
	case <-time.After(d):
		return false
	}
}

func starts(dataDir string) int {
	data, _ := os.ReadFile(filepath.Join(dataDir, "starts.txt"))
	return strings.Count(string(data), "\n")
}

// A node that agrees stops at once. Windows ends an unresponsive node after two seconds; Unix sends SIGTERM
// at the shutdown deadline. Its RPC is trusted only once it has written its cookie.
func TestNodeStops(t *testing.T) {
	for _, c := range []struct {
		kind    string
		trusted bool
		within  time.Duration
	}{
		{"ready", true, 2 * time.Second},
		{"loading", true, 5 * time.Second},
		{"silent", false, 5 * time.Second},
	} {
		t.Run(c.kind, func(t *testing.T) {
			n := fakeNodeProcess(t, c.kind, t.TempDir(), "")
			n.start()
			if !eventually(10*time.Second, func() bool { return n.child() != nil }) {
				t.Fatal("no node started")
			}
			if trusted := eventually(3*time.Second, func() bool { return n.client() != nil }); trusted != c.trusted {
				t.Fatalf("trusted: %v", trusted)
			}
			began := time.Now()
			if runtime.GOOS == "windows" {
				n.stop()
			} else {
				shutdownNode(n, 3*time.Second)
			}
			if !stoppedWithin(n, c.within) {
				t.Fatalf("the node still runs %v after stop", time.Since(began))
			}
			if phase, message := n.state(); phase != "stopped" || message != "" {
				t.Errorf("%s %q", phase, message)
			}
		})
	}
}

// Asked to stop before the node starts, the program starts none.
func TestStopBeforeStart(t *testing.T) {
	dir := t.TempDir()
	n := fakeNodeProcess(t, "ready", dir, "")
	n.stop()
	n.start()
	if !stoppedWithin(n, 2*time.Second) {
		t.Fatal("not stopped")
	}
	if starts(dir) != 0 {
		t.Error("a node was started")
	}
}

// A cookie left by an earlier node is removed, and never makes another program's port trusted.
func TestStaleCookie(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "signet"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cookiePath(dir), []byte("__cookie__:stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	n := fakeNodeProcess(t, "silent", dir, "")
	n.start()
	if !eventually(10*time.Second, func() bool { return n.child() != nil }) {
		t.Fatal("no node started")
	}
	time.Sleep(time.Second)
	if n.client() != nil {
		t.Error("a node that never listened is trusted")
	}
	if _, err := os.Stat(cookiePath(dir)); err == nil {
		t.Error("the stale cookie is still there")
	}
}

// While an earlier node holds the data folder, the start is tried again; stop ends the trying.
func TestNodeLockedRetries(t *testing.T) {
	dir := t.TempDir()
	n := fakeNodeProcess(t, "locked", dir, "")
	n.start()
	if !eventually(10*time.Second, func() bool { return starts(dir) >= 2 }) {
		t.Fatalf("starts: %d", starts(dir))
	}
	n.stop()
	if !stoppedWithin(n, 5*time.Second) {
		t.Fatal("not stopped")
	}
	if _, message := n.state(); message != "" {
		t.Errorf("message after stop: %q", message)
	}
}

// A node program that is not the one the witness was built with is not started.
func TestWrongNodeProgram(t *testing.T) {
	dir := t.TempDir()
	n := fakeNodeProcess(t, "ready", dir, strings.Repeat("0", 64))
	n.start()
	if !stoppedWithin(n, 5*time.Second) {
		t.Fatal("not stopped")
	}
	if _, message := n.state(); !strings.Contains(message, "not the one") {
		t.Errorf("message %q", message)
	}
	if starts(dir) != 0 {
		t.Error("the node was started")
	}
}

func TestNodeArgsAreJoinMd(t *testing.T) {
	args := nodeArgs(`C:\Users\a b\AppData\Local\Ark0Witness`, 40123)
	for _, want := range []string{
		"-noconf",
		"-nosettings",
		`-datadir=C:\Users\a b\AppData\Local\Ark0Witness`,
		"-signet=1",
		"-signetchallenge=5121026fc5d8e79a9fbc8bc3dea07d82641d16717ce0b000c13c512d8e8c1788c6e5da51ae",
		"-signetpowtargetspacing=90@8064",
		"-addnode=node.ark0.si:38433",
		"-listen=0",
		"-rpcbind=127.0.0.1",
		"-rpcallowip=127.0.0.1",
		"-rpcport=40123",
	} {
		if !slices.Contains(args, want) {
			t.Errorf("missing %s in %v", want, args)
		}
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "-rpcpassword") || strings.HasPrefix(arg, "-rpcuser") || strings.HasPrefix(arg, "-rpcbind=0") {
			t.Errorf("unexpected %s", arg)
		}
	}
}

func TestCleanHex(t *testing.T) {
	for _, c := range []struct {
		in, out string
		ok      bool
	}{
		{"0200", "0200", true},
		{" 02 00\r\n ab\tCD ", "0200abCD", true},
		{"", "", false},
		{"   ", "", false},
		{"020", "", false},
		{"02zz", "", false},
		{"0x0200", "", false},
		{"02,00", "", false},
		{"０２", "", false},
		{strings.Repeat("00", maxTxHex/2+1), "", false},
	} {
		got, ok := cleanHex(c.in)
		if ok != c.ok || (ok && got != c.out) {
			t.Errorf("cleanHex(%.20q) = %q, %v; want %q, %v", c.in, got, ok, c.out, c.ok)
		}
	}
	if got, ok := cleanHex(strings.Repeat("ab", maxTxHex/2)); !ok || len(got) != maxTxHex {
		t.Errorf("the largest transaction is refused")
	}
}

func TestLanguages(t *testing.T) {
	for _, lang := range []string{"en", "zh", "ja", "ko", "ru", "es"} {
		c, ok := consoleTexts[lang]
		if !ok {
			t.Fatalf("no console text for %s", lang)
		}
		for _, field := range []string{c.Title, c.Tagline, c.Running, c.KeepOpen, c.Already, c.Busy, c.MissingNode, c.WrongNode,
			c.NoDataFolder, c.StartFailed, c.Stopping, c.Stopped, c.PressEnter} {
			if strings.TrimSpace(field) == "" {
				t.Errorf("%s: an empty console text", lang)
			}
		}
		if !strings.Contains(c.NoDataFolder, "%v") || !strings.Contains(c.StartFailed, "%v") || strings.Contains(c.Stopped, "%") {
			t.Errorf("%s: a console text has the wrong placeholders", lang)
		}
	}
	for id, want := range map[uint16]string{0x0804: "zh", 0x0404: "zh", 0x0409: "en", 0x0809: "en", 0x0411: "ja", 0x0412: "ko",
		0x0419: "ru", 0x0c0a: "es", 0x080a: "es", 0x0407: "", 0x040c: ""} {
		if got := languageOfLANGID(id); got != want {
			t.Errorf("LANGID %#x: %q, want %q", id, got, want)
		}
	}
	for locale, want := range map[string]string{"zh_CN.UTF-8": "zh", "ja-JP": "ja", "ko_KR": "ko", "ru": "ru", "es_MX.utf8": "es",
		"en_US": "en", "de_DE.UTF-8": "", "C": "", "": ""} {
		if got := languageOfLocale(locale); got != want {
			t.Errorf("locale %q: %q, want %q", locale, got, want)
		}
	}
	if pickLanguage("", "fr", "ja") != "ja" || pickLanguage("xx") != "en" {
		t.Error("pickLanguage")
	}
}

var placeholder = regexp.MustCompile(`\{[a-z0-9]+\}`)

// The page's strings: every language has every key, with the same placeholders, and no claim the sites never make.
func TestPageStrings(t *testing.T) {
	var all map[string]map[string]string
	data, err := os.ReadFile("ui/strings.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &all); err != nil {
		t.Fatal(err)
	}
	en := all["en"]
	banned := []string{"quantum-safe", "quantum safe", "quantum-resistant", "quantum resistant", "quantum-proof", "抗量子", "量子安全",
		"耐量子", "量子耐性", "양자 내성", "양자내성", "양자 안전", "квантово-устойчив", "квантовоустойчив", "квантово-безопас",
		"resistente a la computación cuántica", "a prueba de cuánticos", "postcuántico seguro"}
	for _, lang := range []string{"en", "zh", "ja", "ko", "ru", "es"} {
		strs, ok := all[lang]
		if !ok {
			t.Fatalf("no strings for %s", lang)
		}
		if len(strs) != len(en) {
			t.Errorf("%s has %d strings, en %d", lang, len(strs), len(en))
		}
		for key, text := range en {
			got, ok := strs[key]
			if !ok || strings.TrimSpace(got) == "" {
				t.Errorf("%s: missing %s", lang, key)
				continue
			}
			a, b := placeholder.FindAllString(text, -1), placeholder.FindAllString(got, -1)
			slices.Sort(a)
			slices.Sort(b)
			if !slices.Equal(a, b) {
				t.Errorf("%s.%s placeholders %v, en %v", lang, key, b, a)
			}
		}
		for key, text := range strs {
			for _, word := range banned {
				if strings.Contains(strings.ToLower(text), word) {
					t.Errorf("%s.%s says %q", lang, key, word)
				}
			}
		}
	}
	// Every key the page uses exists.
	page, _ := os.ReadFile("ui/index.html")
	script, _ := os.ReadFile("ui/app.js")
	used := regexp.MustCompile(`data-t="([a-z0-9_]+)"`).FindAllStringSubmatch(string(page), -1)
	used = append(used, regexp.MustCompile(`\bt\('([a-z0-9_]+)'`).FindAllStringSubmatch(string(script), -1)...)
	for _, state := range []string{"ok", "pending", "mismatch"} {
		used = append(used, []string{"", "ark0_" + state})
	}
	if len(used) < 40 {
		t.Fatalf("found only %d keys in use", len(used))
	}
	for _, m := range used {
		if strings.HasSuffix(m[1], "_") {
			continue // a prefix completed at run time, such as ark0_ + state, listed above
		}
		if _, ok := en[m[1]]; !ok {
			t.Errorf("the page uses %s, which has no string", m[1])
		}
	}
}

// fakeNode answers JSON-RPC like Bitcoin Core, only with the cookie written into dataDir.
type fakeNode struct {
	t       *testing.T
	dataDir string
	cookie  string
	answers map[string]func(params []json.RawMessage) (any, *rpcError)
	calls   []string
	server  *httptest.Server
}

func newFakeNode(t *testing.T) *fakeNode {
	f := &fakeNode{t: t, dataDir: t.TempDir(), cookie: "__cookie__:0123abcd", answers: map[string]func([]json.RawMessage) (any, *rpcError){}}
	if err := os.MkdirAll(filepath.Join(f.dataDir, "signet"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cookiePath(f.dataDir), []byte(f.cookie), 0o600); err != nil {
		t.Fatal(err)
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func portOf(t *testing.T, server *httptest.Server) int {
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func (f *fakeNode) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(f.cookie)) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var req struct {
		ID     json.RawMessage   `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	body, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(body, &req); err != nil {
		f.t.Errorf("bad request %s", body)
		return
	}
	f.calls = append(f.calls, req.Method)
	answer, ok := f.answers[req.Method]
	var result any
	var rpcErr *rpcError
	if ok {
		result, rpcErr = answer(req.Params)
	} else {
		rpcErr = &rpcError{Code: rpcMethodNotFound, Message: "Method not found"}
	}
	w.Header().Set("Content-Type", "application/json")
	if rpcErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": rpcErr, "id": req.ID})
}

// ark0 makes the fake node answer as an Ark-0 node at the given height.
func (f *fakeNode) ark0(height int64, drillState string, ecDisabled bool) {
	f.answers["getblockchaininfo"] = func([]json.RawMessage) (any, *rpcError) {
		return map[string]any{"blocks": height, "headers": height, "initialblockdownload": false, "bestblockhash": "00aa"}, nil
	}
	f.answers["getconnectioncount"] = func([]json.RawMessage) (any, *rpcError) { return 1, nil }
	f.answers["getblockhash"] = func(p []json.RawMessage) (any, *rpcError) {
		switch string(p[0]) {
		case "1264":
			return block1264, nil
		case "30000":
			return "000030000hash", nil
		}
		return nil, &rpcError{Code: -8, Message: "Block height out of range"}
	}
	f.answers["gettripwireinfo"] = func([]json.RawMessage) (any, *rpcError) {
		return map[string]any{"tip_height": height, "tip_hash": "00aa", "height": 30000, "leaf_version": "e0",
			"ec_disabled_next_block": ecDisabled, "tripwires": []map[string]any{
				{"outpoint": "4319a38c0842846474ff038b54f22aec844147a97c2abc462bfb92957c1bc3a0:1", "state": "unspent", "label": "BIP-341 NUMS"},
				{"outpoint": "8991bb83673474b7c324f303365479c093d0b34f5e7d7ddf0648bcfdb1d42495:0", "state": drillState, "label": "drill"},
			}}, nil
	}
	f.answers["getnetworkinfo"] = func([]json.RawMessage) (any, *rpcError) {
		return map[string]any{"subversion": "/Satoshi:31.1.0/"}, nil
	}
}

func (f *fakeNode) node() *node {
	n := newNode("bitcoind", "", f.dataDir)
	n.rpc = newRPCClient(portOf(f.t, f.server), cookiePath(f.dataDir))
	return n
}

func TestStatusAndReport(t *testing.T) {
	f := newFakeNode(t)
	f.ark0(30100, "missing", true)
	n := f.node()
	c := &collector{node: n, dataDir: f.dataDir}
	s := c.collect()
	if s.Node != "running" || s.Blocks != 30100 || s.Ark0 != "ok" || s.H3 != 30000 || s.BlockH3 != "000030000hash" ||
		s.TripwireInfo != "ok" || s.TripwireTip != 30100 || !s.ECDisabledNextBlock || len(s.Tripwires) != 2 ||
		s.Tripwires[1].Label != "drill" || s.Subversion != "/Satoshi:31.1.0/" || s.Peers != 1 {
		t.Fatalf("status %+v", s)
	}
	if phase, _ := n.state(); phase != "running" {
		t.Errorf("phase %s", phase)
	}
	report := witnessReport(s, &checkResult{At: "2026-10-30 08:20:00", Txid: "tx1", Reason: "mandatory-script-verify-flag-failed (" + ecDisabledMessage + ")"})
	for _, want := range []string{"Block 1264 matches Ark-0: yes", "Tip: height 30100, hash 00aa", "Block 30000 (M3): 000030000hash",
		"Tripwire drill 8991bb83673474b7c324f303365479c093d0b34f5e7d7ddf0648bcfdb1d42495:0: missing", "ec_disabled_next_block: true",
		"Checked transaction tx1 at 2026-10-30 08:20:00 UTC: rejected, mandatory-script-verify-flag-failed"} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, f.dataDir) {
		t.Error("the report names the data folder")
	}

	// The tripwires come with the block they are of, which the report names even when the tip has moved on.
	answer := f.answers["gettripwireinfo"]
	f.answers["gettripwireinfo"] = func(p []json.RawMessage) (any, *rpcError) {
		info, _ := answer(p)
		info.(map[string]any)["tip_height"], info.(map[string]any)["tip_hash"] = 30101, "00bb"
		return info, nil
	}
	s = (&collector{node: f.node(), dataDir: f.dataDir}).collect()
	if r := witnessReport(s, nil); !strings.Contains(r, "Tip: height 30101, hash 00bb") || s.Blocks != 30100 {
		t.Errorf("tripwire tip:\n%s", r)
	}

	// The tripwires already at block 30000 while the chain info still said 29999: block 30000 counts as reached.
	h := newFakeNode(t)
	h.ark0(29999, "unspent", false)
	answer = h.answers["gettripwireinfo"]
	h.answers["gettripwireinfo"] = func(p []json.RawMessage) (any, *rpcError) {
		info, _ := answer(p)
		info.(map[string]any)["tip_height"], info.(map[string]any)["tip_hash"] = 30000, "00cc"
		return info, nil
	}
	s = (&collector{node: h.node(), dataDir: h.dataDir}).collect()
	if !s.ReachedH3 || s.BlockH3 != "000030000hash" {
		t.Errorf("crossing 30000: %+v", s)
	}
	// Past 30000 with the hash of block 30000 not read: reached, not "not reached yet".
	h.answers["getblockhash"] = func(p []json.RawMessage) (any, *rpcError) {
		if string(p[0]) == "1264" {
			return block1264, nil
		}
		return nil, &rpcError{Code: -1, Message: "boom"}
	}
	s = (&collector{node: h.node(), dataDir: h.dataDir}).collect()
	if r := witnessReport(s, nil); !strings.Contains(r, "Block 30000 (M3): reached, hash not read just now") {
		t.Errorf("hash not read:\n%s", r)
	}

	// Before 30000, the hash of block 30000 is not asked for.
	g := newFakeNode(t)
	g.ark0(22600, "unspent", false)
	s = (&collector{node: g.node(), dataDir: g.dataDir}).collect()
	if s.BlockH3 != "" || s.ECDisabledNextBlock || s.Ark0 != "ok" || s.TripwireInfo != "ok" {
		t.Errorf("status %+v", s)
	}
	if r := witnessReport(s, nil); !strings.Contains(r, "Block 30000 (M3): not reached yet") || strings.Contains(r, "Checked") {
		t.Errorf("report:\n%s", r)
	}
	// A node that is not running yet.
	if r := witnessReport(status{Version: "v", Now: "now", Node: "starting"}, nil); !strings.Contains(r, "Node: starting") || strings.Contains(r, "Tip") {
		t.Errorf("report:\n%s", r)
	}
}

func TestStatusEdges(t *testing.T) {
	// A node without gettripwireinfo is not M3.
	f := newFakeNode(t)
	f.ark0(22600, "unspent", false)
	delete(f.answers, "gettripwireinfo")
	s := (&collector{node: f.node(), dataDir: f.dataDir}).collect()
	if s.TripwireInfo != "absent" || !strings.Contains(witnessReport(s, nil), "not the M3 release") {
		t.Errorf("not M3 not seen: %+v", s)
	}
	// A failed gettripwireinfo, or an answer without the flag, leaves the tripwires unknown rather than in place.
	for _, answer := range []func([]json.RawMessage) (any, *rpcError){
		func([]json.RawMessage) (any, *rpcError) { return nil, &rpcError{Code: -1, Message: "boom"} },
		func([]json.RawMessage) (any, *rpcError) {
			return map[string]any{"tip_height": 22600, "tip_hash": "00aa", "height": 30000, "tripwires": []any{}}, nil
		},
		func([]json.RawMessage) (any, *rpcError) {
			return map[string]any{"tip_hash": "00aa", "height": 30000, "tripwires": []any{}, "ec_disabled_next_block": false}, nil
		},
		func([]json.RawMessage) (any, *rpcError) {
			return map[string]any{"tip_height": -1, "tip_hash": "00aa", "height": 30000, "tripwires": []any{}, "ec_disabled_next_block": false}, nil
		},
	} {
		f.answers["gettripwireinfo"] = answer
		s := (&collector{node: f.node(), dataDir: f.dataDir}).collect()
		if r := witnessReport(s, nil); s.TripwireInfo != "unknown" || len(s.Tripwires) != 0 || s.ECDisabledNextBlock ||
			!strings.Contains(r, "Tripwires: unknown") || strings.Contains(r, "ec_disabled_next_block") {
			t.Errorf("unknown tripwires: %+v\n%s", s, r)
		}
	}
	// Another chain.
	f.ark0(22600, "unspent", false)
	f.answers["getblockhash"] = func([]json.RawMessage) (any, *rpcError) { return "0000other", nil }
	if s := (&collector{node: f.node(), dataDir: f.dataDir}).collect(); s.Ark0 != "mismatch" {
		t.Errorf("mismatch not seen: %+v", s)
	}
	// Block 1264 not read: not checked rather than mismatched.
	f.answers["getblockhash"] = func([]json.RawMessage) (any, *rpcError) { return nil, &rpcError{Code: -1, Message: "boom"} }
	if s := (&collector{node: f.node(), dataDir: f.dataDir}).collect(); s.Ark0 != "pending" ||
		!strings.Contains(witnessReport(s, nil), "Block 1264 matches Ark-0: not checked yet") {
		t.Errorf("pending not seen: %+v", s)
	}
	// A running node that does not answer once is not shown at height 0.
	g := newFakeNode(t)
	g.ark0(22600, "unspent", false)
	running := g.node()
	if s := (&collector{node: running, dataDir: g.dataDir}).collect(); s.Node != "running" {
		t.Fatalf("%+v", s)
	}
	g.answers["getblockchaininfo"] = func([]json.RawMessage) (any, *rpcError) { return nil, &rpcError{Code: -1, Message: "boom"} }
	s = (&collector{node: running, dataDir: g.dataDir}).collect()
	if r := witnessReport(s, nil); s.Node != "noanswer" || !strings.Contains(r, "Node: did not answer just now") || strings.Contains(r, "Tip") {
		t.Errorf("no answer: %+v\n%s", s, r)
	}
	// A node that was running and has just exited, before the program knows what follows.
	gone := newNode("bitcoind", "", t.TempDir())
	gone.markRunning()
	if s := (&collector{node: gone, dataDir: f.dataDir}).collect(); s.Node != "noanswer" {
		t.Errorf("just exited: %+v", s)
	}
	// Still loading.
	f.answers["getblockchaininfo"] = func([]json.RawMessage) (any, *rpcError) {
		return nil, &rpcError{Code: rpcInWarmup, Message: "Loading block index…"}
	}
	n := f.node()
	if s := (&collector{node: n, dataDir: f.dataDir}).collect(); s.Node != "starting" || s.Warmup != "Loading block index…" {
		t.Errorf("warmup: %+v", s)
	}
	// A wrong cookie gets no answer.
	if err := os.WriteFile(cookiePath(f.dataDir), []byte("__cookie__:other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := n.rpc.call("getblockcount", nil, nil); err != errNotReady {
		t.Errorf("wrong cookie: %v", err)
	}
	// No cookie yet.
	_ = os.Remove(cookiePath(f.dataDir))
	if err := n.rpc.call("getblockcount", nil, nil); err != errNotReady {
		t.Errorf("no cookie: %v", err)
	}
}

// Only the answer to the call made counts; a redirect is not followed.
func TestRPCAnswers(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "signet"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cookiePath(dir), []byte("__cookie__:x"), 0o600); err != nil {
		t.Fatal(err)
	}
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("a redirect was followed") }))
	defer elsewhere.Close()
	for body, want := range map[string]string{
		`{"result":{"blocks":7},"error":null,"id":"1"}`:                         "",
		`{"result":{"blocks":7},"error":null,"id":"2"}`:                         "not ready",
		`{"result":{"blocks":7},"error":null,"id":1}`:                           "not ready",
		`{"result":{"blocks":7},"error":null}`:                                  "not ready",
		`{"result":null,"error":null,"id":"1"}`:                                 "without a result",
		`{"error":null,"id":"1"}`:                                               "without a result",
		`{"result":"7","error":null,"id":"1"}`:                                  "cannot unmarshal",
		`{"result":null,"error":{"code":-5,"message":"refused here"},"id":"1"}`: "refused here",
		`null`:          "not ready",
		`<html></html>`: "not ready",
		"redirect":      "not ready",
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if body == "redirect" {
				http.Redirect(w, r, elsewhere.URL, http.StatusTemporaryRedirect)
				return
			}
			_, _ = io.WriteString(w, body)
		}))
		var out struct {
			Blocks int64 `json:"blocks"`
		}
		err := newRPCClient(portOf(t, server), cookiePath(dir)).call("getblockchaininfo", nil, &out)
		switch {
		case want == "" && (err != nil || out.Blocks != 7):
			t.Errorf("%s: %v %+v", body, err, out)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%s: %v, want %q", body, err, want)
		}
		// A call whose result is not used, such as stop, is held to the same answer.
		err = newRPCClient(portOf(t, server), cookiePath(dir)).callWithin(time.Second, "stop", nil, nil)
		if (want == "" || want == "cannot unmarshal") != (err == nil) {
			t.Errorf("stop, %s: %v", body, err)
		}
		server.Close()
	}
}

func TestCheckTransaction(t *testing.T) {
	f := newFakeNode(t)
	var sent []json.RawMessage
	f.answers["testmempoolaccept"] = func(p []json.RawMessage) (any, *rpcError) {
		sent = p
		return []map[string]any{{"txid": "tx1", "allowed": false, "reject-reason": "mandatory-script-verify-flag-failed (" + ecDisabledMessage + ")"}}, nil
	}
	res, err := checkTransaction(f.node().rpc, "0200")
	if err != nil || res.Allowed || !res.ECDisabled || res.Txid != "tx1" {
		t.Fatalf("%+v %v", res, err)
	}
	if len(sent) != 2 || string(sent[0]) != `["0200"]` || string(sent[1]) != "0" {
		t.Errorf("params %s", sent)
	}
	for _, method := range f.calls {
		if method != "testmempoolaccept" {
			t.Errorf("unexpected call %s", method)
		}
	}
	f.answers["testmempoolaccept"] = func([]json.RawMessage) (any, *rpcError) {
		return nil, &rpcError{Code: -22, Message: "TX decode failed"}
	}
	if res, err := checkTransaction(f.node().rpc, "0200"); err != nil || res.Error != "TX decode failed" || res.ECDisabled {
		t.Errorf("%+v %v", res, err)
	}
	f.answers["testmempoolaccept"] = func([]json.RawMessage) (any, *rpcError) {
		return []map[string]any{{"txid": "tx2", "allowed": true}}, nil
	}
	if res, err := checkTransaction(f.node().rpc, "0200"); err != nil || !res.Allowed || res.ECDisabled {
		t.Errorf("%+v %v", res, err)
	}
}

func testServer(t *testing.T, n *node) (*server, *http.Client) {
	s, err := newServer(n, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s, &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func do(t *testing.T, c *http.Client, method, target, host string, header map[string]string, body string) (*http.Response, string) {
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, string(data)
}

func TestServerBoundaries(t *testing.T) {
	f := newFakeNode(t)
	f.ark0(22600, "unspent", false)
	f.answers["testmempoolaccept"] = func([]json.RawMessage) (any, *rpcError) {
		return []map[string]any{{"txid": "tx1", "allowed": true}}, nil
	}
	s, c := testServer(t, f.node())
	base := s.url()
	if !regexp.MustCompile(`^http://127\.0\.0\.1:\d+/[0-9a-f]{64}/$`).MatchString(base) || !pageURLPattern.MatchString(base) {
		t.Fatalf("url %s", base)
	}

	resp, body := do(t, c, "GET", base, "", nil, "")
	if resp.StatusCode != 200 || !strings.Contains(body, "<script src=\"app.js\"") {
		t.Fatalf("page: %d %s", resp.StatusCode, body)
	}
	for k, v := range map[string]string{"Content-Security-Policy": csp, "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer",
		"Cache-Control": "no-store", "X-Frame-Options": "DENY"} {
		if resp.Header.Get(k) != v {
			t.Errorf("%s: %q", k, resp.Header.Get(k))
		}
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("CORS")
	}
	if resp, body := do(t, c, "GET", base+"strings.js", "", nil, ""); resp.StatusCode != 200 || !strings.HasPrefix(body, "window.STRINGS = {") {
		t.Errorf("strings.js: %d %.40s", resp.StatusCode, body)
	}

	// Another name for 127.0.0.1 (DNS rebinding), a wrong token, another path: turned away.
	if resp, _ := do(t, c, "GET", base, "evil.example:"+strings.Split(s.addr, ":")[1], nil, ""); resp.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("rebinding host: %d", resp.StatusCode)
	}
	if resp, _ := do(t, c, "GET", base, "localhost:"+strings.Split(s.addr, ":")[1], nil, ""); resp.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("localhost: %d", resp.StatusCode)
	}
	wrong := "http://" + s.addr + "/" + strings.Repeat("0", 64) + "/"
	if resp, _ := do(t, c, "GET", wrong, "", nil, ""); resp.StatusCode != 404 {
		t.Errorf("wrong token: %d", resp.StatusCode)
	}
	for _, path := range []string{"/", "/api/status", "/" + s.token, "/" + s.token + "x/", "/" + s.token + "/../" + s.token + "/",
		"/" + s.token + "/ui/app.js", "/" + s.token + "/strings.json", "/" + s.token + "/index.html", "/" + s.token + "/%2e%2e/main.go"} {
		if resp, _ := do(t, c, "GET", "http://"+s.addr+path, "", nil, ""); resp.StatusCode != 404 {
			t.Errorf("%s: %d", path, resp.StatusCode)
		}
	}

	// Status and report are GET only; check is POST with JSON from the page's own origin.
	if resp, body := do(t, c, "GET", base+"api/status", "", nil, ""); resp.StatusCode != 200 || !strings.Contains(body, `"blocks":22600`) {
		t.Errorf("status: %d %s", resp.StatusCode, body)
	}
	if resp, _ := do(t, c, "POST", base+"api/status", "", nil, ""); resp.StatusCode != 405 {
		t.Errorf("POST status: %d", resp.StatusCode)
	}
	if resp, _ := do(t, c, "GET", base+"api/check", "", nil, ""); resp.StatusCode != 405 {
		t.Errorf("GET check: %d", resp.StatusCode)
	}
	jsonType := map[string]string{"Content-Type": "application/json"}
	for _, h := range []map[string]string{
		{"Content-Type": "text/plain"},
		{"Content-Type": "application/x-www-form-urlencoded"},
		{"Content-Type": "application/json", "Origin": "https://evil.example"},
		{"Content-Type": "application/json", "Origin": "null"},
		{"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site"},
	} {
		if resp, body := do(t, c, "POST", base+"api/check", "", h, `{"hex":"0200"}`); resp.StatusCode < 400 {
			t.Errorf("%v accepted: %s", h, body)
		}
	}
	for _, bad := range []string{`{"hex":"02zz"}`, `{"hex":""}`, `not json`, `{"hex":"` + strings.Repeat("0", maxCheckBody) + `"}`} {
		if resp, body := do(t, c, "POST", base+"api/check", "", jsonType, bad); resp.StatusCode != 400 || !strings.Contains(body, `"hex"`) {
			t.Errorf("%.20s: %d %s", bad, resp.StatusCode, body)
		}
	}
	same := map[string]string{"Content-Type": "application/json; charset=utf-8", "Origin": "http://" + s.addr, "Sec-Fetch-Site": "same-origin"}
	if resp, body := do(t, c, "POST", base+"api/check", "", same, `{"hex":"02 00"}`); resp.StatusCode != 200 || !strings.Contains(body, `"allowed":true`) {
		t.Errorf("check: %d %s", resp.StatusCode, body)
	}
	if _, body := do(t, c, "GET", base+"api/report", "", nil, ""); !strings.Contains(body, "Checked transaction tx1") {
		t.Errorf("report after a check:\n%s", body)
	}
	if slices.Contains(f.calls, "sendrawtransaction") {
		t.Error("a transaction was sent")
	}
}

func TestServerWithoutNode(t *testing.T) {
	n := newNode("bitcoind", "", t.TempDir())
	s, c := testServer(t, n)
	if resp, body := do(t, c, "GET", s.url()+"api/status", "", nil, ""); resp.StatusCode != 200 || !strings.Contains(body, `"node":"starting"`) {
		t.Errorf("status: %d %s", resp.StatusCode, body)
	}
	resp, body := do(t, c, "POST", s.url()+"api/check", "", map[string]string{"Content-Type": "application/json"}, `{"hex":"0200"}`)
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, `"node"`) {
		t.Errorf("check without a node: %d %s", resp.StatusCode, body)
	}
}

func TestFilesAndPaths(t *testing.T) {
	dir := t.TempDir()
	page := "http://127.0.0.1:40123/" + strings.Repeat("ab", 32) + "/"
	for text, ok := range map[string]bool{page + "\n": true, "https://evil.example/": false, "http://127.0.0.1:1/x/": false,
		"http://localhost:40123/" + strings.Repeat("ab", 32) + "/": false, page + "x": false} {
		_ = os.WriteFile(filepath.Join(dir, pageFile), []byte(text), 0o600)
		if _, got := readPageURL(dir); got != ok {
			t.Errorf("page %q: %v", text, got)
		}
	}

	exe := filepath.Join(dir, "bitcoind")
	if err := os.WriteFile(exe, []byte("node"), 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("node"))
	if checkNodeProgram(exe, "") != nil || checkNodeProgram(exe, hex.EncodeToString(sum[:])) != nil ||
		checkNodeProgram(exe, strings.ToUpper(hex.EncodeToString(sum[:]))) != nil {
		t.Error("the right program is refused")
	}
	if checkNodeProgram(exe, strings.Repeat("0", 64)) == nil {
		t.Error("a changed program is accepted")
	}
	if _, err := nodeProgram(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing program is accepted")
	}
	if _, err := nodeProgram(dir); err == nil {
		t.Error("a folder is accepted as the program")
	}
	// The program checked is the program started.
	if runtime.GOOS == "windows" {
		bare := filepath.Join(dir, "node")
		_ = os.WriteFile(bare, []byte("node"), 0o700)
		_ = os.WriteFile(bare+".exe", []byte("other"), 0o700)
		if _, err := nodeProgram(bare); err == nil {
			t.Error("a name Windows would run as name.exe is accepted")
		}
	} else {
		plain := filepath.Join(dir, "plain")
		_ = os.WriteFile(plain, []byte("node"), 0o600)
		if _, err := nodeProgram(plain); err == nil {
			t.Error("a file that cannot be run is accepted")
		}
		if got, err := nodeProgram(exe); err != nil || got != exe {
			t.Errorf("node program %s %v", got, err)
		}
	}
	if got, err := dataFolder(filepath.Join(dir, "data")); err != nil || !filepath.IsAbs(got) {
		t.Errorf("data folder %s %v", got, err)
	}
}

func TestLockInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), lockFile)
	release, err := lockInstance(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockInstance(path); err == nil {
		t.Error("a second lock was granted")
	}
	release()
	again, err := lockInstance(path)
	if err != nil {
		t.Errorf("no lock after release: %v", err)
	} else {
		again()
	}
}

func TestErrorText(t *testing.T) {
	out := "Bitcoin Core starting\nError: Cannot obtain a lock on directory /x. Bitcoin Core is probably already running.\nShutdown\n"
	if got := lastError(out); !strings.HasPrefix(got, "Error: Cannot obtain a lock") {
		t.Errorf("lastError: %q", got)
	}
	if lastError("") != "" || lastError("bye\n") != "bye" {
		t.Error("lastError fallbacks")
	}
	if clip("見証見証", 2) != "見証…" || clip("abc", 5) != "abc" {
		t.Error("clip")
	}
	var tl tail
	tl.limit = 4
	_, _ = tl.Write([]byte("abcdef"))
	if tl.String() != "cdef" {
		t.Errorf("tail %q", tl.String())
	}
}
