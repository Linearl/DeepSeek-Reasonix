package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
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

// startTakeoverRequestWatcher polls for a remote takeover request marker
// (<session>.takeover-request, written by the serve takeover endpoint) while
// this tab holds the session lease. When a request arrives the tab yields:
// the lease is released (so the serve-side acquire succeeds), the tab flips
// to read-only, and the tree metadata refresh tells the frontend.
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
				from := ""
				if raw, rerr := os.ReadFile(marker); rerr == nil {
					from = strings.TrimSpace(string(raw))
				}
				takeoverBridgeMu.Lock()
				sink := takeoverPromptSink
				takeoverBridgeMu.Unlock()
				if sink == nil {
					// Headless: historical behavior — yield without a prompt.
					slog.Info("desktop: takeover request detected, yielding (no prompt sink)", "marker", marker)
					t.yieldSessionLeaseToTakeover(path, marker, false)
					return
				}
				slog.Info("desktop: takeover request detected, prompting user", "marker", marker, "from", from)
				reply := registerTakeoverPending(marker)
				sink(takeoverDecisionReq{Marker: marker, Path: path, From: from, Reply: reply})
				select {
				case accept := <-reply:
					unregisterTakeoverPending(marker)
					if !accept {
						slog.Info("desktop: takeover request rejected by user", "marker", marker)
						_ = os.Remove(marker)
						continue // tab keeps its lease; keep watching
					}
					t.yieldSessionLeaseToTakeover(path, marker, true)
					return
				case <-time.After(9 * time.Second):
					// Serve's own takeover poll times out at 9s with 409, so
					// an unanswered prompt must resolve as a refusal.
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

// yieldSessionLeaseToTakeover performs an accepted handoff: interrupt a
// running turn first (handoffMode=interrupt — never cut a mid-write), then
// release the lease so the serve-side acquire succeeds. ReadOnly is
// deliberately not flipped: it persists in desktop-tabs.json and would lock
// the tab out of writing across restarts; releasing the lease is enough and
// the next local message reacquires it cleanly.
func (t *WorkspaceTab) yieldSessionLeaseToTakeover(path, marker string, interrupt bool) {
	if interrupt && t.hasActiveRuntimeWork() && t.Ctrl != nil {
		slog.Info("desktop: cancelling active turn before yield (handoffMode=interrupt)", "path", path)
		t.Ctrl.Cancel()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) && t.hasActiveRuntimeWork() {
			time.Sleep(100 * time.Millisecond)
		}
	}
	t.sessionLeaseMu.Lock()
	old := t.sessionLease
	t.sessionLease = nil
	t.sessionLeaseMu.Unlock()
	if old != nil {
		old.Release()
	}
	_ = os.Remove(marker)
	slog.Info("desktop: session yielded to remote takeover", "path", path)
}

// ResolveTakeoverDecision is the frontend's answer to an app:takeover-request
// prompt (task 36 Phase 1). A stale or duplicate answer returns false and
// changes nothing.
func (a *App) ResolveTakeoverDecision(marker string, accept bool) bool {
	return SubmitTakeoverDecision(strings.TrimSpace(marker), accept)
}
