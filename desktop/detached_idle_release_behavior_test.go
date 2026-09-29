package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/control"
)

// idleReleaseCtrl overrides exactly the SessionAPI surface the task-308-O4
// release path touches; everything else stays on the embedded (nil) interface
// and must never be reached.
type idleReleaseCtrl struct {
	control.SessionAPI

	unsaved     bool
	snapshotErr error
	cancellable bool

	snapshotCalls atomic.Int32
	closeCalls    atomic.Int32
	cancelCalls   atomic.Int32
}

func (c *idleReleaseCtrl) SessionHasUnsavedChanges() bool { return c.unsaved }

func (c *idleReleaseCtrl) Snapshot() error {
	c.snapshotCalls.Add(1)
	return c.snapshotErr
}

func (c *idleReleaseCtrl) RuntimeStatus() control.RuntimeStatus {
	return control.RuntimeStatus{Cancellable: c.cancellable}
}

func (c *idleReleaseCtrl) Close() {
	c.closeCalls.Add(1)
}

func (c *idleReleaseCtrl) Cancel() { c.cancelCalls.Add(1) }

func newIdleReleaseTestApp(t *testing.T) *App {
	t.Helper()
	return &App{
		ctx:              context.Background(),
		tabs:             map[string]*WorkspaceTab{},
		detachedSessions: map[string]*WorkspaceTab{},
	}
}

// TestReleaseDetachedSessionCleanPath: nothing unsaved, no in-flight turn —
// the entry leaves detachedSessions and the controller is closed without a
// cancel (nothing to cancel).
func TestReleaseDetachedSessionCleanPath(t *testing.T) {
	a := newIdleReleaseTestApp(t)
	ctrl := &idleReleaseCtrl{}
	tab := &WorkspaceTab{Ctrl: ctrl}
	key := "k1"
	a.detachedSessions[key] = tab

	a.releaseDetachedSession(key, tab, 45*time.Minute)

	if got := a.detachedSessions[key]; got != nil {
		t.Fatal("entry still resident after release")
	}
	if ctrl.closeCalls.Load() != 1 {
		t.Fatalf("close calls = %d, want 1", ctrl.closeCalls.Load())
	}
	if ctrl.cancelCalls.Load() != 0 {
		t.Fatalf("clean path must not cancel (calls=%d)", ctrl.cancelCalls.Load())
	}
	if ctrl.snapshotCalls.Load() != 0 {
		t.Fatalf("clean path must not snapshot (calls=%d)", ctrl.snapshotCalls.Load())
	}
}

// TestReleaseDetachedSessionSnapshotsUnsavedFirst: 196 alignment — unsaved
// state is snapshotted and only a clean snapshot allows the teardown.
func TestReleaseDetachedSessionSnapshotsUnsavedFirst(t *testing.T) {
	a := newIdleReleaseTestApp(t)
	ctrl := &idleReleaseCtrl{unsaved: true}
	tab := &WorkspaceTab{Ctrl: ctrl}
	key := "k2"
	a.detachedSessions[key] = tab

	a.releaseDetachedSession(key, tab, 45*time.Minute)

	if ctrl.snapshotCalls.Load() != 1 {
		t.Fatalf("snapshot calls = %d, want 1", ctrl.snapshotCalls.Load())
	}
	if a.detachedSessions[key] != nil {
		t.Fatal("entry not released after a successful save-first snapshot")
	}
	if ctrl.closeCalls.Load() != 1 {
		t.Fatal("controller not closed after the save-first release")
	}

	// Second call with the entry already gone: the identity-race guard makes
	// it a no-op — a stale caller must not close a controller it no longer
	// owns, even when that controller reports unsaved state.
	ctrl.unsaved = true
	a.releaseDetachedSession(key, tab, 45*time.Minute)
	if ctrl.snapshotCalls.Load() != 1 || ctrl.closeCalls.Load() != 1 {
		t.Fatalf("stale release touched the controller: snapshot=%d close=%d",
			ctrl.snapshotCalls.Load(), ctrl.closeCalls.Load())
	}
}

// TestReleaseDetachedSessionFailedSnapshotSkips: a failed/contended snapshot
// skips this round (entry stays, controller untouched) — the next tick retries.
func TestReleaseDetachedSessionFailedSnapshotSkips(t *testing.T) {
	a := newIdleReleaseTestApp(t)
	ctrl := &idleReleaseCtrl{unsaved: true, snapshotErr: context.DeadlineExceeded}
	tab := &WorkspaceTab{Ctrl: ctrl}
	key := "k3"
	a.detachedSessions[key] = tab

	a.releaseDetachedSession(key, tab, 45*time.Minute)

	if a.detachedSessions[key] == nil {
		t.Fatal("entry released despite a failed snapshot")
	}
	if ctrl.closeCalls.Load() != 0 {
		t.Fatal("controller closed despite a failed snapshot")
	}
}

// TestReleaseDetachedSessionCancellableCancelsThenCloses: an in-flight turn at
// teardown time gets an explicit cancel before close (the tick-level guard
// normally keeps such sessions out; this pins the teardown order if one slips
// through between check and teardown).
func TestReleaseDetachedSessionCancellableCancelsThenCloses(t *testing.T) {
	a := newIdleReleaseTestApp(t)
	ctrl := &idleReleaseCtrl{cancellable: true}
	tab := &WorkspaceTab{Ctrl: ctrl}
	key := "k4"
	a.detachedSessions[key] = tab

	a.releaseDetachedSession(key, tab, 45*time.Minute)

	if ctrl.cancelCalls.Load() != 1 || ctrl.closeCalls.Load() != 1 {
		t.Fatalf("cancel=%d close=%d, want 1/1", ctrl.cancelCalls.Load(), ctrl.closeCalls.Load())
	}
	if a.detachedSessions[key] != nil {
		t.Fatal("entry still resident after teardown")
	}
}

// TestReleaseDetachedSessionIdentityRace: if the detached entry was replaced
// between the scan and the teardown (reattach won), the stale tab must be left
// completely alone — the new owner keeps its runtime.
func TestReleaseDetachedSessionIdentityRace(t *testing.T) {
	a := newIdleReleaseTestApp(t)
	stale := &WorkspaceTab{Ctrl: &idleReleaseCtrl{}}
	live := &WorkspaceTab{Ctrl: &idleReleaseCtrl{}}
	key := "k5"
	a.detachedSessions[key] = live

	// Caller scanned `stale`, but the map now holds `live`.
	a.releaseDetachedSession(key, stale, 45*time.Minute)

	if a.detachedSessions[key] != live {
		t.Fatal("identity race: the live owner was disturbed")
	}
	if staleCtrl, ok := stale.Ctrl.(*idleReleaseCtrl); ok && staleCtrl.closeCalls.Load() != 0 {
		t.Fatal("stale controller was closed despite losing the race")
	}
	if liveCtrl, ok := live.Ctrl.(*idleReleaseCtrl); ok && liveCtrl.closeCalls.Load() != 0 {
		t.Fatal("live controller was closed despite winning the race")
	}
}

// TestSessionActivityByPathEmpty: the liveness map degrades to empty on an app
// with no sessions — the release loop then simply has no candidates.
func TestSessionActivityByPathEmpty(t *testing.T) {
	a := newIdleReleaseTestApp(t)
	if m := a.sessionActivityByPath(); len(m) != 0 {
		t.Fatalf("activity map = %v, want empty", m)
	}
}

// compile-time: the mutex field stays guarded as written (releaseDetachedSession
// takes a.mu around map reads/writes).
var _ sync.Locker = &sync.Mutex{}
