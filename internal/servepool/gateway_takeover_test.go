package servepool

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Task 249: POST /p/<id>/takeover-session is gated before it reaches the
// project serve — a denied takeover must not spawn a serve, an allowed one
// must arrive at the serve with its body intact, and no gate keeps the
// historical pass-through.

func takeoverGateTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	m := newTestManager(t, root)
	return m, root
}

// backendPort extracts the loopback port of an httptest server.
func backendPort(t *testing.T, ts *httptest.Server) int {
	t.Helper()
	port, err := strconv.Atoi(strings.TrimPrefix(ts.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatalf("parse backend port from %s: %v", ts.URL, err)
	}
	return port
}

// takeoverGateForceRunning points a project's pooled-serve record at a live
// test backend so handleProxy can forward without spawning the real reasonix
// binary.
func takeoverGateForceRunning(t *testing.T, m *Manager, id string, port int) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[id]
	if !ok {
		t.Fatalf("project %q not registered", id)
	}
	p.state = "running"
	p.port = port
	p.lastUse = time.Now()
}

func TestGatewayTakeoverGateDenyBeforeSpawn(t *testing.T) {
	m, root := takeoverGateTestManager(t)
	id := WorkspaceSlug(root)

	var got TakeoverGateRequest
	g := NewGateway(m, "s")
	g.SetTakeoverGate(func(req TakeoverGateRequest) (bool, int, string) {
		got = req
		return false, http.StatusConflict, "takeover rejected by the desktop user; this device keeps local control"
	})
	ts := httptest.NewServer(g)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/p/"+id+"/takeover-session",
		strings.NewReader(`{"name":"sess-a","from":"gc-device-1"}`))
	req.Header.Set("Authorization", "Bearer s")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	if !strings.Contains(string(body), "rejected by the desktop user") {
		t.Fatalf("deny message = %q, want the gate's explicit refusal", string(body))
	}
	if got.SessionName != "sess-a" || got.From != "gc-device-1" {
		t.Fatalf("gate request = %+v, want parsed name/from", got)
	}
	if got.ProjectRoot == "" {
		t.Fatalf("gate request missing project root")
	}
	// A denied takeover must not spawn a serve: the project never left the
	// unopened state (initial state is "stopped"; a spawn attempt would have
	// recorded a failure and an error on the fake binary).
	m.mu.Lock()
	p := m.projects[id]
	failures, spawnErr := p.failures, p.err
	m.mu.Unlock()
	if failures != 0 || spawnErr != "" {
		t.Fatalf("project failures=%d err=%q after a denied takeover; the gate must run before Open", failures, spawnErr)
	}
}

func TestGatewayTakeoverGateAllowForwardsBody(t *testing.T) {
	m, root := takeoverGateTestManager(t)
	id := WorkspaceSlug(root)

	var mu sync.Mutex
	var gotPath, gotName, gotFrom string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		gotPath = r.URL.Path
		var body struct {
			Name string `json:"name"`
			From string `json:"from"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotName, gotFrom = body.Name, body.From
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	port := backendPort(t, backend)

	g := NewGateway(m, "s")
	g.SetTakeoverGate(func(TakeoverGateRequest) (bool, int, string) { return true, 0, "" })
	ts := httptest.NewServer(g)
	defer ts.Close()

	takeoverGateForceRunning(t, m, id, port)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/p/"+id+"/takeover-session",
		strings.NewReader(`{"name":"sess-a","from":"gc-device-1"}`))
	req.Header.Set("Authorization", "Bearer s")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 from the project serve", resp.StatusCode)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/takeover-session" {
		t.Fatalf("backend path = %q, want the prefix-stripped takeover route", gotPath)
	}
	if gotName != "sess-a" || gotFrom != "gc-device-1" {
		t.Fatalf("forwarded body = name:%q from:%q, want the buffered request body intact", gotName, gotFrom)
	}
}

func TestGatewayTakeoverPassthroughWithoutGate(t *testing.T) {
	m, root := takeoverGateTestManager(t)
	id := WorkspaceSlug(root)

	var mu sync.Mutex
	hit := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hit = true
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	// No gate installed: the historical pass-through must be unchanged.
	g := NewGateway(m, "s")
	ts := httptest.NewServer(g)
	defer ts.Close()

	takeoverGateForceRunning(t, m, id, backendPort(t, backend))

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/p/"+id+"/takeover-session",
		strings.NewReader(`{"name":"sess-a"}`))
	req.Header.Set("Authorization", "Bearer s")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	mu.Lock()
	defer mu.Unlock()
	if !hit {
		t.Fatal("backend not hit; nil gate must pass through")
	}
}

func TestGatewayTakeoverGateMalformedBodyStillGated(t *testing.T) {
	m, _ := takeoverGateTestManager(t)
	id := m.Projects()[0].ID
	if id == "" {
		t.Fatal("no project registered")
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	var got TakeoverGateRequest
	g := NewGateway(m, "s")
	g.SetTakeoverGate(func(req TakeoverGateRequest) (bool, int, string) {
		got = req
		return true, 0, ""
	})
	ts := httptest.NewServer(g)
	defer ts.Close()

	takeoverGateForceRunning(t, m, id, backendPort(t, backend))

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/p/"+id+"/takeover-session",
		strings.NewReader(`not-json`))
	req.Header.Set("Authorization", "Bearer s")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want the allowed takeover forwarded", resp.StatusCode)
	}
	if got.SessionName != "" || got.From != "" {
		t.Fatalf("gate request = %+v, want empty name/from for a malformed body", got)
	}
	if got.ProjectRoot == "" {
		t.Fatalf("gate root empty, want the project root")
	}
}
