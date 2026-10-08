package main

import "fmt"

// SetActiveTab switches the frontend's active tab. Restored remote shells
// reconnect only when activated.
func (a *App) SetActiveTab(tabID string) error {
	a.tabSelectionMu.Lock()
	defer a.tabSelectionMu.Unlock()

	a.remoteTabMu.Lock()
	if _, isRemote := a.remoteTabs[tabID]; isRemote {
		switchingFromLocal := a.remoteTabLayout.activeID == ""
		a.remoteTabMu.Unlock()
		if switchingFromLocal {
			if err := a.snapshotActiveLocalBeforeRemote(); err != nil {
				return err
			}
		}

		a.remoteTabMu.Lock()
		tab, isRemote := a.remoteTabs[tabID]
		if !isRemote {
			a.remoteTabMu.Unlock()
			return fmt.Errorf("tab %q not found", tabID)
		}
		a.remoteTabLayout.activeID = tabID
		revive := tab.state == "disconnected"
		terminalState, terminalErr := "", ""
		if revive {
			tab.state = "connecting"
		} else if tab.state == "error" || tab.state == "serve_down" {
			terminalState, terminalErr = tab.state, tab.err
		}
		hostID, workspace := tab.ref.HostID, tab.ref.Workspace
		a.remoteTabMu.Unlock()
		if revive {
			a.emitRemoteTabState(tabID, "connecting", "")
			a.goRemoteTabSafe("remoteTabServe", func() { a.bootstrapRemoteTab(tabID, hostID, workspace) })
		} else if terminalState != "" {
			// A restored shell can fail before its React surface subscribes. Re-publish
			// the authoritative terminal state on activation so the recovery UI does
			// not remain on an inferred connecting placeholder.
			a.emitRemoteTabState(tabID, terminalState, terminalErr)
		}
		a.saveTabsFromRemote()
		return nil
	}
	a.remoteTabMu.Unlock()
	a.mu.RLock()
	_, ok := a.tabs[tabID]
	alreadyActive := a.activeTabID == tabID
	a.mu.RUnlock()
	if !ok {
		return fmt.Errorf("tab %q not found", tabID)
	}
	if alreadyActive {
		a.remoteTabMu.Lock()
		a.remoteTabLayout.activeID = ""
		a.remoteTabMu.Unlock()
		a.saveTabsFromRemote()
		return nil
	}
	a.mu.RLock()
	active := a.tabs[a.activeTabID]
	a.mu.RUnlock()
	if err := a.snapshotTabForSwitch(active); err != nil {
		return err
	}

	a.mu.Lock()
	if _, ok := a.tabs[tabID]; !ok {
		a.mu.Unlock()
		return fmt.Errorf("tab %q not found", tabID)
	}
	if a.activeTabID == tabID {
		a.mu.Unlock()
		a.remoteTabMu.Lock()
		a.remoteTabLayout.activeID = ""
		a.remoteTabMu.Unlock()
		a.saveTabsFromRemote()
		return nil
	}
	a.activeTabID = tabID
	next := a.tabs[tabID]
	// 任务 619 ②: activating a lazy restored tab kicks its controller build.
	// Startup only builds the previously-active (and autopilot) tabs; the rest
	// stay published skeletons. A click is the load trigger.
	kickLazyBuild := lazyTabNeedsBuildLocked(next)
	// A direct click supersedes pending publication without cancelling its
	// build: the tab stays open, and selecting that same tab keeps it alive.
	supersededReq, supersededTab := a.supersedePendingTopicActivationLocked(tabID, false)
	dir, entries, activeID, version := a.saveTabsCollectLocked()
	a.mu.Unlock()
	a.remoteTabMu.Lock()
	a.remoteTabLayout.activeID = ""
	a.remoteTabMu.Unlock()

	// I/O outside the lock — disk writes can block for hundreds of ms on
	// Windows when antivirus or the search indexer briefly locks the file.
	a.saveTabsWrite(dir, entries, activeID, version)
	if active != nil {
		active.clearRuntimeDisplayCurrency()
	}
	if next != nil {
		next.clearRuntimeDisplayCurrency()
	}
	if supersededReq != "" {
		a.emitTopicActivation(TopicActivationEvent{RequestID: supersededReq, TabID: supersededTab, Phase: topicActivationPhaseCancelled})
	}
	if kickLazyBuild {
		a.startTabControllerBuild(next)
	}
	a.kickDeferredRebuildRetry()
	return nil
}

// snapshotTabForSwitch is the SetActiveTab form of snapshotTabForAction. A tab
// click must not queue on the session save-path mutex: behind a large
// session's full-decode load that wait measured p50 28s / max 69s (DAG
// research 2026-10-04) and is the whole "tab switch takes >10s" report. With
// controller support, an uncontended path snapshots inline exactly as before;
// a busy path skips the wait and hands the flush to the tab's background
// snapshot loop (scheduleTabSnapshot), which writes once the lock frees —
// append-only semantics make a seconds-late flush lossless. Close, shutdown,
// and remote-takeover paths keep their synchronous snapshots.
func (a *App) snapshotTabForSwitch(tab *WorkspaceTab) error {
	if tab == nil {
		return nil
	}
	a.mu.RLock()
	readOnly := tab.ReadOnly
	ctrl := tab.Ctrl
	a.mu.RUnlock()
	if readOnly || ctrl == nil {
		return nil
	}
	free, ok := ctrl.(interface {
		SnapshotIfSavePathFree() (bool, error)
	})
	if !ok {
		// Controllers without the capability keep the previous blocking form.
		return a.snapshotTabForAction(tab, "switching tabs")
	}
	attempted, err := free.SnapshotIfSavePathFree()
	if err != nil {
		a.reportTabSnapshotError(tab, "switching tabs", err)
		return fmt.Errorf("save current session before switching tabs: %w", err)
	}
	if !attempted {
		// Save path busy: switch now, flush in the background. The loop is
		// single-flight per tab and refuses work once the tab closes, so it
		// cannot race DeleteSession (#4384).
		a.scheduleTabSnapshot(tab.ID)
	}
	return nil
}
