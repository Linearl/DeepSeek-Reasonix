package main

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/plugin"
	"reasonix/internal/provider"
)

func (a *App) Effort() EffortInfo {
	return a.EffortForTab("")
}

// effortForTabDirect is the unbounded effort read: provider entry resolution
// (per-root config snapshot plus session-binding reconcile per call; a full
// disk load before task 609) followed by capability mapping. Task 421 bounds it behind EffortForTab in effort_fetch.go; binding surfaces must go through EffortForTab so a stalled read cannot hang a tab switch.
func (a *App) effortForTabDirect(tabID string) EffortInfo {
	entry, err := a.currentProviderEntryForTab(tabID)
	if err != nil {
		return EffortInfo{Current: "auto", Levels: []string{}}
	}
	cap := config.EffortCapabilityForEntry(entry)
	if !cap.Supported {
		return EffortInfo{Supported: false, Current: "auto", Default: cap.Default, Levels: []string{}}
	}
	levels := cap.Levels
	if levels == nil {
		levels = []string{}
	}
	return EffortInfo{Supported: true, Current: config.EffortDisplay(entry), Default: cap.Default, Levels: levels, Options: config.ReasoningCapabilityForEntry(entry).Options, AliasFold: cap.AliasFold}
}

func (a *App) SetEffort(level string) error {
	return a.SetEffortForTab("", level)
}

func (a *App) SetEffortForTab(tabID, level string) error {
	tab := a.tabByID(tabID)
	if tab == nil {
		if strings.TrimSpace(tabID) == "" {
			entry, err := a.currentProviderEntryForTab("")
			if err != nil {
				return err
			}
			effort, err := config.NormalizeEffort(entry, level)
			if err != nil {
				return err
			}
			return a.applyProviderEffortConfig(entry, effort)
		}
		return fmt.Errorf("tab %q not found", tabID)
	}
	// Build+swap path; serialize with the other rebuild paths (see
	// runtimeRebuildMu). The tab==nil branch above goes through
	// applyProviderEffortConfig → rebuildSetting, which takes the lock itself. switchStarted feeds the fast-path elapsed_ms lines (task 148) so an effort-vs-model fast-path comparison reads from logs directly; the fallback path keeps its own runtime build end line.
	switchStarted := time.Now()
	pendingSequence := a.deferredRebuildSequence(tab.ID)
	a.runtimeRebuildMu.Lock()
	defer a.runtimeRebuildMu.Unlock()
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	handled, err := a.tryEffortFastPaths(tab, tabID, level, switchStarted)
	if err != nil {
		return err
	}
	if handled {
		return nil
	}
	prevPath := a.reconciledSessionPathForTab(tab)
	if prevPath == "" {
		prevPath = a.currentSessionPathFor(tab)
	}
	// Recomputing prevPath after this attach would be a dead store: it is
	// unconditionally derived again after ensureTabControllerWorkspace below.
	if a.controllerForTab(tab) == nil && prevPath != "" {
		a.attachExistingSessionRuntime(tab, prevPath, a.ctx)
	}
	if err := rebuildControllerActiveWorkErrorFor(a.controllerForTab(tab), "effort"); err != nil {
		return err
	}
	if err := a.ensureTabControllerWorkspace(tab); err != nil {
		return err
	}
	prevPath = a.reconciledSessionPathForTab(tab)
	if prevPath == "" {
		prevPath = a.currentSessionPathFor(tab)
	}
	if a.controllerForTab(tab) == nil && prevPath != "" && a.attachExistingSessionRuntime(tab, prevPath, a.ctx) {
		prevPath = a.reconciledSessionPathForTab(tab)
		if prevPath == "" {
			prevPath = a.currentSessionPathFor(tab)
		}
		if err := rebuildControllerActiveWorkErrorFor(a.controllerForTab(tab), "effort"); err != nil {
			return err
		}
	}
	snap := a.tabRuntimeSnapshot(tab)
	runtime := snap.normalizedRuntime()
	entry, err := a.currentProviderEntryForTab(tabID)
	if err != nil {
		return err
	}
	modelRef := entry.Name + "/" + entry.Model
	effort, err := config.NormalizeEffort(entry, level)
	if err != nil {
		return err
	}
	var carried []provider.Message
	oldCtrl := a.controllerForTab(tab)
	if oldCtrl != nil {
		if prevPath == "" {
			prevPath = oldCtrl.SessionPath()
		}
		if err := a.ensureTabSessionLeaseForRebuild(tab, prevPath, "effort"); err != nil {
			return err
		}
		if err := a.snapshotTabForAction(tab, "changing effort"); err != nil {
			return err
		}
		prevPath = sessionPathAfterSnapshot(oldCtrl, prevPath)
		carried = oldCtrl.History()
	}
	sharedHost := a.lookupSharedHost(snap.sharedHostKey)
	runtimeBuildStart := time.Now()
	slog.Info("desktop: runtime build begin", "tab", tabID, "trigger", "set-effort", "model", modelRef, "effort", level, "autopilot", tab.autopilot) // task 196: name who is building a runtime, so a startup burst can be attributed instead of inferred.
	defer logRuntimeBuildEnd("set-effort", runtimeBuildStart, sharedHost != nil, "path", "fallback")                                                 // task 334: reaching the build = both fast paths declined; reason rides the agent: effort override declined line.
	newCtrl, err := a.buildEffortController(tab, tabID, modelRef, effort, snap, runtime, oldCtrl, sharedHost)
	if err != nil {
		return err
	}
	return a.swapEffortController(tab, newCtrl, oldCtrl, modelRef, effort, prevPath, runtime, pendingSequence, carried)
}

// tryEffortFastPaths attempts the same-level short circuit and the
// per-request effort override; it reports whether the switch was handled
// without a rebuild. Callers hold runtimeRebuildMu and tab.turnStartMu.
func (a *App) tryEffortFastPaths(tab *WorkspaceTab, tabID, level string, switchStarted time.Time) (bool, error) {
	// Same-level short circuit, before any attach/workspace side effects:
	// switching to the depth this tab already runs must not pay for a full
	// runtime rebuild (nor even re-enter the attach/workspace paths). tab.effort holds the depth the tab currently runs — seeded from the provider entry at attach time and rewritten by every effort/model switch.
	if tab.effort != nil {
		if entry, err := a.currentProviderEntryForTab(tabID); err == nil {
			if effort, err := config.NormalizeEffort(entry, level); err == nil &&
				strings.EqualFold(strings.TrimSpace(*tab.effort), effort) {
				slog.Info("desktop: effort switch", "tab", tabID, "path", "fast-same-level", "level", effort, "elapsed_ms", time.Since(switchStarted).Milliseconds()) // task 334: fast path observability (paired against runtime build end path=fallback); elapsed_ms: task 148.
				return true, nil
			}
		}
	}
	// Per-request fast path: providers whose effort vocabulary is request-
	// scoped (provider.Request.EffortOverride) take the new depth on the next
	// call, so switching costs no rebuild at all. Providers that cannot vary depth per request return false here and fall through to the build+swap path below, which keeps re-anchoring semantics (recovery branches, snapshot) identical to the model switch.
	if ctrl := a.controllerForTab(tab); ctrl != nil {
		if entry, err := a.currentProviderEntryForTab(tabID); err == nil {
			effort, nerr := config.NormalizeEffort(entry, level)
			if nerr != nil && !config.IsEffortNotConfigurable(nerr) {
				// Task 354: an unsupported level is a usage error — fail fast
				// with no build instead of paying the full rebuild for a level
				// the provider never offered. The capability-class (not-configurable) error keeps falling through to rebuild.
				return false, nerr
			}
			if nerr == nil {
				if setter, ok := ctrl.(interface {
					SetSessionEffortOverride(string) bool
				}); ok && setter.SetSessionEffortOverride(effort) {
					a.mu.Lock()
					tab.effort = &effort
					a.mu.Unlock()
					slog.Info("desktop: effort switch", "tab", tabID, "path", "fast-per-request", "level", effort, "elapsed_ms", time.Since(switchStarted).Milliseconds()) // task 334: fast path observability; declines log on the agent side with reason; elapsed_ms: task 148.
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// buildEffortController builds the replacement controller for an effort
// switch (both fast paths declined; the fallback reason rides the agent line).
func (a *App) buildEffortController(tab *WorkspaceTab, tabID, modelRef string, effort string, snap tabRuntimeSnapshot, runtime normalizedTabRuntime, oldCtrl control.SessionAPI, sharedHost *plugin.Host) (*control.Controller, error) {
	newCtrl, err := boot.Build(a.bootContext(), boot.Options{
		RestartUpdater:             restartUpdaterAdapter{a},
		AutonomousUpdateController: newAutonomousUpdateController(a),
		WorktreeProjectOpener:      appWorktreeProjectOpener{app: a}, // 任务128：会话自主建项目——注册为项目并开后台 tab
		Model:                      modelRef,
		Autopilot:                  tab.autopilot,
		MaxRuntime:                 tab.autopilotMaxRuntime,
		AutopilotApprovalGrace:     tab.autopilotApprovalGrace,
		AutopilotAskTimeoutEnabled: tab.autopilotAskTimeoutEnabled,
		AutopilotAskWait:           tab.autopilotAskWait,
		RequireKey:                 false,
		StatsSource:                "desktop",
		TaskStore:                  a.taskStore(),
		OnConfigLoadWarnings:       a.configLoadWarningsHandler(),
		Sink:                       snap.sink,
		WorkspaceRoot:              snap.workspaceRoot,
		SessionDir:                 sessionDirForSnapshot(snap),
		EffortOverride:             &effort,
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
		// Keep the private temporary directory across effort switches (#7575).
		SessionTemp: sessionTempFromController(oldCtrl),
	})
	if err != nil {
		return nil, err
	}
	a.bindControllerDisplayRecorder(newCtrl)
	configureControllerRuntime(newCtrl, oldCtrl, runtime)
	return newCtrl, nil
}

// swapEffortController authorizes and installs the effort-switch controller.
func (a *App) swapEffortController(tab *WorkspaceTab, newCtrl *control.Controller, oldCtrl control.SessionAPI, modelRef string, effort string, prevPath string, runtime normalizedTabRuntime, pendingSequence uint64, carried []provider.Message) error {
	path := agent.ContinueSessionPath(prevPath, newCtrl.SessionDir(), newCtrl.Label())
	if err := a.ensureTabSessionLeaseForRebuild(tab, path, "effort"); err != nil {
		newCtrl.Close()
		return err
	}
	restoredRuntime, err := resumeControllerRuntimeWithMessages(newCtrl, carried, path, runtime)
	if err != nil {
		newCtrl.Close()
		return err
	}
	a.mu.Lock()
	if err := a.authorizeTabReplacementLocked(tab, newCtrl, "switching effort", "effort-switch"); err != nil {
		a.mu.Unlock()
		newCtrl.Close()
		tab.releaseSessionLease()
		return err
	}
	tab.Ctrl = newCtrl
	tab.model = modelRef
	tab.effort = &effort
	tab.Label = newCtrl.Label()
	applyNormalizedRuntimeToTabLocked(tab, restoredRuntime)
	clearTabStartupError(tab)
	tab.Ready = true
	a.supersedeTabBuildLocked(tab)
	a.saveTabsLocked()
	a.mu.Unlock()
	if oldCtrl != nil {
		oldCtrl.Close()
	}
	a.clearDeferredRebuildVersion(tab.ID, pendingSequence)
	a.persistTabSessionPath(tab, path)
	a.notifyTabRuntimeRebuilt(tab)
	return nil
}

// SetAgentPresetDeprecatedNotice is returned by the deprecated execution-mode
// Wails methods. Reasonix runs one adaptive standard execution; these methods
// remain bound for one compatibility version as no-op wrappers: they never require an idle tab, never save a mode, and never rebuild an agent.
const SetAgentPresetDeprecatedNotice = "Reasonix now uses one adaptive standard execution: planning, verification, and review strength follow task risk automatically. Execution modes are no longer switchable; this call is accepted for compatibility and ignored."

func (a *App) SetTokenMode(mode string) error {
	// Deprecated no-op compatibility wrapper.
	return a.SetAgentPreset(boot.NormalizeAgentPreset(mode))
}

func (a *App) SetTokenModeForTab(tabID, mode string) error {
	// Deprecated no-op compatibility wrapper.
	return a.SetAgentPresetForTab(tabID, boot.NormalizeAgentPreset(mode))
}

// SetAgentPreset is a deprecated no-op compatibility wrapper.
func (a *App) SetAgentPreset(preset string) error {
	return a.SetAgentPresetForTab("", preset)
}

// SetAgentPresetForTab is a deprecated no-op compatibility wrapper: it accepts
// the legacy argument, does not require an idle tab, saves no mode, rebuilds
// no agent, and always succeeds with the deprecation notice.
func (a *App) SetAgentPresetForTab(tabID, preset string) error {
	normalized, err := boot.NormalizeAgentPresetErr(preset)
	if err != nil {
		return err
	}
	if tab := a.tabByID(tabID); tab == nil && strings.TrimSpace(tabID) != "" {
		return fmt.Errorf("tab %q not found", tabID)
	}
	return a.SetQualityFloorForTab(tabID, normalized)
}

// persistTabTokenMode persists the deprecated dual-write compatibility values
// (agentPreset=balanced, tokenMode=full) so one-version-old clients keep
// parsing tab state and session metas. The values are fixed; nothing reads them to alter runtime behavior.
func (a *App) persistTabTokenMode(tab *WorkspaceTab) {
	if a == nil || tab == nil {
		return
	}
	a.mu.Lock()
	a.saveTabsLocked()
	a.mu.Unlock()
	_ = a.saveTabSessionMetaForCurrentSession(tab)
}

func (a *App) applyProviderEffortConfig(entry *config.ProviderEntry, effort string) error {
	return a.applyConfigChange(func(cfg *config.Config) error {
		if _, ok := cfg.Provider(entry.Name); !ok {
			if err := cfg.UpsertProvider(*entry); err != nil {
				return err
			}
		}
		if entry.Kind == "anthropic" && effort != "" && entry.Thinking == "" {
			if err := cfg.SetProviderThinking(entry.Name, "adaptive"); err != nil {
				return err
			}
		}
		for _, name := range providerEffortTargetNames(cfg, entry) {
			if err := cfg.SetProviderEffort(name, effort); err != nil {
				return err
			}
		}
		return nil
	})
}

func providerEffortTargetNames(cfg *config.Config, entry *config.ProviderEntry) []string {
	if cfg == nil || entry == nil {
		return nil
	}
	out := []string{entry.Name}
	seen := map[string]bool{entry.Name: true}
	kind := officialProviderKindFromEntry(*entry)
	if kind == "" {
		return out
	}
	var family []string
	switch kind {
	case "deepseek":
		family = []string{"deepseek", "deepseek-flash", "deepseek-pro"}
	}
	for _, name := range family {
		if seen[name] {
			continue
		}
		p, ok := cfg.Provider(name)
		if !ok || officialProviderKindFromEntry(*p) != kind {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func (a *App) runEffortCommandForTab(tabID, input string) {
	entry, err := a.currentProviderEntryForTab(tabID)
	if err != nil {
		a.noticeForTab(tabID, "effort: "+err.Error())
		return
	}
	cap := config.EffortCapabilityForEntry(entry)
	if !cap.Supported {
		a.noticeForTab(tabID, fmt.Sprintf("effort is not configurable for %s", entry.Name))
		return
	}
	args := strings.Fields(input)
	if len(args) < 2 {
		a.noticeForTab(tabID, fmt.Sprintf("effort for %s: %s (default: %s; options: %s)", entry.Name, config.EffortDisplay(entry), cap.Default, strings.Join(cap.Levels, "|")))
		return
	}
	if len(args) > 2 {
		a.noticeForTab(tabID, "usage: /effort "+strings.Join(cap.Levels, "|"))
		return
	}
	effort, err := config.NormalizeEffort(entry, args[1])
	if err != nil {
		a.noticeForTab(tabID, err.Error())
		return
	}
	if err := a.SetEffortForTab(tabID, args[1]); err != nil {
		a.noticeForTab(tabID, "effort: "+err.Error())
		return
	}
	display := effort
	if display == "" {
		display = "auto"
	}
	a.noticeForTab(tabID, fmt.Sprintf("effort for %s set to %s", entry.Name, display))
}

func (a *App) currentProviderEntryForTab(tabID string) (*config.ProviderEntry, error) {
	readStart := time.Now()
	// Task 639/691: the session reconcile below is defensive healing with a
	// full disk walk (project registry reads, per-dir session path
	// validation, branch-meta sidecar loads). A settled tab re-derives the same binding on every read, so reconcileTabWithPinnedSessionMeta itself now serves the memoized outcome (tab_reconcile_memo.go) and only walks when an input that could move a binding changed.
	reconcileStart := time.Now()
	a.reconcileTabWithPinnedSessionMeta(a.tabByID(tabID))
	reconcileMs := time.Since(reconcileStart)
	a.mu.RLock()
	ref := ""
	workspaceRoot := ""
	effortOverride := (*string)(nil)
	if tab := a.tabByIDLocked(tabID); tab != nil {
		ref = tab.model
		workspaceRoot = tab.WorkspaceRoot
		effortOverride = cloneStringPtr(tab.effort)
	}
	a.mu.RUnlock()
	// Task 609: resolve against the per-root config snapshot instead of a
	// full LoadForRoot per call — this line was the read-side hot path behind
	// EffortForTab (431-1350ms, multi-second AV outliers per task 421). The snapshot reloads itself when the tracked config files change, so value freshness matches a fresh load.
	reloadsBefore := a.cfgSnapshotReloadCount(workspaceRoot)
	snapshotStart := time.Now()
	cfg, err := a.cachedConfigForRoot(workspaceRoot)
	snapshotMs := time.Since(snapshotStart)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(ref) == "" {
		ref = cfg.DefaultModel
	}
	resolveStart := time.Now()
	config.NormalizeLegacyMimoCustomProvidersForRefs(cfg, ref)
	resolved, _, ok := cfg.ResolveModelWithFallback(ref)
	if !ok {
		return nil, fmt.Errorf("unknown model %q", ref)
	}
	entry, ok := cfg.ResolveModel(resolved)
	if !ok {
		return nil, fmt.Errorf("unknown model %q", resolved)
	}
	resolveMs := time.Since(resolveStart)
	if effortOverride != nil {
		entry.Effort = *effortOverride
	}
	// Task 639: answer "what is left of the read after the task-609 snapshot"
	// from the log instead of from code reading. The capability mapping in
	// effortForTabDirect is deliberately not a segment: it is pure in-memory table work, so any real cost shows up as total_ms exceeding the sum of the segments here.
	logEffortReadBreakdown(tabID, time.Since(readStart), reconcileMs, snapshotMs, resolveMs,
		a.cfgSnapshotReloadCount(workspaceRoot) != reloadsBefore)
	return entry, nil
}
