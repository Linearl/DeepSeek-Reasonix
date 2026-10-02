package main

// Task 456: desktop restart recovery chain self-deadlock. The 2026-10-02
// incident: after a desktop restart the restored tab launched its session
// runtime in the background and that runtime held the lease (lease writer pid
// = the new desktop itself), while the visible tab's attach was refused with
// "already open in another Reasonix window" — the window's own UI and its own
// background runtime did not recognize each other. The takeover escape hatch
// only scanned resident serves, so the session could never be opened.
//
// Three fixes live here:
//
//   ① Restore ordering — persisted leftover records are reconciled BEFORE any
//      runtime is launched: duplicate persisted tab entries for one session
//      collapse to a single tab (dedupeRestoredTabEntries), and stale lease
//      records whose recorded holder process is dead are retired
//      (reconcileRestoredSessionLeaseRecords) so the fresh runtime starts
//      against a clean record.
//
//   ② Takeover widening — when no resident serve holds the session, the local
//      lease record is inspected instead of reporting the dead-end "no
//      resident serve" error. A self-held or dead-holder lease is adopted into
//      this window (adoptLocalLeaseHeldSession); a live foreign local runtime
//      gets precise guidance instead.
//
//   ③ Holder liveness — every local-holder message names the pid and whether
//      it is alive, plus the cleanup step ("restart the desktop" reaps dead
//      leftovers, taskkill for live ones), so the error is never unsolvable.

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"reasonix/internal/agent"
)

// leaseReconcileProcessAlive factors the pid probe for test injection,
// mirroring takeoverProcessAlive / servepool's processAlive.
var leaseReconcileProcessAlive = desktopProcessAlive

// leaseHolderVerdict classifies the recorded holder of a session lease
// relative to this process. Pure data so every caller (restore reconcile,
// takeover fallback, error text) shares one definition of "stale".
type leaseHolderVerdict int

const (
	leaseHolderNone        leaseHolderVerdict = iota // no record at all
	leaseHolderForeignLive                           // another live process holds it — respect
	leaseHolderForeignDead                           // recorded holder is gone — stale leftover
	leaseHolderSelf                                  // this process's own record
)

// classifyLeaseHolder maps a lease record onto the verdict. A record without
// a usable pid is ForeignLive (fail toward respecting an unknown holder); the
// lock probe in the callers still separates a live holder from a torn file.
func classifyLeaseHolder(info *agent.SessionLeaseInfo, selfPID int, selfWriter string, alive func(int) bool) leaseHolderVerdict {
	if info == nil {
		return leaseHolderNone
	}
	host, _ := os.Hostname()
	sameHost := strings.TrimSpace(info.Hostname) == "" || strings.TrimSpace(info.Hostname) == strings.TrimSpace(host)
	if sameHost && info.PID == selfPID && (selfWriter == "" || info.WriterID == selfWriter) {
		return leaseHolderSelf
	}
	if info.PID > 0 && !alive(info.PID) {
		return leaseHolderForeignDead
	}
	return leaseHolderForeignLive
}

// dedupeRestoredTabEntries collapses persisted tab entries that point at the
// same session (task 456 ①, the "already open" leftover record form): one
// session must come back as ONE tab, otherwise two restored builds race for
// the lease and the loser wedges in lease_blocked against its own sibling.
// Entries without a session path (blank tabs) are never dropped. The first
// occurrence in persisted order wins, so the user's tab order and the active
// tab stay stable.
func dedupeRestoredTabEntries(entries []desktopTabEntry) []desktopTabEntry {
	seen := map[string]bool{}
	out := make([]desktopTabEntry, 0, len(entries))
	dropped := 0
	for _, entry := range entries {
		key := sessionRuntimeKey(entry.SessionPath)
		if key == "" {
			out = append(out, entry)
			continue
		}
		if seen[key] {
			dropped++
			continue
		}
		seen[key] = true
		out = append(out, entry)
	}
	if dropped > 0 {
		slog.Info("desktop: dropped duplicate restored tab entries for the same session",
			"dropped", dropped, "kept", len(out))
	}
	return out
}

// reconcileRestoredSessionLeaseRecords retires stale lease records for the
// sessions about to be restored (task 456 ①: reconcile before the runtime is
// launched). A record whose holder process is dead cannot come back: the OS
// lock died with the process, so the leftover identity would only poison
// later reclaim and takeover decisions against the fresh runtime. Live
// foreign holders and active handoff reservations are respected untouched
// (ClearStaleSessionLeaseInfo proves the lock free before removing anything).
func (a *App) reconcileRestoredSessionLeaseRecords(entries []desktopTabEntry) {
	seen := map[string]bool{}
	for _, entry := range entries {
		key := sessionRuntimeKey(entry.SessionPath)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		info, locked, err := agent.InspectSessionLease(key)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				slog.Debug("desktop: restored session lease record unreadable; leaving it to the lock",
					"err", err)
			}
			continue
		}
		if locked {
			// A live holder keeps the lock; nothing to reconcile.
			continue
		}
		switch classifyLeaseHolder(info, os.Getpid(), agent.SessionWriterID(), leaseReconcileProcessAlive) {
		case leaseHolderForeignDead, leaseHolderSelf:
			cleared, clearErr := agent.ClearStaleSessionLeaseInfo(key)
			if clearErr != nil {
				slog.Warn("desktop: stale restored-session lease record could not be cleared",
					"pid", holderPIDForLog(info), "err", clearErr)
				continue
			}
			if cleared {
				slog.Info("desktop: retired stale lease record left by a dead holder before restoring the session",
					"pid", holderPIDForLog(info))
			}
		default:
			// ForeignLive on a free lock: reported by the next acquire as a
			// normal busy; nothing destructive here.
		}
	}
}

func holderPIDForLog(info *agent.SessionLeaseInfo) int {
	if info == nil {
		return 0
	}
	return info.PID
}

// localLeaseHolder is what the takeover fallback learns from the local lease
// record when no resident serve holds the session.
type localLeaseHolder struct {
	verdict leaseHolderVerdict
	info    *agent.SessionLeaseInfo
	locked  bool
}

// inspectLocalLeaseHolder reads the session's local lease record and classifies
// the holder (task 456 ②). inspect errors degrade to verdict None: the caller
// then keeps the historical behavior instead of failing takeover on probe
// damage.
func (a *App) inspectLocalLeaseHolder(path string) localLeaseHolder {
	key := sessionRuntimeKey(path)
	if key == "" {
		return localLeaseHolder{verdict: leaseHolderNone}
	}
	info, locked, err := agent.InspectSessionLease(key)
	if err != nil || info == nil {
		return localLeaseHolder{verdict: leaseHolderNone}
	}
	return localLeaseHolder{
		verdict: classifyLeaseHolder(info, os.Getpid(), agent.SessionWriterID(), leaseReconcileProcessAlive),
		info:    info,
		locked:  locked,
	}
}

// localLeaseHolderGuidance renders the task 456 ③ user-facing message for a
// local lease holder: pid, alive/dead, and a next step that actually exists.
// This replaces the dead-end "no resident serve on this machine holds this
// session" for every locally-held session.
func localLeaseHolderGuidance(holder localLeaseHolder) (string, bool) {
	switch holder.verdict {
	case leaseHolderSelf:
		if holder.locked {
			return "this window's own background runtime still holds this session (pid is this window); confirming the takeover releases the leftover runtime and reopens the session here", true
		}
		return "a leftover lease record from this window blocks the session; confirming the takeover retires the record and reopens the session here", true
	case leaseHolderForeignDead:
		return fmt.Sprintf("leftover runtime pid=%d (dead) still holds this session's lease record; confirming the takeover clears it and reopens the session — restarting the desktop clears it too",
			holder.info.PID), true
	case leaseHolderForeignLive:
		return fmt.Sprintf("leftover runtime pid=%d (alive) on this machine holds this session; close that window/runtime or run taskkill /PID %d, then retry the takeover or reopen the session",
			holder.info.PID, holder.info.PID), true
	default:
		return "", false
	}
}

// adoptLocalLeaseHeldSession is the takeover executed against the LOCAL lease
// instead of a serve (task 456 ②). It retires a provably-stale record (dead
// holder, or this window's own leftover), then rebuilds the tab so the normal
// startup bind picks the now-free lease up. A live foreign holder is refused
// with the task 456 ③ guidance — the OS lock of a live process is never
// stolen.
func (a *App) adoptLocalLeaseHeldSession(tab *WorkspaceTab, path string) error {
	key := sessionRuntimeKey(path)
	if key == "" {
		return fmt.Errorf("tab has no session")
	}
	sourceEpoch := a.runtimeEpochForTabLocked(tab)
	holder := a.inspectLocalLeaseHolder(path)
	switch holder.verdict {
	case leaseHolderForeignLive:
		if guidance, ok := localLeaseHolderGuidance(holder); ok {
			return errors.New(guidance)
		}
		return errors.New("a live local runtime holds this session")
	case leaseHolderForeignDead, leaseHolderNone:
		// The lock may still be held by a live process whose record was torn
		// away (pid reuse, AV quarantine): only clear when the lock is free.
		if holder.locked {
			return errors.New("this session's lease is locked by an unidentified local process; restart the desktop to clear it, then reopen the session")
		}
		if cleared, err := agent.ClearStaleSessionLeaseInfo(key); err != nil {
			return fmt.Errorf("retire stale lease record: %w", err)
		} else if cleared {
			slog.Info("desktop: takeover retired a stale local lease record",
				"pid", holderPIDForLog(holder.info))
		}
	case leaseHolderSelf:
		if holder.locked {
			// The incident shape: the lease is held inside this very process
			// by a runtime the visible tab does not recognize. Release it only
			// when every in-process holder is a provable zombie (no live
			// controller, no build in flight); a functional owner is refused.
			if released := a.releaseZombieSessionLeaseHoldersForKey(key, tab); released == 0 {
				return errors.New("this window already has a live runtime for this session; switch to or close that tab, then retry the takeover")
			}
		} else if _, err := agent.ClearStaleSessionLeaseInfo(key); err != nil {
			return fmt.Errorf("retire self lease record: %w", err)
		}
	}

	// Same commit discipline as the serve takeover: one serialized transaction
	// from the state check through controller publication.
	a.runtimeRebuildMu.Lock()
	defer a.runtimeRebuildMu.Unlock()
	switch state := a.takeoverTabStateAt(tab, sourceEpoch, path); {
	case state == takeoverTabUnavailable || tab.sessionLeaseRuntimeKey() != "":
		return fmt.Errorf("tab changed while taking over the session; retry")
	case state == takeoverTabLocalSpectator:
		// A spectator tab is fed by a serve mirror; its session returns via
		// the serve takeover path, never by local adoption.
		return fmt.Errorf("this tab is a mirrored spectator; use the serve takeover to take the session back")
	}
	if err := a.rebuildStartupTabLocked(tab); err != nil {
		return err
	}
	if ctrl := a.controllerForTab(tab); ctrl == nil || sessionRuntimeKey(ctrl.SessionPath()) != key || tab.sessionLeaseRuntimeKey() != key {
		return fmt.Errorf("session startup did not publish the adopted controller")
	}
	a.setTabReadOnly(tab.ID, false)
	a.clearDeferredRebuildVersion(tab.ID, a.deferredRebuildSequence(tab.ID))
	slog.Info("desktop: adopted locally-held session into this window", "holder_pid", holderPIDForLog(holder.info))
	return nil
}

// zombieLeaseHolderLocked reports whether tab is a provable in-process zombie
// holder of key: it bound the lease but never published a controller and has
// no build in flight, so nothing functional can ever release it. App.mu must
// be held. The registry phase guards the pre-restore corner where a tab
// object exists without runtime bookkeeping.
func (a *App) zombieLeaseHolderLocked(tab *WorkspaceTab, key string) bool {
	if tab == nil || tab.Ctrl != nil || tab.buildDone != nil {
		return false
	}
	if tab.sessionLeaseRuntimeKey() != key {
		return false
	}
	if rt := a.runtimeForTabLocked(tab); rt != nil && rt.Phase == sessionRuntimeReady {
		return false
	}
	return true
}

// releaseZombieSessionLeaseHoldersForKey releases the session lease of every
// in-process zombie holder of key (task 456 ②: desktop-internal adoption of
// the incident shape). It returns how many holders were released; 0 means a
// functional owner is holding the key and the caller must not proceed.
// Zombies are collected and released under App.mu: the lease helpers only
// take the tab's leaf sessionLeaseMu, never App.mu, so the order is safe, and
// holding App.mu closes the check-to-release race against a build starting.
func (a *App) releaseZombieSessionLeaseHoldersForKey(key string, except *WorkspaceTab) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	released := 0
	for _, candidate := range a.runtimeTabsLocked() {
		if candidate == nil || candidate == except {
			continue
		}
		if !a.zombieLeaseHolderLocked(candidate, key) {
			continue
		}
		candidate.releaseSessionLease()
		if rt := a.runtimeForTabLocked(candidate); rt != nil {
			a.removeSessionRuntimeMappingsLocked(rt)
		}
		released++
	}
	if detached := a.detachedSessions[key]; detached != nil && detached != except && a.zombieLeaseHolderLocked(detached, key) {
		detached.releaseSessionLease()
		if rt := a.runtimeForTabLocked(detached); rt != nil {
			a.removeSessionRuntimeMappingsLocked(rt)
		}
		released++
	}
	if released > 0 {
		slog.Info("desktop: released zombie in-process lease holder(s) blocking the session",
			"released", released)
	}
	return released
}

// describeLocalLeaseBlocker appends the task 456 ③ pid-liveness explanation to
// a failed takeover when a local lease record — not a serve — is what holds
// the session. ok is false when no local holder explains the failure and the
// caller keeps its own error text.
func (a *App) describeLocalLeaseBlocker(path string) (string, bool) {
	holder := a.inspectLocalLeaseHolder(path)
	if guidance, ok := localLeaseHolderGuidance(holder); ok {
		return guidance, true
	}
	return "", false
}

// reconcileRestoredSessionKeys is the entrypoint restoreOrBuildTabs calls
// before any restored tab build is launched (task 456 ① ordering contract).
func (a *App) reconcileRestoredSessionKeys(entries []desktopTabEntry) {
	if len(entries) == 0 {
		return
	}
	a.reconcileRestoredSessionLeaseRecords(entries)
}
