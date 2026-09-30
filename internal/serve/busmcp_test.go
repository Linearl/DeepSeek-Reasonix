package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"reasonix/internal/busmcp"
	"reasonix/internal/config"
)

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
