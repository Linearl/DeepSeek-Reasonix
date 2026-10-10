package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
)

// Task 539: the yielding state machine — the tab keeps its lease while the
// turn finishes, blocks new work, publishes the marker three-state protocol,
// and rolls back (lease kept) when the serve side withdraws.

type yieldCtrlStub struct {
	control.SessionAPI
	status  control.RuntimeStatus
	cancels int
}

func (s *yieldCtrlStub) RuntimeStatus() control.RuntimeStatus { return s.status }
func (s *yieldCtrlStub) Cancel()                              { s.cancels++ }

func newYieldFixture(t *testing.T) (*App, *WorkspaceTab, string) {
	t.Helper()
	root := t.TempDir()
	dir := config.ProjectSessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sess-yield.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	tab := &WorkspaceTab{ID: "yield-tab", SessionPath: path, WorkspaceRoot: root}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}}
	return app, tab, path
}

func TestYieldMachineCompletesAfterRuntimeDrains(t *testing.T) {
	_, tab, path := newYieldFixture(t)
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatal(err)
	}
	tab.sessionLease = lease
	tab.storeSessionLeaseRuntimeKey(sessionRuntimeKey(path))
	ctrl := &yieldCtrlStub{status: control.RuntimeStatus{Running: true}}
	tab.Ctrl = ctrl
	marker := agent.TakeoverRequestMarkerPath(path)
	if err := os.WriteFile(marker, []byte(agent.SessionWriterID()), 0o600); err != nil {
		t.Fatal(err)
	}

	beginSessionYieldToTakeover(tab, path, marker, false)
	if !sessionYieldActiveForTab(tab) {
		t.Fatal("yield not registered")
	}
	// While the turn runs the lease must stay held and the marker must flip
	// to pending (accepted).
	deadline := time.Now().Add(3 * time.Second)
	for {
		raw, err := os.ReadFile(marker)
		if err == nil && agent.ParseTakeoverMarker(string(raw)).Kind == agent.TakeoverMarkerKindPending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("marker never flipped to pending")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if lease.Released() {
		t.Fatal("lease released while the turn is still running")
	}
	// The turn ends: the machine completes with the reservation + ack.
	ctrl.status = control.RuntimeStatus{}
	if !waitForSessionYieldIdle(tab, 5*time.Second) {
		t.Fatal("yield never completed after the turn drained")
	}
	if !lease.Released() {
		t.Fatal("lease not released after completion")
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("yielded marker missing: %v", err)
	}
	state := agent.ParseTakeoverMarker(string(raw))
	if state.Kind != agent.TakeoverMarkerKindYielded || state.HandoffID == "" {
		t.Fatalf("marker = %+v, want yielded ack", state)
	}
	if _, err := agent.TryAcquireSessionLeaseWithHandoff(path, state.WriterID, state.HandoffID); err != nil {
		t.Fatalf("reservation not consumable: %v", err)
	}
	if ctrl.cancels != 0 {
		t.Fatalf("turn cancelled %d times; route A never cancels", ctrl.cancels)
	}
}

func TestYieldMachineRollsBackWhenMarkerWithdrawn(t *testing.T) {
	_, tab, path := newYieldFixture(t)
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatal(err)
	}
	tab.sessionLease = lease
	tab.storeSessionLeaseRuntimeKey(sessionRuntimeKey(path))
	tab.Ctrl = &yieldCtrlStub{status: control.RuntimeStatus{Running: true}}
	marker := agent.TakeoverRequestMarkerPath(path)
	if err := os.WriteFile(marker, []byte(agent.SessionWriterID()), 0o600); err != nil {
		t.Fatal(err)
	}

	beginSessionYieldToTakeover(tab, path, marker, false)
	// Wait until the machine saw the marker (pending flip) so removing it is
	// a genuine withdrawal, not a not-yet-arrived request.
	deadline := time.Now().Add(3 * time.Second)
	for {
		raw, err := os.ReadFile(marker)
		if err == nil && agent.ParseTakeoverMarker(string(raw)).Kind == agent.TakeoverMarkerKindPending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("marker never flipped to pending")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Serve gives up: the poll removes the marker on its T2 timeout.
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if !waitForSessionYieldIdle(tab, 5*time.Second) {
		t.Fatal("yield never rolled back after marker withdrawal")
	}
	if lease.Released() {
		t.Fatal("rollback must keep the lease")
	}
	if _, err := agent.TryAcquireSessionLease(path); err == nil {
		t.Fatal("lease bookkeeping lost after rollback: the tab must still hold the session")
	}
}

func TestYieldForcedCancelsTheTurn(t *testing.T) {
	_, tab, path := newYieldFixture(t)
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatal(err)
	}
	tab.sessionLease = lease
	tab.storeSessionLeaseRuntimeKey(sessionRuntimeKey(path))
	ctrl := &yieldCtrlStub{status: control.RuntimeStatus{Running: true}}
	tab.Ctrl = ctrl
	marker := agent.TakeoverRequestMarkerPath(path)
	// Forced marker written before the yield starts (direct-serve topology:
	// the serve writes `forced:<id>` and the watcher starts the yield).
	if err := os.WriteFile(marker, []byte(agent.FormatTakeoverMarkerForced(agent.SessionWriterID())), 0o600); err != nil {
		t.Fatal(err)
	}

	beginSessionYieldToTakeover(tab, path, marker, false)
	deadline := time.Now().Add(3 * time.Second)
	for ctrl.cancels == 0 {
		if time.Now().After(deadline) {
			t.Fatal("forced marker never cancelled the turn")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctrl.status = control.RuntimeStatus{}
	if !waitForSessionYieldIdle(tab, 5*time.Second) {
		t.Fatal("forced yield never completed")
	}
	if !lease.Released() {
		t.Fatal("forced yield did not release the lease")
	}
}
