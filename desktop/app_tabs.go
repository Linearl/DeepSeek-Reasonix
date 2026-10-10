package main

import (
	"context"
	"log/slog"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/i18n"
	"reasonix/internal/repair"
)

// sessionTempFromController returns the logical-session private temporary

// restoreOrBuildTabs restores the tabs from the last session, or creates a
// default Global tab on first launch.
func (a *App) restoreOrBuildTabs() {
	restoreStartedAt := time.Now()
	defer a.recoverToPending("restoreOrBuildTabs")
	// Unblock startup work gated on the restore (recovery GC) no matter how
	// this returns — including the recover path above.
	defer a.markTabsRestored()
	// Reap any orphaned codegraph processes from a previous crash or older
	// version that leaked them, so they don't accumulate across restarts.
	a.reapOrphanCodeGraph()
	ctx := a.ctx
	ensureWorkspace()

	// Run legacy config migration before the first config load so the
	// freshly written config (including the user's default_model) is
	// picked up by Load instead of falling back to built-in defaults.
	_, _ = config.MigrateLegacyIfNeeded()
	if err := reconcileTopicArchiveMetadataPending(a.deleteTopic); err != nil {
		slog.Warn("desktop: topic archive metadata reconciliation remains pending")
	}
	f := loadTabsFile()
	_, _ = recoverLegacyProjectSidebarRoots(f)
	_, _ = config.ApplyUserConfigUpgradesOnStartup(config.UserConfigPath())
	_, _ = config.MigrateMCPToUserConfigOnUpgrade(desktopMCPMigrationRoots(f))
	// Task 116: materialize the three shipped playbooks under the user skills
	// dir so they are editable. Existing files are never overwritten.
	installShippedPlaybooksToUserDir()

	// Load i18n from the first available config.
	// Prefer DesktopLanguage (desktop UI setting) over Language (CLI setting),
	// so the user's language choice in desktop settings takes effect.
	startupCfg, cfgErr := config.Load()
	if cfgErr == nil {
		cfg := startupCfg
		lang := cfg.DesktopLanguage()
		if lang == "" {
			lang = cfg.Language
		}
		a.setDesktopLocale(i18n.DetectLanguage(lang))
	}
	if cfgErr != nil || singleSurfaceLayoutStyle(startupCfg.DesktopLayoutStyle()) {
		f = singleSurfaceTabsFile(f)
	}
	// Restore remote tabs as disconnected shells; activation performs the
	// first network work so desktop startup remains offline-safe.
	a.restoreRemoteTabShells(f)
	// Task 456 ①: reconcile the persisted leftover records BEFORE any runtime
	// is launched: collapse duplicates first, then retire stale lease records
	// whose holder process is dead, so the fresh runtime starts against a clean
	// record (live foreign holders and active handoff reservations untouched).
	f.Tabs = dedupeRestoredTabEntries(f.Tabs)
	a.reconcileRestoredSessionKeys(f.Tabs)
	if len(f.Tabs) > 0 {
		toBuild := a.restorePersistedTabs(ctx, f)
		a.settleRestoredTabSkeleton(f, toBuild, restoreStartedAt)
		return
	}
	if len(f.RemoteTabs) > 0 {
		// A remote-only single-surface layout is restored above as disconnected
		// shells. It is not a first launch and must not grow a fallback Global tab.
		return
	}

	// First launch: create a default Global tab.
	tab := a.createTabEntry("global", globalTabWorkspaceRoot(), "")
	tab.sink = &tabEventSink{tabID: tab.ID, app: a, ctx: ctx}
	tab.TopicTitle = "Global"
	a.mu.Lock()
	a.tabs[tab.ID] = tab
	a.tabOrder = append(a.tabOrder, tab.ID)
	a.activeTabID = tab.ID
	a.mu.Unlock()
	a.startTabControllerBuild(tab)
}

// restorePersistedTabs materializes every persisted tab entry into a published
// WorkspaceTab skeleton and returns the tabs whose controller must be built.
func (a *App) restorePersistedTabs(ctx context.Context, f desktopTabsFile) []*WorkspaceTab {
	toBuild := make([]*WorkspaceTab, 0, len(f.Tabs))
	for _, entry := range f.Tabs {
		// Task 186: a persisted tab sitting on one of the host's own
		// directories restores as a Global tab; otherwise its first turn would
		// index the topic under a project entry the next save strips.
		scope, workspaceRoot := normalizeWorkspaceScope(entry.Scope, entry.WorkspaceRoot)
		releaseAdmission, admissionErr := a.beginProjectRuntimeAdmission(scope, workspaceRoot)
		if admissionErr != nil {
			continue
		}
		a.mu.Lock()
		id := a.restoredTabIDLocked(entry.ID)
		a.mu.Unlock()

		var tab *WorkspaceTab
		if scope == "project" {
			tab = a.createTabEntryWithID(scope, workspaceRoot, entry.TopicID, id)
		} else {
			tab = a.createTabEntryWithID("global", globalTabWorkspaceRoot(), entry.TopicID, id)
		}
		tab.model = entry.Model
		tab.effort = cloneStringPtr(entry.Effort)
		// The role entry seeds the quality floor: delivery (and legacy
		// delivery labels) raise it; light folds to standard.
		if entry.QualityFloor == control.QualityFloorDelivery {
			tab.qualityFloor = control.QualityFloorDelivery
		} else {
			tab.qualityFloor = ""
		}
		tab.mode = persistedTabMode(entry.Mode)
		// Validate the persisted goal against the session's goal-state
		// sidecar: a typed /new or /clear rotates the session without passing
		// App.NewSession/ClearSession, so entry.Goal can be stale. A stopped
		// goal-state on the fresh path stops a restart from re-seeding the
		// cleared goal; no sidecar keeps the persisted goal (legacy).
		tab.goal = runningTabSessionGoal(strings.TrimSpace(entry.SessionPath), strings.TrimSpace(entry.Goal))
		tab.toolApprovalMode = normalizeToolApprovalMode(entry.ToolApprovalMode)
		if tab.toolApprovalMode == control.ToolApprovalAsk && tabModeHasAutoApproveTools(entry.Mode) {
			tab.toolApprovalMode = control.ToolApprovalYolo
		}
		// Task 49 A2 + 465 (X4 断点 C): an unattended run continues across a
		// restart. The tab entry column and the goal-state sidecar feed the
		// flag, then the task-325 gate has the final say: a persisted
		// unattended run may only come back under yolo. The sidecar decides
		// whether THIS run was unattended; the preferences only supply the
		// bound (the previous deadline died with the process; without a
		// usable bound the run stays interactive).
		if entry.Autopilot || tabSessionAutopilot(tab.SessionPath) {
			if on, maxRuntime, grace, askEnabled, askWait, askAutoContinue := desktopAutopilotDefaults(); on {
				tab.autopilot, tab.autopilotMaxRuntime, tab.autopilotApprovalGrace, tab.autopilotAskTimeoutEnabled, tab.autopilotAskWait, tab.autopilotAskAutoContinue = gateRestoredAutopilotDefaults(on, maxRuntime, grace, askEnabled, askWait, askAutoContinue, tab.toolApprovalMode)
			}
		}
		tab.SessionPath = strings.TrimSpace(entry.SessionPath)
		tab.ReadOnly = entry.ReadOnly
		restoreTabPinnedContext(tab, entry.PinnedFiles)
		tab.Takeover.Spectator = entry.TakeoverSpectator
		tab.sink = &tabEventSink{tabID: tab.ID, app: a, ctx: ctx}
		a.publishRestoredTab(tab, releaseAdmission)
		toBuild = append(toBuild, tab)
	}
	return toBuild
}

// settleRestoredTabSkeleton settles the active tab, publishes the restored
// skeleton event, and throttles the foreground-first controller builds.
func (a *App) settleRestoredTabSkeleton(f desktopTabsFile, toBuild []*WorkspaceTab, restoreStartedAt time.Time) {
	a.mu.Lock()
	if _, ok := a.tabs[f.ActiveTab]; ok {
		a.activeTabID = f.ActiveTab
	} else {
		ordered := a.orderedTabIDsLocked()
		if len(ordered) > 0 {
			a.activeTabID = ordered[0]
		}
	}
	a.saveTabsLocked()
	a.mu.Unlock()
	// 任务 619 ①: the skeleton is fully published — every restored tab is in
	// a.tabs and the active id is settled. Tell the frontend NOW so the tab bar
	// renders from ListTabs; the frontend also polls as a backstop for the
	// emit-before-subscribe race.
	if a.ctx != nil {
		a.runtimeEvents.Emit(a.ctx, tabsRestoredEvent)
	}
	slog.Info("desktop: startup tab skeleton published",
		"tabs", len(toBuild),
		"active", a.activeTabID,
		"elapsed_ms", time.Since(restoreStartedAt).Milliseconds())
	// 任务 619 ②: foreground-first, lazy background (task 405 Q2 already
	// ordered the active tab first and throttled the storm). Only the tab the
	// user is looking at — plus tabs that must resume unattended work —
	// builds at startup; the rest stay published skeletons whose transcript
	// stays readable cold and whose build kicks from SetActiveTab.
	restored := orderTabsActiveFirst(toBuild, a.activeTabID)
	sem := make(chan struct{}, startupBootConcurrency)
	for _, tab := range startupBuildSet(restored) {
		a.startTabControllerBuildThrottled(tab, sem)
	}
}

func (a *App) createTabEntry(scope, workspaceRoot, topicID string) *WorkspaceTab {
	return a.createTabEntryWithID(scope, workspaceRoot, topicID, newTabID())
}

// desktopAutopilotDefaults returns the unattended-run settings a newly-created
// desktop session starts with, read from the [desktop] preferences. Autopilot
// without a wall-clock bound is the one combination the CLI refuses outright, so
// it is refused here too rather than silently running a desktop session with no
// limit: a missing or malformed bound leaves autopilot off. The task-477 pair
// rides along: the ask-timeout sub-option switch and its wait (0 = the
// controller's built-in default — 15s under the sub-option, 10m without it).
// The task-544 ask auto-continue switch rides along too, independent of the
// 477 pair.
func desktopAutopilotDefaults() (bool, time.Duration, time.Duration, bool, time.Duration, bool) {
	cfg := config.LoadForEdit(config.UserConfigPath())
	if !cfg.Desktop.Autopilot {
		return false, 0, 0, false, 0, false
	}
	maxRuntime, err := time.ParseDuration(strings.TrimSpace(cfg.Desktop.AutopilotMaxRuntime))
	if err != nil || maxRuntime <= 0 {
		return false, 0, 0, false, 0, false
	}
	var grace time.Duration
	if raw := strings.TrimSpace(cfg.Desktop.AutopilotApprovalGrace); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			grace = d
		}
	}
	askEnabled := cfg.Desktop.ExperimentalAutopilotAskTimeout
	var askWait time.Duration
	if askEnabled {
		// The config reader already clamps into 1..3600 and defaults to 15;
		// a malformed value degrades to 0 (= the controller's built-in
		// default) instead of arming an out-of-policy timeout.
		if seconds := cfg.AutopilotAskWaitSecondsEffective(); seconds > 0 {
			askWait = time.Duration(seconds) * time.Second
		}
	}
	return true, maxRuntime, grace, askEnabled, askWait, cfg.Desktop.ExperimentalAutopilotAskAutoContinue
}

func desktopNewSessionDefaults(scope, workspaceRoot string) (string, string, string) {
	userCfg := config.LoadForEdit(config.UserConfigPath())
	modelCfg := userCfg
	if strings.TrimSpace(scope) == "project" && strings.TrimSpace(workspaceRoot) != "" {
		if cfg, err := config.LoadForRootReadOnly(workspaceRoot); err == nil {
			modelCfg = cfg
		}
	}
	return resolveNewSessionModel(modelCfg), normalizeToolApprovalMode(userCfg.DesktopDefaultToolApprovalMode()), userCfg.DefaultSubagentPolicy()
}

// resolveNewSessionModel picks the model a fresh session starts on. A
// default_model that resolves but has no API key in the current environment
// would boot every new tab straight into the missing-key notice, so fall
// through to the first provider that is actually configured, mirroring the
// Configured() gate in Config.ResolveModelWithFallback's fallback chain. An
// allowed chat default is preserved when every eligible provider is keyless so
// the existing missing-key notice still tells the user what to fix. When no
// desktop-accessible chat model exists, the empty result lets tab startup show
// an actionable setup error instead of re-admitting an ineligible default.
func resolveNewSessionModel(cfg *config.Config) string {
	def := strings.TrimSpace(cfg.DefaultModel)
	config.NormalizeLegacyMimoCustomProvidersForRefs(cfg, def)
	if resolved, _, ok := cfg.ResolveDesktopNewSessionModel(); ok {
		// Keep provider identity explicit at the new-session boundary: a bare
		// model id is ambiguous when two gateways expose the same model, and a
		// provider-only ref compares unequal to the canonical ref on a tab.
		if entry, found := cfg.ResolveModel(resolved); found {
			return entry.Name + "/" + entry.Model
		}
		return resolved
	}
	return ""
}

func (a *App) createTabEntryWithID(scope, workspaceRoot, topicID, id string) *WorkspaceTab {
	model, toolApprovalMode, subagentPolicy := desktopNewSessionDefaults(scope, workspaceRoot)
	// Task 325: a fresh tab may only start unattended when its approval default
	// is yolo — otherwise desktopAutopilotDefaults is refused here too.
	autopilot, maxRuntime, approvalGrace, askEnabled, askWait, askAutoContinue := desktopAutopilotDefaults()
	autopilot, maxRuntime, approvalGrace, askEnabled, askWait, askAutoContinue = gateRestoredAutopilotDefaults(autopilot, maxRuntime, approvalGrace, askEnabled, askWait, askAutoContinue, toolApprovalMode)
	return &WorkspaceTab{
		ID:                         id,
		Scope:                      scope,
		WorkspaceRoot:              workspaceRoot,
		TopicID:                    topicID,
		TopicTitle:                 topicTitleForTab(scope, workspaceRoot, topicID),
		topicTitleSource:           loadTopicTitleSource(topicTitleRoot(scope, workspaceRoot), topicID),
		model:                      model,
		qualityFloor:               "",
		mode:                       tabModeFromAxes(false, toolApprovalMode == control.ToolApprovalYolo),
		toolApprovalMode:           toolApprovalMode,
		subagentPolicy:             subagentPolicy,
		autopilot:                  autopilot,
		autopilotMaxRuntime:        maxRuntime,
		autopilotApprovalGrace:     approvalGrace,
		autopilotAskTimeoutEnabled: askEnabled,
		autopilotAskWait:           askWait,
		autopilotAskAutoContinue:   askAutoContinue,
		disabledMCP:                map[string]ServerView{},
	}
}

func (a *App) snapshotAllTabs() {
	a.mu.RLock()
	tabs := a.runtimeTabsLocked()
	a.mu.RUnlock()
	for _, t := range tabs {
		if err := a.snapshotTabDurable(t); err != nil {
			slog.Warn("desktop: snapshot all tabs failed", "tab", t.ID, "err", err)
		}
	}
}

// shutdown snapshots all tabs, saves the final window geometry, and closes tabs.
func (a *App) shutdown(context.Context) {
	if a.remoteWindowTicket != "" {
		// Remote web window child has no local state to stop.
		return
	}
	// Freeze publication, then cancel off-barrier history, catalog, and plugin
	// work so normal quit never waits for background I/O.
	a.shuttingDown.Store(true)
	a.cancelAllTabBuilds()
	a.stopSessionCatalog(250 * time.Millisecond)
	completeDesktopShutdown(a.lifecycle.tracker, a.shutdownBody)
}

// domReady is called (via OnDomReady) after the webview finishes loading its DOM
// but before the StartHidden window is presented. It restores saved geometry,
// then delegates presentation to the platform-aware shell coordinator.
func (a *App) domReady(_ context.Context) {
	// JSC has installed its lazy signal handlers by this point. Restore the
	// SA_ONSTACK flags required by Go; this is a no-op outside Linux.
	repairWebKitSignalHandlers()

	if a.remoteWindowTicket != "" {
		a.domReadyRemoteWindow()
		return
	}
	if a.desktopShell.coordinator != nil {
		a.desktopShell.coordinator.markDOMReady()
	}

	state, ok := loadWindowState()
	if ok {
		// Validate saved position against current screens (Wails v2 exposes no
		// per-screen origin, so only a basic sanity check): Windows border
		// insets are legal; large off-screen positions re-center.
		maxW, maxH := 0, 0
		screens, err := runtime.ScreenGetAll(a.ctx)
		if err == nil {
			for _, sc := range screens {
				if sc.Size.Width > maxW {
					maxW = sc.Size.Width
				}
				if sc.Size.Height > maxH {
					maxH = sc.Size.Height
				}
			}
		}
		if windowPositionRestorable(state, maxW, maxH) {
			runtime.WindowSetPosition(a.ctx, state.X, state.Y)
		} else {
			runtime.WindowCenter(a.ctx)
		}
	} else {
		runtime.WindowCenter(a.ctx)
	}

	if ok && state.Maximised {
		if goruntime.GOOS == "windows" {
			// Preserve the established Windows maximise -> show ordering through
			// the unified presentation plan without appending SW_RESTORE.
			a.backgroundMaximised.Store(true)
		} else {
			runtime.WindowMaximise(a.ctx)
		}
	}

	a.showMainWindowFrom("startup_dom_ready")
}

func (a *App) completeFrontendStartup() {
	a.markDesktopHealthy()
	ctx := a.ctx
	a.goSafe("recordHealthyConfig", func() {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return
		}
		if err := a.commitPendingUpdateHealth(); err != nil {
			slog.Warn("desktop: commit healthy update", "err", err)
		}
		if err := repair.RecordHealthyConfig(version); err != nil {
			slog.Debug("desktop: record last-known-good config", "err", err)
		}
		if archived, err := archiveSupersededPendingUpdateAfterReady(); err != nil {
			slog.Warn("desktop: retire superseded update", "err", err)
		} else if archived {
			slog.Info("desktop: archived superseded update transaction")
		}
	})
}

// ReportDesktopWebViewReady is the content-process heartbeat. OnDomReady proves
// native navigation completed; this bound call additionally proves that React
// and the Wails bridge are responsive after a renderer reload.
func (a *App) ReportDesktopWebViewReady() {
	if a == nil || a.shuttingDown.Load() || a.forceQuit.Load() {
		return
	}
	if a.webView2Recovery != nil {
		a.webView2Recovery.reportReady()
	}
	a.reportLinuxWebKitFrontendReady()
	if a.desktopShell.coordinator != nil {
		first, healthy := a.desktopShell.coordinator.markFrontendHeartbeat(time.Now())
		if first {
			a.goSafe("startDesktopTrayAfterFrontendReady", func() { a.startTray() })
		}
		if healthy {
			if a.desktopShell.linuxRecovery != nil {
				a.desktopShell.linuxRecovery.frontendHealthy()
			}
			a.completeFrontendStartup()
		}
	}
}

func (a *App) commitPendingUpdateHealth() error {
	if a == nil || strings.TrimSpace(a.healthyUpdateCreatedAt) == "" ||
		strings.TrimSpace(a.healthyUpdateTransactionID) == "" {
		return nil
	}
	return markPendingUpdateHealthyAfterReady(
		version,
		a.healthyUpdateCreatedAt,
		a.healthyUpdateTransactionID,
	)
}
