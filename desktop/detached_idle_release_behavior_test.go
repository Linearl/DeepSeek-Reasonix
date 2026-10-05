package main

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/agent"
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
	// sessionPath feeds SessionPath() for the task-499 invalidate capture;
	// empty keeps currentSessionPath on the tab-field fallback.
	sessionPath string

	snapshotCalls atomic.Int32
	closeCalls    atomic.Int32
	cancelCalls   atomic.Int32
}

func (c *idleReleaseCtrl) SessionHasUnsavedChanges() bool { return c.unsaved }

func (c *idleReleaseCtrl) SessionPath() string { return c.sessionPath }

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

// TestReleaseDetachedSessionInvalidatesGraphCache is the task-499 ② desktop
// half: releasing a detached runtime must drop the process-wide replayed
// graph for that session — the retention half of the 10-05 bloat (14.6 GB
// heap) was exactly these never-invalidated entries. The cache is populated
// the honest way (a real Save + LoadSession, the load path Puts the replayed
// state), the release runs on the task-308 harness, and the assertion reads
// the agent counters: the invalidation counter moves and a follow-up
// Invalidate finds nothing left for that path.
func TestReleaseDetachedSessionInvalidatesGraphCache(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	a := newIdleReleaseTestApp(t)

	sessionPath := filepath.Join(t.TempDir(), "sess_499.jsonl")
	sess := agent.NewSession("system")
	if err := sess.Save(sessionPath); err != nil {
		t.Fatalf("seed session save: %v", err)
	}
	if _, err := agent.LoadSession(sessionPath); err != nil {
		t.Fatalf("seed session load (populates the graph cache): %v", err)
	}
	if _, ok := agent.InvalidateSessionGraph(sessionPath); !ok {
		t.Fatal("precondition: the seeded session must be in the graph cache")
	}
	// Re-seed after the precondition probe consumed the entry.
	if _, err := agent.LoadSession(sessionPath); err != nil {
		t.Fatalf("re-seed session load: %v", err)
	}
	invalidationsBefore := agent.SessionGraphCacheInvalidations()

	ctrl := &idleReleaseCtrl{sessionPath: sessionPath}
	tab := &WorkspaceTab{ID: "invalidate_tab", Ctrl: ctrl, SessionPath: sessionPath}
	key := sessionRuntimeKey(sessionPath)
	a.detachedSessions[key] = tab

	a.releaseDetachedSession(key, tab, 45*time.Minute)

	if got := agent.SessionGraphCacheInvalidations(); got != invalidationsBefore+1 {
		t.Fatalf("invalidations = %d, want exactly %d (the release must drop the graph once)",
			got, invalidationsBefore+1)
	}
	if _, ok := agent.InvalidateSessionGraph(sessionPath); ok {
		t.Fatal("the released session's graph must be gone from the cache")
	}
}
