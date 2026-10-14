package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed ui/index.html ui/app.js ui/style.css ui/strings.json
var ui embed.FS

// csp lets the page load only its own files and talk only to this server.
const csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// maxCheckBody bounds the body of a check: the transaction and a little JSON around it.
const maxCheckBody = maxTxHex + 4096

// server is the status page. It listens on 127.0.0.1 only, on a port the system picks, and answers only
// under a random path, so that no other site can find or call it.
type server struct {
	addr      string // 127.0.0.1:port, the only Host header answered
	token     string
	node      *node
	collector *collector
	assets    map[string]asset

	mu        sync.Mutex
	lastCheck *checkResult
}

type asset struct {
	body        []byte
	contentType string
}

func newServer(n *node, dataDir string) (*server, error) {
	assets, err := loadAssets()
	if err != nil {
		return nil, err
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &server{
		addr:      l.Addr().String(),
		token:     hex.EncodeToString(token),
		node:      n,
		collector: &collector{node: n, dataDir: dataDir},
		assets:    assets,
	}
	hs := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	go func() { _ = hs.Serve(l) }()
	return s, nil
}

// url is the page's address, token included.
func (s *server) url() string {
	return "http://" + s.addr + "/" + s.token + "/"
}

// loadAssets reads the embedded page. The strings go to the page as a script, so that the page needs no
// request beyond its own files.
func loadAssets() (map[string]asset, error) {
	return loadAssetsForPlatform(runtime.GOOS)
}

func loadAssetsForPlatform(platform string) (map[string]asset, error) {
	files := map[string][]byte{}
	for _, name := range []string{"index.html", "app.js", "style.css", "strings.json"} {
		data, err := ui.ReadFile("ui/" + name)
		if err != nil {
			return nil, err
		}
		files[name] = data
	}
	var parsed map[string]map[string]string
	files["strings.json"] = []byte(fillProgramNames(string(files["strings.json"]), platform))
	if err := json.Unmarshal(files["strings.json"], &parsed); err != nil {
		return nil, err
	}
	script := append([]byte("window.STRINGS = "), bytes.TrimSpace(files["strings.json"])...)
	script = append(script, ";\n"...)
	return map[string]asset{
		"":           {files["index.html"], "text/html; charset=utf-8"},
		"app.js":     {files["app.js"], "text/javascript; charset=utf-8"},
		"style.css":  {files["style.css"], "text/css; charset=utf-8"},
		"strings.js": {script, "text/javascript; charset=utf-8"},
	}, nil
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	// Only this server's own address is answered: a site that reaches 127.0.0.1 under a name of its own
	// (DNS rebinding) is turned away.
	if r.Host != s.addr {
		http.Error(w, "unknown host", http.StatusMisdirectedRequest)
		return
	}
	name, ok := s.route(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch name {
	case "api/status":
		if allow(w, r, http.MethodGet) {
			writeJSON(w, http.StatusOK, s.collector.get())
		}
	case "api/report":
		if allow(w, r, http.MethodGet) {
			s.mu.Lock()
			last := s.lastCheck
			s.mu.Unlock()
			h.Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte(witnessReport(s.collector.get(), last)))
		}
	case "api/check":
		s.check(w, r)
	default:
		a, found := s.assets[name]
		if !found {
			http.NotFound(w, r)
			return
		}
		if allow(w, r, http.MethodGet) {
			h.Set("Content-Type", a.contentType)
			_, _ = w.Write(a.body)
		}
	}
}

// route takes "/<token>/<name>" apart. Any other path, or a wrong token, is not found.
func (s *server) route(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/")
	if !ok || len(rest) <= len(s.token) || rest[len(s.token)] != '/' {
		return "", false
	}
	if subtle.ConstantTimeCompare([]byte(rest[:len(s.token)]), []byte(s.token)) != 1 {
		return "", false
	}
	return rest[len(s.token)+1:], true
}

// allow answers 405 to any other method; GET also allows HEAD.
func allow(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method || (method == http.MethodGet && r.Method == http.MethodHead) {
		return true
	}
	w.Header().Set("Allow", method)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

// check has the node judge a pasted transaction.
func (s *server) check(w http.ResponseWriter, r *http.Request) {
	if !allow(w, r, http.MethodPost) {
		return
	}
	// The page posts JSON from its own origin. A page of another site cannot send this content type without a
	// preflight, which this server never grants; browsers also mark its request with Origin and Sec-Fetch-Site.
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "type"})
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+s.addr {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin"})
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin"})
		return
	}
	var req struct {
		Hex string `json:"hex"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCheckBody)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hex"})
		return
	}
	raw, ok := cleanHex(req.Hex)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hex"})
		return
	}
	rpc := s.node.client()
	if rpc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "node"})
		return
	}
	res, err := checkTransaction(rpc, raw)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "node"})
		return
	}
	s.mu.Lock()
	s.lastCheck = &res
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, res)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
