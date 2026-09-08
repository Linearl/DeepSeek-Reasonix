package servepool

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Gateway is the single-entry HTTP surface in front of the serve pool.
// Remote clients (GrandCouncil, Reasonix-web) talk to exactly one endpoint;
// /p/<project-id>/* requests are lazily spawned and reverse-proxied to the
// project's 127.0.0.1 serve. All endpoints require the gateway bearer token.
type Gateway struct {
	mgr   *Manager
	token string
	proxy map[string]*httputil.ReverseProxy
	// sessionsSources registers inline /sessions providers for virtual
	// projects (no backing serve process). Keyed by project id.
	sessionsMu      sync.RWMutex
	sessionsSources map[string]func() []SessionEntry
}

// NewGateway builds the gateway handler. token must be a non-empty secret
// (desktop-generated and shown in settings; clients configure it once).
func NewGateway(mgr *Manager, token string) *Gateway {
	if strings.TrimSpace(token) == "" {
		token = newToken()
	}
	return &Gateway{
		mgr:             mgr,
		token:           token,
		proxy:           map[string]*httputil.ReverseProxy{},
		sessionsSources: map[string]func() []SessionEntry{},
	}
}

// SetSessionsSource registers an inline /sessions provider for a virtual
// project id. The provider runs on the gateway process (the desktop app),
// so it can read session metadata without a spawned serve.
func (g *Gateway) SetSessionsSource(id string, fn func() []SessionEntry) {
	g.sessionsMu.Lock()
	defer g.sessionsMu.Unlock()
	g.sessionsSources[id] = fn
}

// Token returns the gateway bearer token (for UI display / first setup).
func (g *Gateway) Token() string { return g.token }

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &loggingResponseWriter{ResponseWriter: w}
	defer func() {
		// Access log for the phone-connect/disconnect triage (2026-09-01
		// desktop deaths): every request records method, path, status,
		// duration, and remote address in the desktop rolling log.
		log.Printf("[gateway] %s %s -> %d in %s remote=%s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond), r.RemoteAddr)
	}()
	if !g.authorized(r) {
		http.Error(rec, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/status":
		g.handleStatus(rec, r)
	case r.Method == http.MethodGet && r.URL.Path == "/manifest":
		g.handleManifest(rec, r)
	case r.Method == http.MethodPost && r.URL.Path == "/projects/open":
		g.handleOpen(rec, r)
	case strings.HasPrefix(r.URL.Path, "/p/"):
		g.handleProxy(rec, r)
	default:
		http.NotFound(rec, r)
	}
}

// loggingResponseWriter captures the status code for the gateway access log.
type loggingResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggingResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

// handleStatus makes the gateway handshake-compatible with single-serve
// clients (GrandCouncil's ConnectionTester probes GET /status and expects a
// JSON body containing "label" or "plan"). It only answers after the gateway
// Bearer auth, so it doubles as a liveness probe for remote clients.
func (g *Gateway) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"label":   "serve pool gateway",
		"gateway": true,
		"plan":    false,
	})
}

func (g *Gateway) authorized(r *http.Request) bool {
	// Accept both bearer-header and ?token= query auth, mirroring the
	// single-serve auth (serve/auth.go reads r.URL.Query().Get("token")).
	// GrandCouncil's HttpClientFactory injects ?token= (not a bearer header),
	// so the gateway must honor it or remote clients get HTTP 401.
	given := ""
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(auth, "Bearer ") {
		given = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	if given == "" {
		given = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if given == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(given), []byte(g.token)) == 1
}

func (g *Gateway) handleManifest(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, g.mgr.Projects())
}

func (g *Gateway) handleOpen(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.ID) == "" {
		http.Error(w, `missing "id"`, http.StatusBadRequest)
		return
	}
	if err := g.mgr.Open(strings.TrimSpace(body.ID)); err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"status": "running"})
}

// handleProxy routes /p/<id>/<rest> to the project's serve, lazily spawning
// it on first use, stripping the /p/<id> prefix before forwarding. Virtual
// projects (manifest-only, no serve process) are served inline instead:
// /sessions comes from the registered sessions source; other paths answer
// 501 until inline handlers exist for them.
func (g *Gateway) handleProxy(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/p/")
	id, tail, ok := strings.Cut(rest, "/")
	if !ok {
		http.Error(w, "project route must be /p/<id>/...", http.StatusBadRequest)
		return
	}
	id = strings.TrimSpace(id)
	if g.mgr.IsVirtual(id) {
		g.handleVirtual(w, r, id, tail)
		return
	}
	if err := g.mgr.Open(id); err != nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	g.mgr.Touch(id)
	port := g.mgr.Port(id)
	if port == 0 {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]string{"error": "project serve not ready"})
		return
	}
	proxy := g.proxyFor(id, port)
	// Rewrite the incoming request onto the target path (prefix stripped).
	out := r.Clone(r.Context())
	out.URL.Path = "/" + tail
	if out.URL.RawPath != "" {
		out.URL.RawPath = "/" + strings.TrimPrefix(out.URL.RawPath, "/p/"+id+"/")
	}
	// Forward per-project auth. Serves in token mode authenticate via the
	// reasonix_token cookie or the ?token= query (checkToken) — they do not
	// read Authorization headers, and the incoming request still carries the
	// gateway's own ?token= which must not leak upstream (it would fail the
	// serve's query check before the cookie path and yield 401).
	if tok := g.mgr.Token(id); tok != "" {
		out.Header.Set("Cookie", "reasonix_token="+tok)
		out.Header.Set("Authorization", "Bearer "+tok)
	}
	if q := out.URL.Query(); q.Get("token") != "" {
		q.Del("token")
		out.URL.RawQuery = q.Encode()
	}
	proxy.ServeHTTP(w, out)
}

// handleVirtual serves a virtual project's requests inline from the gateway
// process (which for the desktop is the app itself, with direct access to
// session data). Only GET /sessions is supported for now; other paths answer
// 501 so clients can degrade gracefully.
func (g *Gateway) handleVirtual(w http.ResponseWriter, r *http.Request, id, tail string) {
	if r.Method != http.MethodGet || tail != "sessions" {
		writeJSONStatus(w, http.StatusNotImplemented, map[string]string{
			"error": "virtual project supports GET /sessions only",
		})
		return
	}
	g.sessionsMu.RLock()
	fn := g.sessionsSources[id]
	g.sessionsMu.RUnlock()
	if fn == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]string{
			"error": "no sessions source registered for " + id,
		})
		return
	}
	sessions := fn()
	if sessions == nil {
		sessions = []SessionEntry{}
	}
	writeJSON(w, sessions)
}

func (g *Gateway) proxyFor(id string, port int) *httputil.ReverseProxy {
	if p, ok := g.proxy[id]; ok {
		return p
	}
	target, _ := url.Parse("http://127.0.0.1:" + itoa(port))
	p := httputil.NewSingleHostReverseProxy(target)
	p.FlushInterval = -1 // stream SSE/turn deltas to remote clients immediately
	p.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		// The serve's hostGuard (DNS-rebinding defense) rejects any Host that
		// is not loopback/its listen address — keep the remote client's Host
		// header (e.g. 10.0.2.2:18789) out of the proxied request.
		req.Host = target.Host
	}
	// Proxy write-back failures are the phone-disconnect signal the 2026-09-01
	// desktop-death triage needs: log them through the standard logger (the
	// desktop redirects it to the rolling file) instead of the silent default.
	logger := log.New(log.Writer(), "[gateway-proxy] ", log.LstdFlags)
	p.ErrorLog = logger
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Printf("proxy write-back failed project=%s %s %s: %v", id, r.Method, r.URL.Path, err)
		// The cached serve is unreachable (process died / port stale): drop
		// the running cache so the next request re-spawns instead of every
		// call failing against the dead port.
		g.mgr.Invalidate(id)
		writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": "upstream serve unreachable"})
	}
	g.proxy[id] = p
	return p
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func writeJSON(w http.ResponseWriter, v any) {
	writeJSONStatus(w, http.StatusOK, v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
