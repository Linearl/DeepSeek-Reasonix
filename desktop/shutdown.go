package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/repair"
	"reasonix/internal/stats"
)

// shutdownWatchdogTimeout bounds teardown so a wedged body() can never keep
// the old process alive (task 272 incident ①): the launcher waits
// relaunchWaitTimeout=90s for the old PID, and while the old one lingered the
// freshly booted instance hit the single-instance lock and killed itself —
// "background process never stopped AND nothing restarted". MUST stay well
// below that 90s: 20s leaves the launcher a full minute of margin.
var shutdownWatchdogTimeout = 20 * time.Second

// shutdownExit lets the watchdog's force-exit be observed in tests without
// killing the test binary.
var shutdownExit = os.Exit

// completeDesktopShutdown removes the lifecycle record only after every
// shutdown defer has returned normally. A panic in teardown deliberately leaves
// the shutting_down record behind for the next launch to diagnose — body runs
// on THIS goroutine so panics keep that original propagation semantics; only
// a body that silently wedges (locks, slow snapshots) is force-exited.
func completeDesktopShutdown(tracker *desktopLifecycleTracker, body func()) {
	tracker.stopWriter()
	tracker.mark("shutting_down")
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-time.After(shutdownWatchdogTimeout):
			// The phase is whatever shutdownBody last marked — each stage
			// below stamps its own, so the log names the wedge point.
			slog.Error("desktop: shutdown watchdog fired; forcing exit",
				"phase", tracker.phase(), "timeout", shutdownWatchdogTimeout)
			tracker.mark("wedged")
			shutdownExit(1)
		}
	}()
	body()
	// Task 272 G2: an exit with no terminal log line left "when did the old
	// process die" undecidable — the incident log just stopped. Name the
	// normal completion before the lifecycle record is removed.
	slog.Info("desktop: shutdown teardown complete", "phase", tracker.phase())
	close(done)
	tracker.clean()
}

func (a *App) shutdownBody() {
	// Task 342: the endpoint file must never survive the process — a stale
	// port would point the CDP verification flow at a dead (or worse, a
	// different) browser. Best effort, before anything else can fail.
	removeCDPDebugEndpointFile()
	if a.perfMonitor != nil {
		a.perfMonitor.Stop()
		a.perfMonitor = nil
	}
	if a.topicState != nil {
		defer a.topicState.close()
	}
	if a.desktopShell.linuxRecovery != nil {
		a.desktopShell.linuxRecovery.stop()
	}
	if a.desktopShell.coordinator != nil {
		a.desktopShell.coordinator.stop()
	}
	if a.workspaceHub != nil {
		a.workspaceHub.close()
	}
	// A real quit terminates web windows whose tunnels die with this process.
	// Remote Serve stays resident; background tray close never reaches shutdown
	// and therefore keeps those windows alive.
	a.closeAllRemoteWindows()
	// Run after controller teardown (and after its deferred lifecycle unlocks)
	// so every accepted usage record reaches disk before a normal app exit.
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()
		// Task 653: drain whatever tab snapshots are still queued before the
		// process exits — since App.startup the desktop-tabs.json write runs
		// on the tabsSaveQueue flusher, and a pending snapshot would otherwise
		// be lost to exit. Bounded like stats.Flush; the shutdown watchdog
		// bounds the teardown either way.
		a.flushQueuedTabsSave(250 * time.Millisecond)
		_ = stats.Flush(flushCtx, config.StatsDir())
		_ = flushDesktopDerivedCatalogs(flushCtx)
	}()
	a.stopDeferredRebuildRetry()
	// End every takeover mirror before controller teardown releases the leases:
	// Serve's mirror-end then hands those sessions straight back to their
	// remote tabs instead of waiting out the stale-mirror timeout.
	a.stopTakeoverMirrors()
	a.stopHistoryIndexMigration()
	a.stopHistoryIdlePrefetch()
	a.stopMainThreadWatchdog()
	if a.heartbeat != nil {
		a.heartbeat.Stop()
	}
	if a.sessionCollab != nil {
		// Stop the delivery ticker with the process; an unstopped goroutine would
		// keep claiming mail while the controllers it delivers into are gone.
		a.sessionCollab.Stop()
	}
	a.stopBotRuntime()
	a.stopRemoteRuntime()
	// Task 272 L2: close the serve pool on the normal exit path. Manager.Close
	// is the only place that kills pool serves and nothing on the old shutdown
	// chain called it — even a clean quit left orphan `reasonix-cli serve`
	// processes holding session leases (incident ②). closeServePool is
	// idempotent; the per-spawn Job (L2) still reaps orphans for every other
	// death mode (taskkill, crash).
	a.closeServePool()
	a.lifecycle.tracker.mark("closing_serve_pool_done")
	// Task 439: stop the embedded zcode task bus with the process. No-op
	// when the lab switch never armed it (the default).
	a.closeZcodeTaskBus()
	a.lifecycle.tracker.mark("closing_zcode_task_bus_done")
	a.stopTray()
	// Terminal process shutdown is independent from controller teardown. Do it
	// before acquiring runtime lifecycle locks so a slow PTY cannot delay while
	// holding locks used by Wails-bound chat calls.
	if a.terminals != nil {
		a.terminals.closeAll()
	}
	// Save window geometry synchronously from Go so it's persisted even if the
	// frontend's beforeunload promise hasn't resolved yet.
	a.saveWindowStateSync()
	// Serialize shutdown with controller rebuilds and live MCP mutations. This
	// uses the same lifecycle lock order as lockMCPMutation so launch authorization
	// or reconnect cannot have its captured Host closed underneath it.
	// Stage marker BEFORE the lock: these two mutexes are the top wedge suspects
	// (task 272 ①) and the watchdog log must be able to say "waiting_runtime_locks".
	a.lifecycle.tracker.mark("waiting_runtime_locks")
	a.runtimeRebuildMu.Lock()
	defer a.runtimeRebuildMu.Unlock()
	a.runtimeAdmissionMu.Lock()
	defer a.runtimeAdmissionMu.Unlock()
	// Close every shared plugin host before releasing the lifecycle barrier,
	// even if a tab cleanup panics. G1: mark inside the defer so a watchdog
	// firing mid-tail names the section, not just "shutting_down".
	defer func() {
		a.lifecycle.tracker.mark("closing_shared_hosts")
		a.closeAllSharedHosts()
	}()
	a.lifecycle.tracker.mark("closing_tabs")

	a.mu.RLock()
	tabs := a.runtimeTabsLocked()
	type shutdownItem struct {
		tab      *WorkspaceTab
		ctrl     control.SessionAPI
		readOnly bool
	}
	items := make([]shutdownItem, 0, len(tabs))
	for _, t := range tabs {
		if t.Ctrl != nil {
			items = append(items, shutdownItem{tab: t, ctrl: t.Ctrl, readOnly: t.ReadOnly})
		}
	}
	a.mu.RUnlock()
	for _, it := range items {
		// G1: the incident's wedge candidates were SnapshotForShutdown/Close
		// hitting a 117MB full save — mark per tab so the watchdog's phase
		// names exactly which tab teardown blocked.
		a.lifecycle.tracker.mark("closing_tab:" + it.tab.ID)
		if !it.readOnly {
			if err := it.ctrl.SnapshotForShutdown(); err != nil {
				slog.Warn("desktop: shutdown snapshot failed", "tab", it.tab.ID, "err", err)
			}
		}
		it.ctrl.Close()
		if !a.returnTakeoverLeaseForShutdown(it.tab) {
			it.tab.releaseSessionLease()
		}
		a.mu.Lock()
		a.releaseSessionRuntimeLocked(it.tab)
		a.mu.Unlock()
	}
	// Every lease is released; taken-over sessions can go straight back to
	// their remote tabs.
	a.endTakeoverMirrors()
	if a.startupReady.Load() {
		// A stable React + Wails heartbeat is sufficient health evidence even if
		// the user closes before the delayed commit task runs.
		if err := a.commitPendingUpdateHealth(); err != nil {
			slog.Warn("desktop: commit healthy update during shutdown", "err", err)
		}
		a.retireSupersededDuringShutdown()
		// Independent last-known-good config snapshot after a successful UI session.
		_ = repair.RecordHealthyConfig(version)
	}
}

// shutdownRetireSuperseded is a seam for tests (task 763): shutdownBody's
// retire-superseded call, indirected like restartQuit/versionSwitchQuit.
var shutdownRetireSuperseded = archiveSupersededPendingUpdateAfterReady

// retireSupersededDuringShutdown runs the shutdown-face retire-superseded
// check — unless this process is exiting as an update restart. Task 763 (21.5
// 装机实测): past the swap commit the active install IS the version being
// switched to, so "active install version X does not match running version Y"
// fails by design on every update restart, and the WARN landed in the exact
// log window users grep after an update — it read as a crash misjudgment.
// The freshly relaunched version re-runs the same check against the settled
// install in completeFrontendStartup, where a mismatch IS a real anomaly.
// Normal quits keep today's behavior (including the WARN for a genuine
// mismatch, e.g. a manual downgrade).
func (a *App) retireSupersededDuringShutdown() {
	if a.updateRestartExit.Load() {
		slog.Info("desktop: update-restart exit; retire-superseded check deferred to the relaunched version")
		return
	}
	if archived, err := shutdownRetireSuperseded(); err != nil {
		slog.Warn("desktop: retire superseded update during shutdown", "err", err)
	} else if archived {
		slog.Info("desktop: archived superseded update transaction during shutdown")
	}
}
