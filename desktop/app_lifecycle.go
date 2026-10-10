package main

import (
	"context"
	"log/slog"
	"net/http"
	goruntime "runtime"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"reasonix/desktop/internal/instanceidentity"
	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"
)

// eventChannel is the Wails runtime event name the frontend subscribes to for the
// agent's typed event stream. One channel carries every event kind; the payload's
// `kind` field discriminates — the desktop analogue of the serve transport's SSE
// `data:` frames.
const eventChannel = "agent:event"

const singleInstanceIDPrefix = instanceidentity.Prefix

func singleInstanceID() string { return instanceidentity.ForHome(config.ReasonixHomeDir()) }

type desktopShellRuntimeState struct {
	coordinator   *desktopShellCoordinator
	linuxRecovery *linuxWebKitRecoveryCoordinator
	trayState     string
	trayReason    string
}

// jsProfilingMiddleware opts every asset response into the JS Self-Profiling
// document policy so the frontend performance monitor can attach sampled stacks
// to long-task reports. Chromium WebViews (WebView2) honor it; WebKit ignores
// both the header and the API, so the frontend degrades to unattributed reports.
func (a *App) jsProfilingMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Document-Policy", "js-profiling")
			next.ServeHTTP(w, r)
		})
	}
}

// NewApp constructs the bound object. Tabs are restored in startup from the
// last session's desktop-tabs.json.
func NewApp() *App {
	a := &App{
		tabs:                 map[string]*WorkspaceTab{},
		runtimeByID:          map[string]*desktopSessionRuntime{},
		runtimeBySessionKey:  map[string]*desktopSessionRuntime{},
		catalogReconcileJobs: map[string]*desktopCatalogReconcileJob{},
		detachedSessions:     map[string]*WorkspaceTab{},
		mediaTokens:          newMediaTokenStore(),
		botInstalls:          map[string]*botInstallSession{},
		botRuntime:           newDesktopBotRuntime(),
		remoteWindows:        newRemoteWindowRegistry(),
		topicState:           desktopTopicState,
		worktreeReservations: worktreeRuntimeReservations{
			cleanup: map[string]struct{}{},
			merge:   map[string]struct{}{},
		},
	}
	// Task 36: remote takeover requests prompt the user instead of yielding
	// silently, and serve's device lease is observed for runtime read-only.
	// Task 539: yield lifecycle notices reach the frontend as runtime events.
	a.registerRemoteWriteAuthorityHook()
	RegisterTakeoverPromptSink(func(req takeoverDecisionReq) {
		runtimeEventsEmitFallback(a.ctx, "app:takeover-request", map[string]string{
			"marker": req.Marker,
			"path":   req.Path,
			"from":   req.From,
		})
	})
	RegisterTakeoverYieldNotifier(func(kind, path, detail string) {
		runtimeEventsEmitFallback(a.ctx, "app:takeover-yield", map[string]string{
			"kind":   kind,
			"path":   path,
			"detail": detail,
		})
	})
	a.remoteWindowOwnerID = newRemoteWindowOwnerID()
	a.desktopShell.trayState = "probing"
	a.webView2Recovery = newWebView2RecoveryCoordinator(a)
	a.desktopShell.linuxRecovery = newLinuxWebKitRecoveryCoordinator(a)
	a.desktopShell.coordinator = newDesktopShellCoordinator(a)
	a.servePool = nil
	a.gatewayAddr = ""
	a.gatewaySrv = nil
	a.workspaceHub = newWorkspaceChangeHub(a)
	a.terminals = newTerminalManager(a)
	a.botBridge = a.newBotBridge()
	return a
}

func (a *App) bootContext() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// logRuntimeBuildEnd pairs with the "runtime build begin" lines (task 334).
// The elapsed covers begin → function exit — the bench's "build begin → can
// send a message" window (lease/resume/swap included) — and carries whether
// this build reused the shared plugin host, so a reuse miss shows up here
// instead of being inferred from a slow mcp stage.
func logRuntimeBuildEnd(trigger string, buildStart time.Time, sharedHostReused bool, extra ...any) {
	args := []any{"trigger", trigger, "ms", time.Since(buildStart).Milliseconds(), "shared_host_reused", sharedHostReused}
	args = append(args, extra...)
	slog.Info("desktop: runtime build end", args...)
}

// Platform exposes the native OS to the frontend so chrome/layout affordances can
// stay platform-scoped instead of relying on browser user-agent guesses.
func (a *App) Platform() string {
	return goruntime.GOOS
}

// startup runs once the webview process is up, before the frontend can issue any
// bound call. It captures the Wails context (needed for EventsEmit), then kicks
// off the initialization in a background goroutine so the webview loads immediately.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.shuttingDown.Store(false)
	a.startupRegisterProcessHelpers()
	a.startWindowsWebView2StartupFallback(ctx)
	publishCDPDebugEndpoint()
	a.webView2Recovery.startGuidance(ctx)
	a.desktopShell.coordinator.start(ctx)
	a.lifecycle.tracker.markAsync("ready")
	if a.remoteWindowTicket != "" {
		// Remote web window child: no local tabs, tray, heartbeat, providers,
		// or remote manager. domReady consumes the ticket and navigates; the
		// owner watcher closes the window if the primary Desktop disappears.
		a.watchRemoteWindowOwner(ctx)
		return
	}
	a.startupStartLocalServices(ctx)
	a.startupPushAgentSettings()
	a.startupStartMonitors()
	a.startupStartBackgroundLoops()
	a.startupKickTabRestore()
}

// startupRegisterProcessHelpers wires the process-wide hooks that assume one
// desktop App per process, and claims the pre-Wails diagnostics lock.
func (a *App) startupRegisterProcessHelpers() {
	// Task 225: the cascade-approval resolver needs the live App to find the
	// task source's controller. One desktop App per process.
	cascadeApp = a
	// Task 254: capability-routed tool calls never cross the agent's
	// context-binding point, so register the process-wide restart fallbacks
	// next to cascadeApp (idempotent; nil in CLI/serve hosts).
	tool.SetFallbackAutonomousUpdateController(newAutonomousUpdateController(a))
	tool.SetFallbackRestartUpdater(restartUpdaterAdapter{a})
	// Task 203: the head-divergence notice needs a tab-registry fact to tell a
	// real second tab from an in-process race; register the probe once per
	// process (nil in CLI/serve -- the local class then only logs).
	control.SetConcurrentDualTabProbe(a.isConversationDualOpen)
	// Only the process that claimed the pre-Wails diagnostics lock consumes
	// lifecycle evidence. This remains correct on Linux where Wails invokes
	// OnStartup before its DBus single-instance handoff.
	initializeLifecycleDiagnostics(a)
}

// startupStartLocalServices starts everything this process now owns as the
// local-desktop host: quit hook, tab persistence, serve pool, task bus, tray.
func (a *App) startupStartLocalServices(ctx context.Context) {
	installSystemQuitHook()
	// Task 653: from here on this process owns local tabs -- start the
	// desktop-tabs.json flusher so saveTabsLocked enqueues instead of writing
	// under App.mu (the remote web-window child kept the sync fallback).
	a.startTabsSaveFlusher()
	// Task 211 P2: clear shadow project roots (registry entries pointing into the
	// app's own session storage) before anything reads the project tree.
	pruneShadowProjectRoots()
	// Serve pool gateway is opt-in via the Settings -> integration panel; only
	// start it on launch if the user previously enabled the toggle.
	if servepoolEnabled() {
		a.startServePool(ctx)
	}
	// Task 439: built-in zcode task bus, opt-in via the lab switch (default
	// off): the desktop hosts the bus MCP endpoint itself, no external
	// `reasonix serve`/vbs needed.
	if cfg, err := config.Load(); err == nil {
		a.startZcodeTaskBus(cfg)
	}
	a.startTray()
	a.enableDeferredRebuildRetry()
	a.startHistoryIndexMigration()
	a.startHistoryIdlePrefetch()
}

// startupPushAgentSettings forwards the config values the agent layer cannot
// read itself (layering): once at boot, again on every settings change.
func (a *App) startupPushAgentSettings() {
	// Task 333: forward the event-log rotation gate into the agent's save
	// path. Before any push the agent-side value equals the default config,
	// so CLI/serve hosts and tests keep today's behavior.
	if cfg, err := config.Load(); err == nil {
		agent.SetEventsAutoRotation(
			config.EventsAutoRotationMode(cfg),
			config.EventsRotationFactor(cfg),
			config.EventsRotationCapMB(cfg),
		)
		// Task 196fix2/499: the same load pushes the tunable graph-cache LRU
		// capacity and byte ceilings so the save-path cache is bounded from
		// boot (0/unset reads as the built-in defaults).
		agent.SetSessionGraphCacheCapacity(config.DagGraphCacheCapacity(cfg))
		agent.SetSessionGraphCacheMaxBytes(config.DagGraphCacheMaxMB(cfg) << 20)
		agent.SetSessionGraphCacheEntryMaxBytes(config.DagGraphCacheEntryMaxMB(cfg) << 20)
	} else {
		// Without the push the gate stays on the pre-push default (manual):
		// a config that says "off" would keep rotating until a successful
		// push. Log the divergence instead of hiding it (review, 2026-09-28).
		slog.Warn("desktop: events rotation gate not pushed at boot (config load failed)", "err", err)
	}
}

// startupStartMonitors starts the opt-in perf/heap monitors, repairs native
// integration, and arms the run diagnostics and main-thread watchdog.
func (a *App) startupStartMonitors() {
	// Task 184: the performance monitor is opt-in and restart-scoped (its interval
	// and file table come from the config read here). When the switch is off this
	// block does nothing at all.
	if cfg, err := config.Load(); err == nil &&
		cfg.Agent.ExperimentalPerfMonitor {
		interval, retention, heapInterval, paths := perfMonitorSettings(cfg)
		monitor := newPerfMonitor(a, perfMonitorDir(), interval, retention, heapInterval, paths)
		// 任务 501: 高峰快照挂在同一采样循环上，随 monitor 的启停启停。开关
		// 关着时这两个字段是零值，循环行为逐字节不变。
		monitor.heapHighEnabled, monitor.heapHighThresholdMB = perfMonitorHeapHighSettings(cfg)
		a.perfMonitor = monitor
		monitor.Start()
		slog.Info("desktop: perf monitor started",
			"intervalSeconds", interval.Seconds(), "retentionHours", retention.Hours(),
			"heapIntervalSeconds", heapInterval.Seconds(), "patterns", len(paths),
			"heapHighEnabled", monitor.heapHighEnabled,
			"heapHighThresholdMB", int64(monitor.heapHighThresholdMB))
	} else if err == nil &&
		cfg.Agent.ExperimentalHeapHighProfile {
		// 任务 501: 高峰开关开着但主监控关着——触发器骑在采样循环上，主监控
		// 不跑它永远不会命中；把「下一步」直接写进日志（设置面板可开主监控）。
		slog.Info("desktop: perf monitor heap-high armed but perf monitor off — the trigger rides the perf monitor's sampling loop; enable experimental_perf_monitor to activate it")
	}
	a.goSafe("repairDesktopIconIntegration", func() {
		if err := repairDesktopIconIntegration(); err != nil {
			slog.Debug("desktop: repair native icon integration", "err", err)
		}
	})
	a.goSafe("applyWindowIconsFromExecutable", func() {
		applyWindowIconsFromExecutable()
	})

	if cfg, err := config.Load(); err == nil && cfg.DesktopMetrics() && version != "dev" {
		a.metrics.Store(newMetricsAggregator(config.MemoryUserDir()))
		a.recordSettingsMetricsSnapshot(cfg)
	}
	a.recordPreviousRunDiagnostics()
	a.observeIncompleteWindowRestore()
	a.startMainThreadWatchdog()
}

// startupStartBackgroundLoops arms the long-lived background loops: cold-cache
// compaction, idle release, lease sweeping, heartbeat, session collab.
func (a *App) startupStartBackgroundLoops() {
	// Task 297: the cold-cache compact pass ticks unconditioned; the lab
	// switch is read live inside the loop, so OFF is one config read per
	// tick and ON applies without a restart.
	a.startColdCacheCompactLoop()
	// Task 308-O4: release detached/idle runtimes (gate: env minutes, default off).
	a.startDetachedIdleReleaseLoop()
	// Task 485 P1: release self-held orphan session leases (leaked-handle
	// backstop -- the lock itself cannot arbitrate a handle this process lost).
	a.startSessionLeaseLeakSweeper()
	// Task 308-O3: apply the soft memory limit from config (live-capable via
	// SetGoMemLimitMB in settings; startup applies the stored value once).
	if cfg, _, err := a.loadDesktopUserConfigForView(); err == nil {
		applyGoMemLimit(cfg)
	}
	a.heartbeat = newHeartbeatEngine(a)
	// Task 244 B1: call-time evaluation (S4) -- the burn guard reads the
	// saved switch on every run, so toggling it in settings applies without a
	// restart. Missing config = off.
	a.heartbeat.idleTerminate = func() bool {
		cfg, err := config.Load()
		if err != nil {
			return false
		}
		// Task 517: the B1 gate rides the merged safety/cost switch.
		// Task 722: fine-grained sub-switch override (nil = inherit the master).
		return cfg.SafetyIdleTerminateEnabled()
	}
	// Task 742: same call-time contract -- the background gate reads the saved
	// switch on every run, so toggling it in settings applies to the next
	// scheduled run without a restart. Missing config = off.
	a.heartbeat.heartbeatBackground = func() bool {
		cfg, err := config.Load()
		if err != nil {
			return false
		}
		return cfg.Desktop.ExperimentalHeartbeatBackground
	}
	a.heartbeat.Start()
	// Expose the scheduler's admin surface to agent tools (task 201): the
	// adapter keeps the engine as the single source of truth; CLI/serve
	// sessions keep the tools failing with a "no engine attached" error.
	builtin.SetHeartbeatManager(newHeartbeatManagerAdapter(a.heartbeat))

	a.sessionCollab = newSessionCollabPump(a)
	a.sessionCollab.Start()
	// 任务731: the abnormal-stop auto-resume watchdog is purely event-driven
	// (no ticker): the S1 tap lives in tabEventSink.Emit, the S2 escalation
	// in the 579 bridge's exhaustion hook.
	a.autopilotResume = newAutopilotResumeWatchdog(a)
}

// startupKickTabRestore launches tab restoration and the first-shot services
// that must run only after the restore goroutine is on its way.
func (a *App) startupKickTabRestore() {
	a.mu.Lock()
	a.tabsRestored = make(chan struct{})
	a.mu.Unlock()
	go a.restoreOrBuildTabs()
	a.registerHistoryIndexEvents()
	a.startSessionCatalog()
	a.goSafe("refreshBotRuntime", a.refreshBotRuntime)
	a.goSafe("sendStartupPing", a.sendStartupPing)
	a.goSafe("flushMetrics", a.flushMetrics)
	// Task 663 gap (4): capture the pending-crash queue BEFORE the flush ships
	// or drops it, so startup's one-click analysis can offer the previous
	// run's Go panic. Synchronous on purpose.
	a.snapshotPendingCrashForAnalysis()
	a.goSafe("flushPendingCrash", a.flushPendingCrash)
	// After restoreOrBuildTabs is launched: the GC's first sweep waits on
	// tabsRestored so it never observes the pre-restore empty tab map.
	a.startRecoveryGC()
}

func (a *App) beforeClose(ctx context.Context) bool {
	if a.remoteWindowTicket != "" {
		// A remote web window closes immediately — nothing to snapshot, lease,
		// or hide. Closing it must not stop the remote Serve or the main
		// process's SSH connection.
		return false
	}
	if a.forceQuit.Swap(false) || consumeSystemQuitRequested() {
		return false
	}
	cfg, _, err := a.loadDesktopUserConfigForView()
	if err != nil {
		cfg = config.LoadForEdit(config.UserConfigPath())
	}
	if cfg.DesktopCloseBehavior() == "background" {
		if !a.backgroundCloseHasRestorePath() {
			return false
		}
		// Never query native maximise state here: during close the Win32 DPI
		// path can report 0 and panic inside Wails ScaleToDefaultDPI. Use the
		// last frontend-reported geometry instead.
		a.backgroundMaximised.Store(a.lastKnownMaximised())
		a.saveWindowStateSync()
		a.snapshotAllTabs()
		if a.desktopShell.coordinator != nil {
			return a.desktopShell.coordinator.hideToBackground(ctx, func() bool {
				return backgroundCloseUsesApplicationHide(goruntime.GOOS) || a.isTrayReady()
			})
		}
		hideForBackground(ctx)
		return true
	}
	return false
}

const backgroundCloseTrayReadyTimeout = 500 * time.Millisecond

func (a *App) backgroundCloseHasRestorePath() bool {
	if backgroundCloseUsesApplicationHide(goruntime.GOOS) {
		return backgroundCloseHasRestorePathFor(goruntime.GOOS, false, false)
	}
	if !a.startTray() {
		return false
	}
	return backgroundCloseHasRestorePathFor(goruntime.GOOS, true, a.waitForTrayReady(backgroundCloseTrayReadyTimeout))
}

func (a *App) waitForTrayReady(timeout time.Duration) bool {
	if a.isTrayReady() {
		return true
	}
	ready := a.trayReadySignal()
	if ready == nil {
		return false
	}
	if timeout <= 0 {
		select {
		case <-ready:
			return a.isTrayReady()
		default:
			return false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ready:
		return a.isTrayReady()
	case <-timer.C:
		return a.isTrayReady()
	}
}

func (a *App) isTrayReady() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.trayReady
}

func (a *App) trayReadySignal() <-chan struct{} {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.tray == nil {
		return nil
	}
	return a.tray.ready
}

// markTabsRestored closes the tabsRestored gate exactly once. Safe when the
// channel was never created (tests that drive App without startup).
func (a *App) markTabsRestored() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tabsRestored == nil {
		return
	}
	select {
	case <-a.tabsRestored:
	default:
		close(a.tabsRestored)
	}
}

// tabsRestoredSignal returns a channel closed once tab restore has completed.
// When startup never armed the gate (tests), it reports already-restored.
func (a *App) tabsRestoredSignal() <-chan struct{} {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.tabsRestored == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return a.tabsRestored
}

func (a *App) showMainWindow() {
	a.showMainWindowFrom("menu")
}

func (a *App) secondInstanceLaunch() {
	// Task 272 G3: this exact path ran silently in the incident — the
	// launcher timed out, booted a fresh instance, lost the single-instance
	// lock to the wedged old process, and exited without one log line.
	slog.Info("desktop: second instance launched while a previous desktop is still running; yielding to the existing process")
	a.showMainWindowFrom("second_instance")
}

func (a *App) quitApp() {
	if a.ctx == nil {
		return
	}
	a.forceQuit.Store(true)
	runtime.Quit(a.ctx)
}

func hideForBackground(ctx context.Context) {
	if backgroundCloseUsesApplicationHide(goruntime.GOOS) {
		runtime.Hide(ctx)
		return
	}
	runtime.WindowHide(ctx)
}

func backgroundCloseUsesApplicationHide(goos string) bool {
	return goos == "darwin"
}

func backgroundCloseHasRestorePathFor(goos string, trayStarted, trayReady bool) bool {
	return backgroundCloseUsesApplicationHide(goos) || (trayStarted && trayReady)
}

type backgroundRestorePlan struct {
	maximiseBeforeShow  bool
	unminimiseAfterShow bool
}

func backgroundRestorePlanFor(goos string, wasMaximised bool) backgroundRestorePlan {
	if backgroundRestoreShouldMaximise(goos, wasMaximised) {
		return backgroundRestorePlan{maximiseBeforeShow: true}
	}
	return backgroundRestorePlan{unminimiseAfterShow: true}
}

func backgroundRestoreShouldMaximise(goos string, wasMaximised bool) bool {
	return wasMaximised && !backgroundCloseUsesApplicationHide(goos)
}

// tabsRestoredEvent tells the frontend the persisted tab list (desktop-tabs.json)
// has been published and ListTabs now carries every restored tab's title, long
// before any controller build finishes (任务 619: render from this skeleton).
const tabsRestoredEvent = "tabs:restored"

// tabBackendActivatedEvent tells the frontend the backend opened AND activated
// a tab outside any frontend navigation (today: the one-click analysis family,
// task 688); the payload mirrors TopicActivationEvent's shape: {tabId, reason}.
const tabBackendActivatedEvent = "tab:backend-activated"

// BackendTabActivatedEvent is the payload of tabBackendActivatedEvent.
type BackendTabActivatedEvent struct {
	TabID  string `json:"tabId"`
	Reason string `json:"reason"`
}
