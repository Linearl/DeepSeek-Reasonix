package main

// Task 790 acceptance: the resident ACP listener ships off (铁律 2) and the
// flag is the only gate. Off = zero behaviour (no listener, no port bound);
// on = the desktop hosts a loopback ACP endpoint where every connection is an
// independent client surface speaking the same JSON-RPC as `reasonix acp`.

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
	"time"

	"reasonix/internal/config"
)

// Compile-time pin, same discipline as zcodeTaskBusBindings: the frontend
// calls these bindings, so deleting either must fail the build instead of
// resurfacing as a dead selector behind a clicking switch.
type acpListenBindings interface {
	SetExperimentalACPListen(bool) error
	SetACPListenPort(int) error
	ACPListenStatus() map[string]any
}

var _ acpListenBindings = (*App)(nil)

// acpStatus fetches and types the App status map.
func acpStatus(t *testing.T, app *App) map[string]any {
	t.Helper()
	st := app.ACPListenStatus()
	for _, key := range []string{"enabled", "port", "running", "addr", "err"} {
		if _, ok := st[key]; !ok {
			t.Fatalf("ACPListenStatus missing %q: %+v", key, st)
		}
	}
	return st
}

// assertNothingListensOnDefaultPort pins the 铁律 2 zero-port half: with the
// switch off the default port must stay unbound — a dial is refused because
// nothing (this process included) is listening.
func assertNothingListensOnDefaultPort(t *testing.T) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", "127.0.0.1:8790", 500*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatal("default ACP port 8790 is open while the switch is off — the flag-off path bound a port")
	}
}

// TestACPListenOffByDefaultZeroBehavior pins the 铁律 2 half of the
// acceptance: with the lab switch off, startACPListener returns before
// touching the network — no host is armed, nothing listens (the default port
// stays unbound), and the status reads not-enabled/not-running.
func TestACPListenOffByDefaultZeroBehavior(t *testing.T) {
	isolateDesktopUserDirs(t)
	// An address that would fail loudly if the flag-off path ever tried to
	// listen (port 1 is privileged and unbindable in practice).
	t.Setenv("REASONIX_ACP_LISTEN_ADDR", "127.0.0.1:1")
	app := &App{}
	app.startACPListener(&config.Config{})
	if app.acpListener != nil {
		t.Fatal("flag-off start must arm nothing")
	}
	st := acpStatus(t, app)
	if st["enabled"].(bool) || st["running"].(bool) {
		t.Fatalf("flag-off status must read enabled=false running=false, got %+v", st)
	}
	if st["err"].(string) != "" {
		t.Fatalf("flag-off is not an error state, got %q", st["err"])
	}
	// The default port the switch would bind stays unbound.
	assertNothingListensOnDefaultPort(t)
	// closeACPListener on a never-started app must be a safe no-op.
	app.closeACPListener()
}

// TestACPListenOnHostsEndpoint pins the feature half: with the switch on, the
// desktop hosts the ACP endpoint on loopback and a TCP client completes the
// initialize handshake — the same JSON-RPC surface the stdio command serves.
func TestACPListenOnHostsEndpoint(t *testing.T) {
	isolateDesktopUserDirs(t)
	// Port 0: the OS picks a free port, so the test never fights the real
	// 8790 a resident listener (or anything else) may hold.
	t.Setenv("REASONIX_ACP_LISTEN_ADDR", "127.0.0.1:0")
	app := &App{}
	cfg := &config.Config{}
	cfg.Desktop.ExperimentalACPListen = true
	app.startACPListener(cfg)
	st := acpStatus(t, app)
	if !st["running"].(bool) {
		t.Fatalf("flag-on start must host the listener, got %+v", st)
	}
	addr := st["addr"].(string)
	if addr == "" {
		t.Fatal("running listener must report its bound address")
	}
	defer app.closeACPListener()

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()
	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1}}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("send initialize: %v", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read initialize reply: %v", err)
	}
	var resp struct {
		ID     *json.Number `json:"id"`
		Result struct {
			ProtocolVersion int `json:"protocolVersion"`
			AgentInfo       struct {
				Name string `json:"name"`
			} `json:"agentInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("decode reply %q: %v", line, err)
	}
	if resp.ID == nil || resp.ID.String() != "1" {
		t.Errorf("reply id = %v, want 1", resp.ID)
	}
	if resp.Result.ProtocolVersion != 1 {
		t.Errorf("protocolVersion = %d, want 1", resp.Result.ProtocolVersion)
	}
	if resp.Result.AgentInfo.Name != "reasonix" {
		t.Errorf("agentInfo.name = %q, want reasonix", resp.Result.AgentInfo.Name)
	}
}

// TestACPListenBindFailureIsVisibleDegraded pins the failure posture: a bind
// failure (something else holds the port) leaves the desktop alive with a
// status-visible error, never a crash.
func TestACPListenBindFailureIsVisibleDegraded(t *testing.T) {
	isolateDesktopUserDirs(t)
	// Occupy the address first, then ask the listener for the same one.
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("blocker listen: %v", err)
	}
	defer blocker.Close()
	t.Setenv("REASONIX_ACP_LISTEN_ADDR", blocker.Addr().String())

	app := &App{}
	cfg := &config.Config{}
	cfg.Desktop.ExperimentalACPListen = true
	app.startACPListener(cfg)
	st := acpStatus(t, app)
	if st["running"].(bool) {
		t.Fatal("bind conflict must not report running")
	}
	if st["err"].(string) == "" {
		t.Fatal("bind conflict must surface an error on the status")
	}
	defer app.closeACPListener()
}
