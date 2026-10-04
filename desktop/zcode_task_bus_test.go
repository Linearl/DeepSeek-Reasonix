package main

// Task 439 acceptance: the built-in zcode task bus ships off (铁律 2) and the
// flag is the only gate. Off = zero behaviour (no listener, nothing mounted);
// on = the desktop itself serves the bus MCP endpoint with the enrolled role
// table, fail-closed on every broken-input path.

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"reasonix/internal/config"
)

// Compile-time pin, same discipline as outputStyleBindings: the frontend calls
// these bindings, so deleting either must fail the build instead of resurfacing
// as a dead selector behind a clicking switch.
type zcodeTaskBusBindings interface {
	SetExperimentalZcodeTaskBus(bool) error
	ZcodeTaskBusStatus() map[string]any
}

var _ zcodeTaskBusBindings = (*App)(nil)

// busStatus fetches and types the App status map.
func busStatus(t *testing.T, app *App) map[string]any {
	t.Helper()
	st := app.ZcodeTaskBusStatus()
	for _, key := range []string{"enabled", "running", "addr", "endpoint", "roles", "err"} {
		if _, ok := st[key]; !ok {
			t.Fatalf("ZcodeTaskBusStatus missing %q: %+v", key, st)
		}
	}
	return st
}

// TestZcodeTaskBusOffByDefaultZeroBehavior pins the 铁律 2 half of the
// acceptance: with the lab switch off, startZcodeTaskBus returns before
// touching the network — no host is armed, nothing listens, and the status
// reads not-enabled/not-running with an empty role list.
func TestZcodeTaskBusOffByDefaultZeroBehavior(t *testing.T) {
	isolateDesktopUserDirs(t)
	// A bind address that would fail loudly if the flag-off path ever tried
	// to listen (an invalid address cannot be bound).
	t.Setenv("REASONIX_TASK_BUS_ADDR", "127.0.0.1:1")
	app := &App{}
	cfg := &config.Config{}
	cfg.Serve.BusMCP.Roles = map[string]string{"dev": "tok"}
	app.startZcodeTaskBus(cfg)
	if app.zcodeTaskBus != nil {
		t.Fatal("flag-off start must arm nothing")
	}
	st := busStatus(t, app)
	if st["enabled"].(bool) || st["running"].(bool) {
		t.Fatalf("flag-off status must read enabled=false running=false, got %+v", st)
	}
	if roles := st["roles"].([]string); len(roles) != 0 {
		t.Fatalf("flag-off status must report no roles, got %v", roles)
	}
	if st["err"].(string) != "" {
		t.Fatalf("flag-off is not an error state, got %q", st["err"])
	}
	// closeZcodeTaskBus on a never-started app must be a safe no-op.
	app.closeZcodeTaskBus()
}

// TestZcodeTaskBusOnHostsEndpoint pins the feature half: with the switch on
// and an enrolled role table, the desktop mounts the bus on the loopback
// listener; the endpoint authenticates per-role (401 without/with a wrong
// token) and a correct token reaches the MCP handler (initialize answered).
func TestZcodeTaskBusOnHostsEndpoint(t *testing.T) {
	isolateDesktopUserDirs(t)
	// Port 0: the OS picks a free port, so the test never fights the real
	// 8787 an external serve (or the user's resident bus) may hold.
	t.Setenv("REASONIX_TASK_BUS_ADDR", "127.0.0.1:0")
	app := &App{}
	cfg := &config.Config{}
	cfg.Desktop.ExperimentalZcodeTaskBus = true
	cfg.Serve.BusMCP.Roles = map[string]string{"dev": "devtoken", "heartbeat": "hbtoken"}
	cfg.Serve.BusMCP.MailDir = t.TempDir()
	app.startZcodeTaskBus(cfg)
	if app.zcodeTaskBus == nil {
		t.Fatal("flag-on start must arm the host")
	}
	st := busStatus(t, app)
	if !st["running"].(bool) {
		t.Fatalf("bus must be running after a flag-on start, got %+v", st)
	}
	addr := st["addr"].(string)
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("bus must bind loopback, got %q", addr)
	}
	roles := st["roles"].([]string)
	if len(roles) != 2 || roles[0] != "dev" || roles[1] != "heartbeat" {
		t.Fatalf("status must list the sorted enrolled roles, got %v", roles)
	}
	if want := "http://" + addr + "/mcp"; st["endpoint"].(string) != want {
		t.Fatalf("endpoint = %q, want %q", st["endpoint"], want)
	}

	post := func(token, body string) (int, string) {
		req, err := http.NewRequest("POST", "http://"+addr+"/mcp", strings.NewReader(body))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		// The MCP streamable transport requires both content types in Accept.
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /mcp: %v", err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return resp.StatusCode, string(b)
	}

	// Fail closed: the route answers 401 without or with a wrong token —
	// proof the routes are mounted AND gated (never open access).
	for _, tc := range []struct{ label, token string }{
		{"no token", ""},
		{"wrong token", "nope"},
	} {
		if code, _ := post(tc.token, "{}"); code != http.StatusUnauthorized {
			t.Fatalf("%s: want 401, got %d", tc.label, code)
		}
	}

	// Feature live: a valid role token gets past auth and the MCP handler
	// answers a real initialize handshake.
	const init = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"task439-test","version":"0"}}}`
	code, body := post("devtoken", init)
	if code != http.StatusOK {
		t.Fatalf("initialize with valid token: want 200, got %d (%s)", code, body)
	}
	if !strings.Contains(body, "jsonrpc") {
		t.Fatalf("initialize response is not JSON-RPC: %s", body)
	}

	// Shutdown stops the listener: the port refuses new connections and the
	// status flips to not-running.
	app.closeZcodeTaskBus()
	if app.zcodeTaskBus != nil {
		t.Fatal("close must disarm the host")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			break // refused: the listener is really gone
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatalf("listener still accepting after close on %s", addr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st := busStatus(t, app); st["running"].(bool) {
		t.Fatalf("status must read not-running after close, got %+v", st)
	}
}

// TestZcodeTaskBusEmptyRolesFailClosed pins the broken-config posture: the
// lab switch alone must not open anything — an empty role table mounts
// nothing and surfaces the reason on the status.
func TestZcodeTaskBusEmptyRolesFailClosed(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("REASONIX_TASK_BUS_ADDR", "127.0.0.1:0")
	app := &App{}
	cfg := &config.Config{}
	cfg.Desktop.ExperimentalZcodeTaskBus = true
	app.startZcodeTaskBus(cfg)
	if app.zcodeTaskBus == nil || app.zcodeTaskBus.bus != nil {
		t.Fatalf("empty role table must mount nothing, got %+v", app.zcodeTaskBus)
	}
	st := busStatus(t, app)
	if st["running"].(bool) {
		t.Fatal("empty role table must not report running")
	}
	if st["err"].(string) == "" {
		t.Fatal("empty role table must surface a visible reason")
	}
}

// TestZcodeTaskBusPortBusyVisible pins the degraded-bind path: when the
// fixed port is already held (e.g. an external serve from the pre-439 vbs
// era), the desktop reports not-running with the reason instead of crashing.
func TestZcodeTaskBusPortBusyVisible(t *testing.T) {
	isolateDesktopUserDirs(t)
	// Hold a port, then point the bus at it.
	holder, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind holder: %v", err)
	}
	defer holder.Close()
	t.Setenv("REASONIX_TASK_BUS_ADDR", holder.Addr().String())
	app := &App{}
	cfg := &config.Config{}
	cfg.Desktop.ExperimentalZcodeTaskBus = true
	cfg.Serve.BusMCP.Roles = map[string]string{"dev": "devtoken"}
	cfg.Serve.BusMCP.MailDir = t.TempDir()
	app.startZcodeTaskBus(cfg)
	st := busStatus(t, app)
	if st["running"].(bool) {
		t.Fatal("busy port must not report running")
	}
	if !strings.Contains(st["err"].(string), "listen") {
		t.Fatalf("busy port must surface the bind failure, got %q", st["err"])
	}
	// Cleanup must also tolerate this degraded shape.
	app.closeZcodeTaskBus()
}

// TestZcodeTaskBusSettingsRoundTrip pins the settings face: the switch reads
// back through both settings views, flips only via the setter, and ships off.
func TestZcodeTaskBusSettingsRoundTrip(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := &App{}
	if got := app.Settings().ExperimentalZcodeTaskBus; got {
		t.Fatal("default settings view must read the switch off")
	}
	if got := app.DesktopStartupSettings().ExperimentalZcodeTaskBus; got {
		t.Fatal("default startup view must read the switch off")
	}
	if err := app.SetExperimentalZcodeTaskBus(true); err != nil {
		t.Fatalf("SetExperimentalZcodeTaskBus: %v", err)
	}
	if got := app.Settings().ExperimentalZcodeTaskBus; !got {
		t.Fatal("Settings() must read the switch back on")
	}
	if got := app.DesktopStartupSettings().ExperimentalZcodeTaskBus; !got {
		t.Fatal("DesktopStartupSettings() must read the switch back on")
	}
	if err := app.SetExperimentalZcodeTaskBus(false); err != nil {
		t.Fatalf("SetExperimentalZcodeTaskBus(false): %v", err)
	}
	if got := app.Settings().ExperimentalZcodeTaskBus; got {
		t.Fatal("Settings() must read the flip back off")
	}
}

// TestZcodeTaskBusStatusJSON pins the Wails wire shape: the status map must
// marshal with the exact keys the lab card reads.
func TestZcodeTaskBusStatusJSON(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := &App{}
	b, err := json.Marshal(app.ZcodeTaskBusStatus())
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	for _, key := range []string{"enabled", "running", "addr", "endpoint", "roles", "err"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("status JSON missing %q: %s", key, b)
		}
	}
}
