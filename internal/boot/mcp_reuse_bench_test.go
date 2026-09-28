package boot

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/plugin"
)

// handshakeStub is mcpHostSessionStub plus the two knobs the task-334 bench
// needs: an initialize counter (the handshake itself, not a tools/call) and a
// per-handshake delay that simulates the installed mcp stage (19-21s of the
// 24s switch latency) at a scale the test can afford.
func handshakeStub(t *testing.T, name string, delay time.Duration, handshakes *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     *json.RawMessage `json:"id"`
			Method string           `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			if handshakes != nil {
				handshakes.Add(1)
			}
			if delay > 0 {
				time.Sleep(delay)
			}
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]any{"name": name, "version": "1"},
				"capabilities":    map[string]any{"tools": map[string]any{}},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name":        "ping",
				"description": "probe tool",
				"inputSchema": map[string]any{"type": "object"},
			}}}
		default:
			http.Error(w, "unsupported method", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result})
	}))
}

// TestMCPReuseAcrossRebuildsBench is the task-334 bench: one rebuild pays the
// handshake delay, the next rebuild with an unchanged MCP config and the same
// shared host pays none — proved three independent ways (handshake counters,
// wall clock under the injected delay, and the boot: mcp stage connect_fresh
// log line) plus the two negative directions: a config change must handshake
// the new server, and REASONIX_MCP_NO_REUSE must restore full handshakes.
func TestMCPReuseAcrossRebuildsBench(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	writeFile(t, dir, "reasonix.toml", mcpCapabilityTestProviderConfig)
	registerBootTokenProfileTestProvider()
	setBootTokenProfileTestProvider(t, testutil.NewMock("mcp-reuse-bench", testutil.Turn{Text: "ok"}))

	const handshakeDelay = time.Second // scales the installed 19-21s mcp stage down to test budget.
	var hsA, hsB atomic.Int32
	srvA := handshakeStub(t, "bench-a", handshakeDelay, &hsA)
	defer srvA.Close()
	srvB := handshakeStub(t, "bench-b", 0, &hsB)
	defer srvB.Close()

	specA := plugin.Spec{Name: "bench-a", Type: "http", URL: srvA.URL, Authorized: true}
	specB := plugin.Spec{Name: "bench-b", Type: "http", URL: srvB.URL, Authorized: true}
	shared := plugin.NewHostWithProfile(plugin.HostProfileInteractive)

	// Capture the boot: mcp stage line — connect_fresh is the handshake count
	// this build performed.
	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	build := func(specs []plugin.Spec) time.Duration {
		start := time.Now()
		ctrl, err := Build(context.Background(), Options{
			Sink:         event.Discard,
			SharedHost:   shared,
			ExtraPlugins: specs,
		})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		ctrl.Close()
		return time.Since(start)
	}
	stageLog := func() string {
		s := logBuf.String()
		logBuf.Reset()
		return s
	}

	// Round 1: fresh host → the handshake (and its delay) must be paid once.
	first := build([]plugin.Spec{specA})
	if got := hsA.Load(); got != 1 {
		t.Fatalf("first build handshakes=%d, want 1", got)
	}
	if first < handshakeDelay {
		t.Fatalf("first build took %v, want >= injected handshake delay %v (injection must bite)", first, handshakeDelay)
	}
	if log := stageLog(); !strings.Contains(log, "connect_fresh=1") {
		t.Fatalf("first build mcp stage log missing connect_fresh=1: %s", log)
	}

	// Round 2: same host, same config → zero new handshakes, no delay, and the
	// stage log must say so (this is the number the installed switch reads).
	second := build([]plugin.Spec{specA})
	if got := hsA.Load(); got != 1 {
		t.Fatalf("reused build added handshakes: got total %d, want 1 (no new handshake)", got)
	}
	if second >= handshakeDelay {
		t.Fatalf("reused build took %v, want < handshake delay %v — reuse did not skip the handshake", second, handshakeDelay)
	}
	if second >= 2*time.Second {
		t.Fatalf("reused build took %v, want <= 2s (the task-334 switch target)", second)
	}
	if log := stageLog(); !strings.Contains(log, "connect_fresh=0") {
		t.Fatalf("reused build mcp stage log missing connect_fresh=0 (config-unchanged proof): %s", log)
	}

	// Negative A: config change → the new server must handshake even though
	// bench-a still reuses (1 fresh connect = only the added server).
	build([]plugin.Spec{specA, specB})
	if got := hsB.Load(); got != 1 {
		t.Fatalf("config-changed build handshakes(bench-b)=%d, want 1 (new server must handshake)", got)
	}
	if got := hsA.Load(); got != 1 {
		t.Fatalf("config-changed build handshakes(bench-a)=%d, want 1 (unchanged server must still reuse)", got)
	}
	if log := stageLog(); !strings.Contains(log, "connect_fresh=1") {
		t.Fatalf("config-changed mcp stage log missing connect_fresh=1: %s", log)
	}

	// Negative B: the iron-law-1 escape hatch — with REASONIX_MCP_NO_REUSE set
	// the shared host is ignored, so even a known server handshakes again.
	t.Setenv("REASONIX_MCP_NO_REUSE", "1")
	build([]plugin.Spec{specB})
	if got := hsB.Load(); got != 2 {
		t.Fatalf("NO_REUSE build handshakes(bench-b)=%d, want 2 (full handshake restored)", got)
	}
	if log := stageLog(); !strings.Contains(log, "connect_fresh=1") {
		t.Fatalf("NO_REUSE mcp stage log missing connect_fresh=1: %s", log)
	}
}
