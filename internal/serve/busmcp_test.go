package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"reasonix/internal/busmcp"
	"reasonix/internal/config"
	"reasonix/internal/control"
)

// busTestToken is a syntactically valid stand-in bearer token; busmcp.New
// only needs a non-empty table, no hex enforcement at construction time.
const busTestToken = "tok-0000000000000000000000000000dev"

// TestHandlerWithBusMountedBootsWithoutRouteConflict pins task 434: with
// bus_mcp enabled the full handler() wiring must build and serve. Go 1.22+'s
// ServeMux panics when one mux holds both the method-less "/mcp" and the
// upstream catch-all "GET /" ("matches more methods, but a more specific
// path") — that panic was serve's startup crash with bus_mcp enabled. The
// registration must stay method-scoped; the catch-all must stay catch-all.
func TestHandlerWithBusMountedBootsWithoutRouteConflict(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handler() with bus_mcp enabled panicked on route registration: %v", r)
		}
	}()
	s := New(control.New(control.Options{}), NewBroadcaster(), config.ServeConfig{
		BusMCP: config.BusMCPConfig{
			Enabled: true,
			Roles:   map[string]string{"dev": busTestToken},
			MailDir: t.TempDir(),
		},
	})
	if s.bus == nil {
		// A vacuous pass here would hide the conflict: fail-closed means
		// nothing is mounted, so the mux would never see /mcp at all.
		t.Fatal("bus failed to construct — test would not exercise the conflict")
	}
	h := s.Handler()

	// POST /mcp reaches the bus endpoint: without a bearer it must be the
	// bus's own 401, not the mux's 404/405 for an unreachable route. The
	// trio loop pins each method-scoped registration — an unregistered
	// method would 405 at the mux instead of reaching bus auth.
	for _, method := range []string{"POST", "GET", "DELETE"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/mcp", strings.NewReader("{}"))
		req.Host = "127.0.0.1" // httptest defaults to example.com; hostGuard 421s that
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s /mcp without bearer: want 401 from bus auth, got %d", method, rec.Code)
		}
	}

	// The upstream surface is untouched: GET / still answers with the index
	// page, and the catch-all still catches unknown GET paths (both were
	// served before the fix; narrowing GET / to GET /{$} would 404 them).
	for _, target := range []string{"/", "/some-unknown-page"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", target, nil)
		req.Host = "127.0.0.1"
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: want 200 via the unchanged catch-all, got %d", target, rec.Code)
		}
	}
}

// TestBusPublicPathExemption pins the contract between the bus and the
// serve-wide auth gate: /mcp and /bus/events skip the browser cookie/query
// gate only when a bus actually mounted (they carry their own per-role
// bearer tokens); every other path, and the same paths with no bus, still
// require the frontend token.
func TestBusPublicPathExemption(t *testing.T) {
	newGate := func(busPublic bool) *authGate {
		ag := newAuthGate(config.ServeConfig{AuthMode: "token", Token: "frontend-token"})
		ag.busPublic = busPublic
		return ag
	}
	passes := func(ag *authGate, method, target string) bool {
		called := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
		ag.middleware(next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, target, nil))
		return called
	}

	busGate := newGate(true)
	for _, target := range []string{"/mcp", "/bus/events"} {
		if !passes(busGate, "POST", target) {
			t.Errorf("bus-mounted gate should pass %s", target)
		}
		if !passes(busGate, "GET", target) {
			t.Errorf("bus-mounted gate should pass GET %s", target)
		}
	}
	if passes(busGate, "POST", "/inbox") {
		t.Error("bus-mounted gate must still guard /inbox")
	}
	// The MCP endpoint is exactly /mcp; anything below it is an unknown
	// route and must keep the frontend gate.
	if passes(busGate, "POST", "/mcp/session") {
		t.Error("path below /mcp is not a bus public path (exact match only)")
	}

	bareGate := newGate(false)
	for _, target := range []string{"/mcp", "/bus/events"} {
		if passes(bareGate, "POST", target) {
			t.Errorf("gate without bus must guard %s", target)
		}
	}
}

// TestRegisterBusRoutesNilBusIsFailClosed: no bus, no routes — a disabled or
// broken config must leave /mcp and /bus/events as ordinary unknown routes.
func TestRegisterBusRoutesNilBusIsFailClosed(t *testing.T) {
	s := &Server{}
	mux := http.NewServeMux()
	s.registerBusRoutes(mux)
	for _, target := range []string{"/mcp", "/bus/events"} {
		resp := httptest.NewRecorder()
		mux.ServeHTTP(resp, httptest.NewRequest("POST", target, strings.NewReader("{}")))
		if resp.Code != http.StatusNotFound {
			t.Fatalf("%s without bus: want 404, got %d", target, resp.Code)
		}
	}
}

// TestRegisterBusRoutesMounted wires a real bus through the same middleware
// chain handler() uses (auth → hostGuard → csrfGuard → mux) and walks one
// denied and one accepted request end to end.
func TestRegisterBusRoutesMounted(t *testing.T) {
	bus, err := busmcp.New(busmcp.Config{
		Enabled: true,
		Roles:   map[string]string{"dev": "tok-0000000000000000000000000000dev"},
		MailDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ag := newAuthGate(config.ServeConfig{AuthMode: "none"})
	s := &Server{bus: bus, auth: ag}
	s.auth.busPublic = true

	mux := http.NewServeMux()
	s.registerBusRoutes(mux)
	handler := ag.middleware(mux)

	denied := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(denied, req)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("mcp without bearer: want 401, got %d", denied.Code)
	}

	ok := httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/mcp", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer tok-0000000000000000000000000000dev")
	handler.ServeHTTP(ok, req)
	if ok.Code == http.StatusUnauthorized {
		t.Fatalf("mcp with bearer: got 401 (status %d, body %s)", ok.Code, ok.Body.String())
	}
}
