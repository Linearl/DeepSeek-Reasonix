package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/plugin"
)

func (a *App) rebindTabToSessionPath(tab *WorkspaceTab, sessionPath string) error {
	sessionPath = canonicalTabSessionPath(sessionPath)
	if sessionPath == "" {
		return fmt.Errorf("session path is required")
	}
	loaded, err := loadResumableSession(sessionPath)
	if err != nil {
		return err
	}
	return a.rebindTabToLoadedSessionPath(tab, sessionPath, loaded)
}

func (a *App) rebindTabToLoadedSessionPath(tab *WorkspaceTab, sessionPath string, loaded *agent.Session) error {
	if tab == nil {
		return fmt.Errorf("tab is not ready")
	}
	pendingSequence := a.deferredRebuildSequence(tab.ID)
	sessionPath = canonicalTabSessionPath(sessionPath)
	if sessionPath == "" {
		return fmt.Errorf("session path is required")
	}
	if agent.IsCleanupPending(sessionPath) {
		return fmt.Errorf("session is pending cleanup")
	}
	if loaded == nil {
		var err error
		loaded, err = loadResumableSession(sessionPath)
		if err != nil {
			return err
		}
	}
	// Session rebinding is a candidate transaction: the source controller, lease,
	// runtime key, epoch, and profile stay live until the target controller has
	// built, restored, validated, and acquired its own lease.
	a.runtimeRebuildMu.Lock()
	defer a.runtimeRebuildMu.Unlock()

	// Fence an in-flight startup before waiting for the admission barrier: it
	// discards itself at its short publication check once superseded. App.mu is
	// released before barrier acquisition, so no inverted lock nesting appears.
	a.mu.Lock()
	if tab.removed || a.tabs[tab.ID] != tab {
		a.mu.Unlock()
		return fmt.Errorf("tab is not ready")
	}
	currentPath := ""
	if tab.Ctrl != nil {
		currentPath = strings.TrimSpace(tab.Ctrl.SessionPath())
	}
	if currentPath == "" {
		currentPath = strings.TrimSpace(tab.SessionPath)
	}
	if sessionRuntimeKey(currentPath) == sessionRuntimeKey(sessionPath) {
		// Same session: leave any in-flight build alone — resuming the
		// session a build is already binding must stay a no-op.
		a.mu.Unlock()
		return nil
	}
	a.supersedeTabBuildLocked(tab)
	source := snapshotTabRuntimeLocked(tab)
	a.mu.Unlock()

	// If the target session has a detached runtime, reattach it instead of
	// building a new controller: on Windows a second same-process handle cannot
	// lock a file already held by the detached controller's fd (#6955).
	targetKey := sessionRuntimeKey(sessionPath)
	a.mu.Lock()
	detached := a.detachedSessions[targetKey]
	hasDetached := detached != nil && detached.Ctrl != nil
	a.mu.Unlock()

	if hasDetached {
		return a.rebindViaDetachedReattach(tab, sessionPath, source, pendingSequence)
	}
	return a.rebindViaFreshBuild(tab, sessionPath, loaded, pendingSequence, source)
}

// rebindViaDetachedReattach replaces the tab's runtime with the target's
// already-running detached runtime under the admission barrier. Callers hold
// runtimeRebuildMu; the source snapshot must be current.
func (a *App) rebindViaDetachedReattach(tab *WorkspaceTab, sessionPath string, source tabRuntimeSnapshot, pendingSequence uint64) error {
	a.runtimeAdmissionMu.Lock()
	tab.turnStartMu.Lock()

	a.mu.Lock()
	if tab.removed || a.tabs[tab.ID] != tab || tab.Ctrl != source.ctrl {
		a.mu.Unlock()
		tab.turnStartMu.Unlock()
		a.runtimeAdmissionMu.Unlock()
		return fmt.Errorf("tab changed while reattaching session; retry")
	}
	a.mu.Unlock()

	if source.ctrl != nil {
		if err := a.snapshotTabForAction(tab, "switching sessions"); err != nil {
			tab.turnStartMu.Unlock()
			a.runtimeAdmissionMu.Unlock()
			return err
		}
		if oldPath := a.reconciledSessionPathForTab(tab); oldPath != "" {
			if err := a.saveTabSessionMeta(tab, oldPath); err != nil {
				tab.turnStartMu.Unlock()
				a.runtimeAdmissionMu.Unlock()
				return fmt.Errorf("save current session metadata before switching sessions: %w", err)
			}
		}
	}

	a.mu.Lock()
	if tab.removed || a.tabs[tab.ID] != tab || tab.Ctrl != source.ctrl {
		a.mu.Unlock()
		tab.turnStartMu.Unlock()
		a.runtimeAdmissionMu.Unlock()
		return fmt.Errorf("tab changed while reattaching session; retry")
	}
	a.mu.Unlock()

	detachSource := controllerHasActiveRuntimeWork(source.ctrl)
	oldCtrl, oldSink, oldLease, oldHostKey, attached := a.reattachDetachedSessionRuntimeForRebind(
		tab, source, sessionPath, detachSource,
	)
	if !attached {
		tab.turnStartMu.Unlock()
		a.runtimeAdmissionMu.Unlock()
		return fmt.Errorf("failed to reattach detached session runtime")
	}

	if oldSink != nil {
		oldSink.setBinding("", nil)
		oldSink.clearContext()
	}
	if oldCtrl != nil {
		oldCtrl.Close()
	}
	if oldHostKey != "" {
		a.releaseSharedHost(oldHostKey)
	}
	if oldLease != nil {
		oldLease.Release()
	}

	a.clearDeferredRebuildVersion(tab.ID, pendingSequence)
	a.emitReady(a.ctx, tab.ID)

	tab.turnStartMu.Unlock()
	a.runtimeAdmissionMu.Unlock()
	return nil
}

// rebindViaFreshBuild builds a fresh candidate controller for the target
// session and commits it transactionally. Callers hold runtimeRebuildMu.
func (a *App) rebindViaFreshBuild(tab *WorkspaceTab, sessionPath string, loaded *agent.Session, pendingSequence uint64, source tabRuntimeSnapshot) error {
	a.runtimeAdmissionMu.Lock()
	defer a.runtimeAdmissionMu.Unlock()
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	a.mu.Lock()
	if tab.removed || a.tabs[tab.ID] != tab || tab.Ctrl != source.ctrl {
		a.mu.Unlock()
		return fmt.Errorf("tab changed while preparing to switch sessions; retry")
	}
	source = snapshotTabRuntimeLocked(tab)
	a.mu.Unlock()

	if source.ctrl != nil {
		if err := a.snapshotTabForAction(tab, "switching sessions"); err != nil {
			return err
		}
		if oldPath := a.reconciledSessionPathForTab(tab); oldPath != "" {
			if err := a.saveTabSessionMeta(tab, oldPath); err != nil {
				return fmt.Errorf("save current session metadata before switching sessions: %w", err)
			}
		}
	}

	// Snapshot recovery may have retargeted the source controller and runtime.
	// Refresh the identity before reserving the target alias.
	a.mu.Lock()
	if tab.removed || a.tabs[tab.ID] != tab || tab.Ctrl != source.ctrl {
		a.mu.Unlock()
		return fmt.Errorf("tab changed while preparing to switch sessions; retry")
	}
	source = snapshotTabRuntimeLocked(tab)
	a.mu.Unlock()

	transition, err := a.reserveSessionRuntimePath(tab, sessionPath)
	if err != nil {
		return userFacingSessionLeaseError("", err)
	}
	committed := false
	defer func() {
		if !committed {
			a.rollbackSessionRuntimePath(transition)
		}
	}()

	profile := loadTabSessionProfile(sessionPath)
	detachSource := controllerHasActiveRuntimeWork(source.ctrl)
	candidateNeedsHostRef := detachSource || source.ctrl == nil
	candidate, err := a.buildSessionRebindCandidate(tab, source, sessionPath, loaded, profile, candidateNeedsHostRef)
	if err != nil {
		return fmt.Errorf("resume session: %w", err)
	}
	defer func() {
		if !committed {
			candidate.close()
		}
	}()

	targetLease, err := a.acquireCandidateSessionLease(tab, sessionPath)
	if err != nil {
		return err
	}
	defer func() {
		if !committed {
			targetLease.Release()
		}
	}()
	if err := a.runRebindCandidateHook("lease_acquired"); err != nil {
		return fmt.Errorf("resume session: %w", err)
	}

	if err := a.commitSessionRebind(tab, source, transition, candidate, targetLease, sessionPath, detachSource, pendingSequence, &committed); err != nil {
		return err
	}
	committed = true
	return nil
}

// commitSessionRebind revalidates the source runtime generation, atomically
// publishes the target controller/lease/profile/path under App.mu, and runs
// the post-commit teardown outside the lock. *committed flips to true at the
// original set point so the caller's rollback defers keep their semantics.
func (a *App) commitSessionRebind(tab *WorkspaceTab, source tabRuntimeSnapshot, transition sessionRuntimePathTransition, candidate *sessionRebindCandidate, targetLease *agent.SessionLease, sessionPath string, detachSource bool, pendingSequence uint64, committed *bool) error {
	// All fallible candidate work is complete. Revalidate the source runtime
	// generation, atomically publish the target controller/lease/profile/path,
	// and advance the epoch in the same App.mu commit.
	a.mu.Lock()
	if err := a.validateAndBindSessionRebindLocked(tab, source, transition, candidate, targetLease); err != nil {
		a.mu.Unlock()
		return err
	}
	var oldLease *agent.SessionLease
	oldCtrl := tab.Ctrl
	oldSink := tab.sink
	if detachSource {
		if !a.detachRuntimeForReplacementLocked(tab) {
			a.mu.Unlock()
			return fmt.Errorf("current session runtime cannot be detached")
		}
		if a.runtimeBySessionKey[transition.targetKey] == transition.runtime {
			delete(a.runtimeBySessionKey, transition.targetKey)
		}
	} else {
		if !a.commitSessionRuntimePathLocked(transition) {
			a.mu.Unlock()
			return fmt.Errorf("tab runtime changed while switching sessions; retry")
		}
		oldLease = tab.takeSessionLease()
	}
	tab.adoptSessionLease(targetLease)
	targetLease = nil
	tab.Ctrl = candidate.ctrl
	tab.sink = candidate.sink
	tab.SessionPath = sessionPath
	tab.model = candidate.model
	tab.Label = candidate.ctrl.Label()
	applyNormalizedRuntimeToTabLocked(tab, candidate.runtime)
	tab.Ready = true
	clearTabStartupError(tab)
	tab.ActivityStatus = ""
	tab.replaceTelemetry(candidate.telemetry, sessionRuntimeKey(sessionPath))
	if tab.sink != nil {
		tab.sink.setBinding(tab.ID, a, tab.SessionGeneration)
		tab.sink.setContext(a.ctx)
	}
	// Mirror wiring and async adoption inspect App state, so run them after
	// this transaction releases App.mu: publishing the committed identity
	// first lets the stale-result fence observe one coherent generation.
	shouldAdopt := !tab.ReadOnly
	if detachSource {
		a.newSessionRuntimeLocked(tab, transition.targetKey)
	}
	newEpoch := a.advanceSessionRuntimeEpochLocked(tab)
	a.saveTabsLocked()
	candidate.ctrl = nil
	candidate.sink = nil
	*committed = true
	a.mu.Unlock()
	a.attachTakeoverMirror(tab.ID, sessionPath)
	if shouldAdopt {
		go a.adoptSessionFromLocalServe(tab.ID, sessionPath)
	}
	// Test-only observation point: the replacement is committed but the retired
	// sink still carries its old epoch. Production has no hook and immediately
	// fences that sink below.
	_ = a.runRebindCandidateHook("committed")

	// Teardown happens after publication and outside App.mu, so every target
	// failure above leaves source ownership intact. Fence the retired sink
	// before closing the old controller so a close-time event cannot interfere.
	if !detachSource {
		if oldSink != nil {
			oldSink.setBinding("", nil)
			oldSink.clearContext()
		}
		if oldCtrl != nil {
			oldCtrl.Close()
		}
		if oldLease != nil {
			oldLease.Release()
		}
	}
	a.persistTabSessionPath(tab, sessionPath)
	a.clearDeferredRebuildVersion(tab.ID, pendingSequence)
	a.notifyTabRuntimeRebuiltAtEpoch(tab, newEpoch)
	a.emitReady(a.ctx, tab.ID)
	return nil
}

// reattachDetachedSessionRuntimeForRebind atomically replaces tab with the
// already-running detached target. If the visible source is still active, its
// controller, sink, lease, and runtime registry entry move to detachedSessions
// in the same App.mu transaction; an idle source is returned for off-lock
// teardown. The caller must hold runtimeRebuildMu, runtimeAdmissionMu, and
// tab.turnStartMu so detachSource cannot become stale through new turn admission.
func (a *App) reattachDetachedSessionRuntimeForRebind(
	tab *WorkspaceTab,
	source tabRuntimeSnapshot,
	sessionPath string,
	detachSource bool,
) (control.SessionAPI, *tabEventSink, *agent.SessionLease, string, bool) {
	key := sessionRuntimeKey(sessionPath)
	if tab == nil || key == "" {
		return nil, nil, nil, "", false
	}

	a.mu.Lock()
	if tab.removed || a.tabs[tab.ID] != tab || tab.Ctrl != source.ctrl {
		a.mu.Unlock()
		return nil, nil, nil, "", false
	}
	detached := a.detachedSessions[key]
	if detached == nil || detached.Ctrl == nil {
		a.mu.Unlock()
		return nil, nil, nil, "", false
	}
	if rt := a.runtimeForTabLocked(detached); rt != nil {
		if rt.Phase != sessionRuntimeReady {
			a.mu.Unlock()
			return nil, nil, nil, "", false
		}
	} else if !detached.Ready {
		// Compatibility for detached runtimes created before the process-local
		// registry existed.
		a.mu.Unlock()
		return nil, nil, nil, "", false
	}

	oldCtrl := tab.Ctrl
	oldSink := tab.sink
	var oldLease *agent.SessionLease
	oldHostKey := ""
	if detachSource {
		if !a.detachRuntimeForReplacementLocked(tab) {
			a.mu.Unlock()
			return nil, nil, nil, "", false
		}
		// Ownership moved to the detached clone. Nothing from the source may be
		// closed or released after the target becomes visible.
		oldCtrl = nil
		oldSink = nil
	} else {
		// Prevent applyRuntimeTab from overwriting resources owned by the idle
		// source. Teardown remains outside the app lock as on the normal rebuild
		// path; the detached target already owns a separate shared-host ref.
		oldLease = tab.takeSessionLease()
		oldHostKey = takeTabSharedHostKey(tab)
	}

	delete(a.detachedSessions, key)
	applyRuntimeTab(tab, detached, sessionPath, a.ctx, a)
	a.saveTabsLocked()
	attachedCtrl := tab.Ctrl
	attachedSink := tab.sink
	attachedEpoch := a.runtimeEpochForTabLocked(tab)
	a.mu.Unlock()

	a.replayPendingPromptsAfterRuntimeAttach(tab.ID, attachedSink, attachedCtrl, attachedEpoch)
	return oldCtrl, oldSink, oldLease, oldHostKey, true
}

type sessionRebindCandidate struct {
	app               *App
	ctrl              control.SessionAPI
	sink              *tabEventSink
	model             string
	runtime           normalizedTabRuntime
	telemetry         tabTelemetrySnapshot
	sharedHostKey     string
	ownsSharedHostRef bool
}

func (c *sessionRebindCandidate) close() {
	if c == nil {
		return
	}
	if c.sink != nil {
		c.sink.clearContext()
	}
	if c.ctrl != nil {
		c.ctrl.Close()
		c.ctrl = nil
	}
	if c.ownsSharedHostRef && c.app != nil && c.sharedHostKey != "" {
		c.app.releaseSharedHost(c.sharedHostKey)
		c.ownsSharedHostRef = false
	}
}

func normalizedRuntimeForSessionProfile(profile tabSessionProfile) normalizedTabRuntime {
	temp := &WorkspaceTab{}
	applyTabSessionProfile(temp, profile)
	return snapshotTabRuntimeLocked(temp).normalizedRuntime()
}

func (a *App) runRebindCandidateHook(stage string) error {
	if a == nil || a.rebindCandidateHook == nil {
		return nil
	}
	return a.rebindCandidateHook(stage)
}

func (a *App) buildSessionRebindCandidate(
	tab *WorkspaceTab,
	source tabRuntimeSnapshot,
	sessionPath string,
	loaded *agent.Session,
	profile tabSessionProfile,
	separateRuntime bool,
) (*sessionRebindCandidate, error) {
	root := strings.TrimSpace(source.workspaceRoot)
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		}
	}
	_ = config.MigrateLegacyCredentialsForRoot(root)
	cfg, err := config.LoadForRoot(root)
	if err != nil {
		return nil, err
	}

	model := strings.TrimSpace(source.model)
	if sessionModel, ok := agent.LoadSessionModel(sessionPath); ok {
		config.NormalizeLegacyMimoCustomProvidersForRefs(cfg, sessionModel)
		if _, ok := cfg.ResolveModel(sessionModel); ok {
			model = sessionModel
		}
	}
	if model == "" {
		model = cfg.DefaultModel
	}
	config.NormalizeLegacyMimoCustomProvidersForRefs(cfg, model)
	if resolved, _, ok := cfg.ResolveModelWithFallback(model); ok {
		model = resolved
	}

	sessionDir := controllerSessionDir(source.ctrl)
	if strings.TrimSpace(sessionDir) == "" {
		sessionDir = filepath.Dir(sessionPath)
	}
	sink := &tabEventSink{tabID: tab.ID, app: a}
	runtimeProfile := normalizedRuntimeForSessionProfile(profile)
	sharedHost := a.lookupSharedHost(source.sharedHostKey)
	ownsSharedHostRef := false
	if separateRuntime && source.sharedHostKey != "" {
		sharedHost = a.acquireSharedHost(source.sharedHostKey)
		ownsSharedHostRef = true
	}
	if _, err := loadPinnedContextState(sessionPath); err != nil {
		return nil, err
	}
	runtimeBuildStart := time.Now()
	slog.Info("desktop: runtime build begin", "trigger", "rebind", "model", model, "session", sessionPath, "autopilot", source.autopilot) // task 196: name who is building a runtime, so a startup burst can be attributed instead of inferred.
	defer logRuntimeBuildEnd("rebind", runtimeBuildStart, sharedHost != nil)                                                              // task 334: begin→end pair.
	// Task 363A: feed the pooled assembly (same root+model+effort) so discovery
	// is skipped on the second same-key tab; gate-off keeps the legacy path
	// byte-identical (acquire returns nil, store is a no-op).
	effortKey := ""
	if source.effort != nil {
		effortKey = *source.effort
	}
	assemblyKey := runtimeAssemblyKeyForTab(root, model, effortKey)
	ctrl, assembly, err := boot.BuildWithAssembly(a.bootContext(), a.rebindCandidateBuildOptions(tab, source, model, root, sessionDir, sink, cfg, sharedHost, runtimeProfile, assemblyKey))
	if err != nil {
		sink.clearContext()
		if ownsSharedHostRef {
			a.releaseSharedHost(source.sharedHostKey)
		}
		return nil, err
	}
	a.storeRuntimeAssembly(assemblyKey, assembly)
	candidate := &sessionRebindCandidate{
		app: a, ctrl: ctrl, sink: sink, model: model, runtime: runtimeProfile,
		sharedHostKey: source.sharedHostKey, ownsSharedHostRef: ownsSharedHostRef,
	}
	a.bindControllerDisplayRecorder(ctrl)
	// Propagate the tab's session-level subagent tier to the freshly-built
	// controller (it persists to BranchMeta on the first turn); skip a blank
	// tab.subagentPolicy (normalize would collapse it to a wrong "light").
	if strings.TrimSpace(tab.subagentPolicy) != "" {
		if _, err := agent.NormalizeSubagentPolicy(tab.subagentPolicy); err == nil {
			_ = ctrl.SetSubagentPolicy(tab.subagentPolicy)
		}
	}
	configureControllerRuntime(ctrl, nil, runtimeProfile)
	if err := a.runRebindCandidateHook("built"); err != nil {
		candidate.close()
		return nil, err
	}
	restoredRuntime, err := resumeControllerRuntimeWithSession(ctrl, loaded, sessionPath, runtimeProfile)
	if err != nil {
		candidate.close()
		return nil, err
	}
	candidate.runtime = restoredRuntime
	candidate.telemetry = loadTelemetry(sessionPath + ".telemetry.json")
	if err := a.runRebindCandidateHook("restored"); err != nil {
		candidate.close()
		return nil, err
	}
	return candidate, nil
}

func (a *App) acquireCandidateSessionLease(tab *WorkspaceTab, path string) (*agent.SessionLease, error) {
	lease, err := withSessionLeaseContentionRetry(func() (*agent.SessionLease, error) {
		lease, err := agent.TryAcquireSessionLease(path)
		if err == nil {
			return lease, nil
		}
		if a.canReclaimCurrentProcessSessionLease(tab, path, err) {
			if reclaimed, reclaimErr := agent.TryReclaimCurrentProcessSessionLease(path); reclaimErr == nil {
				return reclaimed, nil
			} else {
				err = reclaimErr
			}
		}
		return nil, err
	})
	if err == nil {
		// Watch for remote takeover requests on every leased tab (the main
		// lease path — remote clients yield the desktop via this watcher).
		tab.startTakeoverRequestWatcher(sessionRuntimeKey(path))
	}
	if err != nil {
		return nil, userFacingSessionLeaseError("", err)
	}
	return lease, nil
}

func loadResumableSession(sessionPath string) (*agent.Session, error) {
	if agent.IsCleanupPending(sessionPath) {
		return nil, fmt.Errorf("session is pending cleanup")
	}
	// First paint: a very large log opens from its trailing window and the
	// reader pages the rest in (task 187); every other LoadSession caller
	// (recovery, GC, migration, export) still gets the complete transcript.
	return agent.LoadSessionTail(sessionPath)
}

// rebindCandidateBuildOptions assembles the boot options for a rebind
// candidate controller: the shared desktop surface plus the runtime-assembly
// reuse pair (embedded struct named explicitly; promoted names cannot appear
// in an outer composite literal).
func (a *App) rebindCandidateBuildOptions(tab *WorkspaceTab, source tabRuntimeSnapshot, model, root, sessionDir string, sink *tabEventSink, cfg *config.Config, sharedHost *plugin.Host, runtimeProfile normalizedTabRuntime, assemblyKey string) boot.Options {
	return boot.Options{
		RestartUpdater:             restartUpdaterAdapter{a},
		AutonomousUpdateController: newAutonomousUpdateController(a),
		WorktreeProjectOpener:      appWorktreeProjectOpener{app: a}, // 任务128：会话自主建项目——注册为项目并开后台 tab
		Model:                      model,
		Autopilot:                  source.autopilot,
		MaxRuntime:                 source.autopilotMaxRuntime,
		AutopilotApprovalGrace:     source.autopilotApprovalGrace,
		AutopilotAskTimeoutEnabled: source.autopilotAskTimeoutEnabled,
		AutopilotAskWait:           source.autopilotAskWait,
		AutopilotAskAutoContinue:   source.autopilotAskAutoContinue,
		RequireKey:                 false,
		StatsSource:                "desktop",
		TaskStore:                  a.taskStore(),
		OnConfigLoadWarnings:       a.configLoadWarningsHandler(),
		Sink:                       a.desktopControllerSink(sink, cfg.Notifications),
		WorkspaceRoot:              root,
		SessionDir:                 sessionDir,
		EffortOverride:             cloneStringPtr(source.effort),
		SharedHost:                 sharedHost,
		MCPHostProfile:             plugin.HostProfileDesktopApps,
		CleanupPendingReconciler:   reconcileDesktopCleanupPending,
		SubagentParentLive:         a.subagentParentProbeForBuild(tab),
		SessionRecoveryMeta:        a.tabSessionRecoveryMeta(tab),
		PinnedContextLoader:        pinnedContextLoader(root),
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
		// ReuseAssembly lives on the embedded RuntimeReload; promoted names
		// cannot appear in the outer literal, so name the embedded struct.
		RuntimeReload: boot.RuntimeReload{
			ReuseAssembly: a.acquireRuntimeAssembly(assemblyKey),
			PreviousPlan:  boot.FullReusePlan(),
		},
	}
}
