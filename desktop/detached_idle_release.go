package main

import (
	"log/slog"
	"os"
	"strconv"
	"time"
)

// Task 308-O4: detached/idle runtime release. Closing a tab moves its live
// runtime into detachedSessions (app.go), where it used to stay resident until
// process exit — the measured 533MB/h WorkingSet climb with no release point.
// This loop releases a detached runtime once its session finished its turn and
// has been idle for N minutes: the session files stay on disk, and reopening
// the conversation takes the normal open path (full hydrate chain, no second
// implementation).
//
// Gate: REASONIX_DETACHED_IDLE_RELEASE_MINUTES — unset/0 disables the loop
// entirely (fork rule 2: the default process keeps today's behaviour); a
// positive value is the idle threshold in minutes. Liveness signals are shared
// with the task-380 cold-cache loop: LastActivityAt (any write refreshes it)
// plus the controller's own turn state, so a session mid-turn or receiving
// steer is never a candidate, and the active (visible) tab can never be one —
// detached entries are by definition not on screen.

const detachedIdleReleaseTick = 5 * time.Minute

// detachedIdleReleaseMinutes resolves the idle threshold: the config value is
// authoritative (task-308-O4 settings key); the env override still wins for
// one-process debugging, and 0 keeps the never-release behaviour.
func (a *App) detachedIdleReleaseMinutes() int {
	if cfg, _, err := a.loadDesktopUserConfigForView(); err == nil && cfg != nil {
		if cfg.Agent.DetachedIdleReleaseMinutes > 0 {
			return cfg.Agent.DetachedIdleReleaseMinutes
		}
		if cfg.Desktop.DetachedIdleReleaseMinutes > 0 {
			return cfg.Desktop.DetachedIdleReleaseMinutes
		}
	}
	return parseDetachedIdleMinutes(os.Getenv("REASONIX_DETACHED_IDLE_RELEASE_MINUTES"))
}

// parseDetachedIdleMinutes is the gate parser: unset/0/negative/garbage all
// disable the loop; a positive value is the idle threshold in minutes.
func parseDetachedIdleMinutes(raw string) int {
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// startDetachedIdleReleaseLoop boots the reaper when the gate is on; the gate
// is re-read every tick so flipping the env on a future restart is enough and
// a disabled loop costs nothing.
func (a *App) startDetachedIdleReleaseLoop() {
	a.goSafe("detachedIdleReleaseLoop", func() {
		ticker := time.NewTicker(detachedIdleReleaseTick)
		defer ticker.Stop()
		for now := range ticker.C {
			a.releaseIdleDetachedSessions(now)
		}
	})
}

// releaseIdleDetachedSessions releases every detached runtime whose session is
// idle past the threshold. One pass, one candidate at a time, each fully torn
// down before the next so a slow Close never stacks.
func (a *App) releaseIdleDetachedSessions(now time.Time) {
	threshold := a.detachedIdleReleaseMinutes()
	if threshold <= 0 {
		return
	}
	idleFor := time.Duration(threshold) * time.Minute
	activity := a.sessionActivityByPath()
	a.mu.Lock()
	keys := make([]string, 0, len(a.detachedSessions))
	for key, tab := range a.detachedSessions {
		keys = append(keys, key)
		_ = tab
	}
	snapshot := make(map[string]*WorkspaceTab, len(keys))
	for _, key := range keys {
		snapshot[key] = a.detachedSessions[key]
	}
	a.mu.Unlock()

	for key, tab := range snapshot {
		if tab == nil || tab.Ctrl == nil {
			continue
		}
		// Turn safety: a cancellable controller means a turn is in flight —
		// never unload under it.
		if tab.Ctrl.RuntimeStatus().Cancellable {
			continue
		}
		last := activity[tab.currentSessionPath()]
		if last.IsZero() || now.Sub(last) < idleFor {
			continue
		}
		a.releaseDetachedSession(key, tab, now.Sub(last))
	}
}

// releaseDetachedSession tears one detached runtime down: final snapshot
// (196 save-chain alignment — never drop unsaved state), removal from
// detachedSessions, controller close, shared-host release. Reopening the
// conversation afterwards takes the normal open path.
func (a *App) releaseDetachedSession(key string, tab *WorkspaceTab, idle time.Duration) {
	// Identity pre-check (task 308-O4): a stale caller — the entry was
	// replaced by reattach or removal between the scan and this call — must
	// not even snapshot the controller it no longer owns.
	a.mu.Lock()
	if a.detachedSessions[key] != tab {
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()

	ctrl := tab.Ctrl
	if ctrl == nil {
		return
	}
	// Save-chain alignment (196): if anything is still unsaved, snapshot it
	// now and only proceed when the durable file is current. A failed or
	// contended save skips this round; the next tick retries.
	if ctrl.SessionHasUnsavedChanges() {
		if err := ctrl.Snapshot(); err != nil {
			slog.Warn("desktop: detached release skipped (snapshot failed, will retry)",
				"key", key, "err", err)
			return
		}
	}

	hostKey := takeTabSharedHostKey(tab)
	a.mu.Lock()
	if a.detachedSessions[key] != tab {
		// Lost a race (reattach or removal) — the runtime has an owner again.
		a.mu.Unlock()
		return
	}
	delete(a.detachedSessions, key)
	a.mu.Unlock()

	if ctrl.RuntimeStatus().Cancellable {
		ctrl.Cancel()
	}
	ctrl.Close()
	if hostKey != "" {
		a.releaseSharedHost(hostKey)
	}
	// Task 485 (P0): the runtime is gone but the tab can still hold the
	// session lease from its last attach. Releasing the runtime without the
	// lease leaked the OS lock handle: the lock stayed HELD by this very
	// process (LockFileEx err=33 against our own PID), every later acquire
	// failed with "refusing session access", and the three automatic cleanup
	// paths (reclaim / clear-stale / held-by-other) all bail out because they
	// require taking the leaked lock first. Drop the lease through the same
	// swap helper the handoff paths use (session_lease_handoff.go) so the
	// takeover watcher stops with it; Release runs after ctrl.Close, so any
	// final authority-guarded save has drained before the lock unlocks.
	if old := tab.swapSessionLease(nil); old != nil {
		old.Release()
	}
	slog.Info("desktop: detached session released (idle)",
		"key", key, "idleMinutes", int(idle.Minutes()))
}

// sessionActivityByPath maps session path → last activity for the liveness
// check, reusing the same listing the cold-cache loop reads (one source of
// truth for idle across 380/O4).
func (a *App) sessionActivityByPath() map[string]time.Time {
	out := make(map[string]time.Time)
	for _, meta := range a.ListSessions() {
		if meta.LastActivityAt > 0 {
			out[meta.Path] = time.UnixMilli(meta.LastActivityAt)
		}
	}
	return out
}
