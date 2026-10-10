package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/botruntime"
	"reasonix/internal/control"
	"reasonix/internal/plugin"
)

func (a *App) clearActiveSessionRuntime(tab *WorkspaceTab, oldCtrl control.SessionAPI) (SessionClearResult, error) {
	if tab == nil || oldCtrl == nil {
		return SessionClearResult{}, fmt.Errorf("workspace is still starting")
	}
	// This is a build+swap of the tab's controller; serialize with the other
	// rebuild paths (see runtimeRebuildMu) so a concurrent rebuild cannot
	// interleave a second swap. Lock order: runtimeRebuildMu → sessionRemovalMu.
	a.runtimeRebuildMu.Lock()
	defer a.runtimeRebuildMu.Unlock()
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	// This path destroys the old session's files (removeDesktopSessionArtifacts);
	// serialize with DeleteSession/TrashTopic/workspace removal so they never
	// trash or restore the same files mid-clear.
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()

	a.reconciledSessionPathForTab(tab)
	oldPath := oldCtrl.SessionPath()
	// Snapshot the tab profile under a.mu: bound methods write these fields
	// under the lock while this rebuild runs off-lock.
	snap := a.tabRuntimeSnapshot(tab)
	oldSink := snap.sink
	if oldSink != nil {
		// Rebind under the runtime key, matching the id cloneDetachedRuntimeTab
		// derives — a raw path here would hash to a different detached id on
		// Windows where keys are case-folded.
		oldSink.setBinding(detachedRuntimeTabID(sessionRuntimeKey(oldPath)), nil)
		oldSink.clearContext()
	}
	if oldCtrl.RuntimeStatus().Cancellable {
		oldCtrl.Cancel()
		if err := waitControllerStopped(oldCtrl); err != nil {
			return SessionClearResult{}, err
		}
	}
	destroy := oldCtrl.BeginDestroySession(oldPath)
	destroys := []control.SessionDestroyHandle{destroy}
	teardownTimedOut := waitDestroyHandles(destroys)
	if teardownTimedOut {
		if err := agent.MarkCleanupPending(oldPath, "clear"); err != nil {
			return SessionClearResult{}, err
		}
	}

	newSink := &tabEventSink{tabID: tab.ID, app: a, ctx: a.ctx}
	sharedHost := a.lookupSharedHost(snap.sharedHostKey)
	runtimeBuildStart := time.Now()
	slog.Info("desktop: runtime build begin", "tab", tab.ID, "trigger", "clear-session", "model", snap.model, "autopilot", snap.autopilot) // task 196: name who is building a runtime, so a startup burst can be attributed instead of inferred.
	defer logRuntimeBuildEnd("clear-session", runtimeBuildStart, sharedHost != nil)                                                        // task 334: begin→end pair = begin → function exit.
	newCtrl, err := boot.Build(a.bootContext(), a.clearSessionBuildOptions(tab, snap, newSink, sharedHost))
	if err != nil {
		if teardownTimedOut {
			// The old session was already marked cleanup-pending, so finish the
			// destroy cleanup instead of re-exposing a runtime in teardown.
			go delayedDesktopSessionCleanup(oldPath, destroys)
		} else {
			finishDestroyHandles(destroys)
		}
		if oldSink != nil {
			oldSink.setBinding(tab.ID, nil)
			oldSink.setContext(a.ctx)
		}
		return SessionClearResult{}, err
	}
	if teardownTimedOut {
		go delayedDesktopSessionCleanup(oldPath, destroys)
	} else {
		if err := removeDesktopSessionArtifacts(oldPath); err != nil {
			finishDestroyHandles(destroys)
			newCtrl.Close()
			return SessionClearResult{}, err
		}
		finishDestroyHandles(destroys)
	}
	a.bindControllerDisplayRecorder(newCtrl)
	newCtrl.EnableInteractiveApproval()
	applyTabModeToController(newCtrl, snap.mode)
	applyTabToolApprovalModeToController(newCtrl, snap.toolApprovalMode)
	// Keep the replacement controller's merged Auto Guard default. Clearing also
	// drops the active goal, which must not seed the replacement conversation.
	path := agent.NewSessionPath(newCtrl.SessionDir(), newCtrl.Label())
	if err := a.ensureTabSessionLeaseForRebuild(tab, path, ""); err != nil {
		newCtrl.Close()
		// Surfaces through ClearSession's Wails return; keep the holder's
		// path/pid/writer id out of it.
		return SessionClearResult{}, userFacingSessionLeaseError("", err)
	}
	newCtrl.SetFreshSessionPath(path)
	if err := initClearedPins(path, newCtrl, oldCtrl, tab); err != nil {
		return SessionClearResult{}, err
	}

	return a.installClearedSessionReplacement(tab, newCtrl, newSink, path, oldCtrl)
}

// installClearedSessionReplacement authorizes and installs the replacement
// controller after a clear. On authorization failure it releases what the
// clear acquired (fresh controller and lease) and finishes the old teardown.
func (a *App) installClearedSessionReplacement(tab *WorkspaceTab, newCtrl *control.Controller, newSink *tabEventSink, path string, oldCtrl control.SessionAPI) (SessionClearResult, error) {
	a.mu.Lock()
	if err := a.authorizeTabReplacementLocked(tab, newCtrl, "clearing the session", "fresh"); err != nil {
		a.mu.Unlock()
		// The old session is already destroyed either way; release what this
		// clear acquired for the replaced tab (fresh controller and its
		// lease) so neither leaks, and still finish the old runtime teardown.
		newCtrl.Close()
		tab.releaseSessionLease()
		oldCtrl.CloseAfterDestroy()
		a.emitProjectTreeChangedForSessionDirs(newCtrl.SessionDir())
		return SessionClearResult{}, err
	}
	installClearedTabRuntime(tab, newCtrl, newSink, path)
	clearTabStartupError(tab)
	tab.goal = ""
	// Supersede any in-flight startup build: the session it was resuming
	// was just destroyed, and finishing later would pass the generation
	// check and overwrite this controller.
	a.supersedeTabBuildLocked(tab)
	a.saveTabsLocked()
	a.mu.Unlock()
	// Same contract as ClearSession's non-running path: the replacement
	// session starts with zero spend.
	tab.resetTelemetry(path)
	a.persistTabSessionPath(tab, path)
	oldCtrl.CloseAfterDestroy()
	a.emitProjectTreeChangedForSessionDirs(newCtrl.SessionDir())
	a.notifyTabRuntimeRebuilt(tab)
	return a.bumpAndSnapshotSessionClear(tab), nil
}

func removeDesktopSessionArtifacts(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	guard, err := acquireSessionRemovalGuard(path)
	if err != nil {
		return err
	}
	return removeDesktopSessionArtifactsWithGuard(path, guard)
}

// clearSessionBuildOptions assembles the boot options for the replacement
// controller built by clearActiveSessionRuntime.
func (a *App) clearSessionBuildOptions(tab *WorkspaceTab, snap tabRuntimeSnapshot, newSink *tabEventSink, sharedHost *plugin.Host) boot.Options {
	return boot.Options{
		RestartUpdater:             restartUpdaterAdapter{a},
		AutonomousUpdateController: newAutonomousUpdateController(a),
		WorktreeProjectOpener:      appWorktreeProjectOpener{app: a}, // 任务128：会话自主建项目——注册为项目并开后台 tab
		Model:                      snap.model,
		Autopilot:                  snap.autopilot,
		MaxRuntime:                 snap.autopilotMaxRuntime,
		AutopilotApprovalGrace:     snap.autopilotApprovalGrace,
		AutopilotAskTimeoutEnabled: snap.autopilotAskTimeoutEnabled,
		AutopilotAskWait:           snap.autopilotAskWait,
		AutopilotAskAutoContinue:   snap.autopilotAskAutoContinue,
		RequireKey:                 false,
		StatsSource:                "desktop",
		TaskStore:                  a.taskStore(),
		OnConfigLoadWarnings:       a.configLoadWarningsHandler(),
		Sink:                       newSink,
		WorkspaceRoot:              snap.workspaceRoot,
		SessionDir:                 sessionDirForSnapshot(snap),
		EffortOverride:             cloneStringPtr(snap.effort),
		SharedHost:                 sharedHost,
		MCPHostProfile:             plugin.HostProfileDesktopApps,
		CleanupPendingReconciler:   reconcileDesktopCleanupPending,
		SubagentParentLive:         a.subagentParentProbeForBuild(tab),
		SessionRecoveryMeta:        a.tabSessionRecoveryMeta(tab),
		PinnedContextLoader:        pinnedContextLoader(snap.workspaceRoot),
		OnSessionRecovered:         a.handleTabSessionRecovered(tab),
		OnSessionTransition:        a.handleTabSessionTransition(tab),
		BeforeInboxDispatch:        a.beforeInboxDispatch,
		OnInboxDispatchExhausted:   a.inboxDispatchExhausted,
		OnSessionTitleChanged:      a.onSessionTitleChanged,
		OnCreateCollabSession:      a.createCollabSession,
		OnSessionStatus:            a.collabSessionStatus,
		OnSessionWorkDetail:        a.collabSessionWorkDetail,
		OnSessionInfo:              a.collabSessionInfo,
		OnSessionGroup:             a.collabSessionGroup,
		OnSessionGroupMatch:        a.collabSessionGroupMatch,
		OnSessionVersions:          a.collabSessionVersions,
		OnAdoptSessionVersion:      a.collabAdoptSessionVersion,
		OnSessionStop:              a.collabSessionStop,
		OnSessionSetModel:          a.collabSessionSetModel,
		OnSessionTurnStatus:        a.collabSessionTurnStatus,
		OnCascadeDelegate:          cascadeDelegateFor,
		OnFallbackSwitch:           a.fallbackSwitch,
		OnDeleteSession:            a.deleteCollabSession,
		OnRenameSession:            a.renameCollabSession,
		OnMoveTopicToGroup:         a.moveCollabTopicToGroup,
	}
}

// DeleteSession moves a saved session to the local trash. If the session still
// has an in-process runtime, the runtime is cancelled and removed first so
// autosave cannot recreate or append to the deleted file later.
func (a *App) DeleteSession(path string) error {
	return friendlySessionFileError(a.deleteSession(path))
}

// DeleteRecoveryCopy is the guarded bulk-cleanup path. The frontend's copy
// marker is only a hint. Open copies are preserved, and the backend holds both
// parent and branch removal guards while re-proving coverage and publishing a
// recoverable trash entry.
func (a *App) DeleteRecoveryCopy(path string) error {
	return friendlySessionFileError(a.deleteRecoveryCopy(path))
}

var errRecoveryCopyNotRedundant = errors.New("recovery session contains content not preserved by its parent")

// errNoRecoveryCopiesToConsolidate is returned when the session-action
// "merge recovery copies" finds no recovery copies for the session.
var errNoRecoveryCopiesToConsolidate = errors.New("this session has no recovery copies to merge")

func (a *App) deleteSession(path string) error {
	dir := a.activeSessionDir()
	sessionPath, key, err := validateSessionPath(dir, path)
	if err != nil {
		var foundErr error
		if dir, sessionPath, foundErr = a.sessionDirForPath(path); foundErr != nil {
			return err
		}
		key = filepath.Base(sessionPath)
	}
	if err := validateSessionTrashTarget(dir, sessionPath, key); err != nil {
		return err
	}
	var fallback fallbackRuntimeTarget
	if err := func() error {
		defer a.lockRuntimeMutation("delete-session")()
		a.sessionRemovalMu.Lock()
		defer a.sessionRemovalMu.Unlock()
		removed, nextFallback := a.removeSessionRuntimeBindings(dir, sessionPath)
		fallback = nextFallback
		if err := a.prepareRemovedSessionRuntimes(removed); err != nil {
			a.closeRemainingRemovedSessionRuntimesAdmissionHeld(removed, map[control.SessionAPI]bool{})
			return err
		}
		closedRemoved := map[control.SessionAPI]bool{}
		destroys := a.destroyHandlesForSession(dir, sessionPath, removed)
		teardownTimedOut := waitDestroyHandles(destroys)
		a.closeRemovedSessionRuntimesForSessionAfterDestroyAdmissionHeld(removed, dir, sessionPath, closedRemoved)
		if teardownTimedOut {
			if err := agent.MarkCleanupPending(sessionPath, "delete"); err != nil {
				a.closeRemainingRemovedSessionRuntimesAfterDestroyAdmissionHeld(removed, closedRemoved)
				return err
			}
			go delayedDesktopSessionTrash(dir, sessionPath, key, destroys)
		} else {
			err = trashSessionArtifacts(dir, sessionPath, key)
			finishDestroyHandles(destroys)
			if err != nil {
				a.closeRemainingRemovedSessionRuntimesAfterDestroyAdmissionHeld(removed, closedRemoved)
				return err
			}
		}
		a.closeRemainingRemovedSessionRuntimesAfterDestroyAdmissionHeld(removed, closedRemoved)
		return nil
	}(); err != nil {
		return err
	}
	if err := botruntime.ForgetAutoSessionMappingsForPath(sessionPath); err != nil {
		slog.Warn("desktop: failed to clear auto bot session mapping", "err", err)
	}
	// Task 499 ②: the session is gone — its replayed graph must not stay
	// pinned in the process cache (the third never-invalidated path the
	// 10-05 diagnosis named alongside tab close and detached release).
	if freed, ok := agent.InvalidateSessionGraph(sessionPath); ok {
		slog.Info("desktop: invalidated dag graph cache on session delete",
			"path", sessionPath, "freed_bytes", freed)
	}
	if fallback.needs {
		fallback = a.sessionDeleteFallbackTarget(fallback)
		if err := a.openFallbackRuntime(fallback); err != nil {
			return err
		}
	}
	a.removeSessionCatalogPath(sessionPath, "session_deleted")
	a.emitProjectTreeChangedForSessionDirs(dir)
	a.invalidatePromptHistoryCache()
	return nil
}

type fallbackRuntimeTarget struct {
	needs         bool
	scope         string
	workspaceRoot string
	topicID       string
}

func (a *App) removeSessionRuntimeBindings(dir, sessionPath string) ([]removedSessionRuntime, fallbackRuntimeTarget) {
	var removed []removedSessionRuntime
	var fallback fallbackRuntimeTarget

	a.mu.Lock()
	for id, tab := range a.tabs {
		if !tabMatchesSession(tab, dir, sessionPath) {
			continue
		}
		if len(removed) == 0 {
			fallback = fallbackRuntimeTarget{scope: tab.Scope, workspaceRoot: tab.WorkspaceRoot, topicID: tab.TopicID}
		}
		removed = append(removed, removedRuntimeFromTab(tab, dir, sessionPath))
		a.markTabRemovedLocked(tab)
		delete(a.tabs, id)
		a.removeTabOrderLocked(id)
		if a.activeTabID == id {
			a.activeTabID = ""
		}
	}
	for key, tab := range a.detachedSessions {
		if !tabMatchesSession(tab, dir, sessionPath) {
			continue
		}
		if len(removed) == 0 {
			fallback = fallbackRuntimeTarget{scope: tab.Scope, workspaceRoot: tab.WorkspaceRoot, topicID: tab.TopicID}
		}
		removed = append(removed, removedRuntimeFromTab(tab, dir, sessionPath))
		a.markTabRemovedLocked(tab)
		delete(a.detachedSessions, key)
	}
	if a.activeTabID == "" && len(a.tabOrder) > 0 {
		a.activeTabID = a.tabOrder[0]
	}
	fallback.needs = len(removed) > 0 && len(a.tabs) == 0
	dir, entries, activeID, version := a.saveTabsCollectLocked()
	a.mu.Unlock()

	a.saveTabsWrite(dir, entries, activeID, version)

	return removed, fallback
}

func (a *App) sessionDeleteFallbackTarget(target fallbackRuntimeTarget) fallbackRuntimeTarget {
	topicID := strings.TrimSpace(target.topicID)
	if topicID == "" {
		return target
	}
	if path, _ := a.findTopicContentSessionForTarget(target.scope, target.workspaceRoot, topicID); path != "" {
		return target
	}
	target.topicID = ""
	return target
}

func removedRuntimeFromTab(tab *WorkspaceTab, dir, sessionPath string) removedSessionRuntime {
	return removedSessionRuntime{
		tab:           tab,
		ctrl:          tab.Ctrl,
		sink:          tab.sink,
		sessionDir:    dir,
		sessionPath:   sessionPath,
		scope:         tab.Scope,
		workspaceRoot: tab.WorkspaceRoot,
		topicID:       tab.TopicID,
		readOnly:      tab.ReadOnly,
	}
}

func tabMatchesSession(tab *WorkspaceTab, dir, sessionPath string) bool {
	if tab == nil {
		return false
	}
	currentPath, _, err := validateSessionPath(dir, tab.currentSessionPath())
	if err == nil && currentPath == sessionPath {
		return true
	}
	if tabRuntimeSessionDir(tab) != dir {
		return false
	}
	currentPath, _, err = validateSessionPath(dir, tab.currentSessionPath())
	return err == nil && currentPath == sessionPath
}

func (a *App) prepareRemovedSessionRuntimes(removed []removedSessionRuntime) error {
	for _, item := range removed {
		if item.sink != nil {
			item.sink.clearContext()
		}
		if item.ctrl == nil {
			continue
		}
		if item.ctrl.Running() {
			item.ctrl.Cancel()
			if err := waitControllerStopped(item.ctrl); err != nil {
				return err
			}
		}
		if item.readOnly {
			continue
		}
		if err := ctrlDurableSnapshot(item.ctrl); err != nil {
			if !errors.Is(err, agent.ErrSessionSnapshotConflict) {
				return err
			}
			slog.Warn("desktop: skipping stale runtime snapshot before removing session",
				"session", item.sessionPath, "err", err)
		}
		item.ctrl.SetSessionPath("")
		a.quiesceTabAutosave(item.tab)
	}
	return nil
}

func waitControllerStopped(ctrl control.SessionAPI) error {
	deadline := time.Now().Add(5 * time.Second)
	for ctrl.Running() {
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for cancelled session work to stop")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func (a *App) destroyHandlesForSession(dir, sessionPath string, removed []removedSessionRuntime) []control.SessionDestroyHandle {
	destroys := a.beginDestroySessionJobs(dir, sessionPath)
	for _, item := range removed {
		if item.ctrl == nil || item.sessionDir != dir || item.sessionPath != sessionPath {
			continue
		}
		destroys = append(destroys, item.ctrl.BeginDestroySession(sessionPath))
	}
	return destroys
}

func waitAllDestroyHandles(destroys []control.SessionDestroyHandle) {
	for _, destroy := range destroys {
		if destroy.WaitAll != nil {
			destroy.WaitAll()
		}
	}
}

func finishDestroyHandles(destroys []control.SessionDestroyHandle) {
	for _, destroy := range destroys {
		if destroy.Finish != nil {
			destroy.Finish()
		}
	}
}

func delayedDesktopSessionCleanup(path string, destroys []control.SessionDestroyHandle) {
	waitAllDestroyHandles(destroys)
	if err := removeDesktopSessionArtifacts(path); err != nil {
		slog.Warn("desktop: delayed session cleanup failed", "path", path, "err", err)
	}
	finishDestroyHandles(destroys)
}

func delayedDesktopSessionTrash(dir, sessionPath, key string, destroys []control.SessionDestroyHandle) {
	waitAllDestroyHandles(destroys)
	if err := trashSessionArtifacts(dir, sessionPath, key); err != nil {
		slog.Warn("desktop: delayed session trash failed", "path", sessionPath, "err", err)
	}
	finishDestroyHandles(destroys)
}

func (a *App) closeRemovedSessionRuntimes(removed []removedSessionRuntime) {
	defer a.lockRuntimeMutation("close-removed-session-runtimes")()
	a.closeRemainingRemovedSessionRuntimesAdmissionHeld(removed, map[control.SessionAPI]bool{})
}

func (a *App) closeRemovedSessionRuntimesForSessionAfterDestroyAdmissionHeld(removed []removedSessionRuntime, dir, sessionPath string, closed map[control.SessionAPI]bool) {
	releasedTabs := map[*WorkspaceTab]bool{}
	for _, item := range removed {
		if item.sessionDir != dir || item.sessionPath != sessionPath {
			continue
		}
		a.closeRemovedSessionRuntime(item, closed, releasedTabs, true)
	}
}

func (a *App) closeRemainingRemovedSessionRuntimesAdmissionHeld(removed []removedSessionRuntime, closed map[control.SessionAPI]bool) {
	releasedTabs := map[*WorkspaceTab]bool{}
	for _, item := range removed {
		a.closeRemovedSessionRuntime(item, closed, releasedTabs, false)
	}
}

func (a *App) closeRemainingRemovedSessionRuntimesAfterDestroyAdmissionHeld(removed []removedSessionRuntime, closed map[control.SessionAPI]bool) {
	releasedTabs := map[*WorkspaceTab]bool{}
	for _, item := range removed {
		a.closeRemovedSessionRuntime(item, closed, releasedTabs, true)
	}
}

func (a *App) closeRemovedSessionRuntime(item removedSessionRuntime, closed map[control.SessionAPI]bool, releasedTabs map[*WorkspaceTab]bool, afterDestroy bool) {
	// Task 715 (714 复核 B4): every removal flow closes through this funnel —
	// drop the replayed-graph cache entry here so a deleted or archived session
	// stops pinning its DAG state (the entry is a pure accelerator).
	if freed, ok := agent.InvalidateSessionGraph(item.sessionPath); ok {
		slog.Info("desktop: invalidated dag graph cache on removed session runtime",
			"path", item.sessionPath, "freed_bytes", freed)
	}
	if item.tab != nil {
		if releasedTabs == nil || !releasedTabs[item.tab] {
			if releasedTabs != nil {
				releasedTabs[item.tab] = true
			}
			a.releaseTabSharedHost(item.tab)
			item.tab.releaseSessionLease()
		}
	}
	if item.ctrl == nil {
		return
	}
	if closed == nil {
		closed = map[control.SessionAPI]bool{}
	}
	if closed[item.ctrl] {
		return
	}
	closed[item.ctrl] = true
	if afterDestroy {
		item.ctrl.CloseAfterDestroy()
		return
	}
	item.ctrl.Close()
}

func (a *App) openFallbackRuntime(target fallbackRuntimeTarget) error {
	scope := target.scope
	root := target.workspaceRoot
	topicID := strings.TrimSpace(target.topicID)
	if scope == "global" {
		root = ""
	}
	if topicID == "" {
		return a.openTransientBlankRuntime(scope, root)
	}
	var err error
	if a.singleSurfaceLayoutEnabled() {
		_, err = a.ActivateTopic(scope, root, topicID, "")
	} else if scope == "global" {
		_, err = a.OpenGlobalTab(topicID)
	} else {
		_, err = a.OpenProjectTab(root, topicID)
	}
	return err
}

func (a *App) openTransientBlankRuntime(scope, workspaceRoot string) error {
	scope = strings.TrimSpace(scope)
	if scope != "project" {
		scope = "global"
	}
	// Task 186: a transient runtime on one of the host's own directories is a
	// global-scope runtime — same rule as the visible tab opens.
	scope, workspaceRoot = normalizeWorkspaceScope(scope, workspaceRoot)
	actualRoot := ""
	if scope == "project" {
		workspaceRoot = normalizeProjectRoot(workspaceRoot)
		if workspaceRoot == "" {
			return fmt.Errorf("workspaceRoot is required")
		}
		actualRoot = workspaceRoot
	} else {
		actualRoot = globalWorkspaceRoot()
		if err := os.MkdirAll(actualRoot, 0o755); err != nil {
			return fmt.Errorf("create global workspace: %w", err)
		}
	}
	releaseAdmission, err := a.beginProjectRuntimeAdmission(scope, actualRoot)
	if err != nil {
		return err
	}
	defer releaseAdmission()
	if scope == "project" {
		saveWorkspace(workspaceRoot)
		a.registerProjectRoot(workspaceRoot)
	}

	model, toolApprovalMode, subagentPolicy := desktopNewSessionDefaults(scope, actualRoot)
	sessionPath, err := createEmptySessionFile(desktopSessionDir(actualRoot), model)
	if err != nil {
		return err
	}
	if err := pinNewEmptySessionBranchMeta(sessionPath, scope, actualRoot, "", defaultTopicTitle); err != nil {
		return err
	}
	tab := &WorkspaceTab{
		Scope:            scope,
		WorkspaceRoot:    actualRoot,
		TopicTitle:       defaultTopicTitle,
		topicTitleSource: topicTitleSourceAuto,
		SessionPath:      sessionPath,
		model:            model,
		qualityFloor:     "",
		mode:             tabModeFromAxes(false, toolApprovalMode == control.ToolApprovalYolo),
		toolApprovalMode: toolApprovalMode,
		subagentPolicy:   subagentPolicy,
		disabledMCP:      map[string]ServerView{},
	}
	a.mu.Lock()
	tab.ID = a.newUniqueTabIDLocked()
	tab.sink = &tabEventSink{tabID: tab.ID, app: a}
	a.tabs[tab.ID] = tab
	a.tabOrder = append(a.tabOrder, tab.ID)
	a.activeTabID = tab.ID
	a.saveTabsLocked()
	a.mu.Unlock()

	a.startTabControllerBuild(tab)
	return nil
}

func (a *App) beginDestroySessionJobs(dir, sessionPath string) []control.SessionDestroyHandle {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var destroys []control.SessionDestroyHandle
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || tab.Ctrl == nil || tabRuntimeSessionDir(tab) != dir {
			continue
		}
		destroys = append(destroys, tab.Ctrl.BeginDestroySession(sessionPath))
	}
	return destroys
}

func (a *App) openSessionPaths(dir string) map[string]struct{} {
	a.mu.RLock()
	paths := make([]string, 0, len(a.tabs)+len(a.detachedSessions))
	for _, tab := range a.runtimeTabsLocked() {
		if tab != nil {
			paths = append(paths, tab.currentSessionPath())
		}
	}
	a.mu.RUnlock()

	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		currentPath, _, err := validateSessionPath(dir, path)
		if err == nil {
			out[currentPath] = struct{}{}
		}
	}
	return out
}

func (a *App) activeSessionPath(dir string) string {
	a.mu.RLock()
	var path string
	if tab := a.tabs[a.activeTabID]; tab != nil {
		path = tab.currentSessionPath()
	}
	a.mu.RUnlock()
	currentPath, _, err := validateSessionPath(dir, path)
	if err != nil {
		return ""
	}
	return currentPath
}
