package main

import (
	"log/slog"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// recoveryGCInterval bounds how often the background sweep repeats after the
// startup run. Covered copies are also reclaimed by the shorter startup grace.
const recoveryGCInterval = 6 * time.Hour

// recoveryGCFollowUpDelay re-runs the short-grace sweep once after startup so
// branches that just crossed the 15-minute idle line are not left until the
// next 6-hour tick.
const recoveryGCFollowUpDelay = 20 * time.Minute

// startRecoveryGC waits for tab restore, runs a short-grace startup sweep (and
// one follow-up), then repeats on the long interval until cancelled. The wait
// is load-bearing: a pre-restore sweep sees every saved tab as closed, letting
// DeleteSession overwrite desktop-tabs.json with an empty snapshot.
func (a *App) startRecoveryGC() {
	a.goSafe("recoveryGC", func() {
		select {
		case <-a.tabsRestoredSignal():
		case <-a.ctx.Done():
			return
		}
		startup := time.NewTimer(agent.RecoveryGCStartupGracePeriod)
		if !waitRecoveryGCStartup(a.ctx.Done(), startup.C) {
			if !startup.Stop() {
				select {
				case <-startup.C:
				default:
				}
			}
			return
		}
		// Protect every upgraded user for a full startup grace before reclaiming.
		a.sweepReclaimableRecoveryBranchesWithGrace(agent.RecoveryGCStartupGracePeriod)
		followUp := time.NewTimer(recoveryGCFollowUpDelay)
		ticker := time.NewTicker(recoveryGCInterval)
		defer ticker.Stop()
		for {
			select {
			case <-a.ctx.Done():
				if !followUp.Stop() {
					select {
					case <-followUp.C:
					default:
					}
				}
				return
			case <-followUp.C:
				// Second short-grace pass, then fall through to long-interval only.
				a.sweepReclaimableRecoveryBranchesWithGrace(agent.RecoveryGCStartupGracePeriod)
				followUp.C = nil
			case <-ticker.C:
				a.sweepReclaimableRecoveryBranches()
			}
		}
	})
}

func waitRecoveryGCStartup(done <-chan struct{}, elapsed <-chan time.Time) bool {
	select {
	case <-elapsed:
		return true
	case <-done:
		return false
	}
}

// sweepReclaimableRecoveryBranches trashes conflict-recovery branches that
// preserve nothing unique (their content is covered by a still-present parent
// session), were never continued on, sat idle past the grace period, and are
// not held by any runtime. Trashing — never hard deletion — keeps every swept
// branch recoverable from the session trash. Returns how many were reclaimed.
func (a *App) sweepReclaimableRecoveryBranches() int {
	return a.sweepReclaimableRecoveryBranchesWithGrace(agent.RecoveryGCGracePeriod)
}

func (a *App) sweepReclaimableRecoveryBranchesWithGrace(grace time.Duration) int {
	return a.reclaimRecoveryBranchesIn(recoveryGCDirs(), time.Now(), grace)
}

func (a *App) reclaimRecoveryBranchesIn(dirs []string, now time.Time, grace time.Duration) int {
	if grace <= 0 {
		grace = agent.RecoveryGCGracePeriod
	}
	reclaimed := 0
	for _, dir := range dirs {
		reclaimable, err := agent.ReclaimableRecoveryBranches(dir, now, grace)
		if err != nil {
			slog.Warn("desktop: scan reclaimable recovery branches", "dir", dir, "err", err)
			continue
		}
		for _, path := range reclaimable {
			// Re-check liveness right before disposal: the scan is a snapshot,
			// and the user may have opened the branch since.
			if agent.SessionLeaseHeld(path) || a.sessionOpenInAnyTab(path) {
				continue
			}
			// DeleteRecoveryCopy re-proves real parent coverage under removal
			// guards. A concurrent continue-edit, missing parent, or busy lease
			// skips without moving or permanently deleting anything.
			if err := a.DeleteRecoveryCopy(path); err != nil {
				slog.Warn("desktop: trash reclaimed recovery branch", "path", path, "err", err)
				continue
			}
			reclaimed++
		}
	}
	if reclaimed > 0 {
		slog.Info("desktop: moved redundant recovery branches to the session trash",
			"count", reclaimed, "grace", grace.String())
	}
	return reclaimed
}

// recoveryGCDirs returns every session directory the desktop lists sessions
// from: the global desktop and legacy shared dirs plus each saved project's
// session dirs, deduplicated.
func recoveryGCDirs() []string {
	seen := map[string]bool{}
	var dirs []string
	add := func(dir string) {
		key := projectRootKey(dir)
		if dir == "" || seen[key] {
			return
		}
		seen[key] = true
		dirs = append(dirs, dir)
	}
	add(desktopSessionDir(globalWorkspaceRoot()))
	add(config.SessionDir())
	for _, project := range loadProjectsFile().Projects {
		if root := normalizeProjectRoot(project.Root); root != "" {
			add(desktopSessionDir(root))
			add(config.ProjectSessionDir(root))
		}
	}
	return dirs
}

// sessionOpenInAnyTab reports whether any tab's current session is path.
// Lease checks cover live runtimes; this additionally covers tabs that hold a
// session without a lease (read-only channel views).
func (a *App) sessionOpenInAnyTab(path string) bool {
	key := sessionRuntimeKey(path)
	if key == "" {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, tab := range a.tabs {
		if tab == nil {
			continue
		}
		if sessionRuntimeKey(tab.currentSessionPath()) == key {
			return true
		}
	}
	return false
}
