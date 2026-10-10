package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/servepool"
)

// Task 249: the gateway takeover gate routes every explicit remote takeover
// through the prompt bridge — accept yields the tab lease and allows, reject
// and timeout deny with an explicit 409 message, and the pass-through cases
// (no sink, unknown session) stay honest.

type gatewayTakeoverFixture struct {
	app     *App
	tab     *WorkspaceTab
	path    string
	prompts []takeoverDecisionReq
}

func newGatewayTakeoverFixture(t *testing.T) *gatewayTakeoverFixture {
	t.Helper()
	RegisterTakeoverPromptSink(nil)
	t.Cleanup(func() { RegisterTakeoverPromptSink(nil) })

	root := t.TempDir()
	dir := config.ProjectSessionDir(root)
	if dir == "" {
		t.Fatal("project session dir unavailable")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sess-gate.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	tab := &WorkspaceTab{ID: "gate-tab", SessionPath: path, WorkspaceRoot: root}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}}
	return &gatewayTakeoverFixture{app: app, tab: tab, path: path}
}

func (f *gatewayTakeoverFixture) request() servepool.TakeoverGateRequest {
	return servepool.TakeoverGateRequest{
		ProjectID:   "proj",
		ProjectRoot: f.tab.WorkspaceRoot,
		SessionName: "sess-gate",
		From:        "gc-device-9",
	}
}

func TestGatewayTakeoverGateAcceptYieldsLease(t *testing.T) {
	f := newGatewayTakeoverFixture(t)
	lease, err := agent.TryAcquireSessionLease(f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.tab.sessionLease = lease
	f.tab.storeSessionLeaseRuntimeKey(sessionRuntimeKey(f.path))
	// The forwarded serve request writes the plain marker right after the
	// gate allows; pre-write it so the yield machine discovers the target
	// writer and completes with a handoff reservation (the normal path when
	// the desktop turn is still running at accept time).
	if err := os.WriteFile(agent.TakeoverRequestMarkerPath(f.path), []byte(agent.SessionWriterID()), 0o600); err != nil {
		t.Fatal(err)
	}

	RegisterTakeoverPromptSink(func(req takeoverDecisionReq) {
		f.prompts = append(f.prompts, req)
		if !strings.HasPrefix(req.Marker, "gateway-takeover-") {
			t.Errorf("marker = %q, want the gateway bridge prefix", req.Marker)
		}
		if req.Path != f.path || req.From != "gc-device-9" {
			t.Errorf("prompt = %+v, want resolved path and device", req)
		}
		SubmitTakeoverDecision(req.Marker, true)
	})

	allow, status, message := f.app.servePoolTakeoverGate(f.request())
	if !allow || status != 0 || message != "" {
		t.Fatalf("gate = (%v, %d, %q), want allow with no denial", allow, status, message)
	}
	if len(f.prompts) != 1 {
		t.Fatalf("prompts = %d, want 1", len(f.prompts))
	}
	// 539 route A: the tab enters the yielding state and — with no runtime
	// work — drains and releases the lease asynchronously. The handoff
	// reservation is published for the serve writer, so the serve poll's
	// WithHandoff consume must succeed.
	if !waitForSessionYieldIdle(f.tab, 5*time.Second) {
		t.Fatal("yield machine never finished")
	}
	if !lease.Released() {
		t.Fatal("lease not released after the yield completed")
	}
	raw, err := os.ReadFile(agent.TakeoverRequestMarkerPath(f.path))
	if err != nil {
		t.Fatalf("yielded marker not written: %v", err)
	}
	state := agent.ParseTakeoverMarker(string(raw))
	if state.Kind != agent.TakeoverMarkerKindYielded || state.WriterID == "" || state.HandoffID == "" {
		t.Fatalf("marker state = %+v, want yielded ack with consume ids", state)
	}
	if _, err := agent.TryAcquireSessionLeaseWithHandoff(f.path, state.WriterID, state.HandoffID); err != nil {
		t.Fatalf("reservation not consumable (serve acquire would fail): %v", err)
	}
}

func TestGatewayTakeoverGateForcedSkipsPromptAndCancels(t *testing.T) {
	f := newGatewayTakeoverFixture(t)
	lease, err := agent.TryAcquireSessionLease(f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.tab.sessionLease = lease
	f.tab.storeSessionLeaseRuntimeKey(sessionRuntimeKey(f.path))
	if err := os.WriteFile(agent.TakeoverRequestMarkerPath(f.path), []byte(agent.SessionWriterID()), 0o600); err != nil {
		t.Fatal(err)
	}

	forced := f.request()
	forced.Force = true
	var forcedNotices []string
	RegisterTakeoverYieldNotifier(func(kind, path, detail string) {
		if kind == "forced" {
			forcedNotices = append(forcedNotices, path)
		}
	})
	t.Cleanup(func() { RegisterTakeoverYieldNotifier(nil) })

	allow, status, _ := f.app.servePoolTakeoverGate(forced)
	if !allow || status != 0 {
		t.Fatalf("forced gate = (%v, %d), want allow", allow, status)
	}
	if len(f.prompts) != 0 {
		t.Fatalf("prompts = %d, want 0 (force skips the desktop prompt)", len(f.prompts))
	}
	if !waitForSessionYieldIdle(f.tab, 5*time.Second) {
		t.Fatal("forced yield never finished")
	}
	if !lease.Released() {
		t.Fatal("forced yield did not release the lease")
	}
	if len(forcedNotices) == 0 {
		t.Fatal("no forced-takeover notice emitted")
	}
}

func TestGatewayTakeoverGateRejectDenies(t *testing.T) {
	f := newGatewayTakeoverFixture(t)
	lease, err := agent.TryAcquireSessionLease(f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.tab.sessionLease = lease

	RegisterTakeoverPromptSink(func(req takeoverDecisionReq) {
		SubmitTakeoverDecision(req.Marker, false)
	})

	allow, status, message := f.app.servePoolTakeoverGate(f.request())
	if allow || status != http.StatusConflict {
		t.Fatalf("gate = (%v, %d, %q), want 409 deny", allow, status, message)
	}
	if !strings.Contains(message, "rejected by the desktop user") {
		t.Fatalf("deny message = %q, want an explicit rejection for the phone", message)
	}
	// The tab keeps its lease on a rejection.
	if _, err := agent.TryAcquireSessionLease(f.path); err == nil {
		t.Fatal("lease was released on reject; the tab must keep local control")
	}
}

func TestGatewayTakeoverGateTimeoutDenies(t *testing.T) {
	f := newGatewayTakeoverFixture(t)
	gatewayTakeoverPromptTimeoutForTest = 50 * time.Millisecond
	t.Cleanup(func() { gatewayTakeoverPromptTimeoutForTest = gatewayTakeoverPromptTimeout })

	lease, err := agent.TryAcquireSessionLease(f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.tab.sessionLease = lease

	RegisterTakeoverPromptSink(func(req takeoverDecisionReq) {
		// Never answers: the bounded prompt window must refuse.
	})

	allow, status, message := f.app.servePoolTakeoverGate(f.request())
	if allow || status != http.StatusConflict {
		t.Fatalf("gate = (%v, %d, %q), want 409 deny", allow, status, message)
	}
	if !strings.Contains(message, "timed out") {
		t.Fatalf("deny message = %q, want the explicit timeout notice", message)
	}
}

func TestGatewayTakeoverGatePassthroughWithoutSink(t *testing.T) {
	f := newGatewayTakeoverFixture(t)
	// Sink stays nil (headless): historical immediate yield semantics.
	allow, status, message := f.app.servePoolTakeoverGate(f.request())
	if !allow || status != 0 || message != "" {
		t.Fatalf("gate = (%v, %d, %q), want pass-through without a sink", allow, status, message)
	}
	if len(f.prompts) != 0 {
		t.Fatalf("prompts = %d, want 0", len(f.prompts))
	}
}

func TestGatewayTakeoverGateUnknownSessionPassesThrough(t *testing.T) {
	f := newGatewayTakeoverFixture(t)
	RegisterTakeoverPromptSink(func(req takeoverDecisionReq) {
		f.prompts = append(f.prompts, req)
	})
	req := f.request()
	req.SessionName = "missing-session"
	allow, status, message := f.app.servePoolTakeoverGate(req)
	if !allow || status != 0 || message != "" {
		t.Fatalf("gate = (%v, %d, %q), want pass-through so the serve 404s honestly", allow, status, message)
	}
	if len(f.prompts) != 0 {
		t.Fatalf("prompts = %d, want 0 for an unknown session", len(f.prompts))
	}
}

func TestGatewayTakeoverGateRefusesUnsafeName(t *testing.T) {
	f := newGatewayTakeoverFixture(t)
	RegisterTakeoverPromptSink(func(req takeoverDecisionReq) {
		f.prompts = append(f.prompts, req)
	})
	for _, name := range []string{"", ".", "..", `..\escape`, `../escape`, `a/b`} {
		req := f.request()
		req.SessionName = name
		allow, _, _ := f.app.servePoolTakeoverGate(req)
		if !allow {
			t.Fatalf("name %q: gate denied; unsafe names pass through to serve validation", name)
		}
	}
	if len(f.prompts) != 0 {
		t.Fatalf("prompts = %d, want 0 for unsafe names", len(f.prompts))
	}
}
