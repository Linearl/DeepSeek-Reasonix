package main

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"reasonix/internal/notify"
	"reasonix/internal/servepool"
	"reasonix/internal/sessioncatalog"
	"reasonix/internal/taskmonitor"
)

// App is the Wails-bound application object: the desktop frontend's command
// surface. Its exported methods (Submit/Cancel/Approve/…) are generated into JS
// bindings. The app manages multiple WorkspaceTabs — each with its own controller
// scoped to a project workspace — and routes commands to the active tab. Events
// flow the other way: each tab's controller emits to a tabEventSink that
// forwards events tagged with tabId to the webview via runtime.EventsEmit.
// sessionStoreState remembers the conversation-store mode (task 155) this
// process started with: the settings view compares it against the configured
// mode to flag a restart that is still pending, and the setter stamps the
// audit log with it.
type sessionStoreState struct {
	sessionStorageModeMu    sync.Mutex
	sessionStorageBootValue string
	sessionStorageBootSet   bool
}

// topicActivationTicketState holds ticketed topic activation bookkeeping
// (StartTopicActivation). Guarded by App.mu: activationGen bumps on every
// activation-or-supersede so a background completion can tell whether it still
// owns publication; the pending request/tab pair identifies the in-flight
// ticketed activation whose completion may still prune and emit "ready".
type topicActivationTicketState struct {
	activationGen             uint64
	latestActivationRequestID string
	pendingActivationTabID    string
}

// diagnosticsState carries the pre-Wails diagnostics ownership flags and the
// healthy-update identity captured before Wails starts. A process may commit
// only the complete probationary transaction it actually booted from, never a
// rewritten or later same-version retry.
type diagnosticsState struct {
	diagnosticsOwner           bool
	diagnosticsOwnerRelease    func()
	diagnosticsConfigLoaded    bool
	diagnosticsTelemetry       bool
	healthyUpdateCreatedAt     string
	healthyUpdateTransactionID string
}

// remoteWindowState is the remote web-window child state: ticket/host-key pair
// set from argv before Wails starts, the owner identity scoping child
// single-instance locks, and the mutex serializing ticket consumption so a
// handoff arriving before domReady cannot be overridden by the initial ticket.
type remoteWindowState struct {
	remoteWindowMu             sync.Mutex
	remoteWindowTicket         string
	remoteWindowHostKey        string
	remoteWindowTicketConsumed bool
	remoteWindowOwnerID        string
	remoteWindowParentPID      int
}

// appUiState holds the process-wide UI flags flipped outside the tab registry:
// forced quit, the last-known maximise state, the desktop locale, and whether
// the tray finished starting.
type appUiState struct {
	forceQuit           atomic.Bool
	backgroundMaximised atomic.Bool
	desktopLocale       atomic.Int32
	trayReady           bool
}

// gatewayState is the opt-in serve-pool gateway endpoint.
type gatewayState struct {
	gatewayAddr string
	gatewayBind string
}

// tabsSaveState serializes writes to desktop-tabs.json and its fixed .tmp
// path; the versions fence stale flushes (tabsSaveVersion is protected by
// App.mu, tabsLastWrittenVersion by tabsSaveMu).
type tabsSaveState struct {
	tabsSaveMu             sync.Mutex
	tabsSaveVersion        uint64
	tabsLastWrittenVersion uint64
}

// updaterState guards the single native download/install operation. Checks are
// read-only and may overlap; cache mutation and installation fail fast when
// another updater operation is already active.
type updaterState struct {
	updaterOperationMu sync.Mutex
	updaterOperationID string
}

type App struct {
	ctx          context.Context
	workspaceHub *workspaceChangeHub
	topicState   *topicStateManager
	// topicTitleMutationMu keeps the authoritative title commit and its Tab /
	// session-sidecar publication in the same order for manual and automatic
	// renames. It is never held by generic topic-state reads or other metadata.
	topicTitleMutationMu sync.Mutex

	sessionStoreState

	// bindingNoticeSeen records the task-211 binding-switch banner keys already
	// shown in this process (audit-3 M2: per-process, not per-tab — reopening a
	// settled session in a fresh tab must not warn again). Guarded by a.mu.
	bindingNoticeSeen map[string]bool

	// autonomousMu guards the pending update target staged by the restart_update
	// tool's set_target action (task 254). It is its own mutex, never the tab
	// lock: set_target and execute are separate tool calls and must not race
	// with, or wait behind, tab state.
	autonomousMu      sync.Mutex
	autonomousPending pendingUpdateTarget

	// crashAnalysisMu guards the task-663 one-click analysis state: which
	// session hosts the running analysis (CrashAnalysisProgress read model)
	// and the boot-time pending-crash snapshot that survives flushPendingCrash
	// shipping or dropping the queue files. Low-frequency UI state; its own
	// mutex so it never waits behind the tab lock.
	crashAnalysisMu     sync.Mutex
	crashAnalysisRun    *crashAnalysisRun
	pendingCrashReports []string

	// Task 421: bounded effort re-fetch (see effort_fetch.go). effortCache
	// holds the last completed EffortInfo per tab ID ("" = the active-tab
	// form) so a read that blows the timeout serves this instead of stalling
	// the switch-tab ancillary batch. Display data only. The stub, the
	// limit override and reconcileReadProbe are test-only
	// (catalogReconcileHook convention: set before concurrent calls,
	// zero/nil in production). Task 639: reconcileReadProbe fires right
	// before the effort read path runs a real session reconcile, so tests
	// can count reconcile executions behind the tab memo.
	effortReadStub          func(tabID string) EffortInfo
	effortReadLimitOverride time.Duration
	effortCacheMu           sync.Mutex
	effortCache             map[string]effortCacheEntry
	reconcileReadProbe      func(tabID string)

	// Task 609: per-workspace-root config snapshots for the effort read path
	// (see config_snapshot.go). Keys are cleaned roots; each snapshot reloads
	// itself only when the tracked config files' mtime/size fingerprints
	// change, so warm reads never pay a full config load. Read-path cache
	// only — writes keep going through applyConfigChange / LoadForEdit*.
	cfgSnapshotsMu sync.Mutex
	cfgSnapshots   map[string]*configSnapshot

	// Task 691: memoized session-binding resolves (see
	// binding_resolve_cache.go). One tab switch used to fire 30-70 full
	// session-directory walks (knownSessionDirs + per-dir validation +
	// branch-meta sidecar reads); the cache serves repeat/concurrent callers
	// while every stamped input — registry files, tab-derived dirs, probed
	// session files and sidecars — still fingerprints unchanged, and
	// single-flights the walkers racing on the same path. walkHook is
	// test-only (set before concurrent calls, nil in production).
	bindingResolve bindingResolveCache

	// sessionCatalog is a disposable, asynchronously opened projection of
	// authoritative session sidecars. Project-shell APIs must tolerate nil here:
	// opening, migration, repair, and corruption recovery never gate the UI.
	sessionCatalog     atomic.Pointer[sessioncatalog.Catalog]
	catalogLifecycleMu sync.Mutex
	catalogCancel      context.CancelFunc
	catalogDone        chan struct{}
	catalogRebuildMu   sync.Mutex
	catalogRebuild     *sessionCatalogRebuildFlight
	catalogRebuilding  atomic.Bool
	shuttingDown       atomic.Bool
	// topicIndexWriteFailures counts best-effort topic-index writes that
	// failed (task 550 ①): a swallowed error here used to produce a session
	// whose tab and sidebar could disagree with the persisted index with no
	// trace. Every failure is logged (slog warn) and counted so the
	// topic-inventory reconcile can treat the topic as suspect instead of
	// silently trusting the index.
	topicIndexWriteFailures atomic.Uint64
	// topicInventory caches the three-source reconcile result (task 550 ②).
	topicInventory topicInventoryState
	// perfMonitor is the opt-in host performance sampler (task 184). Nil unless
	// the experiment is on: "off" means no ticker, goroutine or file handle.
	perfMonitor *perfMonitor
	// catalogReconcileJobs coalesces both the legacy pre-scan and catalog scan.
	// Catalog deduplicates its worker; this also prevents callers from
	// stampeding the otherwise-unbounded pre-scan goroutines.
	catalogReconcileMu   sync.Mutex
	catalogReconcileJobs map[string]*desktopCatalogReconcileJob
	// Test-only deterministic boundary, set before concurrent requests.
	catalogReconcileHook func(sessioncatalog.DirectoryTarget)
	// catalogRebuildJoinHook is test-only: it proves concurrent Wails callers
	// joined the published rebuild flight before its completion was released.
	catalogRebuildJoinHook func()
	// projectTreeCatalogRefreshHook is test-only: it proves runtime-only
	// navigation never falls back to the broad catalog refresh path.
	projectTreeCatalogRefreshHook func()
	catalogReconcileDoneHook      func(sessioncatalog.DirectoryTarget)
	// catalogRegisteredProjectRoots bounds activation-triggered discovery to
	// once per project per process. Failed pre-catalog attempts are removed so
	// a later activation retries after the asynchronous catalog opens.
	catalogRegisteredProjectRoots sync.Map

	// taskCtrl is the process-wide task-monitor control service (lazy; see
	// taskControl). One instance serializes control operations in-process.
	taskCtrl     *taskmonitor.ControlService
	taskCtrlOnce sync.Once

	// mu protects the tab map, tabOrder, activeTabID, and per-tab fields that are read
	// from bound methods. All bound methods that touch a controller use activeCtrl().
	mu          sync.RWMutex
	tabs        map[string]*WorkspaceTab
	tabOrder    []string
	activeTabID string
	readyHook   func()
	// tabSelectionMu serializes cross-registry activation. A remote selection
	// must not overtake the local-session snapshot that makes switching safe.
	tabSelectionMu sync.Mutex
	// sessionVersionActivationMu serializes version selection's validation,
	// preference update, and tab rebind so concurrent Wails calls cannot publish
	// a different active version than the one persisted as preferred.
	sessionVersionActivationMu sync.Mutex

	topicActivationTicketState
	// activationEventHook is test-only: when set it replaces the
	// "topic:activation" runtime event emission so tests capture events
	// synchronously. Set before starting concurrent work, never mutate after.
	activationEventHook func(TopicActivationEvent)
	// tabBuildStartHook is test-only: called at the top of every tab
	// controller build (even already-superseded ones) so ordering tests can
	// gate builds. Same set-before-concurrency rule.
	tabBuildStartHook func(tabID string)
	// configLoadForRootHook is test-only: called from the background meta
	// extras refresh so tests can prove MetaForTab itself never loads config.
	configLoadForRootHook func(root string)

	// runtimeByID/runtimeBySessionKey form the process-local ownership registry.
	// App.mu guards both maps and every desktopSessionRuntime field.
	runtimeByID         map[string]*desktopSessionRuntime
	runtimeBySessionKey map[string]*desktopSessionRuntime

	// tabsRestored is closed when restoreOrBuildTabs has finished populating
	// a.tabs from desktop-tabs.json (or built the first-launch tab). Startup
	// work that inspects "which sessions are open" or persists the tab list
	// (recovery GC's DeleteSession does both) must wait on it: running against
	// the pre-restore empty tab map would treat every saved tab's session as
	// closed and could overwrite desktop-tabs.json with an empty snapshot.
	tabsRestored chan struct{}

	// projectTreeChangedHook is test-only: set once before any concurrency
	// starts, then read lock-free from emitProjectTreeChanged (whose callers
	// may or may not hold a.mu, so it cannot re-lock). Never write it after
	// startup.
	projectTreeChangedHook func()
	projectTreeRuntime     projectTreeRuntimeState
	runtimeStateProjection desktopRuntimeProjection
	remoteRuntimeSync      remoteRuntimeSync

	// singleSurfaceMu serializes open/reuse plus visible-tab pruning for the
	// one-conversation layout so overlapping navigation cannot remove the tab
	// another navigation is still activating.
	singleSurfaceMu sync.Mutex

	// worktreeMergeMu serializes the inspect-confirm-merge/finalize mutation
	// boundary. Git identities are still revalidated after workspace leases are
	// acquired; this mutex only prevents duplicate in-process Wails calls.
	worktreeMergeMu sync.Mutex
	// Worktree runtime reservations are ordered before App.mu. Runtime owners
	// hold this gate through final publication; callers must never acquire it
	// under App.mu. Merge reservations cover both the source and isolated roots,
	// while cleanup reservations cover the complete allocation through removal.
	worktreeReservations worktreeRuntimeReservations
	// navigationIntent linearizes frontend intent publication with the final
	// merged-worktree removal before the runtime mutation barrier and App.mu.
	navigationIntent navigationIntentFence

	// sessionRemovalMu serializes operations that remove visible or detached
	// session bindings. Those operations may snapshot controllers before
	// deletion; keep that snapshot outside a.mu, but do not let DeleteSession or
	// topic/workspace removal trash the same files while it is in flight.
	sessionRemovalMu sync.Mutex

	// runtimeRebuildMu serializes controller rebuilds (build + swap), teardown,
	// and MCP lifecycle mutations. Two concurrent rebuilds of the same tab both
	// pass the tab-identity check at swap time, while MCP launch authorization racing
	// a toggle/reconnect can restore stale tools or launch a second single-instance
	// server. MCP paths insert extensionBuildMu between runtimeRebuildMu and
	// runtimeAdmissionMu; both orders end at App.mu -> Host/Registry.
	runtimeRebuildMu sync.Mutex
	// runtimeAdmissionMu is the runtime lifecycle barrier. Foreground turn-start
	// tokens and the short publication phase of asynchronous controller builds
	// hold the read side; runtime teardown and MCP lifecycle mutations hold the
	// write side so their captured controller/Host cannot be replaced, closed, or
	// handed a late turn in flight. Writers already hold runtimeRebuildMu, making
	// them mutually exclusive. Read holders must never acquire runtimeRebuildMu,
	// or a queued writer would deadlock the pair.
	runtimeAdmissionMu sync.RWMutex
	// runtimeMutationBeforeLockHook is test-only. Set it before starting concurrent
	// calls and never mutate it afterward.
	runtimeMutationBeforeLockHook func(string)
	// modelSwitchTimingHook is test-only. Production diagnostics use the same
	// sanitized timing record through debug logging.
	modelSwitchTimingHook func(modelSwitchTiming)
	// rebindCandidateHook is test-only. It exposes deterministic transaction
	// boundaries without weakening the production lock order. Set it before
	// starting a rebind and never mutate it until that rebind returns.
	rebindCandidateHook func(string) error
	// providerCatalogBeforeCredentialLockHook is test-only. It pauses catalog
	// compare-and-apply after its optimistic credential snapshot but before the
	// shared credential lock and authoritative re-read.
	providerCatalogBeforeCredentialLockHook func(string)

	// tryRunMu guards tryRunCancel — the cancel handle for the single
	// in-flight settings-page subagent try run (TrySubagentProfile /
	// CancelTrySubagentProfile).
	tryRunMu     sync.Mutex
	tryRunCancel context.CancelFunc

	// Task 557: registry of running foreground (synchronous) sub-agents per
	// tab, maintained from content-free lifecycle telemetry (see
	// foreground_subagents.go). Foreground children block inside the parent
	// turn and never register as jobs, so the capsule's job-based running
	// list cannot see them; this registry is the third running-work source.
	// Lazy-init under its own mutex — never held together with a.mu.
	foregroundSubagentsMu sync.RWMutex
	foregroundSubagents   map[string]map[string]foregroundSubagentEntry

	updaterState

	// deferredRebuild tracks tabs whose settings were saved but whose runtime
	// could not refresh because the session lease was held by another process.
	deferredRebuild deferredRebuildState

	// collabParkWaiters tracks the per-tab deferred-park goroutines (task 738):
	// collaboration stand-up tabs whose park attempt raced the async controller
	// build. The waiter parks the tab once the runtime publishes; entries are
	// added and removed under a.mu and never persisted.
	collabParkWaiters map[string]bool

	// historySliceMu guards the windowed-history background bookkeeping:
	// single-flight display-index rebuilds for live sessions, the startup
	// index-migration worker's cancel handle, and the idle-prefetch worker's
	// kick channel + cancel handle (任务 451 方案 B). Never held while calling
	// controller or session methods.
	historySliceMu              sync.Mutex
	historyIndexRebuilds        map[string]chan struct{}
	historyIndexMigrationCancel context.CancelFunc
	historyDerived              historyDerivedCache
	historyIdlePrefetchKick     chan struct{}
	historyIdlePrefetchCancel   context.CancelFunc

	// detachedSessions keeps live session runtimes whose visible tab was closed.
	// It is process-local by design: shutdown closes every detached controller.
	detachedSessions map[string]*WorkspaceTab

	// takeoverMirrors tracks sessions this desktop took over from a local
	// serve: the tab writes locally while its events mirror to the remote tab.
	takeoverMirrors        map[string]*takeoverMirror
	takeoverAdoptRevisions map[string]uint64
	takeoverMu             sync.Mutex
	// serveProbeUntil suppresses serve probing after a failed handshake
	// (rotated token file); guarded by serveProbeMu.
	serveProbeUntil map[string]time.Time
	serveProbeMu    sync.Mutex

	// sharedHosts holds one *plugin.Host per workspace root, shared by all
	// controllers/tabs in that root so MCP subprocesses (CodeGraph, etc.) are
	// spawned once instead of N times. Lifecycle: first Acquire creates the
	// host, last Release closes it.
	sharedHosts   map[string]*sharedPluginHost
	sharedHostsMu sync.Mutex
	// extensionGeneration fences off-lock shared-host boot against MCP mutations;
	// stale generations abandon publication instead of restoring old tools.
	extensionGeneration atomic.Uint64
	extensionBuildMu    sync.RWMutex

	tabsSaveState
	// tabsSaveQueue (task 653) defers the desktop-tabs.json write out of the
	// App.mu critical section: saveTabsLocked collects under the lock and
	// enqueues; one flusher goroutine (started by App.startup) coalesces and
	// writes. Before the flusher starts, enqueues fall back to the pre-653
	// synchronous write. See tabs_save_queue.go.
	tabsSaveQueue tabsSaveQueue

	appUiState
	tray               *desktopTray
	desktopShell       desktopShellRuntimeState
	hangWatchdogMu     sync.Mutex
	hangWatchdogCancel context.CancelFunc

	mediaTokens *mediaTokenStore
	botInstalls map[string]*botInstallSession
	botRuntime  *desktopBotRuntime
	// botBridge gives the embedded bot gateway a god view over desktop
	// sessions (/desktop commands). Set once in NewApp before any tab exists,
	// read-only afterwards, so tabEventSink.Emit reads it without a lock.
	botBridge *botBridgeHub

	metrics atomic.Pointer[metricsAggregator] // non-nil only when desktop.metrics is opted in; swapped live by SetDesktopMetrics

	notificationSenderOnce sync.Once
	notificationSender     notify.Sender

	runtimeEvents  asyncRuntimeEmitter
	mcpAppsSandbox mcpAppsSandbox

	// terminals owns local PTY/ConPTY sessions. It is intentionally separate
	// from chat runtimes: terminal lifecycle must never acquire App.mu or the
	// controller rebuild locks while process I/O is blocked.
	terminals *terminalManager

	// Remote SSH module: the manager is created lazily on the first remote
	// binding call and closed on shutdown.
	remoteMu      sync.Mutex
	remoteRuntime remoteKernel

	// Remote web windows (SSH Serve child processes). The main process tracks
	// the live child plus transient handoff processes for each host. Host-scoped
	// lifecycle operations are generation-fenced and serialized so an overlapping
	// disconnect/stop cannot miss a window that is still being spawned. Closing a
	// window releases only its registration, while the remote Serve and the SSH
	// connection keep running. The child deliberately skips local runtimes.
	remoteWindows *remoteWindowRegistry
	servePool     *servepool.Manager
	gatewaySrv    *http.Server
	gatewayState
	// Task 439: the embedded zcode task bus (lab switch
	// experimental_zcode_task_bus, 铁律 2 default off). Nil = not running,
	// the only state a default install ever reaches.
	zcodeTaskBus           *zcodeTaskBusHost
	remoteWindowLifecycles remoteWindowLifecycleRegistry
	remoteWindowOpener     func(remoteWindowLaunch) error // test-only injection
	// Remote project tabs are in-app surfaces bound to a remote workspace.
	// Project pins persist in user config; open tab shells persist separately
	// and restore disconnected until the user activates them.
	remoteTabMu     sync.Mutex
	remoteTabs      map[string]*remoteTab
	remoteTabLayout remoteTabLayoutState
	remoteTabTasks  sync.WaitGroup
	// remoteTabModelMu makes the caller's current-model snapshot, the remote
	// Serve rebuild, and the tab metadata commit one transaction. Without it,
	// overlapping switches could roll remote config back to a stale model.
	remoteTabModelMu          sync.Mutex
	modelSettingsSubmitMu     sync.Mutex
	modelSettingsReceipts     map[string]modelSettingsReceipt
	modelSettingsReceiptOrder []string
	// remoteEventHook observes remote events in tests; production leaves it nil.
	remoteEventHook func(name string, payload any)
	// credProxy is the lazy app-wide key holder for local-proxy mode.
	credProxyMu sync.Mutex
	credProxy   *credentialProxy
	remoteWindowState
	remoteWindow *remoteWindowLaunch

	// promptHistoryTape is a lazy, cursor-addressed view of prompt history. It
	// stores session order and per-session parsed entries only after that session is
	// reached by ↑ navigation. See ScanPromptHistory.
	promptHistoryMu   sync.Mutex
	promptHistoryTape *promptHistoryTape

	skillRootsMu    sync.Mutex
	skillRootsCache skillRootsCache

	heartbeat *HeartbeatEngine // scheduled heartbeat tasks; nil until startup
	// sessionCollab is the task 19 delivery pump: it moves talk_to_session
	// mailbox messages into the target tab's inbox. Nil until startup.
	sessionCollab *sessionCollabPump
	// autopilotResume is the 任务731 abnormal-stop auto-resume watchdog
	// (579 家族第三形态): event-driven bounded continuation for unattended
	// sessions. Nil until startup; every entry point is nil-safe.
	autopilotResume *autopilotResumeWatchdog
	lifecycle       desktopLifecycleRuntime
	// diagnosticsOwner is acquired before Wails starts so Linux's OnStartup
	// ordering cannot let a second-instance handoff create lifecycle evidence.
	diagnosticsState
	// startupReady records that React rendered and the Wails bridge heartbeat
	// succeeded. DOM navigation alone is not application health.
	startupReady     atomic.Bool
	webView2Recovery *webView2RecoveryCoordinator
}

// ModelInfo is one (provider, model) the bottom switcher can pick. Ref ("provider/
// model") is what SetModel takes; Provider/Model are for display.
// DirEntry is one entry in the "@" file-reference menu.
