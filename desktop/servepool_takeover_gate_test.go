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

// tl1854 R3: the dialog is a veto window, not a confirmation — nobody at the
// desktop is the NORMAL remote-takeover scenario, so an unanswered prompt now
// ALLOWS the takeover and walks the same non-interrupting yield path as an
// explicit accept (the old deadline-refusal made takeover unusable exactly
// when it matters). The explicit 拒绝 verdict below stays a hard 409.
func TestGatewayTakeoverGateTimeoutAllowsAndYields(t *testing.T) {
	f := newGatewayTakeoverFixture(t)
	gatewayTakeoverVetoWindowForTest = 50 * time.Millisecond
	t.Cleanup(func() { gatewayTakeoverVetoWindowForTest = gatewayTakeoverVetoWindow })

	lease, err := agent.TryAcquireSessionLease(f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.tab.sessionLease = lease
	// The forwarded serve request writes the plain marker right after the gate
	// allows; pre-write it so the yield machine can complete deterministically.
	if err := os.WriteFile(agent.TakeoverRequestMarkerPath(f.path), []byte(agent.SessionWriterID()), 0o600); err != nil {
		t.Fatal(err)
	}

	var forcedNotices []string
	RegisterTakeoverYieldNotifier(func(kind, path, detail string) {
		if kind == "forced" {
			forcedNotices = append(forcedNotices, path)
		}
	})
	t.Cleanup(func() { RegisterTakeoverYieldNotifier(nil) })

	RegisterTakeoverPromptSink(func(req takeoverDecisionReq) {
		// Never answers: the bounded veto window must ALLOW, not refuse. The
		// payload must also carry the window so the dialog countdown matches.
		if req.VetoWindowMs <= 0 {
			t.Errorf("prompt VetoWindowMs = %d, want the gate's veto window", req.VetoWindowMs)
		}
	})

	allow, status, message := f.app.servePoolTakeoverGate(f.request())
	if !allow || status != 0 || message != "" {
		t.Fatalf("gate = (%v, %d, %q), want timeout-allow with no denial", allow, status, message)
	}
	// Same machine as the accept path: the tab entered the yielding state and,
	// with no runtime work, drained and released the lease with a consumable
	// handoff reservation.
	if !waitForSessionYieldIdle(f.tab, 5*time.Second) {
		t.Fatal("yield machine never finished after the veto window")
	}
	if !lease.Released() {
		t.Fatal("lease not released after the timeout-allow yield")
	}
	if len(forcedNotices) != 0 {
		t.Fatalf("forced notices = %v, want none (timeout-allow must not interrupt a running turn)", forcedNotices)
	}
}

func TestGatewayTakeoverGateTimeoutStaleRejectIsDropped(t *testing.T) {
	f := newGatewayTakeoverFixture(t)
	gatewayTakeoverVetoWindowForTest = 50 * time.Millisecond
	t.Cleanup(func() { gatewayTakeoverVetoWindowForTest = gatewayTakeoverVetoWindow })

	lease, err := agent.TryAcquireSessionLease(f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.tab.sessionLease = lease
	if err := os.WriteFile(agent.TakeoverRequestMarkerPath(f.path), []byte(agent.SessionWriterID()), 0o600); err != nil {
		t.Fatal(err)
	}

	var markers []string
	RegisterTakeoverPromptSink(func(req takeoverDecisionReq) {
		markers = append(markers, req.Marker)
	})

	allow, status, _ := f.app.servePoolTakeoverGate(f.request())
	if !allow || status != 0 {
		t.Fatalf("gate = (%v, %d), want timeout-allow", allow, status)
	}
	// A late rejection on the already-timed-out dialog must be a no-op — the
	// answer channel was unregistered before the deadline fired.
	if len(markers) != 1 {
		t.Fatalf("markers = %v, want exactly one prompt", markers)
	}
	if SubmitTakeoverDecision(markers[0], false) {
		t.Fatal("stale rejection after the veto window was accepted; it must be dropped")
	}
	// The yield still completes: the late click changed nothing.
	if !waitForSessionYieldIdle(f.tab, 5*time.Second) {
		t.Fatal("yield machine never finished")
	}
	if !lease.Released() {
		t.Fatal("lease not released; the stale rejection must not roll the takeover back")
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

// tl1854 R2: an idle session (the phone /resume'd it, so the pooled serve
// holds the lease itself and no desktop tab does) must pass through WITHOUT
// the desktop prompt — neither answer could change the outcome, and the
// forwarded serve request answers 204 on its own (self-held takeover).
func TestGatewayTakeoverGatePassesThroughIdleSession(t *testing.T) {
	f := newGatewayTakeoverFixture(t)
	// The tab exists but holds NO lease: nothing for a desktop yield to serve.
	RegisterTakeoverPromptSink(func(req takeoverDecisionReq) {
		f.prompts = append(f.prompts, req)
	})
	allow, status, message := f.app.servePoolTakeoverGate(f.request())
	if !allow || status != 0 || message != "" {
		t.Fatalf("gate = (%v, %d, %q), want pass-through for a session no tab holds", allow, status, message)
	}
	if len(f.prompts) != 0 {
		t.Fatalf("prompts = %d, want 0 (idle session must not prompt)", len(f.prompts))
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
