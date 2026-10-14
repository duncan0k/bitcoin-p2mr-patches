package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestNodeArgsAreJoinMd(t *testing.T) {
	args := nodeArgs(`C:\Users\a b\AppData\Local\Ark0Witness`, 40123)
	for _, want := range []string{
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
		if !strings.Contains(c.NoDataFolder, "%v") || !strings.Contains(c.StartFailed, "%v") || !strings.Contains(c.Stopped, "%s") {
			t.Errorf("%s: a console text lost its placeholder", lang)
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

func (f *fakeNode) port() int {
	u, _ := url.Parse(f.server.URL)
	port, _ := strconv.Atoi(u.Port())
	return port
}

func (f *fakeNode) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(f.cookie)) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var req struct {
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
	_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": rpcErr, "id": "witness"})
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
		return map[string]any{"height": 30000, "leaf_version": "e0", "ec_disabled_next_block": ecDisabled, "tripwires": []map[string]any{
			{"outpoint": "4319a38c0842846474ff038b54f22aec844147a97c2abc462bfb92957c1bc3a0:1", "state": "unspent", "label": "BIP-341 NUMS"},
			{"outpoint": "8991bb83673474b7c324f303365479c093d0b34f5e7d7ddf0648bcfdb1d42495:0", "state": drillState, "label": "drill"},
		}}, nil
	}
	f.answers["getnetworkinfo"] = func([]json.RawMessage) (any, *rpcError) {
		return map[string]any{"subversion": "/Satoshi:31.1.0/"}, nil
	}
}

func (f *fakeNode) node() *node {
	n := newNode("bitcoind", f.dataDir)
	n.rpc = newRPCClient(f.port(), cookiePath(f.dataDir))
	return n
}

func TestStatusAndReport(t *testing.T) {
	f := newFakeNode(t)
	f.ark0(30100, "missing", true)
	n := f.node()
	c := &collector{node: n, dataDir: f.dataDir}
	s := c.collect()
	if s.Node != "running" || s.Blocks != 30100 || s.Ark0 != "ok" || s.H3 != 30000 || s.BlockH3 != "000030000hash" ||
		!s.ECDisabledNextBlock || len(s.Tripwires) != 2 || s.Tripwires[1].Label != "drill" || s.Subversion != "/Satoshi:31.1.0/" || s.Peers != 1 {
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

	// Before 30000, the hash of block 30000 is not asked for.
	g := newFakeNode(t)
	g.ark0(22600, "unspent", false)
	s = (&collector{node: g.node(), dataDir: g.dataDir}).collect()
	if s.BlockH3 != "" || s.ECDisabledNextBlock || s.Ark0 != "ok" {
		t.Errorf("status %+v", s)
	}
	if r := witnessReport(s, nil); !strings.Contains(r, "Block 30000 (M3): not reached yet") || strings.Contains(r, "Checked") {
		t.Errorf("report:\n%s", r)
	}
}

func TestStatusEdges(t *testing.T) {
	// A node without gettripwireinfo is not M3.
	f := newFakeNode(t)
	f.ark0(22600, "unspent", false)
	delete(f.answers, "gettripwireinfo")
	if s := (&collector{node: f.node(), dataDir: f.dataDir}).collect(); !s.NotM3 {
		t.Errorf("not M3 not seen: %+v", s)
	}
	// Another chain.
	f.ark0(22600, "unspent", false)
	f.answers["getblockhash"] = func([]json.RawMessage) (any, *rpcError) { return "0000other", nil }
	if s := (&collector{node: f.node(), dataDir: f.dataDir}).collect(); s.Ark0 != "mismatch" {
		t.Errorf("mismatch not seen: %+v", s)
	}
	// Still loading.
	f.answers["getblockchaininfo"] = func([]json.RawMessage) (any, *rpcError) {
		return nil, &rpcError{Code: rpcInWarmup, Message: "Loading block index…"}
	}
	n := f.node()
	if s := (&collector{node: n, dataDir: f.dataDir}).collect(); s.Node != "starting" || s.Warmup != "Loading block index…" {
		t.Errorf("warmup: %+v", s)
	}
	if !n.rpc.alive() {
		t.Error("a loading node is alive")
	}
	// A wrong cookie gets no answer.
	if err := os.WriteFile(cookiePath(f.dataDir), []byte("__cookie__:other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n.rpc.alive() {
		t.Error("a node that refuses the cookie is not this folder's")
	}
	// No cookie yet.
	_ = os.Remove(cookiePath(f.dataDir))
	if err := n.rpc.call("getblockcount", nil, nil); err != errNotReady {
		t.Errorf("no cookie: %v", err)
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
	n := newNode("bitcoind", t.TempDir())
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
	if _, ok := readPort(dir); ok {
		t.Error("port without a file")
	}
	if err := writePort(dir, 40123); err != nil {
		t.Fatal(err)
	}
	if port, ok := readPort(dir); !ok || port != 40123 {
		t.Errorf("port %d %v", port, ok)
	}
	for _, bad := range []string{"0", "70000", "x", ""} {
		_ = os.WriteFile(filepath.Join(dir, portFile), []byte(bad), 0o600)
		if _, ok := readPort(dir); ok {
			t.Errorf("port %q accepted", bad)
		}
	}
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
	if lastError("") != "the node stopped" || lastError("bye\n") != "bye" {
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
