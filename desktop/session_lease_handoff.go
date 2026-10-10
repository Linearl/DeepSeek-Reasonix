package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/serve"
)

// handoffSessionLease acquires path and publishes it on the tab without
// releasing the previous lease. Recovery callbacks use the returned lease to
// retire the old path only after the current authority-guarded save returns.
func (t *WorkspaceTab) handoffSessionLease(path string) (*agent.SessionLease, error) {
	if t == nil || t.ReadOnly {
		return nil, nil
	}
	key := sessionRuntimeKey(path)
	if key == "" {
		return nil, nil
	}
	t.sessionLeaseMu.Lock()
	if t.sessionLease != nil && sessionRuntimeKey(t.sessionLease.Path()) == key {
		t.storeSessionLeaseRuntimeKey(key)
		t.sessionLeaseMu.Unlock()
		return nil, nil
	}
	lease, err := agent.TryAcquireSessionLease(key)
	if err != nil {
		t.sessionLeaseMu.Unlock()
		return nil, err
	}
	if hook := sessionLeaseAcquireHookForTest; hook != nil {
		hook()
	}
	old := t.sessionLease
	t.sessionLease = lease
	t.storeSessionLeaseRuntimeKey(key)
	t.sessionLeaseMu.Unlock()
	t.startTakeoverRequestWatcher(key)
	return old, nil
}

func (t *WorkspaceTab) swapSessionLease(lease *agent.SessionLease) *agent.SessionLease {
	if t == nil {
		return lease
	}
	t.sessionLeaseMu.Lock()
	old := t.sessionLease
	t.sessionLease = lease
	key := ""
	if lease != nil {
		key = sessionRuntimeKey(lease.Path())
	}
	t.storeSessionLeaseRuntimeKey(key)
	t.sessionLeaseMu.Unlock()
	if lease != nil {
		t.startTakeoverRequestWatcher(key)
	} else {
		t.stopTakeoverRequestWatcher()
	}
	return old
}

func (a *App) handoffTabRecoveryLease(tab *WorkspaceTab, recoveryPath string) error {
	if tab == nil || tab.ReadOnly {
		return nil
	}
	transition, err := a.reserveSessionRuntimePath(tab, recoveryPath)
	if err != nil {
		return fmt.Errorf("acquire recovery session lease: %w", userFacingSessionLeaseError("", err))
	}
	oldLease, err := tab.handoffSessionLease(recoveryPath)
	if err != nil {
		a.rollbackSessionRuntimePath(transition)
		slog.Warn("desktop: acquire recovery session lease", "path", recoveryPath, "err", err)
		reason := "lease_unavailable"
		if errors.Is(err, agent.ErrSessionLeaseHeld) {
			reason = "lease_held"
		}
		_ = agent.UpdateBranchMeta(recoveryPath, false, func(meta *agent.BranchMeta) error {
			meta.VersionKind = agent.VersionRecovery
			meta.VersionState = agent.VersionPending
			return nil
		})
		a.emitRuntimeEvent("session:recovery-failed", sessionRecoveryFailedEvent{
			Reason: reason, ConversationID: tab.TopicID, TopicID: tab.TopicID,
			RecoveryPath:  recoveryPath,
			WorkspaceRoot: tab.WorkspaceRoot,
			CanContinue:   false, RecoveryPending: true,
		})
		return fmt.Errorf("acquire recovery session lease: %w", userFacingSessionLeaseError("", err))
	}
	if err := bindTabWriteAuthority(tab, tab.Ctrl); err != nil {
		newLease := tab.swapSessionLease(oldLease)
		_ = bindTabWriteAuthority(tab, tab.Ctrl)
		a.rollbackSessionRuntimePath(transition)
		if newLease != nil {
			newLease.Release()
		}
		return fmt.Errorf("bind recovery session authority: %w", err)
	}
	a.commitSessionRuntimePath(transition)
	if oldLease != nil {
		go oldLease.Release()
	}
	return nil
}

// handleTabSessionTransition moves a tab's lease and binds the unpublished
// target Session before its controller switches paths. The source controller
// remains fully usable when any acquisition or bind step fails.
func (a *App) handleTabSessionTransition(tab *WorkspaceTab) func(control.SessionTransitionInfo) error {
	return func(info control.SessionTransitionInfo) error {
		if tab == nil || tab.ReadOnly {
			return nil
		}
		if info.Reason == "fork" || info.Reason == "branch" {
			if err := copyPinnedContextState(info.OriginalPath, info.TargetPath); err != nil {
				return fmt.Errorf("copy pinned context to %s: %w", info.Reason, err)
			}
		}
		pinnedState, err := loadPinnedContextState(info.TargetPath)
		if err != nil {
			return fmt.Errorf("load target pinned context: %w", err)
		}
		transition, err := a.reserveSessionRuntimePath(tab, info.TargetPath)
		if err != nil {
			return fmt.Errorf("acquire target session lease: %w", userFacingSessionLeaseError("", err))
		}
		oldLease, err := tab.handoffSessionLease(info.TargetPath)
		if err != nil {
			a.rollbackSessionRuntimePath(transition)
			return fmt.Errorf("acquire target session lease: %w", userFacingSessionLeaseError("", err))
		}
		tab.sessionLeaseMu.Lock()
		lease := tab.sessionLease
		tab.sessionLeaseMu.Unlock()
		if err := info.BindWriteAuthority(lease); err != nil {
			newLease := tab.swapSessionLease(oldLease)
			a.rollbackSessionRuntimePath(transition)
			if newLease != nil {
				newLease.Release()
			}
			return fmt.Errorf("bind target session authority: %w", err)
		}
		a.mu.Lock()
		if tab.removed || !a.runtimeOwnerLiveLocked(transition.runtime) || !a.commitSessionRuntimePathLocked(transition) {
			a.mu.Unlock()
			newLease := tab.swapSessionLease(oldLease)
			a.rollbackSessionRuntimePath(transition)
			if newLease != nil {
				newLease.Release()
			}
			return fmt.Errorf("bind target session: tab runtime changed; retry")
		}
		tab.SessionPath = canonicalTabSessionPath(info.TargetPath)
		// Rotations initiated inside the controller (e.g. /extract's NewSession)
		// bypass the desktop ClearSession wrapper, so the tab-local generation
		// must bump here: the frontend watches it to re-hydrate the transcript
		// onto the rotated session instead of keeping the stale one.
		tab.SessionGeneration++
		if a.tabs[tab.ID] == tab {
			a.saveTabsLocked()
		}
		a.mu.Unlock()
		if oldLease != nil {
			go oldLease.Release()
		}
		info.OnCommit(func() {
			tab.setPinnedFiles(pinnedState.Files)
			a.emitRuntimeEvent(tabMetaRefreshEventChannel, TabMetaRefreshEvent{TabID: tab.ID, Meta: a.MetaForTab(tab.ID)})
		})
		a.emitProjectTreeChangedForSessionDirs(sessionDirectoryForPath(info.TargetPath))
		return nil
	}
}

// takeoverPromptTimeout bounds one desktop prompt. It aligns with the 539
// T2 window (SessionTakeoverYieldWindow): the user can watch the notice at
// leisure because accepting no longer interrupts the running turn. Serve's
// takeover poll waits the same window for the yield.
var takeoverPromptTimeout = agent.SessionTakeoverYieldWindow

// takeoverPromptTimeoutForTest lets tests shrink the blocking window.
var takeoverPromptTimeoutForTest = takeoverPromptTimeout

// startTakeoverRequestWatcher polls for a remote takeover request marker
// (<session>.takeover-request, written by the serve takeover endpoint) while
// this tab holds the session lease. When a request arrives the tab prompts;
// on accept (or headless) the tab ENTERS THE YIELDING STATE (539 route A):
// the lease is kept, the running turn finishes, then the lease is released
// with a handoff reservation and the marker flips to the yielded ack. The
// tab never cancels the turn here — only a forced request does (T3).
func (t *WorkspaceTab) startTakeoverRequestWatcher(path string) {
	if t == nil || t.takeoverWatchStop != nil {
		return
	}
	stop := make(chan struct{})
	t.takeoverWatchStop = stop
	go func() {
		marker := path[:len(path)-len(".jsonl")] + ".takeover-request"
		slog.Info("desktop: takeover watcher started", "path", path, "marker", marker)
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if _, err := os.Stat(marker); err != nil {
					continue
				}
				raw, rerr := os.ReadFile(marker)
				if rerr != nil {
					continue
				}
				state := agent.ParseTakeoverMarker(string(raw))
				switch state.Kind {
				case agent.TakeoverMarkerKindForced:
					// Remote forced takeover (GC's double-gated button) in the
					// direct-serve topology: no desktop prompt — cancel and
					// yield (T3). The machine cancels the running turn.
					slog.Info("desktop: forced takeover request detected, yielding without prompt", "marker", marker)
					beginSessionYieldToTakeover(t, path, marker, true)
					return
				case agent.TakeoverMarkerKindPending, agent.TakeoverMarkerKindYielded:
					// A takeover already in flight (the gateway gate accepted
					// it, or this very tab's yield machine published the ack):
					// never double-prompt.
					continue
				case agent.TakeoverMarkerKindRequest:
					if sessionYieldActiveForTab(t) {
						// Our own yield machine is about to rewrite this
						// marker to pending; don't race it with a prompt.
						continue
					}
				default:
					continue
				}
				from := state.TargetWriterID
				takeoverBridgeMu.Lock()
				sink := takeoverPromptSink
				takeoverBridgeMu.Unlock()
				if sink == nil {
					// Headless: no prompt, but the yield still honors the
					// running turn (539: headless skips the dialog, not the
					// wait).
					slog.Info("desktop: takeover request detected, yielding (no prompt sink)", "marker", marker)
					beginSessionYieldToTakeover(t, path, marker, false)
					return
				}
				slog.Info("desktop: takeover request detected, prompting user", "marker", marker, "from", from)
				reply := registerTakeoverPending(marker)
				sink(takeoverDecisionReq{Marker: marker, Path: path, From: from, Reply: reply})
				promptTimeout := takeoverPromptTimeoutForTest
				select {
				case accept := <-reply:
					unregisterTakeoverPending(marker)
					if !accept {
						slog.Info("desktop: takeover request rejected by user", "marker", marker)
						_ = os.Remove(marker)
						continue // tab keeps its lease; keep watching
					}
					beginSessionYieldToTakeover(t, path, marker, false)
					return
				case <-time.After(promptTimeout):
					// Serve's takeover poll waits T2 for a PENDING yield; a
					// never-answered prompt must resolve as a refusal so the
					// remote side is not strung along.
					unregisterTakeoverPending(marker)
					slog.Info("desktop: takeover prompt timed out, refusing", "marker", marker)
					_ = os.Remove(marker)
				case <-stop:
					unregisterTakeoverPending(marker)
					return
				}
			}
		}
	}()
}

func (t *WorkspaceTab) stopTakeoverRequestWatcher() {
	if t == nil || t.takeoverWatchStop == nil {
		return
	}
	select {
	case <-t.takeoverWatchStop:
	default:
		close(t.takeoverWatchStop)
	}
	t.takeoverWatchStop = nil
}

// takeoverDecisionReq is one pending remote-takeover prompt handed to the
// App-owned sink (task 36 Phase 1). Reply receives the user's verdict.
type takeoverDecisionReq struct {
	Marker string
	Path   string
	From   string
	Reply  chan bool
}

var (
	takeoverBridgeMu   sync.Mutex
	takeoverPromptSink func(req takeoverDecisionReq)
	takeoverPending    = map[string]chan bool{}
)

// RegisterTakeoverPromptSink installs the App-owned emitter. Call once at
// startup; a nil sink keeps the historical immediate-yield fallback for
// headless/test processes.
func RegisterTakeoverPromptSink(fn func(req takeoverDecisionReq)) {
	takeoverBridgeMu.Lock()
	takeoverPromptSink = fn
	takeoverBridgeMu.Unlock()
}

// SubmitTakeoverDecision answers a pending request from the frontend. It
// returns false when no request is waiting (stale dialog, double click).
func SubmitTakeoverDecision(marker string, accept bool) bool {
	takeoverBridgeMu.Lock()
	reply, ok := takeoverPending[marker]
	delete(takeoverPending, marker)
	takeoverBridgeMu.Unlock()
	if !ok {
		return false
	}
	select {
	case reply <- accept:
	default:
	}
	return true
}

func registerTakeoverPending(marker string) chan bool {
	takeoverBridgeMu.Lock()
	defer takeoverBridgeMu.Unlock()
	reply := make(chan bool, 1)
	takeoverPending[marker] = reply
	return reply
}

func unregisterTakeoverPending(marker string) {
	takeoverBridgeMu.Lock()
	delete(takeoverPending, marker)
	takeoverBridgeMu.Unlock()
}

// yieldSessionLeaseToTakeover was the interrupt-style handoff (cancel the
// turn, wait ≤3s, release immediately). Task 539 replaced it with the
// yielding state machine in session_yielding.go: the lease is kept until the
// turn finishes, then released with a handoff reservation — see
// beginSessionYieldToTakeover.

// ResolveTakeoverDecision is the frontend's answer to an app:takeover-request
// prompt (task 36 Phase 1). A stale or duplicate answer returns false and
// changes nothing.
func (a *App) ResolveTakeoverDecision(marker string, accept bool) bool {
	return SubmitTakeoverDecision(strings.TrimSpace(marker), accept)
}

// remoteDeviceReadOnly mirrors serve's device lease into this process (task
// 36 Phase 2): while a remote device holds write authority the desktop runs
// in a NON-PERSISTED read-only state — it must never touch the persisted
// tab.ReadOnly field (pitfall 1: that would survive restarts) nor call
// setTabReadOnly (pitfall 2: it detaches terminals). The flag only drives
// UI state; enforcement itself is serve's device-level submit rejection.
var remoteDeviceReadOnly atomic.Bool

// RemoteDeviceReadOnly reports the live runtime-only read-only flag for the
// frontend (never persisted; resets to false on restart by construction).
func RemoteDeviceReadOnly() bool { return remoteDeviceReadOnly.Load() }

// registerRemoteWriteAuthorityHook observes serve's device lease (same
// process). held=true flips the flag and releases every tab's session lease
// so no local writer races the device; held=false restores write authority
// and the next local message reacquires leases normally.
func (a *App) registerRemoteWriteAuthorityHook() {
	serve.SetRemoteWriteAuthorityHook(func(deviceID string, held bool) {
		remoteDeviceReadOnly.Store(held)
		slog.Info("desktop: remote write authority", "device", deviceID, "held", held)
		if held {
			a.releaseAllTabSessionLeases()
		}
		runtimeEventsEmitFallback(a.ctx, "app:remote-write-authority", map[string]any{
			"device": deviceID,
			"held":   held,
		})
	})
}

// releaseAllTabSessionLeases walks every tab and drops its session lease.
// Leases reacquire on the next local message; nothing persisted changes.
// Task 539 fuse (W3 same-disease path): this hook fires after the serve
// acquired — the turn is normally long gone — but if a tab still has live
// runtime work (residual T3 window, future callers), cancel it explicitly
// and tell the user instead of releasing under a running turn (its queued
// saves would Stale-reject as lost writes).
func (a *App) releaseAllTabSessionLeases() {
	a.mu.Lock()
	tabs := make([]*WorkspaceTab, 0, len(a.tabs))
	for _, tab := range a.tabs {
		tabs = append(tabs, tab)
	}
	a.mu.Unlock()
	for _, tab := range tabs {
		if tab.hasActiveRuntimeWork() && tab.Ctrl != nil {
			path := tab.currentSessionPath()
			slog.Info("desktop: remote device holds write authority, cancelling active turn before lease release", "tab", tab.ID, "path", path)
			tab.Ctrl.Cancel()
			notifyTakeoverYield("forced", path, "远程设备已持有写权，本地回合被中断")
		}
		tab.releaseSessionLeaseQuietly()
	}
}

// releaseSessionLeaseQuietly drops this tab's lease without touching
// persisted ReadOnly and without detaching the tab's terminal.
func (t *WorkspaceTab) releaseSessionLeaseQuietly() {
	if t == nil {
		return
	}
	t.sessionLeaseMu.Lock()
	old := t.sessionLease
	t.sessionLease = nil
	// Task 611: clear the runtime key like every other release path
	// (releaseSessionLease / releaseSessionLeaseForKey) — this is the single
	// convergence point that stops the takeover-request watcher and syncs the
	// 485 leak-sweeper tracker. Skipping it leaked one 3s os.Stat poller per
	// quiet-released tab for the rest of the process lifetime (a long-lived
	// desktop process only grows; in the test binary the leaked pollers were
	// the "leftover watcher" population every full-package goroutine dump
	// pointed at — 327/439/611 dumps alike).
	t.storeSessionLeaseRuntimeKey("")
	t.sessionLeaseMu.Unlock()
	if old != nil {
		old.Release()
		slog.Info("desktop: session lease released for remote device", "tab", t.ID)
	}
}
