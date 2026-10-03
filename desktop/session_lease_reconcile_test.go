package main

// Task 456 acceptance tests: the 2026-10-02 desktop restart self-deadlock —
// a restored tab's own background runtime held the session lease while the
// visible tab was refused with "already open in another Reasonix window",
// and the takeover dialog dead-ended on "no resident serve". ① restore
// reconciles leftover records before launching runtimes, ② takeover adopts
// locally-held sessions, ③ every refusal names the holder pid and whether it
// is alive with a workable cleanup step.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/store"
)

func write456SessionPath(t *testing.T, name string) string {
	t.Helper()
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write placeholder session: %v", err)
	}
	return path
}

func new456AppWithTab(t *testing.T, tabID, path string) (*App, *WorkspaceTab) {
	t.Helper()
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab := app.createTabEntryWithID("global", globalTabWorkspaceRoot(), "", tabID)
	tab.SessionPath = path
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	installNoopRuntimeEvents(app, tab.sink)
	app.mu.Lock()
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	app.mu.Unlock()
	t.Cleanup(func() {
		if ctrl := app.controllerForTab(tab); ctrl != nil {
			ctrl.Close()
		}
		tab.releaseSessionLease()
	})
	return app, tab
}

// TestDedupeRestoredTabEntriesKeepsFirstPerSession covers the restore-order
// fix (task 456 ①): the persisted tabs file carrying the same session twice
// ("已打开" leftover record form) must come back as ONE tab per session, so
// two restored builds never race for one lease. Blank tabs (no session path)
// are never dropped, and the first occurrence wins so order stays stable.
func TestDedupeRestoredTabEntriesKeepsFirstPerSession(t *testing.T) {
	shared := `C:\sessions\fork-dev-2.jsonl`
	entries := []desktopTabEntry{
		{ID: "a", SessionPath: shared},
		{ID: "b", SessionPath: ""},                 // blank tab: keep
		{ID: "c", SessionPath: strings.ToLower(shared)}, // case-folded duplicate on Windows
		{ID: "d", SessionPath: `C:\sessions\other.jsonl`},
		{ID: "e", SessionPath: shared}, // exact duplicate
	}
	got := dedupeRestoredTabEntries(entries)
	if len(got) != 3 {
		t.Fatalf("deduped entries = %d, want 3 (a, blank b, d)", len(got))
	}
	if got[0].ID != "a" || got[1].ID != "b" || got[2].ID != "d" {
		t.Fatalf("kept ids = %v, want [a b d] (first occurrence wins, order stable)", []string{got[0].ID, got[1].ID, got[2].ID})
	}
}

// TestReconcileRestoredSessionsRetiresDeadHolderRecord is task 456 ①: a lease
// record left by a dead holder (the restart leftover) must be retired BEFORE
// the restored tab's runtime launches, and a live holder's record must be
// respected untouched.
func TestReconcileRestoredSessionsRetiresDeadHolderRecord(t *testing.T) {
	isolateDesktopUserDirs(t)
	prevAlive := leaseReconcileProcessAlive
	t.Cleanup(func() { leaseReconcileProcessAlive = prevAlive })

	deadPID := 456001
	leaseReconcileProcessAlive = func(pid int) bool { return pid != deadPID }

	path := write456SessionPath(t, "restore-dead-holder.jsonl")
	key := sessionRuntimeKey(path)
	if err := agent.SaveSessionLeaseInfo(path, agent.SessionLeaseInfo{
		SessionPath: key, WriterID: "writer-dead", PID: deadPID,
		Hostname: "restart-leftover", AcquiredAt: time.Now().Add(-2 * time.Hour).UTC(),
	}); err != nil {
		t.Fatalf("stage stale lease record: %v", err)
	}

	app := NewApp()
	app.reconcileRestoredSessionKeys([]desktopTabEntry{{ID: "t", SessionPath: path}})

	if _, err := agent.LoadSessionLeaseInfo(key); !os.IsNotExist(err) {
		t.Fatalf("stale record survived the reconcile: err=%v, want gone", err)
	}
	// The freed session must be acquirable right away — the fresh runtime's
	// launch order contract.
	lease, err := agent.TryAcquireSessionLease(key)
	if err != nil {
		t.Fatalf("acquire after reconcile: %v", err)
	}
	lease.Release()

	// Live holder: same staging, but the injected probe says alive — the
	// record must survive and the session must stay busy.
	livePID := 456002
	leaseReconcileProcessAlive = func(pid int) bool { return pid == livePID }
	path2 := write456SessionPath(t, "restore-live-holder.jsonl")
	key2 := sessionRuntimeKey(path2)
	if err := agent.SaveSessionLeaseInfo(path2, agent.SessionLeaseInfo{
		SessionPath: key2, WriterID: "writer-live", PID: livePID,
		Hostname: "other-window", AcquiredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("stage live lease record: %v", err)
	}
	app.reconcileRestoredSessionKeys([]desktopTabEntry{{ID: "t2", SessionPath: path2}})
	// The reconcile's lock probe leaves an empty .lease.lock behind (probe
	// side effect, same as production InspectSessionLease); remove it so the
	// assert reads the staged sidecar record directly. The live holder's
	// identity must have survived untouched.
	_ = os.Remove(store.SessionLeaseLock(key2))
	info, err := agent.LoadSessionLeaseInfo(key2)
	if err != nil || info == nil || info.PID != livePID {
		t.Fatalf("live holder record was touched: info=%v err=%v", info, err)
	}
}

// TestLeaseBlockedTabReclaimsAfterZombieSiblingRelease is the incident
// reproduction (task 456 ①/② core): the window's own zombie runtime (a tab
// that bound the lease, never published a controller, and has no build in
// flight) must not wedge the visible tab forever. The startup bind reclaims
// the lease instead of reporting "already open in another Reasonix window"
// against a holder inside its own process.
func TestLeaseBlockedTabReclaimsAfterZombieSiblingRelease(t *testing.T) {
	isolateDesktopUserDirs(t)
	path := write456SessionPath(t, "zombie-sibling.jsonl")
	key := sessionRuntimeKey(path)

	app, blocked := new456AppWithTab(t, "blocked", path)

	// The zombie: bound the lease, no controller, no build in flight — the
	// exact state a restore build is left in when its controller boot fails
	// after the lease bind.
	zombie := app.createTabEntryWithID("global", globalTabWorkspaceRoot(), "", "zombie")
	zombie.SessionPath = path
	zombie.sink = &tabEventSink{tabID: zombie.ID, app: app}
	if err := zombie.ensureSessionLease(path); err != nil {
		t.Fatalf("zombie lease bind: %v", err)
	}
	app.mu.Lock()
	app.tabs[zombie.ID] = zombie
	app.tabOrder = append(app.tabOrder, zombie.ID)
	app.mu.Unlock()
	t.Cleanup(func() { zombie.releaseSessionLease() })

	// Pre-fix this failed with ErrSessionLeaseHeld forever (the sibling veto).
	if err := app.ensureTabSessionLeaseForRebuild(blocked, path, ""); err != nil {
		t.Fatalf("startup bind against own zombie holder: %v", err)
	}
	if blocked.sessionLeaseRuntimeKey() != key {
		t.Fatalf("blocked tab lease key = %q, want %q", blocked.sessionLeaseRuntimeKey(), key)
	}
	if zombie.sessionLeaseRuntimeKey() != "" {
		t.Fatal("zombie sibling still holds the lease after the reclaim")
	}
	// The zombie's registry entry must not survive as a phantom owner.
	app.mu.RLock()
	rt := app.runtimeBySessionKey[key]
	zombieStillOwner := rt != nil && rt.Owner == zombie
	app.mu.RUnlock()
	if zombieStillOwner {
		t.Fatal("zombie sibling is still the registered runtime owner")
	}
}

// TestLeaseBlockedTabStillRefusesFunctionalSibling pins the guard: a sibling
// with a LIVE controller is a real occupant, and its lease is never stolen by
// the reclaim path.
func TestLeaseBlockedTabStillRefusesFunctionalSibling(t *testing.T) {
	isolateDesktopUserDirs(t)
	path := write456SessionPath(t, "functional-sibling.jsonl")

	app, blocked := new456AppWithTab(t, "blocked", path)

	owner := app.createTabEntryWithID("global", globalTabWorkspaceRoot(), "", "owner")
	owner.SessionPath = path
	owner.sink = &tabEventSink{tabID: owner.ID, app: app}
	ctrl := control.New(control.Options{
		Executor:    agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard),
		SessionPath: path,
		Sink:        event.Discard,
	})
	owner.Ctrl = ctrl
	t.Cleanup(ctrl.Close)
	if err := owner.ensureSessionLease(path); err != nil {
		t.Fatalf("owner lease bind: %v", err)
	}
	app.mu.Lock()
	app.tabs[owner.ID] = owner
	app.tabOrder = append(app.tabOrder, owner.ID)
	app.mu.Unlock()
	t.Cleanup(func() { owner.releaseSessionLease() })

	err := app.ensureTabSessionLeaseForRebuild(blocked, path, "")
	if !errors.Is(err, agent.ErrSessionLeaseHeld) {
		t.Fatalf("bind against a functional sibling = %v, want ErrSessionLeaseHeld", err)
	}
	if owner.sessionLeaseRuntimeKey() == "" {
		t.Fatal("functional sibling lost its lease")
	}
}

// TestQueryTakeoverFallsBackToLocalLeaseHolder is task 456 ②/③: with no
// resident serve, the takeover query must inspect the local lease record —
// a self-held lease is reported as adoptable and never as the dead-end
// "no resident serve on this machine holds this session".
func TestQueryTakeoverFallsBackToLocalLeaseHolder(t *testing.T) {
	isolateDesktopUserDirs(t)
	path := write456SessionPath(t, "local-holder.jsonl")

	app, blocked := new456AppWithTab(t, "blocked", path)
	blocked.StartupErrLeaseHeld = true

	// The in-process holder: this window's own background runtime shape.
	holderLease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("holder lease: %v", err)
	}
	t.Cleanup(holderLease.Release)

	view, err := app.QuerySessionTakeover(blocked.ID)
	if err != nil {
		t.Fatalf("QuerySessionTakeover: %v", err)
	}
	if view.Available != true {
		t.Fatalf("self-held session not adoptable: %+v", view)
	}
	if view.Holder != "desktop-local" {
		t.Fatalf("holder = %q, want desktop-local", view.Holder)
	}
	if strings.Contains(view.Reason, "no resident serve") {
		t.Fatalf("dead-end error surfaced for a locally-held session: %q", view.Reason)
	}
	if view.HolderPID != os.Getpid() {
		t.Fatalf("holder pid = %d, want %d", view.HolderPID, os.Getpid())
	}
}

// TestTakeoverSessionAdoptsSelfHeldSession is the full acceptance ② run:
// confirming the takeover on a session this window's own zombie runtime holds
// must adopt it (release the zombie, rebuild the tab) instead of erroring.
func TestTakeoverSessionAdoptsSelfHeldSession(t *testing.T) {
	isolateDesktopUserDirs(t)
	path := write456SessionPath(t, "adopt-self-held.jsonl")
	key := sessionRuntimeKey(path)

	app, blocked := new456AppWithTab(t, "blocked", path)
	blocked.StartupErrLeaseHeld = true
	blocked.StartupErr = (&sessionLeaseBusyError{}).Error()

	zombie := app.createTabEntryWithID("global", globalTabWorkspaceRoot(), "", "zombie")
	zombie.SessionPath = path
	zombie.sink = &tabEventSink{tabID: zombie.ID, app: app}
	if err := zombie.ensureSessionLease(path); err != nil {
		t.Fatalf("zombie lease bind: %v", err)
	}
	app.mu.Lock()
	app.tabs[zombie.ID] = zombie
	app.tabOrder = append(app.tabOrder, zombie.ID)
	app.mu.Unlock()
	t.Cleanup(func() { zombie.releaseSessionLease() })

	if err := app.TakeoverSession(blocked.ID, "wait"); err != nil {
		t.Fatalf("takeover of a self-held session: %v", err)
	}
	if ctrl := app.controllerForTab(blocked); ctrl == nil {
		t.Fatal("adopted tab has no controller")
	}
	if blocked.sessionLeaseRuntimeKey() != key {
		t.Fatalf("adopted tab lease key = %q, want %q", blocked.sessionLeaseRuntimeKey(), key)
	}
	if zombie.sessionLeaseRuntimeKey() != "" {
		t.Fatal("zombie still holds the lease after the adoption")
	}
}

// TestTakeoverFailureNamesLocalHolderPidAndFate is acceptance ③: the refusal
// must name the holder pid, say whether it is alive, and give a next step
// that exists — never the bare "no resident serve".
func TestTakeoverFailureNamesLocalHolderPidAndFate(t *testing.T) {
	isolateDesktopUserDirs(t)
	prevAlive := leaseReconcileProcessAlive
	t.Cleanup(func() { leaseReconcileProcessAlive = prevAlive })

	t.Run("dead holder offers cleanup", func(t *testing.T) {
		deadPID := 456777
		leaseReconcileProcessAlive = func(pid int) bool { return pid != deadPID }
		path := write456SessionPath(t, "dead-holder-takeover.jsonl")
		key := sessionRuntimeKey(path)
		if err := agent.SaveSessionLeaseInfo(path, agent.SessionLeaseInfo{
			SessionPath: key, WriterID: "writer-dead", PID: deadPID,
			Hostname: "restart-leftover", AcquiredAt: time.Now().Add(-time.Hour).UTC(),
		}); err != nil {
			t.Fatalf("stage stale record: %v", err)
		}
		app, blocked := new456AppWithTab(t, "blocked", path)
		blocked.StartupErrLeaseHeld = true

		view, err := app.QuerySessionTakeover(blocked.ID)
		if err != nil {
			t.Fatalf("QuerySessionTakeover: %v", err)
		}
		if !view.Available {
			t.Fatalf("dead-holder session not adoptable: %+v", view)
		}
		for _, want := range []string{"pid=456777", "(dead)", "restart"} {
			if !strings.Contains(view.Reason, want) {
				t.Fatalf("reason %q missing %q", view.Reason, want)
			}
		}
		if strings.Contains(view.Reason, "no resident serve") {
			t.Fatalf("dead-end error surfaced: %q", view.Reason)
		}
	})

	t.Run("live holder gets taskkill guidance", func(t *testing.T) {
		livePID := 456888
		leaseReconcileProcessAlive = func(pid int) bool { return pid == livePID }
		path := write456SessionPath(t, "live-holder-takeover.jsonl")
		key := sessionRuntimeKey(path)
		if err := agent.SaveSessionLeaseInfo(path, agent.SessionLeaseInfo{
			SessionPath: key, WriterID: "writer-live", PID: livePID,
			Hostname: "other-window", AcquiredAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("stage live record: %v", err)
		}
		app, blocked := new456AppWithTab(t, "blocked", path)
		blocked.StartupErrLeaseHeld = true

		view, err := app.QuerySessionTakeover(blocked.ID)
		if err != nil {
			t.Fatalf("QuerySessionTakeover: %v", err)
		}
		if view.Available {
			t.Fatalf("live foreign holder must not be adoptable: %+v", view)
		}
		for _, want := range []string{"pid=456888", "(alive)", "taskkill /PID 456888"} {
			if !strings.Contains(view.Reason, want) {
				t.Fatalf("reason %q missing %q", view.Reason, want)
			}
		}
		if strings.Contains(view.Reason, "no resident serve") {
			t.Fatalf("dead-end error surfaced: %q", view.Reason)
		}
	})
}
