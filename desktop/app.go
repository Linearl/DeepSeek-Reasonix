package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"reasonix/internal/agent"
	"reasonix/internal/billing"
	"reasonix/internal/boot"
	"reasonix/internal/botruntime"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/eventwire"
	"reasonix/internal/evidence"
	"reasonix/internal/extension/providerext"
	"reasonix/internal/i18n"
	"reasonix/internal/notify"
	"reasonix/internal/plugin"
	"reasonix/internal/provider"
	"reasonix/internal/servepool"
	"reasonix/internal/sessioncatalog"
	"reasonix/internal/taskmonitor"
	"reasonix/internal/tool"
)

// App is the Wails-bound application object: the desktop frontend's command
// surface. Its exported methods (Submit/Cancel/Approve/…) are generated into JS
// bindings. The app manages multiple WorkspaceTabs — each with its own controller
// scoped to a project workspace — and routes commands to the active tab. Events
// flow the other way: each tab's controller emits to a tabEventSink that
// forwards events tagged with tabId to the webview via runtime.EventsEmit.
type App struct {
	ctx          context.Context
	workspaceHub *workspaceChangeHub
	topicState   *topicStateManager
	// topicTitleMutationMu keeps the authoritative title commit and its Tab /
	// session-sidecar publication in the same order for manual and automatic
	// renames. It is never held by generic topic-state reads or other metadata.
	topicTitleMutationMu sync.Mutex

	// sessionStorageMode remembers the conversation-store mode (task 155) this
	// process started with: the settings view compares it against the configured
	// mode to flag a restart that is still pending, and the setter stamps the
	// audit log with it.
	sessionStorageModeMu    sync.Mutex
	sessionStorageBootValue string
	sessionStorageBootSet   bool

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

	// Ticketed topic activation bookkeeping (StartTopicActivation). Guarded by
	// mu. activationGen bumps on every activation-or-supersede so a background
	// completion can tell whether it still owns publication; the pending
	// request/tab pair identifies the in-flight ticketed activation whose
	// completion may still prune and emit "ready".
	activationGen             uint64
	latestActivationRequestID string
	pendingActivationTabID    string
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

	// updaterOperationMu guards the single native download/install operation.
	// Checks are read-only and may overlap; cache mutation and installation fail
	// fast when another updater operation is already active.
	updaterOperationMu sync.Mutex
	updaterOperationID string

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

	// tabsSaveMu serializes writes to desktop-tabs.json and its fixed .tmp path.
	tabsSaveMu             sync.Mutex
	tabsSaveVersion        uint64 // protected by mu; assigned when collecting a snapshot
	tabsLastWrittenVersion uint64 // protected by tabsSaveMu
	// tabsSaveQueue (task 653) defers the desktop-tabs.json write out of the
	// App.mu critical section: saveTabsLocked collects under the lock and
	// enqueues; one flusher goroutine (started by App.startup) coalesces and
	// writes. Before the flusher starts, enqueues fall back to the pre-653
	// synchronous write. See tabs_save_queue.go.
	tabsSaveQueue tabsSaveQueue

	forceQuit           atomic.Bool
	backgroundMaximised atomic.Bool
	desktopLocale       atomic.Int32
	trayReady           bool
	tray                *desktopTray
	desktopShell        desktopShellRuntimeState
	hangWatchdogMu      sync.Mutex
	hangWatchdogCancel  context.CancelFunc

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
	gatewayAddr   string
	gatewayBind   string
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
	// remoteWindowTicket/remoteWindowHostKey are set from argv before Wails
	// starts in a child process. They gate the blank-shell middleware and the
	// startup branches so the child never initializes local runtimes.
	remoteWindowTicket  string
	remoteWindowHostKey string
	// remoteWindowOwnerID scopes child single-instance locks to one primary
	// Desktop process. remoteWindowParentPID is set only in children and lets
	// them exit when that owner (and therefore its SSH tunnel) disappears.
	remoteWindowOwnerID   string
	remoteWindowParentPID int
	// remoteWindowMu serializes ticket consumption and navigation in a child
	// process so a handoff arriving before domReady cannot be overridden by the
	// initial ticket (or vice versa). remoteWindowTicketConsumed makes the
	// initial handoff idempotent because WebKit fires OnDomReady again after the
	// shell navigates to the remote Serve page.
	remoteWindowMu             sync.Mutex
	remoteWindowTicketConsumed bool
	remoteWindow               *remoteWindowLaunch

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
	diagnosticsOwner        bool
	diagnosticsOwnerRelease func()
	diagnosticsConfigLoaded bool
	diagnosticsTelemetry    bool
	// Healthy-update identity is captured before Wails starts. A process may
	// commit only the complete probationary transaction it actually booted from,
	// never a rewritten or later same-version retry.
	healthyUpdateCreatedAt     string
	healthyUpdateTransactionID string
	// startupReady records that React rendered and the Wails bridge heartbeat
	// succeeded. DOM navigation alone is not application health.
	startupReady     atomic.Bool
	webView2Recovery *webView2RecoveryCoordinator
}

func (a *App) clearActiveSessionRuntime(tab *WorkspaceTab, oldCtrl control.SessionAPI) (SessionClearResult, error) {
	if tab == nil || oldCtrl == nil {
		return SessionClearResult{}, fmt.Errorf("workspace is still starting")
	}
	// This is a build+swap of the tab's controller; serialize with the other
	// rebuild paths (see runtimeRebuildMu) so a concurrent model/effort/settings
	// rebuild cannot interleave a second swap. Lock order:
	// runtimeRebuildMu → sessionRemovalMu (no path acquires them in reverse).
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
	newCtrl, err := boot.Build(a.bootContext(), boot.Options{
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
	})
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

// CheckpointMeta summarises one rewind point (a user turn) for the desktop.
// Optional v2 fields use omitempty so older frontends keep reading the rest.
// (preserving the current tab), keeping code intact, and switches to the new tab.
func (a *App) Fork(turn int) (TabMeta, error) {
	return a.ForkForTab("", turn)
}

// ForkForTab forks the requested source tab even if focus changes before the
// backend begins processing the request. The fork becomes active only while the
// source tab still owns focus, so a later tab selection remains authoritative.
func (a *App) ForkForTab(tabID string, turn int) (TabMeta, error) {
	result, err := a.forkForTabWithOptions(tabID, turn, false)
	return result.Tab, err
}

// ForkWorktreeForTab forks the requested source tab into an isolated Git worktree.
func (a *App) ForkWorktreeForTab(tabID string, turn int) (ForkWorktreeResultView, error) {
	return a.forkForTabWithOptions(tabID, turn, true)
}

// SummarizeFrom / SummarizeUpTo compress model context after / before the start
// of a selected turn. Visible history and checkpoints remain unchanged.
func (a *App) SummarizeFrom(turn int) error {
	return a.SummarizeFromForTab("", turn)
}

func (a *App) SummarizeFromForTab(tabID string, turn int) error {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return readOnlyChannelErr()
	}
	if ctrl == nil {
		return nil
	}
	return ctrl.SummarizeFrom(a.ctx, turn)
}

func (a *App) SummarizeUpTo(turn int) error {
	return a.SummarizeUpToForTab("", turn)
}

func (a *App) SummarizeUpToForTab(tabID string, turn int) error {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return readOnlyChannelErr()
	}
	if ctrl == nil {
		return nil
	}
	return ctrl.SummarizeUpTo(a.ctx, turn)
}

type channelSessionRoute struct {
	channel       string
	channelLabel  string
	remoteID      string
	chatType      string
	userID        string
	threadID      string
	sessionSource string
}

type WorkspaceMeta struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Current bool   `json:"current"`
}

func controllerSessionDir(ctrl control.SessionAPI) string {
	if ctrl != nil {
		if dir := ctrl.SessionDir(); dir != "" {
			return dir
		}
	}
	return desktopSessionDir("")
}

func tabSessionDir(tab *WorkspaceTab) string {
	if tab != nil {
		if tab.WorkspaceRoot != "" {
			return desktopSessionDir(tab.WorkspaceRoot)
		}
		if tab.Ctrl != nil {
			if dir := tab.Ctrl.SessionDir(); dir != "" {
				return dir
			}
		}
	}
	return desktopSessionDir("")
}

func tabRuntimeSessionDir(tab *WorkspaceTab) string {
	if tab != nil && tab.Ctrl != nil {
		if dir, ok := safeControllerSessionDir(tab.Ctrl); ok && strings.TrimSpace(dir) != "" {
			if path := strings.TrimSpace(tab.currentSessionPath()); path != "" {
				if _, _, err := validateSessionPath(dir, path); err == nil {
					return dir
				}
			} else {
				return dir
			}
		}
	}
	return tabSessionDir(tab)
}

func (a *App) activeSessionDir() string {
	tab := a.activeTab()
	if path, ok := a.reconcileTabWithPinnedSessionMeta(tab); ok && strings.TrimSpace(path) != "" {
		return filepath.Dir(path)
	}
	if tab != nil && tab.Ctrl != nil {
		return tabRuntimeSessionDir(tab)
	}
	return tabSessionDir(tab)
}

// ListSessions returns the saved sessions newest-first for the history panel,
// marking the one the current conversation is writing to and attaching any
// user-chosen titles.
func (a *App) ListSessions() []SessionMeta {
	dir := a.activeSessionDir()
	return a.listSessionsFromDir(dir, a.activeSessionPath(dir))
}

// ListSessionsForTab returns sessions from the directory owned by tabID. Task
// Monitor uses this stable target after asynchronous control lookups so a tab
// switch cannot redirect the eventual session lookup to another workspace.
func (a *App) ListSessionsForTab(tabID string) []SessionMeta {
	target, err := a.taskMonitorTargetForTab(tabID)
	if err != nil {
		return []SessionMeta{}
	}
	return a.listSessionsFromDir(target.sessionDir, target.sessionPath)
}

func (a *App) listSessionsFromDir(dir, active string) []SessionMeta {
	catalog := a.sessionCatalog.Load()
	if catalog == nil {
		return []SessionMeta{}
	}
	target := sessioncatalog.DirectoryTarget{Path: dir, Scope: "global"}
	for _, candidate := range a.sessionCatalogTargets() {
		if sameProjectRoot(candidate.Path, dir) {
			target = candidate
			break
		}
	}
	records, err := listCatalogSessionsForDirectory(a.bootContext(), catalog, target, dir)
	if err != nil {
		return []SessionMeta{}
	}
	open := a.openSessionPaths(dir)
	channelRoutes := channelSessionRoutesForDir(dir)
	out := make([]SessionMeta, 0, len(records))
	for _, record := range records {
		_, isOpen := open[record.Path]
		meta := sessionMetaFromCatalog(record, record.Path == active, isOpen)
		if route, ok := channelRoutes[sessionRuntimeKey(record.Path)]; ok {
			applyChannelSessionRoute(&meta, route)
		}
		out = append(out, meta)
	}
	return out
}

// ListTrashedSessions returns sessions that were moved to the local trash,
// newest-deleted first. These can be previewed, restored, or permanently purged.
func (a *App) ListTrashedSessions() []SessionMeta {
	out := []SessionMeta{}
	for _, dir := range a.knownSessionDirs() {
		paths, err := listTrashedSessionFiles(dir)
		if err != nil {
			continue
		}
		titles := loadSessionTitles(dir)
		for _, path := range paths {
			infos, err := agent.ListSessions(filepath.Dir(path))
			if err != nil || len(infos) == 0 {
				continue
			}
			deletedAt := trashedSessionDeletedAt(path)
			title := strings.TrimSpace(infos[0].CustomTitle)
			if title == "" {
				title = titles[filepath.Base(path)]
			}
			out = append(out, sessionMetaFromInfo(infos[0], title, false, false, deletedAt, dir))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DeletedAt == out[j].DeletedAt {
			return out[i].LastActivityAt > out[j].LastActivityAt
		}
		return out[i].DeletedAt > out[j].DeletedAt
	})
	return out
}

func (a *App) trashedSessionDir(path string) (string, error) {
	for _, dir := range a.knownSessionDirs() {
		if _, _, _, err := validateTrashedSessionPath(dir, path); err == nil {
			return dir, nil
		}
	}
	return "", fmt.Errorf("trashed session path outside known session dirs: %s", path)
}

func (a *App) sessionDirForPath(path string) (string, string, error) {
	for _, dir := range a.knownSessionDirs() {
		sessionPath, _, err := validateSessionPath(dir, path)
		if err == nil {
			return dir, sessionPath, nil
		}
	}
	return "", "", fmt.Errorf("session path outside known session dirs: %s", path)
}

func applyChannelSessionRoute(meta *SessionMeta, route channelSessionRoute) {
	if meta == nil {
		return
	}
	meta.Kind = "channel"
	meta.Channel = route.channel
	meta.ChannelLabel = route.channelLabel
	meta.RemoteID = route.remoteID
	meta.ChatType = route.chatType
	meta.UserID = route.userID
	meta.ThreadID = route.threadID
	meta.SessionSource = route.sessionSource
}

func channelSessionRoutesForDir(dir string) map[string]channelSessionRoute {
	userPath := config.UserConfigPath()
	if strings.TrimSpace(userPath) == "" {
		return nil
	}
	cfg := config.LoadForEdit(userPath)
	out := map[string]channelSessionRoute{}
	for _, conn := range cfg.Bot.Connections {
		channel := strings.TrimSpace(conn.Provider)
		if channel == "" {
			continue
		}
		channelLabel := strings.TrimSpace(conn.Label)
		if channelLabel == "" {
			channelLabel = channelDisplayName(channel, conn.Domain)
		}
		for _, mapping := range conn.SessionMappings {
			if strings.TrimSpace(mapping.SessionSource) != "auto" {
				continue
			}
			sessionPath := botSessionPathTarget(mapping.SessionID)
			if sessionPath == "" {
				continue
			}
			validPath, _, err := validateSessionPath(dir, sessionPath)
			if err != nil {
				continue
			}
			key := sessionRuntimeKey(validPath)
			if key == "" {
				continue
			}
			out[key] = channelSessionRoute{
				channel:       channel,
				channelLabel:  channelLabel,
				remoteID:      strings.TrimSpace(mapping.RemoteID),
				chatType:      strings.TrimSpace(mapping.ChatType),
				userID:        strings.TrimSpace(mapping.UserID),
				threadID:      strings.TrimSpace(mapping.ThreadID),
				sessionSource: strings.TrimSpace(mapping.SessionSource),
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func botSessionPathTarget(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(sessionID), "path:") {
		return strings.TrimSpace(sessionID[5:])
	}
	if strings.HasSuffix(sessionID, ".jsonl") || strings.Contains(sessionID, "/") || strings.Contains(sessionID, `\`) || strings.HasPrefix(sessionID, "~") {
		return sessionID
	}
	return ""
}

func channelDisplayName(provider, domain string) string {
	provider = strings.TrimSpace(provider)
	domain = strings.TrimSpace(domain)
	switch provider {
	case "feishu":
		if strings.EqualFold(domain, "lark") {
			return "Lark"
		}
		return "Feishu"
	case "weixin":
		return "WeChat"
	case "qq":
		return "QQ"
	case "dingtalk":
		return "DingTalk"
	default:
		return provider
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
	// Task 715 (714 复核 B4): this funnel is the one point every removal flow
	// (delete, topic archive, collab delete) closes a removed session through —
	// drop its replayed-graph cache entry here so a deleted or archived session
	// stops pinning its DAG state. A session that survives only reloads on next
	// open (the entry is a pure accelerator), so over-invalidation is safe.
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

// RestoreSession moves a trashed session back into the saved-session list.
func (a *App) RestoreSession(path string) error {
	return friendlySessionFileError(a.restoreSession(path))
}

func (a *App) restoreSession(path string) error {
	dir, err := a.trashedSessionDir(path)
	if err != nil {
		return err
	}
	_, key, _, err := validateTrashedSessionPath(dir, path)
	if err != nil {
		return err
	}
	// The destroying/open checks and the trash-entry move must not interleave
	// with DeleteSession/TrashTopic trashing the same entry.
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()
	target := filepath.Join(dir, key)
	if a.sessionDestroying(dir, target) {
		return fmt.Errorf("session cleanup is still in progress: %s", key)
	}
	// A committed archive may have moved the transcript into trash while a
	// Windows file handle temporarily kept one of its sidecars at the live
	// path. The durable cleanup marker makes that partial move recoverable.
	// Finish it before restore preflights the live destinations; otherwise the
	// leftover sidecar is misreported as an unrelated restore conflict.
	if agent.IsCleanupPending(target) {
		_ = reconcileDesktopCleanupPending(dir)
		if agent.IsCleanupPending(target) {
			return fmt.Errorf("session cleanup is still in progress: %s", key)
		}
	}
	if a.sessionOpen(dir, target) {
		return fmt.Errorf("session is open: %s", key)
	}
	if err := restoreTrashedSessionFile(dir, path); err != nil {
		return err
	}
	if err := restoreSessionTopicIndex(dir, target); err != nil {
		return err
	}
	a.requestSessionCatalogPath("", "", target)
	a.emitProjectTreeChangedForSessionDirs(dir)
	a.invalidatePromptHistoryCache()
	return nil
}

func (a *App) sessionDestroying(dir, sessionPath string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || tab.Ctrl == nil || tabRuntimeSessionDir(tab) != dir {
			continue
		}
		if tab.Ctrl.IsDestroyingSession(sessionPath) {
			return true
		}
	}
	return false
}

func (a *App) sessionOpen(dir, sessionPath string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, tab := range a.runtimeTabsLocked() {
		if tabMatchesSession(tab, dir, sessionPath) {
			return true
		}
	}
	return false
}

// PurgeTrashedSession permanently removes a trashed session and its title/display
// sidecars.
func (a *App) PurgeTrashedSession(path string) error {
	return friendlySessionFileError(a.purgeTrashedSession(path, false))
}

// PurgeRecoveryCopy is the guarded permanent-cleanup path. A trashed branch is
// rechecked against its live parent; missing, stale, or divergent data is kept.
func (a *App) PurgeRecoveryCopy(path string) error {
	return friendlySessionFileError(a.purgeTrashedSession(path, true))
}

func (a *App) purgeTrashedSession(path string, requireRedundantRecovery bool) error {
	dir, err := a.trashedSessionDir(path)
	if err != nil {
		return err
	}
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()
	var parentGuard *agent.SessionRemovalGuard
	if requireRedundantRecovery {
		parentGuard, err = agent.TryAcquireRecoveryParentGuard(path, dir)
		if err != nil {
			switch {
			case errors.Is(err, agent.ErrRecoveryBranchNotCovered):
				return errRecoveryCopyNotRedundant
			case errors.Is(err, agent.ErrSessionLeaseHeld):
				return errSessionBusyElsewhere
			default:
				return err
			}
		}
		defer parentGuard.Release()
	}
	if err := purgeTrashedSessionFile(dir, path); err != nil {
		return err
	}
	a.invalidatePromptHistoryCache()
	return nil
}

// RenameSession sets a custom display name for a session (empty clears it back to
// the preview). The transcript file is unchanged; the canonical name lives in
// the branch meta sidecar, with the legacy .titles.json map kept as a
// compatibility write-through for older desktop data paths.
func (a *App) RenameSession(path, title string) error {
	dir := a.activeSessionDir()
	if _, _, err := validateSessionPath(dir, path); err != nil {
		resolvedDir, _, resolveErr := a.sessionDirForPath(path)
		if resolveErr != nil {
			return errors.New("session version is unavailable")
		}
		dir = resolvedDir
	}
	return friendlySessionFileError(a.renameSessionInDir(dir, path, title))
}

func (a *App) renameSessionInDir(dir, path, title string) error {
	sessionPath, _, err := validateSessionPath(dir, path)
	if err != nil {
		return err
	}
	if err := agent.RenameSession(sessionPath, title); err != nil {
		return err
	}
	return a.onSessionTitleChanged(dir, sessionPath, title)
}

func (a *App) renameSessionInDirIfTitleUnchanged(dir, path, expectedTitle, title string) error {
	sessionPath, _, err := validateSessionPath(dir, path)
	if err != nil {
		return err
	}
	if err := agent.RenameSessionIfTitleUnchanged(sessionPath, expectedTitle, title); err != nil {
		return err
	}
	return a.onSessionTitleChanged(dir, sessionPath, title)
}

// onSessionTitleChanged projects the canonical BranchMeta custom title into
// the legacy desktop map and live catalog/UI indexes. The session directory is
// supplied by the owning boot so background tabs never route through whichever
// tab happens to be active when the tool finishes.
func (a *App) onSessionTitleChanged(dir, sessionPath, title string) error {
	validated, _, err := validateSessionPath(dir, sessionPath)
	if err != nil {
		return err
	}
	if err := syncSessionTitleFromBranchMeta(dir, validated); err != nil {
		return err
	}
	// Fork (#8280 residual): the project tree's authoritative topic label
	// comes from the Topic-level titles store, which RenameSession never
	// updated — the sidebar kept the last auto-generated name until some
	// later catalog rebuild, and catalog state flips surfaced the new title
	// only transiently (the "rename flash-back"). Project the manual title
	// into the Topic-level store and live tabs so both rename entries write
	// one visible name. Best-effort: the Session-level rename already
	// succeeded, and a projection miss (catalog-only topic, index not built
	// yet) must not fail the whole rename.
	if trimmed := strings.TrimSpace(title); trimmed != "" {
		if topicID, workspaceRoot, ok := topicIDForSessionPath(dir, validated); ok {
			_ = setTopicTitleWithSource(workspaceRoot, topicID, trimmed, topicTitleSourceManual)
			a.updateOpenTopicTitle(topicID, trimmed, topicTitleSourceManual)
		}
	}
	a.requestSessionCatalogPath("", "", validated)
	a.invalidatePromptHistoryCache()
	a.emitProjectTreeChangedForSessionDirs(dir)
	return nil
}

// ResumeSession snapshots the current conversation, then loads the session at
// path and continues it on the active tab. The model and working folder are
// unchanged; only the transcript is swapped. Returns the resumed messages for
// the frontend to render.
func (a *App) ResumeSession(path string) ([]HistoryMessage, error) {
	return a.ResumeSessionForTab("", path)
}

func (a *App) ResumeSessionPage(path string, limit int) (HistoryPage, error) {
	return a.ResumeSessionPageForTab("", path, limit)
}

func (a *App) ResumeSessionPageForTab(tabID, path string, limit int) (HistoryPage, error) {
	return a.resumeSessionPageForTab(tabID, path, limit)
}

// ResumeSessionForTab is the tab-scoped form of ResumeSession. A saved session
// path is a runtime identity, so changing to a different path must replace the
// tab's controller binding rather than mutating the current controller in place.
func (a *App) ResumeSessionForTab(tabID, path string) ([]HistoryMessage, error) {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if tab == nil || ctrl == nil {
		return []HistoryMessage{}, fmt.Errorf("tab is not ready")
	}
	if continued := a.continuePathForOpen(path); continued != "" {
		path = continued
	}
	sessionPath, _, err := validateSessionPath(controllerSessionDir(ctrl), path)
	if err != nil {
		return nil, err
	}
	if sessionRuntimeKey(tab.currentSessionPath()) == sessionRuntimeKey(sessionPath) {
		a.mu.RLock()
		takeoverSpectator := a.tabs[tab.ID] == tab && tab.Takeover.Spectator
		a.mu.RUnlock()
		if takeoverSpectator {
			return nil, fmt.Errorf("session is held by the remote side; use TakeoverSession to reclaim it")
		}
		a.setTabReadOnly(tab.ID, false)
		// A read-only transcript explicitly reopened for writing re-announces
		// itself so a resident Serve can mirror and later reclaim it.
		a.attachTakeoverMirror(tab.ID, sessionPath)
		go a.adoptSessionFromLocalServe(tab.ID, sessionPath)
		return a.HistoryForTab(tabID), nil
	}
	// Task 196 second round: switching tabs also resumes through here, and the
	// user reports slow switching *out of* a long session - so both directions
	// need the same decomposition as resumeSessionPageForTab.
	resumeStart := time.Now()
	loaded, err := loadResumableSession(sessionPath)
	loadMs := time.Since(resumeStart).Milliseconds()
	if err != nil {
		slog.Info("desktop: resume session stages", "tab", tabID, "path", sessionPath,
			"load_ms", loadMs, "rebind_ms", int64(0), "history_ms", int64(0),
			"total_ms", time.Since(resumeStart).Milliseconds(), "error", err.Error())
		return nil, err
	}

	rebindStart := time.Now()
	if err := a.rebindTabToLoadedSessionPath(tab, sessionPath, loaded); err != nil {
		slog.Info("desktop: resume session stages", "tab", tabID, "path", sessionPath,
			"load_ms", loadMs, "rebind_ms", time.Since(rebindStart).Milliseconds(), "history_ms", int64(0),
			"total_ms", time.Since(resumeStart).Milliseconds(), "error", err.Error())
		return nil, err
	}
	rebindMs := time.Since(rebindStart).Milliseconds()
	a.setTabReadOnly(tab.ID, false)
	a.attachTakeoverMirror(tab.ID, sessionPath)
	go a.adoptSessionFromLocalServe(tab.ID, sessionPath)
	historyStart := time.Now()
	messages := a.HistoryForTab(tabID)
	slog.Info("desktop: resume session stages", "tab", tabID, "path", sessionPath,
		"load_ms", loadMs, "rebind_ms", rebindMs, "history_ms", time.Since(historyStart).Milliseconds(),
		"total_ms", time.Since(resumeStart).Milliseconds())
	return messages, nil
}

// validateChannelSessionPath 校验 bot/channel 会话路径：channel 会话可能位于
// 当前 controller 的 session dir（project scope）或全局 session dir
// （global scope），单 tab 无法同时覆盖两者，因此都放行。
func validateChannelSessionPath(ctrlDir, path string) (string, string, error) {
	if p, b, err := validateSessionPath(ctrlDir, path); err == nil {
		return p, b, nil
	}
	if globalDir := config.SessionDir(); globalDir != "" && globalDir != ctrlDir {
		if p, b, err := validateSessionPath(globalDir, path); err == nil {
			return p, b, nil
		}
	}
	return validateSessionPath(ctrlDir, path)
}

func (a *App) OpenChannelSessionForTab(tabID, path string) ([]HistoryMessage, error) {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if tab == nil || ctrl == nil {
		return []HistoryMessage{}, fmt.Errorf("tab is not ready")
	}
	sessionPath, _, err := validateChannelSessionPath(controllerSessionDir(ctrl), path)
	if err != nil {
		return nil, err
	}
	loaded, err := loadResumableSession(sessionPath)
	if err != nil {
		return nil, err
	}
	if sessionRuntimeKey(tab.currentSessionPath()) != sessionRuntimeKey(sessionPath) {
		if err := a.rebindTabToLoadedSessionPath(tab, sessionPath, loaded); err != nil {
			return nil, err
		}
	}
	a.setTabReadOnly(tab.ID, true)
	return a.HistoryForTab(tab.ID), nil
}

func (a *App) OpenChannelSessionPageForTab(tabID, path string, limit int) (HistoryPage, error) {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if tab == nil || ctrl == nil {
		return HistoryPage{}, fmt.Errorf("tab is not ready")
	}
	sessionPath, _, err := validateChannelSessionPath(controllerSessionDir(ctrl), path)
	if err != nil {
		return HistoryPage{}, err
	}
	loaded, err := loadResumableSession(sessionPath)
	if err != nil {
		return HistoryPage{}, err
	}
	if sessionRuntimeKey(tab.currentSessionPath()) != sessionRuntimeKey(sessionPath) {
		if err := a.rebindTabToLoadedSessionPath(tab, sessionPath, loaded); err != nil {
			return HistoryPage{}, err
		}
	}
	a.setTabReadOnly(tab.ID, true)
	return a.HistoryPageForTab(tab.ID, 0, limit), nil
}

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
	// Session rebinding is a candidate transaction. Keep the source controller,
	// lease, runtime key, epoch, and profile live until the target controller has
	// built, restored, validated, and acquired its own lease. The lifecycle
	// barrier blocks new turns and startup publication across the transaction.
	a.runtimeRebuildMu.Lock()
	defer a.runtimeRebuildMu.Unlock()

	// Fence an in-flight startup before waiting for the admission barrier. Startup
	// work now runs outside that barrier and will discard itself at its short
	// publication check once this generation is superseded. App.mu is released
	// before barrier acquisition, so no inverted lock nesting is introduced.
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

	// If the target session has a detached runtime (from a recent running-session
	// detach), reattach it instead of building a new controller. This avoids the
	// Windows LockFileEx/LOCKFILE_EXCLUSIVE_LOCK conflict where a second handle
	// from the same process cannot lock a file already held by the detached
	// controller's fd (#6955).
	targetKey := sessionRuntimeKey(sessionPath)
	a.mu.Lock()
	detached := a.detachedSessions[targetKey]
	hasDetached := detached != nil && detached.Ctrl != nil
	a.mu.Unlock()

	if hasDetached {
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
	// Wiring a mirror inspects App state under a read lock, so defer it until
	// after this transaction releases App.mu. The same applies to asynchronous
	// adoption: publishing the committed identity first lets its stale-result
	// fence observe one coherent runtime generation.
	shouldAdopt := !tab.ReadOnly
	if detachSource {
		a.newSessionRuntimeLocked(tab, transition.targetKey)
	}
	newEpoch := a.advanceSessionRuntimeEpochLocked(tab)
	a.saveTabsLocked()
	candidate.ctrl = nil
	candidate.sink = nil
	committed = true
	a.mu.Unlock()
	a.attachTakeoverMirror(tab.ID, sessionPath)
	if shouldAdopt {
		go a.adoptSessionFromLocalServe(tab.ID, sessionPath)
	}
	// Test-only observation point: the replacement is committed but the retired
	// sink still carries its old epoch. Production has no hook and immediately
	// fences that sink below.
	_ = a.runRebindCandidateHook("committed")

	// Teardown happens after publication and outside App.mu. The old lease is
	// released only now, so every target failure above leaves source ownership
	// intact. Fence the retired sink before closing the old controller so a
	// close-time event cannot mutate or autosave the replacement runtime.
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
	// Task 363A: feed the pooled assembly (same root+model+effort) so prompt/
	// skills/commands/hooks discovery is skipped on the second same-key tab;
	// store what this build assembled for the next one. Gate-off keeps the
	// legacy path byte-identical (acquire returns nil, store is a no-op).
	effortKey := ""
	if source.effort != nil {
		effortKey = *source.effort
	}
	assemblyKey := runtimeAssemblyKeyForTab(root, model, effortKey)
	ctrl, assembly, err := boot.BuildWithAssembly(a.bootContext(), boot.Options{
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
	})
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
	// A freshly-built controller starts with an empty tier (normalize→light).
	// Propagate the tab's session-level tier so a new session honors the
	// global default and a rebound session keeps its last chosen tier; the
	// controller persists it to BranchMeta on the first turn. Skip a blank
	// tab.subagentPolicy (normalize would collapse it to "light" and wrongly
	// call SetSubagentPolicy with an empty string).
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
	// First paint: a very large log is opened from its trailing window and the
	// reader pages the rest in, instead of blocking the tab on a full replay
	// (task 187). Every other LoadSession caller - recovery, GC, migration,
	// export - still gets the complete transcript.
	return agent.LoadSessionTail(sessionPath)
}

// PreviewSession reads a saved session for display only. It does not snapshot or
// swap the active controller, so the history drawer can call it while a turn runs.
func (a *App) PreviewSession(path string) ([]HistoryMessage, error) {
	sessionDir, sessionPath, err := a.sessionDirForPath(path)
	if err != nil {
		return nil, err
	}
	return previewSessionMessages(sessionDir, sessionPath)
}

func (a *App) ListWorkspaces() []WorkspaceMeta {
	migrateLegacyWorkspacesIntoProjects()
	activeRoot := ""
	cur, _ := os.Getwd()
	a.mu.RLock()
	if tab := a.activeTabLocked(); tab != nil && tab.WorkspaceRoot != "" {
		activeRoot = normalizeProjectRoot(tab.WorkspaceRoot)
	}
	a.mu.RUnlock()
	if activeRoot == "" {
		activeRoot = normalizeProjectRoot(cur)
	}
	projects := loadProjectsFile().Projects
	out := make([]WorkspaceMeta, 0, len(projects))
	for _, project := range projects {
		out = append(out, WorkspaceMeta{
			Path:    project.Root,
			Name:    projectDisplayName(project),
			Current: activeRoot != "" && sameProjectRoot(project.Root, activeRoot),
		})
	}
	return out
}

func (a *App) RemoveWorkspace(dir string) error {
	if dir == "" {
		return fmt.Errorf("workspace path is required")
	}
	dir = normalizeProjectRoot(dir)

	var fallback *WorkspaceTab
	// sessionRemovalMu covers every step that can still touch this workspace's
	// session files: snapshotting, unlinking the tab/runtime bindings, and
	// closing the unlinked runtimes (quiescing autosave). Once a runtime is
	// unlinked from a.tabs/detachedSessions it is invisible to
	// DeleteSession/TrashTopic/RestoreSession, so it must stop writing before
	// the lock is released. Project bookkeeping, the fallback controller build,
	// and notifications run after release.
	if err := func() error {
		defer a.lockRuntimeMutation("remove-workspace")()
		a.sessionRemovalMu.Lock()
		defer a.sessionRemovalMu.Unlock()

		type workspaceTabCandidate struct {
			id  string
			tab *WorkspaceTab
		}

		var closeTabs []*WorkspaceTab
		var closeDetached []*WorkspaceTab
		a.mu.Lock()
		for _, tab := range a.tabs {
			if tabInWorkspace(tab, dir) && tab.hasActiveRuntimeWork() {
				a.mu.Unlock()
				return fmt.Errorf("workspace has running sessions; stop them before removing")
			}
		}
		for _, tab := range a.detachedSessions {
			if tabInWorkspace(tab, dir) && tab.hasActiveRuntimeWork() {
				a.mu.Unlock()
				return fmt.Errorf("workspace has running sessions; stop them before removing")
			}
		}
		candidates := make([]workspaceTabCandidate, 0)
		for id, tab := range a.tabs {
			if !tabInWorkspace(tab, dir) {
				continue
			}
			candidates = append(candidates, workspaceTabCandidate{id: id, tab: tab})
		}
		a.mu.Unlock()

		snapshotted := make(map[string]*WorkspaceTab, len(candidates))
		for _, candidate := range candidates {
			id, tab := candidate.id, candidate.tab
			snapshotted[id] = tab
			if err := a.snapshotTabDurable(tab); err != nil {
				slog.Warn("desktop: snapshot before removing workspace failed", "tab", id, "workspace", dir, "err", err)
				return fmt.Errorf("save current session before removing workspace: %w", err)
			}
		}

		a.mu.Lock()
		for _, tab := range a.tabs {
			if tabInWorkspace(tab, dir) && tab.hasActiveRuntimeWork() {
				a.mu.Unlock()
				return fmt.Errorf("workspace has running sessions; stop them before removing")
			}
		}
		for _, tab := range a.detachedSessions {
			if tabInWorkspace(tab, dir) && tab.hasActiveRuntimeWork() {
				a.mu.Unlock()
				return fmt.Errorf("workspace has running sessions; stop them before removing")
			}
		}
		for id, tab := range a.tabs {
			if tabInWorkspace(tab, dir) && snapshotted[id] != tab {
				a.mu.Unlock()
				return fmt.Errorf("workspace tabs changed while removing; retry")
			}
		}
		for _, candidate := range candidates {
			id, tab := candidate.id, candidate.tab
			if tab == nil || a.tabs[id] != tab || !tabInWorkspace(tab, dir) {
				continue
			}
			a.markTabRemovedLocked(tab)
			closeTabs = append(closeTabs, tab)
			delete(a.tabs, id)
			a.removeTabOrderLocked(id)
			if a.activeTabID == id {
				a.activeTabID = ""
			}
		}
		for key, tab := range a.detachedSessions {
			if !tabInWorkspace(tab, dir) {
				continue
			}
			closeDetached = append(closeDetached, tab)
			delete(a.detachedSessions, key)
		}
		if len(a.tabs) == 0 {
			fallback = a.createTabEntry("global", globalTabWorkspaceRoot(), "")
			fallback.TopicTitle = "Global"
			fallback.sink = &tabEventSink{tabID: fallback.ID, app: a, ctx: a.ctx}
			a.tabs[fallback.ID] = fallback
			a.tabOrder = append(a.tabOrder, fallback.ID)
			a.activeTabID = fallback.ID
		} else if a.activeTabID == "" {
			if ordered := a.orderedTabIDsLocked(); len(ordered) > 0 {
				a.activeTabID = ordered[0]
			}
		}
		a.saveTabsLocked()
		a.mu.Unlock()

		for _, tab := range closeTabs {
			a.closeTabRuntimeAdmissionHeld(tab)
		}
		for _, tab := range closeDetached {
			a.closeTabRuntimeAdmissionHeld(tab)
		}
		return nil
	}(); err != nil {
		return err
	}

	// The fallback tab is already linked into a.tabs; its controller build is
	// asynchronous and does not touch removed session files, so it does not
	// need the removal lock.
	if fallback != nil {
		a.startTabControllerBuild(fallback)
	}

	forgetWorkspace(dir)
	if err := removeProject(dir); err != nil {
		return err
	}
	// If the removed workspace was the active one, clear the pointer
	// so we don't leave a stale reference to a deleted project.
	if loadWorkspace() == dir {
		if remaining := loadProjectsFile(); len(remaining.Projects) > 0 {
			// Fall back to the first remaining project
			saveWorkspace(remaining.Projects[0].Root)
		} else {
			// No projects left; clear the active pointer entirely
			clearWorkspace()
		}
	}
	a.emitProjectTreeMetadataChanged()
	return nil
}

func migrateLegacyWorkspacesIntoProjects() {
	legacy := loadWorkspaces()
	if len(legacy) == 0 {
		return
	}
	_ = updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		seen := make(map[string]bool, len(f.Projects)+len(legacy))
		for _, p := range f.Projects {
			seen[projectRootKey(p.Root)] = true
		}
		changed := false
		for _, path := range legacy {
			root := normalizeProjectRoot(path)
			key := projectRootKey(root)
			if root == "" || seen[key] {
				continue
			}
			f.Projects = append(f.Projects, desktopProject{Root: root})
			seen[key] = true
			changed = true
		}
		return changed, nil
	})
}

func workspaceName(path string) string {
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return path
	}
	return name
}

// tabWorkspaceNameForScope resolves the display name for a tab's workspace.
// Callers pass tab.Scope copied under a.mu instead of re-reading the tab.
func tabWorkspaceNameForScope(scope, cwd string) string {
	if scope == "global" {
		return globalProjectTitle()
	}
	return workspaceName(cwd)
}

func (a *App) SwitchWorkspace(dir string) (string, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = home
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}

	// Open a registered topic so the new workspace appears in the project tree
	// immediately instead of only existing as an in-memory tab.
	topic, err := a.CreateTopic("project", dir, "")
	if err != nil {
		return "", err
	}
	var meta TabMeta
	if a.singleSurfaceLayoutEnabled() {
		meta, err = a.ActivateTopic("project", dir, topic.ID, "")
	} else {
		meta, err = a.OpenProjectTab(dir, topic.ID)
	}
	if err != nil {
		return "", err
	}
	return meta.WorkspaceRoot, nil
}

func (a *App) singleSurfaceLayoutEnabled() bool {
	cfg, _, err := a.loadDesktopUserConfigForView()
	if err != nil {
		return true
	}
	return singleSurfaceLayoutStyle(cfg.DesktopLayoutStyle())
}

// HistoryMessage is one prior turn, for the frontend to repopulate its transcript
// after a reload.
type HistoryMessage struct {
	CompletionReceipt  *eventwire.CompletionReceipt `json:"completionReceipt,omitempty"`
	CompletionSummary  *eventwire.CompletionSummary `json:"completionSummary,omitempty"`
	TurnID             string                       `json:"turnId,omitempty"`
	Role               string                       `json:"role"`
	Content            string                       `json:"content"`
	Detail             string                       `json:"detail,omitempty"`
	Code               string                       `json:"code,omitempty"`
	SubmitText         string                       `json:"submitText,omitempty"`
	CheckpointTurn     *int                         `json:"checkpointTurn,omitempty"`
	CreatedAt          int64                        `json:"createdAt,omitempty"`
	Reasoning          string                       `json:"reasoning,omitempty"`
	MemoryCitations    []provider.MemoryCitation    `json:"memoryCitations,omitempty"`
	WorkDurationMs     int64                        `json:"workDurationMs,omitempty"`
	Level              string                       `json:"level,omitempty"`
	ToolCalls          []HistoryToolCall            `json:"toolCalls,omitempty"`
	ToolCallID         string                       `json:"toolCallId,omitempty"`
	ToolName           string                       `json:"toolName,omitempty"`
	ToolResultArchived bool                         `json:"toolResultArchived,omitempty"`
	ToolResultError    string                       `json:"toolResultError,omitempty"`
	// Execution is local shell metadata restored onto ToolCards after history
	// reload. Omitted when absent so older frontends ignore it safely.
	Execution        *provider.ToolExecution          `json:"execution,omitempty"`
	Pending          bool                             `json:"pending,omitempty"`
	Trigger          string                           `json:"trigger,omitempty"`
	Messages         int                              `json:"messages,omitempty"`
	Summary          string                           `json:"summary,omitempty"`
	Archive          string                           `json:"archive,omitempty"`
	DecisionReceipt  *provider.DecisionReceipt        `json:"decisionReceipt,omitempty"`
	Readiness        *event.FinalReadiness            `json:"readiness,omitempty"`
	ReadPause        *provider.ReadPause              `json:"readPause,omitempty"`
	ReadCompletion   *provider.ReadCompletion         `json:"readCompletion,omitempty"`
	ProtocolRecovery *provider.ProtocolRecoveryAction `json:"protocolRecovery,omitempty"`
	Diagnostic       *provider.FailureDiagnostic      `json:"diagnostic,omitempty"`
	ServerSearch     []provider.ServerSearchCall      `json:"serverSearch,omitempty"`
}

func interruptedTurnHistoryNotice(recovery *provider.InterruptedTurnRecovery) HistoryMessage {
	if recovery != nil && recovery.TerminalStatus == "failed" {
		diagnostic := recovery.FailureDiagnostic
		message := "The provider request failed. Check the connection settings and try again."
		detail := provider.FailureDiagnosticDetail(diagnostic)
		// Task 340 (upstream #10778): the summary carries the real provider
		// error - status line and response-body snippet, credential-scrubbed
		// when the record was written. Without it a reload names only the
		// status class and drops the actual cause.
		if recovery.FailureSummary != "" {
			if detail != "" {
				detail += "\n"
			}
			detail += recovery.FailureSummary
		}
		if diagnostic != nil {
			if statusMessage := i18n.M.ProviderStatusMessage(diagnostic.Status); statusMessage != "" {
				message = statusMessage
			} else if diagnostic.Status > 0 {
				message = fmt.Sprintf("Provider request failed (HTTP %d).", diagnostic.Status)
			}
			label := provider.ProviderDisplayLabel(diagnostic.ProviderID, diagnostic.ProviderDisplayName, diagnostic.Protocol)
			if label != "" {
				message = label + ": " + message
			}
		}
		return HistoryMessage{Role: "notice", Level: "warn", Code: event.NoticeCodeProviderRequestFailed, Content: message, Detail: detail, Diagnostic: diagnostic}
	}
	return HistoryMessage{
		Role: "notice", Level: "info", Code: event.NoticeCodeCancelledTurn,
		Content: "This turn was interrupted. Partial output is kept for reference; only completed tool pairs and a bounded recovery summary enter the next model turn. Inspect the workspace before continuing or reverting changes.",
	}
}

type HistoryToolCall struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Arguments         string `json:"arguments"`
	ResolvedName      string `json:"resolvedName,omitempty"`
	CapabilityID      string `json:"capabilityId,omitempty"`
	ResolvedReadOnly  *bool  `json:"resolvedReadOnly,omitempty"`
	Subject           string `json:"subject,omitempty"`
	Summary           string `json:"summary,omitempty"`
	Diff              string `json:"diff,omitempty"`
	Added             int    `json:"added,omitempty"`
	Removed           int    `json:"removed,omitempty"`
	ArgumentsArchived bool   `json:"argumentsArchived,omitempty"`
}

const (
	defaultHistoryPageTurns = 60
	maxHistoryPageTurns     = 200
)

type HistoryPage struct {
	Messages   []HistoryMessage `json:"messages"`
	StartTurn  int              `json:"startTurn"`
	EndTurn    int              `json:"endTurn"`
	TotalTurns int              `json:"totalTurns"`
	HasOlder   bool             `json:"hasOlder"`
	Revision   int64            `json:"revision,omitempty"`
	Digest     string           `json:"digest,omitempty"`
}

// historyProviderMessagesWithPersistedTimes overlays legacy event-record
// timestamps onto a copy for display. It deliberately leaves the controller's
// provider transcript untouched: timestamp migration must not change session
// digests, conflict detection, or model-request cache prefixes.
func historyProviderMessagesWithPersistedTimes(msgs []provider.Message, sessionPath string) []provider.Message {
	if len(msgs) == 0 || strings.TrimSpace(sessionPath) == "" {
		return msgs
	}
	needsPersistedTime := false
	for _, msg := range msgs {
		if msg.CreatedAt <= 0 && agent.IsUserAuthoredTurnMessage(msg) {
			needsPersistedTime = true
			break
		}
	}
	if !needsPersistedTime {
		return msgs
	}
	tr := beginSilentLoader("history_time_backfill", sessionPath)
	users, err := agent.LoadSessionUserMessages(sessionPath)
	if err != nil {
		tr.finish(err, -1)
		return msgs
	}
	if len(users) == 0 {
		tr.finish(nil, 0)
		return msgs
	}
	tr.finish(nil, len(users))
	out := append([]provider.Message(nil), msgs...)
	userIndex := 0
	for i := range out {
		if out[i].Role != provider.RoleUser || agent.IsPinnedContextRevision(out[i]) {
			continue
		}
		if userIndex >= len(users) {
			break
		}
		user := users[userIndex]
		userIndex++
		if out[i].CreatedAt <= 0 && !user.At.IsZero() {
			out[i].CreatedAt = user.At.UnixMilli()
		}
	}
	return out
}

// History returns the session's message log.
func (a *App) History() []HistoryMessage {
	return a.HistoryForTab("")
}

func (a *App) HistoryPage(beforeTurn, limit int) HistoryPage {
	return a.HistoryPageForTab("", beforeTurn, limit)
}

func (a *App) HistoryPageForTab(tabID string, beforeTurn, limit int) HistoryPage {
	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	var ctrl control.SessionAPI
	var sessionDir, sessionPath string
	if tab != nil {
		ctrl = tab.Ctrl
		sessionDir = tabSessionDir(tab)
		sessionPath = tab.currentSessionPath()
	}
	a.mu.RUnlock()
	if ctrl == nil {
		if strings.TrimSpace(sessionPath) == "" {
			return HistoryPage{Messages: []HistoryMessage{}}
		}
		page, err := previewSessionPage(sessionDir, sessionPath, beforeTurn, limit)
		if err != nil {
			return HistoryPage{Messages: []HistoryMessage{}}
		}
		return page
	}
	dir := controllerSessionDir(ctrl)
	path := ctrl.SessionPath()
	msgs := ctrl.History()
	status := ctrl.RuntimeStatus()
	idle := !status.Running && !status.PendingPrompt && !ctrl.SessionHasUnsavedChanges()
	// Session-v4 experiment: when idle, prefer the durable v4 projection via
	// Query so history browsing exercises the v4 store. Live turns keep the
	// agent in-memory history until the next Snapshot mirrors into v4.
	if idle && strings.TrimSpace(path) != "" {
		if c, ok := ctrl.(*control.Controller); ok {
			if v4 := c.SessionV4(); v4 != nil {
				if v4Msgs, hit := v4.HistoryMessages(context.Background(), path); hit {
					msgs = v4Msgs
				} else if loaded, ok := silentLoadSessionSnapshot("history_page_v4_fallback", path); ok {
					msgs = loaded
				}
			} else if loaded, ok := silentLoadSessionSnapshot("history_page_v4_fallback", path); ok {
				msgs = loaded
			}
		} else if loaded, ok := silentLoadSessionSnapshot("history_page_v4_fallback", path); ok {
			msgs = loaded
		}
	}
	page := historyPageFromProviderMessages(
		msgs,
		sessionDisplayResolver(dir, path),
		sessionPlannerDisplayTurns(dir, path),
		ctrl.CheckpointTurnsByMessageIndex(),
		beforeTurn,
		limit,
	)
	digest, _ := agent.ContentDigestForMessages(msgs)
	return historyPageWithFingerprint(page, path, digest)
}

func historyPageWithFingerprint(page HistoryPage, sessionPath, contentDigest string) HistoryPage {
	contentDigest = strings.TrimSpace(contentDigest)
	if strings.TrimSpace(sessionPath) == "" || contentDigest == "" {
		return page
	}
	// Digest is derived from the exact full transcript used to build the page.
	// Never copy a newer sidecar digest onto older page content.
	page.Digest = contentDigest
	if meta, ok, err := agent.LoadBranchMeta(sessionPath); err == nil && ok {
		if strings.TrimSpace(meta.ContentDigest) == contentDigest {
			page.Revision = meta.Revision
		}
	}
	return page
}

func normalizeHistoryPageLimit(limit int) int {
	if limit <= 0 {
		return defaultHistoryPageTurns
	}
	if limit > maxHistoryPageTurns {
		return maxHistoryPageTurns
	}
	return limit
}

func historyPageFromMessages(messages []HistoryMessage, beforeTurn, limit int) HistoryPage {
	limit = normalizeHistoryPageLimit(limit)
	totalTurns := 0
	for _, msg := range messages {
		if msg.Role == "user" {
			totalTurns++
		}
	}
	if beforeTurn <= 0 || beforeTurn > totalTurns {
		beforeTurn = totalTurns
	}
	startTurn := max(beforeTurn-limit, 0)
	page := HistoryPage{
		StartTurn:  startTurn,
		EndTurn:    beforeTurn,
		TotalTurns: totalTurns,
		HasOlder:   startTurn > 0,
	}
	if len(messages) == 0 || startTurn >= beforeTurn {
		page.Messages = []HistoryMessage{}
		return page
	}
	page.Messages = historyMessagesForTurnRange(messages, startTurn, beforeTurn)
	return page
}

func historyMessagesForTurnRange(messages []HistoryMessage, startTurn, endTurn int) []HistoryMessage {
	out := make([]HistoryMessage, 0, len(messages))
	turn := -1
	for _, msg := range messages {
		if msg.Role == "user" {
			turn++
		}
		if turn < 0 {
			if startTurn == 0 {
				out = append(out, msg)
			}
			continue
		}
		if turn >= startTurn && turn < endTurn {
			out = append(out, msg)
		}
	}
	return out
}

func (a *App) HistoryForTab(tabID string) []HistoryMessage {
	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	var ctrl control.SessionAPI
	var sessionDir, sessionPath string
	if tab != nil {
		ctrl = tab.Ctrl
		sessionDir = tabSessionDir(tab)
		sessionPath = tab.currentSessionPath()
	}
	a.mu.RUnlock()
	if ctrl == nil {
		if strings.TrimSpace(sessionPath) == "" {
			return []HistoryMessage{}
		}
		messages, err := previewSessionMessages(sessionDir, sessionPath)
		if err != nil {
			return []HistoryMessage{}
		}
		return messages
	}
	dir := controllerSessionDir(ctrl)
	path := ctrl.SessionPath()
	msgs := historyProviderMessagesWithPersistedTimes(ctrl.History(), path)
	return historyMessagesWithPlannerDisplays(
		msgs,
		sessionDisplayResolver(dir, path),
		sessionPlannerDisplayTurns(dir, path),
		ctrl.CheckpointTurnsByMessageIndex(),
	)
}

func (a *App) HistoryCheckpointTurnsForTab(tabID string) []int {
	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	var ctrl control.SessionAPI
	if tab != nil {
		ctrl = tab.Ctrl
	}
	a.mu.RUnlock()
	if ctrl == nil {
		return []int{}
	}
	return historyCheckpointTurns(
		ctrl.History(),
		sessionDisplayResolver(controllerSessionDir(ctrl), ctrl.SessionPath()),
		ctrl.CheckpointTurnsByMessageIndex(),
	)
}

var pastedTextDisplayLabelPattern = regexp.MustCompile(`^\[(?:已粘贴文本|已貼上文字|Pasted text) #[0-9]+ · [0-9]+ (?:行|lines)\]$`)

// historyReplayUserContent keeps only user-authored replay data. Provider-facing
// capability, goal, hook, and resolved-reference context must not be resubmitted.
func historyReplayUserContent(content string) string {
	return control.StripReferencedContextPrefix(control.StripComposePrefixes(content))
}

// collapseLegacyExpandedPasteDisplay repairs sessions whose user-authored replay
// source still contains an expanded pasted-text block. This includes transcripts
// written before RawContent existed. The expanded block remains in SubmitText so
// edit replay can still reconstruct the card and recover its full payload.
func collapseLegacyExpandedPasteDisplay(content string) string {
	const beginPrefix = "--- Begin "
	for scan := 0; scan < len(content); {
		beginOffset := strings.Index(content[scan:], beginPrefix)
		if beginOffset < 0 {
			break
		}
		begin := scan + beginOffset
		labelStart := begin + len(beginPrefix)
		labelEndOffset := strings.Index(content[labelStart:], " ---")
		if labelEndOffset < 0 {
			break
		}
		labelEnd := labelStart + labelEndOffset
		label := content[labelStart:labelEnd]
		beginEnd := labelEnd + len(" ---")
		if !pastedTextDisplayLabelPattern.MatchString(label) {
			scan = beginEnd
			continue
		}
		endMarker := "--- End " + label + " ---"
		endOffset := strings.Index(content[beginEnd:], endMarker)
		if endOffset < 0 {
			scan = beginEnd
			continue
		}
		labelCopy := strings.LastIndex(content[:begin], label)
		if labelCopy < 0 || strings.TrimSpace(content[labelCopy+len(label):begin]) != "" {
			scan = beginEnd
			continue
		}
		end := beginEnd + endOffset + len(endMarker)
		content = content[:labelCopy+len(label)] + content[end:]
		scan = labelCopy + len(label)
	}
	return strings.TrimSpace(content)
}

// historyUserDisplayContent prefers a persisted display sidecar when one exists.
// Comparing it with the deterministic fallback distinguishes a sidecar hit
// without changing the resolver API used throughout history pagination.
func historyUserDisplayContent(msg provider.Message, resolveUserContent func(string) string) string {
	resolved := strings.TrimSpace(resolveUserContent(msg.Content))
	fallback := strings.TrimSpace(historyReplayUserContent(msg.Content))
	if resolved != "" && resolved != fallback {
		return resolved
	}
	replaySource := agent.UserMessageText(msg)
	if msg.RawContent == "" {
		replaySource = fallback
	}
	return collapseLegacyExpandedPasteDisplay(replaySource)
}

func historyCheckpointTurns(msgs []provider.Message, resolveUserContent func(string) string, checkpointTurns map[int]int) []int {
	out := make([]int, 0)
	for index, msg := range msgs {
		if !agent.IsUserAuthoredTurnMessage(msg) {
			continue
		}
		turn, ok := checkpointTurns[index]
		if !ok {
			turn = -1
		}
		out = append(out, turn)
	}
	return out
}

func historyMessages(msgs []provider.Message, resolveUserContent func(string) string) []HistoryMessage {
	return historyMessagesWithPlannerDisplays(msgs, resolveUserContent, nil, nil)
}

func historyMessagesWithPlannerDisplays(msgs []provider.Message, resolveUserContent func(string) string, plannerTurns []plannerDisplayTurn, checkpointTurns map[int]int) []HistoryMessage {
	replayedTodoArgs := historyTodoArgsWithCompleteSteps(msgs)
	toolResults := historyToolResultsByID(msgs)
	return historyMessagesWithPlannerDisplaysAndLookups(msgs, resolveUserContent, plannerTurns, checkpointTurns, replayedTodoArgs, toolResults)
}

// historyMessageConvertState carries the cross-message state of a provider→
// HistoryMessage conversion pass: the planner-display queue (consumed in order
// per user-text hash) and the canonical-turn suppression a planner interrupt
// notice arms. Keeping it explicit lets the windowed history slice API convert
// one message at a time with exactly the same semantics as a full pass.
type historyMessageConvertState struct {
	plannerByUserHash     map[string][]plannerDisplayTurn
	suppressCanonicalTurn bool
}

func newHistoryMessageConvertState(plannerTurns []plannerDisplayTurn) *historyMessageConvertState {
	return &historyMessageConvertState{plannerByUserHash: plannerTurnsByUserHash(plannerTurns)}
}

func historyMessagesWithPlannerDisplaysAndLookups(
	msgs []provider.Message,
	resolveUserContent func(string) string,
	plannerTurns []plannerDisplayTurn,
	checkpointTurns map[int]int,
	replayedTodoArgs map[string]string,
	toolResults map[string]provider.Message,
) []HistoryMessage {
	out := make([]HistoryMessage, 0, len(msgs))
	state := newHistoryMessageConvertState(plannerTurns)
	for index, m := range msgs {
		out = append(out, state.convertHistoryMessage(index, m, resolveUserContent, checkpointTurns, replayedTodoArgs, toolResults)...)
	}
	return out
}

// convertHistoryMessage converts one provider message into its 0..n history
// rows. index is the message's position in the coordinate system of
// checkpointTurns (window-relative for the legacy full-pass callers, absolute
// for the windowed slice API).
func (state *historyMessageConvertState) convertHistoryMessage(
	index int,
	m provider.Message,
	resolveUserContent func(string) string,
	checkpointTurns map[int]int,
	replayedTodoArgs map[string]string,
	toolResults map[string]provider.Message,
) []HistoryMessage {
	var out []HistoryMessage
	if m.DecisionReceipt != nil {
		return append(out, HistoryMessage{
			Role:            "notice",
			Code:            event.NoticeCodeDecisionReceipt,
			Level:           "info",
			DecisionReceipt: cloneDecisionReceipt(m.DecisionReceipt),
		})
	}
	if rows, handled := historyLocalOnlyRows(m); handled {
		return append(out, rows...)
	}
	if state.suppressCanonicalTurn {
		if !agent.IsUserAuthoredTurnMessage(m) {
			// Host guidance keeps its notice row even inside a suppressed canonical
			// turn; every other non-authored message stays hidden here.
			if rows, handled := hostGuidanceRows(m); handled {
				return append(out, rows...)
			}
			return out
		}
		state.suppressCanonicalTurn = false
	}
	content := m.Content
	var checkpointTurn *int
	if m.Role == provider.RoleUser {
		// Mid-turn steer messages are persisted in the session so they
		// survive tab switches. They are surfaced as a notice (↪ text)
		// — matching the live Steer event look — rather than as a
		// regular user bubble or being filtered as synthetic (#4044).
		// Check against the raw m.Content: resolveUserContent applies
		// StripComposePrefixes which trims trailing whitespace.
		if rows, handled := historySteerRows(m.Content, false); handled {
			return append(out, rows...)
		}
		// Host guidance reaches the transcript as a notice row (task 172); internal
		// protocol messages keep falling through to the filter below.
		if rows, handled := hostGuidanceRows(m); handled {
			return append(out, rows...)
		}
		content = historyUserDisplayContent(m, resolveUserContent)
		if agent.IsHostGeneratedUserMessage(m) {
			return out
		}
		if turn, ok := checkpointTurns[index]; ok {
			turnCopy := turn
			checkpointTurn = &turnCopy
		}
	}
	reasoning := ""
	if m.Role == provider.RoleAssistant || m.LocalOnly {
		reasoning = m.ReasoningContent
	}
	displayRole := string(m.Role)
	if m.LocalOnly {
		displayRole = "assistant"
	}
	hm := HistoryMessage{Role: displayRole, Content: content, CheckpointTurn: checkpointTurn, CreatedAt: m.CreatedAt, Reasoning: reasoning, WorkDurationMs: m.WorkDurationMs}
	if m.Role == provider.RoleAssistant && len(m.MemoryCitations) > 0 {
		hm.MemoryCitations = append([]provider.MemoryCitation(nil), m.MemoryCitations...)
	}
	if m.Role == provider.RoleUser && content != m.Content {
		replay := historyReplayUserContent(m.Content)
		if agent.ContainsMemoryCompilerExecution(m.Content) {
			// Never expose the compiler contract itself. A safely unwrapped
			// slash invocation is useful display metadata, though: it lets the
			// frontend restore the selected skill/subagent in history and trash.
			if strings.HasPrefix(strings.TrimSpace(replay), "/") && replay != content {
				hm.SubmitText = replay
			}
		} else if replay != content {
			hm.SubmitText = replay
		}
	}
	hm.ServerSearch = historyServerSearch(m.ServerSearch)
	if (m.Role == provider.RoleAssistant || m.LocalOnly) && len(m.ToolCalls) > 0 {
		hm.ToolCalls = make([]HistoryToolCall, len(m.ToolCalls))
		for i, tc := range m.ToolCalls {
			args := tc.Arguments
			if tc.Name == "todo_write" {
				if replayed, ok := replayedTodoArgs[tc.ID]; ok {
					args = replayed
				}
			}
			hm.ToolCalls[i] = historyToolCall(tc, args, toolResults[tc.ID])
		}
	}
	if m.Role == provider.RoleTool && !m.LocalOnly {
		hm.ToolCallID = m.ToolCallID
		hm.ToolName = m.Name
		hm.Content, hm.ToolResultArchived, hm.ToolResultError = historyToolResultContent(m.Content, m.ToolCallID != "")
		hm.Execution = m.ToolExecution
	}
	hasVisibleLocalContent := strings.TrimSpace(hm.Content) != "" || strings.TrimSpace(hm.Reasoning) != "" || len(hm.ToolCalls) > 0 || (!m.LocalOnly && m.Role == provider.RoleTool)
	if !m.LocalOnly || hasVisibleLocalContent {
		out = append(out, hm)
	}
	for _, receipt := range m.DecisionReceipts {
		if receipt == nil {
			continue
		}
		out = append(out, HistoryMessage{
			Role:            "notice",
			Code:            event.NoticeCodeDecisionReceipt,
			Level:           "info",
			DecisionReceipt: cloneDecisionReceipt(receipt),
		})
	}
	if m.LocalOnly && m.InterruptedTurn != nil {
		out = append(out, interruptedTurnHistoryNotice(m.InterruptedTurn))
	}
	if m.Role == provider.RoleUser {
		key := messageDisplayKey(agent.UserMessageText(m))
		if turns := state.plannerByUserHash[key]; len(turns) > 0 {
			out = append(out, cloneHistoryMessages(turns[0].Messages)...)
			state.suppressCanonicalTurn = plannerDisplaySuppressesCanonical(turns[0])
			state.plannerByUserHash[key] = turns[1:]
		}
	}
	return out
}

// consumeHistoryPlannerState advances only the cross-message planner state.
// Windowed pages call it for the prefix they do not render, so repeated user
// text and a planner interrupt at a page boundary behave exactly as one full
// conversion pass. Keep the early returns in lock-step with
// convertHistoryMessage: those rows never reach the planner attachment at its
// tail.
func (state *historyMessageConvertState) consumeHistoryPlannerState(m provider.Message, resolveUserContent func(string) string) {
	if m.DecisionReceipt != nil {
		return
	}
	if m.LocalOnly {
		if _, isSteer := agent.SteerText(m.Content); isSteer {
			return
		}
	}
	if state.suppressCanonicalTurn {
		if !agent.IsUserAuthoredTurnMessage(m) {
			return
		}
		state.suppressCanonicalTurn = false
	}
	if m.Role != provider.RoleUser {
		return
	}
	if agent.IsHostGeneratedUserMessage(m) {
		return
	}
	key := messageDisplayKey(agent.UserMessageText(m))
	if turns := state.plannerByUserHash[key]; len(turns) > 0 {
		state.suppressCanonicalTurn = plannerDisplaySuppressesCanonical(turns[0])
		state.plannerByUserHash[key] = turns[1:]
	}
}

func cloneDecisionReceipt(in *provider.DecisionReceipt) *provider.DecisionReceipt {
	if in == nil {
		return nil
	}
	copy := *in
	return &copy
}

func plannerDisplaySuppressesCanonical(turn plannerDisplayTurn) bool {
	for _, message := range turn.Messages {
		if message.Role == "notice" && message.Code == event.NoticeCodeCancelledTurn {
			return true
		}
	}
	return false
}

func historyPageFromProviderMessages(
	msgs []provider.Message,
	resolveUserContent func(string) string,
	plannerTurns []plannerDisplayTurn,
	checkpointTurns map[int]int,
	beforeTurn, limit int,
) HistoryPage {
	limit = normalizeHistoryPageLimit(limit)
	totalTurns := visibleHistoryUserTurns(msgs, resolveUserContent)
	if beforeTurn <= 0 || beforeTurn > totalTurns {
		beforeTurn = totalTurns
	}
	startTurn := max(beforeTurn-limit, 0)
	page := HistoryPage{
		StartTurn:  startTurn,
		EndTurn:    beforeTurn,
		TotalTurns: totalTurns,
		HasOlder:   startTurn > 0,
	}
	if len(msgs) == 0 || startTurn >= beforeTurn {
		page.Messages = []HistoryMessage{}
		return page
	}
	pageMessages, originalIndexes := providerMessagesForVisibleTurnRange(msgs, resolveUserContent, startTurn, beforeTurn)
	page.Messages = historyMessagesWithPlannerDisplaysAndLookups(
		pageMessages,
		resolveUserContent,
		plannerTurns,
		checkpointTurnsForProviderWindow(checkpointTurns, originalIndexes),
		historyTodoArgsWithCompleteSteps(msgs),
		historyToolResultsByID(msgs),
	)
	return page
}

func visibleHistoryUserTurns(msgs []provider.Message, resolveUserContent func(string) string) int {
	total := 0
	for _, msg := range msgs {
		if isVisibleHistoryUser(msg, resolveUserContent) {
			total++
		}
	}
	return total
}

func isVisibleHistoryUser(msg provider.Message, resolveUserContent func(string) string) bool {
	return agent.IsUserAuthoredTurnMessage(msg)
}

func providerMessagesForVisibleTurnRange(msgs []provider.Message, resolveUserContent func(string) string, startTurn, endTurn int) ([]provider.Message, []int) {
	out := make([]provider.Message, 0, len(msgs))
	indexes := make([]int, 0, len(msgs))
	turn := -1
	for index, msg := range msgs {
		if isVisibleHistoryUser(msg, resolveUserContent) {
			turn++
		}
		if turn < 0 {
			if startTurn == 0 {
				out = append(out, msg)
				indexes = append(indexes, index)
			}
			continue
		}
		if turn >= startTurn && turn < endTurn {
			out = append(out, msg)
			indexes = append(indexes, index)
		}
	}
	return out, indexes
}

func checkpointTurnsForProviderWindow(checkpointTurns map[int]int, originalIndexes []int) map[int]int {
	if len(checkpointTurns) == 0 || len(originalIndexes) == 0 {
		return nil
	}
	out := map[int]int{}
	for pageIndex, originalIndex := range originalIndexes {
		if turn, ok := checkpointTurns[originalIndex]; ok {
			out[pageIndex] = turn
		}
	}
	return out
}

func plannerTurnsByUserHash(turns []plannerDisplayTurn) map[string][]plannerDisplayTurn {
	out := map[string][]plannerDisplayTurn{}
	for _, turn := range turns {
		if strings.TrimSpace(turn.UserHash) == "" || len(turn.Messages) == 0 {
			continue
		}
		out[turn.UserHash] = append(out[turn.UserHash], turn)
	}
	return out
}

func cloneHistoryMessages(in []HistoryMessage) []HistoryMessage {
	if len(in) == 0 {
		return nil
	}
	out := make([]HistoryMessage, len(in))
	copy(out, in)
	for i := range out {
		if len(in[i].MemoryCitations) > 0 {
			out[i].MemoryCitations = append([]provider.MemoryCitation(nil), in[i].MemoryCitations...)
		}
		if len(in[i].ToolCalls) > 0 {
			out[i].ToolCalls = append([]HistoryToolCall(nil), in[i].ToolCalls...)
		}
	}
	return out
}

const historyToolPreviewLimit = 2_000

func historyToolCall(tc provider.ToolCall, args string, result provider.Message) HistoryToolCall {
	call := HistoryToolCall{
		ID:               tc.ID,
		Name:             tc.Name,
		ResolvedName:     tc.ResolvedName,
		CapabilityID:     tc.CapabilityID,
		ResolvedReadOnly: tc.ResolvedReadOnly,
		Subject:          historyToolSubject(tc.Name, args),
		Summary:          historyToolSummary(tc.Name, args, result.Content),
		Diff:             tc.Diff,
		Added:            tc.Added,
		Removed:          tc.Removed,
	}
	if tc.Name == "todo_write" {
		call.Arguments = args
		return call
	}
	if tc.ID == "" {
		call.Arguments = args
		return call
	}
	if args != "" {
		call.ArgumentsArchived = true
	}
	return call
}

func historyToolResultsByID(msgs []provider.Message) map[string]provider.Message {
	out := map[string]provider.Message{}
	for _, msg := range msgs {
		if msg.Role != provider.RoleTool || msg.ToolCallID == "" {
			continue
		}
		out[msg.ToolCallID] = msg
	}
	return out
}

func historyToolResultContent(content string, canArchive bool) (display string, archived bool, errPreview string) {
	if content == "" {
		return "", false, ""
	}
	if !canArchive {
		if historyToolResultFailed(content) {
			return content, false, content
		}
		return content, false, ""
	}
	if historyToolResultFailed(content) {
		display = clipHistoryToolPreview(strings.TrimSpace(content))
		return display, display != content, display
	}
	return "", true, ""
}

func clipHistoryToolPreview(s string) string {
	if len(s) <= historyToolPreviewLimit {
		return s
	}
	return strings.TrimSpace(clipStringBytes(s, historyToolPreviewLimit)) + "\n..."
}

func historyToolSubject(name, args string) string {
	a := parseHistoryToolArgs(args)
	var subject string
	switch name {
	case "bash":
		subject = historyArgString(a, "command")
	case "grep", "glob":
		subject = firstNonEmpty(historyArgString(a, "pattern"), historyArgString(a, "path"))
	case "web_fetch":
		subject = historyArgString(a, "url")
	case "task":
		subject = firstNonEmpty(historyArgString(a, "description"), historyArgString(a, "prompt"))
	case "run_skill":
		subject = historyArgString(a, "name")
	case "move_file":
		src := historyArgString(a, "source_path")
		dst := historyArgString(a, "destination_path")
		if src != "" && dst != "" {
			subject = src + " -> " + dst
		} else {
			subject = firstNonEmpty(src, dst)
		}
	case "remember":
		subject = firstNonEmpty(historyArgString(a, "name"), historyArgString(a, "description"))
	case "todo_write", "exit_plan_mode":
		subject = ""
	default:
		subject = firstNonEmpty(historyArgString(a, "path"), historyArgString(a, "file_path"))
	}
	return clipSingleLine(subject, 240)
}

func historyToolSummary(name, args, output string) string {
	if historyToolResultFailed(output) {
		return ""
	}
	a := parseHistoryToolArgs(args)
	switch name {
	case "write_file":
		if content := historyArgString(a, "content"); content != "" {
			return fmt.Sprintf("%d lines", historyLineCount(content))
		}
	case "edit_file":
		oldText := historyArgString(a, "old_string")
		newText := historyArgString(a, "new_string")
		if oldText != "" || newText != "" {
			return fmt.Sprintf("%d -> %d lines", historyLineCount(oldText), historyLineCount(newText))
		}
	case "multi_edit":
		if edits, ok := a["edits"].([]any); ok && len(edits) > 0 {
			return fmt.Sprintf("%d edits", len(edits))
		}
	}
	if output == "" {
		return ""
	}
	switch name {
	case "read_file":
		if strings.HasPrefix(output, "(empty file)") {
			return "empty file"
		}
		if arrows := strings.Count(output, "→"); arrows > 0 {
			return fmt.Sprintf("%d lines", arrows)
		}
		return fmt.Sprintf("%d lines", historyLineCount(output))
	case "grep":
		return fmt.Sprintf("%d matches", historyNonEmptyLineCount(output))
	case "glob":
		return fmt.Sprintf("%d files", historyNonEmptyLineCount(output))
	case "ls":
		return fmt.Sprintf("%d entries", historyNonEmptyLineCount(output))
	case "web_fetch":
		return clipSingleLine(strings.SplitN(output, "\n", 2)[0], 80)
	case "bash":
		if strings.TrimSpace(output) == "" {
			return "no output"
		}
		return fmt.Sprintf("%d lines", historyLineCount(output))
	default:
		return ""
	}
}

func parseHistoryToolArgs(args string) map[string]any {
	if args == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(args), &out); err != nil {
		return map[string]any{}
	}
	return out
}

func historyArgString(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func historyLineCount(s string) int {
	if s == "" {
		return 0
	}
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func historyNonEmptyLineCount(s string) int {
	count := 0
	for line := range strings.SplitSeq(s, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func clipSingleLine(s string, max int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return clipStringBytes(s, max)
	}
	return clipStringBytes(s, max-3) + "..."
}

func clipStringBytes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}

func historyTodoArgsWithCompleteSteps(msgs []provider.Message) map[string]string {
	successful := successfulHistoryToolCallIDs(msgs)
	state := newHistoryTodoArgsState(successful)
	for _, m := range msgs {
		state.consume(m)
	}
	return state.out
}

// historyTodoArgsState retains only the derived todo state needed to render a
// todo_write call. It lets windowed history compute the same result as the
// legacy full conversion while streaming messages in bounded chunks.
type historyTodoArgsState struct {
	successful   map[string]bool
	out          map[string]string
	todos        []evidence.TodoItem
	latestTodoID string
}

func newHistoryTodoArgsState(successful map[string]bool) *historyTodoArgsState {
	return &historyTodoArgsState{successful: successful, out: map[string]string{}}
}

func (state *historyTodoArgsState) consume(m provider.Message) {
	for _, tc := range m.ToolCalls {
		if tc.ID == "" || !state.successful[tc.ID] {
			continue
		}
		switch tc.Name {
		case "todo_write":
			rec := evidence.ReceiptFromToolCall(tc.Name, json.RawMessage(tc.Arguments), true, true)
			if len(rec.Todos) == 0 {
				continue
			}
			state.todos = evidence.NormalizeSerialTodos(rec.Todos)
			state.latestTodoID = tc.ID
			if args, ok := todoArgsJSON(state.todos); ok {
				state.out[state.latestTodoID] = args
			}
		case "complete_step":
			if state.latestTodoID == "" || len(state.todos) == 0 {
				continue
			}
			rec := evidence.ReceiptFromToolCall(tc.Name, json.RawMessage(tc.Arguments), true, true)
			match, ok := evidence.MatchStep(rec.Step, state.todos)
			if !ok || !evidence.AdvanceSerialTodo(state.todos, match.Index-1) {
				continue
			}
			if args, ok := todoArgsJSON(state.todos); ok {
				state.out[state.latestTodoID] = args
			}
		}
	}
}

func successfulHistoryToolCallIDs(msgs []provider.Message) map[string]bool {
	successful := map[string]bool{}
	for _, msg := range msgs {
		if msg.Role != provider.RoleTool || msg.ToolCallID == "" {
			continue
		}
		if !historyToolResultFailed(msg.Content) {
			successful[msg.ToolCallID] = true
		}
	}
	return successful
}

func historyToolResultFailed(content string) bool {
	content = strings.TrimSpace(content)
	return strings.HasPrefix(content, "error:") ||
		strings.HasPrefix(content, "blocked:") ||
		strings.HasPrefix(content, "Error:") ||
		strings.HasPrefix(content, "[error")
}

func todoArgsJSON(todos []evidence.TodoItem) (string, bool) {
	b, err := json.Marshal(map[string]any{"todos": todos})
	if err != nil {
		return "", false
	}
	return string(b), true
}

func previewSessionMessages(sessionDir, path string) ([]HistoryMessage, error) {
	sessionPath, _, err := validateSessionPath(sessionDir, path)
	if err != nil {
		return nil, err
	}
	if out, ok, err := previewEventSessionMessages(sessionPath); ok || err != nil {
		return out, err
	}
	loaded, err := agent.LoadSession(sessionPath)
	if err != nil {
		return nil, err
	}
	return historyMessagesWithPlannerDisplays(
		historyProviderMessagesWithPersistedTimes(loaded.Snapshot(), sessionPath),
		sessionDisplayResolver(sessionDir, sessionPath),
		sessionPlannerDisplayTurns(sessionDir, sessionPath),
		nil,
	), nil
}

func previewSessionPage(sessionDir, path string, beforeTurn, limit int) (HistoryPage, error) {
	sessionPath, _, err := validateSessionPath(sessionDir, path)
	if err != nil {
		return HistoryPage{}, err
	}
	if out, ok, err := previewEventSessionMessages(sessionPath); ok || err != nil {
		if err != nil {
			return HistoryPage{}, err
		}
		return historyPageFromMessages(out, beforeTurn, limit), nil
	}
	loaded, err := agent.LoadSession(sessionPath)
	if err != nil {
		return HistoryPage{}, err
	}
	msgs := loaded.Snapshot()
	digest, _ := agent.ContentDigestForMessages(msgs)
	return historyPageWithFingerprint(historyPageFromProviderMessages(
		historyProviderMessagesWithPersistedTimes(msgs, sessionPath),
		sessionDisplayResolver(sessionDir, sessionPath),
		sessionPlannerDisplayTurns(sessionDir, sessionPath),
		nil,
		beforeTurn,
		limit,
	), sessionPath, digest), nil
}

type previewEventRecord struct {
	Kind             string                    `json:"kind"`
	Type             string                    `json:"type"`
	Role             string                    `json:"role"`
	Origin           provider.MessageOrigin    `json:"origin"`
	TS               json.RawMessage           `json:"ts"`
	Time             json.RawMessage           `json:"time"`
	Timestamp        json.RawMessage           `json:"timestamp"`
	CreatedAt        json.RawMessage           `json:"createdAt"`
	CreatedAtSnake   json.RawMessage           `json:"created_at"`
	UpdatedAt        json.RawMessage           `json:"updatedAt"`
	UpdatedAtSnake   json.RawMessage           `json:"updated_at"`
	Text             string                    `json:"text"`
	Detail           string                    `json:"detail"`
	Code             string                    `json:"code"`
	Content          string                    `json:"content"`
	RawContent       string                    `json:"raw_content"`
	Reasoning        string                    `json:"reasoning"`
	ReasoningContent string                    `json:"reasoningContent"`
	MemoryCitations  []provider.MemoryCitation `json:"memoryCitations"`
	Level            string                    `json:"level"`
	ToolCalls        []previewToolCall         `json:"toolCalls"`
	CallID           string                    `json:"callId"`
	ToolCallID       string                    `json:"toolCallId"`
	ToolName         string                    `json:"toolName"`
	Name             string                    `json:"name"`
	Output           string                    `json:"output"`
	Compaction       *previewCompaction        `json:"compaction"`
	Trigger          string                    `json:"trigger"`
	Messages         int                       `json:"messages"`
	Summary          string                    `json:"summary"`
	Archive          string                    `json:"archive"`
}

type previewToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Function  struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type previewCompaction struct {
	Trigger  string `json:"trigger"`
	Messages int    `json:"messages"`
	Summary  string `json:"summary"`
	Archive  string `json:"archive"`
}

func previewEventSessionMessages(path string) ([]HistoryMessage, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	out := []HistoryMessage{}
	toolName := map[string]string{}
	sawEvent := false
	for {
		var rec previewEventRecord
		if err := dec.Decode(&rec); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if sawEvent {
				return out, true, nil
			}
			return nil, false, nil
		}
		eventName := strings.TrimSpace(rec.Kind)
		if eventName == "" {
			eventName = strings.TrimSpace(rec.Type)
		}
		if eventName == "" {
			continue
		}
		sawEvent = true
		switch eventName {
		case "user.message":
			if rec.Text != "" {
				hm := HistoryMessage{Role: "user", Content: rec.Text}
				if at, ok := promptHistoryEventMillis(rec); ok {
					hm.CreatedAt = at
				}
				out = append(out, hm)
			}
		case "model.final":
			hm := HistoryMessage{Role: "assistant", Content: rec.Content, Reasoning: firstNonEmpty(rec.Reasoning, rec.ReasoningContent)}
			if len(rec.MemoryCitations) > 0 {
				hm.MemoryCitations = append([]provider.MemoryCitation(nil), rec.MemoryCitations...)
			}
			for _, tc := range rec.ToolCalls {
				id := tc.ID
				name := firstNonEmpty(tc.Name, tc.Function.Name)
				args := firstNonEmpty(tc.Arguments, tc.Function.Arguments)
				hm.ToolCalls = append(hm.ToolCalls, historyToolCall(provider.ToolCall{ID: id, Name: name, Arguments: args}, args, provider.Message{}))
				if id != "" {
					toolName[id] = name
				}
			}
			out = append(out, hm)
		case "tool.result":
			callID := firstNonEmpty(rec.CallID, rec.ToolCallID)
			content := firstNonEmpty(rec.Output, rec.Content)
			display, archived, errPreview := historyToolResultContent(content, callID != "")
			if len(out) > 0 && callID != "" {
				updateHistoryToolCallSummary(out, callID, content)
			}
			out = append(out, HistoryMessage{
				Role:               "tool",
				ToolCallID:         callID,
				ToolName:           firstNonEmpty(rec.ToolName, rec.Name, toolName[callID]),
				Content:            display,
				ToolResultArchived: archived,
				ToolResultError:    errPreview,
			})
		case "phase":
			out = append(out, HistoryMessage{Role: "phase", Content: firstNonEmpty(rec.Text, rec.Content)})
		case "notice":
			level := rec.Level
			if level != "warn" {
				level = "info"
			}
			out = append(out, HistoryMessage{Role: "notice", Level: level, Content: firstNonEmpty(rec.Text, rec.Content), Detail: rec.Detail, Code: rec.Code})
		case "compaction_started":
			c := rec.compactionPayload()
			out = append(out, HistoryMessage{Role: "compaction", Pending: true, Trigger: c.Trigger})
		case "compaction_done":
			c := rec.compactionPayload()
			out = append(out, HistoryMessage{
				Role:     "compaction",
				Trigger:  c.Trigger,
				Messages: c.Messages,
				Summary:  c.Summary,
				Archive:  c.Archive,
			})
		}
	}
	return out, sawEvent, nil
}

func (r previewEventRecord) compactionPayload() previewCompaction {
	if r.Compaction != nil {
		return *r.Compaction
	}
	return previewCompaction{Trigger: r.Trigger, Messages: r.Messages, Summary: r.Summary, Archive: r.Archive}
}

func updateHistoryToolCallSummary(out []HistoryMessage, callID, output string) {
	if callID == "" {
		return
	}
	for _, v := range slices.Backward(out) {
		for j := range v.ToolCalls {
			call := &v.ToolCalls[j]
			if call.ID != callID {
				continue
			}
			if call.Summary == "" {
				call.Summary = historyToolSummary(call.Name, call.Arguments, output)
			}
			return
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// ContextUsage returns the latest context-window gauge numbers.
func (a *App) ContextUsage() ContextInfo {
	return a.ContextUsageForTab("")
}

func (a *App) ContextUsageForTab(tabID string) ContextInfo {
	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	var ctrl control.SessionAPI
	if tab != nil {
		ctrl = tab.Ctrl
	}
	a.mu.RUnlock()

	var info ContextInfo
	var snap tabTelemetrySnapshot
	if tab != nil {
		// Re-key first: a controller-side rotation (typed /new) may have
		// swapped sessions without the App noticing, and the stale totals
		// would otherwise be reported — and then persisted — under the new
		// session (#5850).
		if ctrl != nil {
			if sp := ctrl.SessionPath(); sp != "" {
				tab.syncTelemetryToSession(sp)
			}
		}
		snap = tab.displayTelemetrySnapshot()
		info.SessionTokens = snap.Usage.TotalTokens
		info.SessionCost = snap.Usage.SessionCost
		info.SessionCurrency = snap.Usage.SessionCurrency
		info.CacheHitTokens = snap.Usage.CacheHitTokens
		info.CacheMissTokens = snap.Usage.CacheMissTokens
		info.Estimated = snap.Usage.Estimated
		info.SessionCostComplete = snap.Usage.SessionCostComplete
		info.SessionCostQuote = snap.Usage.SessionCostQuote
		info.Sources = snap.Usage.Sources
	}
	if ctrl == nil {
		return info
	}
	// The gauge measures the loaded view, so a rebound session reports its real
	// fill immediately and no longer needs the persisted last-turn fallback.
	used, window := ctrl.ContextSnapshot()
	info.Used = used
	info.Window = window
	info.CompactRatio = ctrl.CompactRatio()
	snapshot := ctrl.ContextMaintenanceSnapshot()
	info.Maintenance = contextMaintenanceInfo(snapshot)
	if snapshot.ContextBudget != nil {
		info.ContextBudget = contextBudgetInfo(snapshot.ContextBudget)
	}
	return info
}

// BalanceInfo is the wallet-balance readout for the status bar. Available is true
// only when a balance was fetched; Display is the exact formatted amount (e.g.
// "¥110.00")
// and is "" when the active provider declares no balance_url — the frontend then
// omits the readout. Err carries a fetch failure for an optional tooltip.
// Wallet balances are displayed in their original currencies; no conversion
// or cross-currency sum is performed.
type BalanceInfo struct {
	Available           bool     `json:"available"`
	Display             string   `json:"display"`
	Detail              string   `json:"detail,omitempty"` // per-wallet original balances
	Complete            bool     `json:"complete"`
	RateDate            string   `json:"rateDate,omitempty"`
	Approx              bool     `json:"approx,omitempty"`
	Currencies          []string `json:"currencies,omitempty"`
	PrimaryCurrency     string   `json:"primaryCurrency,omitempty"`
	CostDisplayCurrency string   `json:"costDisplayCurrency,omitempty"`
	MultiCurrency       bool     `json:"multiCurrency,omitempty"`
	Err                 string   `json:"err,omitempty"`
}

// Balance queries the active provider's wallet balance (a network call). It
// returns an empty (unavailable) readout when no provider balance_url is set, the
// controller is down, or the fetch fails — so the status bar simply shows nothing
// rather than an error.
func (a *App) Balance() BalanceInfo {
	return a.BalanceForTab("")
}

func (a *App) BalanceForTab(tabID string) BalanceInfo {
	currency := a.balanceDisplayCurrency()
	tab, ctrl, generation := a.balanceRequestTarget(tabID)
	if ctrl == nil {
		return BalanceInfo{}
	}
	b, err := ctrl.Balance(a.ctx)
	if err != nil {
		return BalanceInfo{Err: err.Error()}
	}
	if b == nil {
		return BalanceInfo{} // provider declares no balance endpoint
	}
	display := b.DisplayForCurrency(currency)
	currencies := b.Currencies()
	primary := b.PrimaryCurrency()
	a.applyBalanceDisplayHint(tabID, tab, ctrl, currency, primary, generation)
	detail := balanceDetail(b)
	return BalanceInfo{
		Available:           true,
		Display:             display,
		Detail:              detail,
		Complete:            true,
		Currencies:          currencies,
		PrimaryCurrency:     primary,
		CostDisplayCurrency: firstNonEmptyString(currency, primary),
		MultiCurrency:       len(currencies) > 1,
	}
}

func balanceDetail(b *billing.Balance) string {
	if b == nil || len(b.Infos) == 0 {
		return ""
	}
	parts := make([]string, 0, len(b.Infos))
	for _, info := range b.Infos {
		cur := strings.ToUpper(strings.TrimSpace(info.Currency))
		if cur == "" {
			cur = "UNKNOWN"
		}
		parts = append(parts, cur+" "+strings.TrimSpace(info.TotalBalance))
	}
	return strings.Join(parts, "\n")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// balanceDisplayCurrency resolves only an explicit global display currency.
// Automatic mode leaves the wallet in its original currency.
func (a *App) balanceDisplayCurrency() string {
	cfg, _, err := a.loadDesktopUserConfigForView()
	if err != nil {
		return ""
	}
	if pref := cfg.DisplayCurrencyPref(); pref != "" {
		return pref
	}
	return cfg.ExplicitDisplayCurrency()
}

// Meta describes the session for the frontend's header and status line.
type Meta struct {
	Label                 string             `json:"label"`
	Ready                 bool               `json:"ready"`
	Runtime               SessionRuntimeView `json:"runtime"`
	StartupErr            string             `json:"startupErr,omitempty"`
	EventChannel          string             `json:"eventChannel"`
	SessionPath           string             `json:"sessionPath,omitempty"`
	SessionRevision       int64              `json:"sessionRevision,omitempty"`
	SessionDigest         string             `json:"sessionDigest,omitempty"`
	Cwd                   string             `json:"cwd"`
	WorkspaceRoot         string             `json:"workspaceRoot,omitempty"`
	WorkspaceName         string             `json:"workspaceName,omitempty"`
	WorkspacePath         string             `json:"workspacePath,omitempty"`
	GitBranch             string             `json:"gitBranch,omitempty"`
	ImageInputEnabled     bool               `json:"imageInputEnabled"`
	VisionFallbackEnabled bool               `json:"visionFallbackEnabled,omitempty"`
	AutoApproveTools      bool               `json:"autoApproveTools"`
	Bypass                bool               `json:"bypass"` // legacy JSON key for YOLO/full-access tool auto-approval
	CollaborationMode     string             `json:"collaborationMode"`
	ToolApprovalMode      string             `json:"toolApprovalMode"`
	// Autopilot is the RAW first-axis flag (task 465 two-axis matrix). The
	// synthesized CollaborationMode label orders plan>goal>autopilot, so a
	// goal × autopilot tab reports "goal" and the composer could no longer
	// see the unattended state it must show; the raw flag keeps both axes
	// visible at once.
	Autopilot      bool   `json:"autopilot,omitempty"`
	SubagentPolicy string `json:"subagentPolicy,omitempty"`
	// TokenMode and AgentPreset are deprecated dual-write wire values pinned to
	// their safe defaults; one-version-old frontends still parse them.
	TokenMode   string           `json:"tokenMode"`
	AgentPreset string           `json:"agentPreset,omitempty"`
	Goal        string           `json:"goal,omitempty"`
	GoalStatus  string           `json:"goalStatus,omitempty"`
	GoalRuntime *GoalRuntimeView `json:"goalRuntime,omitempty"`
	// Nil means no authoritative snapshot; non-nil empty means clear the panel.
	CanonicalTodos *[]evidence.TodoItem `json:"canonicalTodos,omitempty"`
	// Closed completed todo fingerprints from this session and its lineage.
	DismissedTodoBatches []string `json:"dismissedTodoBatches,omitempty"`
	// PinnedFiles holds metadata about standing pinned context files for this tab.
	PinnedFiles []PinnedFileInfo `json:"pinnedFiles,omitempty"`
	// Remote marks a remote session tab; its readiness is carried by the
	// remote-tab state channel rather than a local controller.
	Remote *RemoteTabRef `json:"remote,omitempty"`
}

type GoalRuntimeView struct {
	TurnsUsed        int    `json:"turnsUsed"`
	TurnsLimit       int    `json:"turnsLimit"` // Deprecated: always 0.
	TokensUsed       int    `json:"tokensUsed"`
	RequestsUsed     int    `json:"requestsUsed,omitempty"`
	WorkDurationMs   int64  `json:"workDurationMs,omitempty"`
	TokensLimit      int    `json:"tokensLimit"` // Deprecated: always 0; retained for bridge compatibility.
	NoProgressTurns  int    `json:"noProgressTurns"`
	NoProgressLimit  int    `json:"noProgressLimit"` // Deprecated: always 0.
	LastReason       string `json:"lastReason,omitempty"`
	StopCause        string `json:"stopCause,omitempty"`
	BudgetExtensions int    `json:"budgetExtensions"` // Deprecated: always 0.
}

func goalRuntimeViewFromController(ctrl control.SessionAPI) *GoalRuntimeView {
	if ctrl == nil {
		return nil
	}
	rt := ctrl.GoalRuntime()
	return &GoalRuntimeView{
		TurnsUsed:        rt.TurnsUsed,
		TurnsLimit:       rt.TurnsLimit,
		TokensUsed:       rt.TokensUsed,
		RequestsUsed:     rt.RequestsUsed,
		WorkDurationMs:   rt.WorkDurationMs,
		TokensLimit:      rt.TokensLimit,
		NoProgressTurns:  rt.NoProgressTurns,
		NoProgressLimit:  rt.NoProgressLimit,
		LastReason:       rt.LastReason,
		StopCause:        rt.StopCause,
		BudgetExtensions: rt.BudgetExtensions,
	}
}

// Meta reports the model label, readiness, any startup error, the working
// directory (for the status line), and the runtime event channel the frontend
// subscribes to.
func (a *App) Meta() Meta {
	return a.MetaForTab("")
}

func (a *App) loadConfigForVision(root string) (*config.Config, error) {
	if hook := a.configLoadForRootHook; hook != nil {
		hook(root)
	}
	return config.LoadForRootWithoutCredentialsReadOnly(root)
}

func (a *App) MetaForTab(tabID string) Meta {
	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	snap := snapshotTabRuntimeLocked(tab)
	runtimeView := a.sessionRuntimeViewLocked(tab)
	a.mu.RUnlock()
	if tab == nil {
		meta := Meta{EventChannel: eventChannel}
		if ref, ok := a.remoteTabRefFor(tabID); ok {
			meta.Remote = &ref
		}
		return meta
	}
	cwd := snap.workspaceRoot
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	// Git branch and image-input capability come from the per-tab cache
	// refreshed in the background (refreshTabMetaExtras); computing them here
	// put a config load + model resolution on every meta request. A miss or
	// stale entry schedules a refresh and serves the last known values (empty
	// on the very first call; the "tab:meta" event delivers the refresh).
	extras, refreshExtras := tabMetaExtrasFor(tab, cwd, snap.model)
	// Native image routing is already frozen in the Controller. Reading this
	// cheap snapshot also makes rebuilds visible immediately, without mixing
	// newly saved config with a provider from the preceding runtime generation.
	if capability, ok := snap.ctrl.(interface{ ImageInputSnapshot() (bool, bool, bool) }); ok {
		if enabled, fallback, available := capability.ImageInputSnapshot(); available {
			extras.imageInputEnabled, extras.visionFallbackEnabled = enabled, fallback
		}
	}
	if refreshExtras {
		a.scheduleTabMetaExtrasRefresh(tab.ID)
	}
	autoApproveTools := snap.ctrl != nil && snap.ctrl.AutoApproveTools()
	collaborationMode := snap.collaborationMode()
	toolApprovalMode := snap.currentToolApprovalMode()
	// Deprecated dual-write wire values: pinned so one-version-old frontends
	// keep parsing meta; nothing branches on them anymore.
	tokenMode := boot.TokenModeFull
	agentPreset := boot.AgentPresetBalanced
	goal := snap.currentGoal()
	goalStatus := snap.currentGoalStatus()
	sessionPath := strings.TrimSpace(snap.sessionPath)
	var sessionRevision int64
	var sessionDigest string
	if branchMeta, ok, err := agent.LoadBranchMeta(sessionPath); err == nil && ok {
		sessionRevision = branchMeta.Revision
		sessionDigest = branchMeta.ContentDigest
	}
	return Meta{
		Label:                 snap.label,
		Ready:                 runtimeView.Phase == sessionRuntimeReady && snap.ctrl != nil,
		Runtime:               runtimeView,
		StartupErr:            snap.startupErr,
		EventChannel:          eventChannel,
		SessionPath:           sessionPath,
		SessionRevision:       sessionRevision,
		SessionDigest:         sessionDigest,
		Cwd:                   cwd,
		WorkspaceRoot:         cwd,
		WorkspaceName:         tabWorkspaceNameForScope(snap.scope, cwd),
		WorkspacePath:         cwd,
		GitBranch:             extras.gitBranch,
		ImageInputEnabled:     extras.imageInputEnabled,
		VisionFallbackEnabled: extras.visionFallbackEnabled,
		AutoApproveTools:      autoApproveTools,
		Bypass:                autoApproveTools,
		CollaborationMode:     collaborationMode,
		TokenMode:             tokenMode,
		AgentPreset:           agentPreset,
		ToolApprovalMode:      toolApprovalMode,
		Autopilot:             snap.autopilot,
		SubagentPolicy:        snap.subagentPolicy,
		Goal:                  goal,
		GoalStatus:            goalStatus,
		GoalRuntime:           goalRuntimeViewFromController(snap.ctrl),
		CanonicalTodos:        ctrlTodos(snap.ctrl),
		DismissedTodoBatches:  a.dismissedTodoBatchesForSession(sessionPath),
		PinnedFiles:           buildPinnedContext(snap.workspaceRoot, tab.GetPinnedFiles()).Infos,
	}
}

// ctrlTodos returns the canonical task list from a session controller, or nil
// if the controller is not yet bound. Used by MetaForTab so the frontend
// task panel has access to the authoritative server-side todo state.
func ctrlTodos(ctrl control.SessionAPI) *[]evidence.TodoItem {
	if ctrl == nil {
		return nil
	}
	todos := ctrl.Todos()
	if todos == nil {
		todos = []evidence.TodoItem{}
	}
	return &todos
}

func (a *App) SetGoal(goal string) error {
	return a.SetGoalForTab("", goal)
}

// SetGoalForTab activates or clears a Goal on the given tab.
//
// Failures must return error so the Wails Promise rejects: the first Goal turn
// can submit a structured Skill without a /goal prose fallback, and the
// frontend aborts that submit when activation fails.
func (a *App) SetGoalForTab(tabID, goal string) error {
	tab := a.tabByID(tabID)
	if tab == nil {
		return a.workspaceNotReadyErr(nil)
	}
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	goal = strings.TrimSpace(goal)
	approvalMode := a.tabRuntimeSnapshot(tab).currentToolApprovalMode()
	a.mu.Lock()
	if a.tabs[tab.ID] != tab {
		a.mu.Unlock()
		return a.workspaceNotReadyErr(nil)
	}
	tab.goal = goal
	if goal != "" {
		tab.mode = tabModeFromAxes(false, approvalMode == control.ToolApprovalYolo)
	}
	ctrl := tab.Ctrl
	plan := tabModeHasPlan(tab.mode)
	tabIDForSave := tab.ID
	a.mu.Unlock()
	if ctrl != nil {
		ctrl.SetPlanMode(plan)
		syncTabGoalToController(ctrl, goal)
	}
	a.mu.Lock()
	if a.tabs[tabIDForSave] == tab {
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	return nil
}

// The composer re-syncs collaboration mode and Goal immediately before every
// send. Keep those acknowledgements idempotent so one multi-turn Goal retains
// its delivery scope; a terminal Goal with the same text still starts a fresh
// scope when the user explicitly enters it again.
func syncTabGoalToController(ctrl control.SessionAPI, goal string) {
	if ctrl == nil {
		return
	}
	goal = strings.TrimSpace(goal)
	if goal != "" && strings.TrimSpace(ctrl.Goal()) == goal && ctrl.GoalStatus() == control.GoalStatusRunning {
		return
	}
	ctrl.SetGoal(goal)
}

func (a *App) ClearGoal() error {
	return a.SetGoal("")
}

func (a *App) ClearGoalForTab(tabID string) error {
	return a.SetGoalForTab(tabID, "")
}

// ResumeGoalForTab re-enters a blocked or stopped Goal while preserving its
// delivery scope, runtime history, and persisted verification checkpoint.
func (a *App) ResumeGoalForTab(tabID string) bool {
	tab := a.tabByID(tabID)
	if tab == nil {
		return false
	}
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	ctrl := a.controllerForTab(tab)
	if ctrl == nil || !ctrl.ResumeGoal() {
		return false
	}
	a.mu.Lock()
	if a.tabs[tab.ID] == tab {
		tab.goal = strings.TrimSpace(ctrl.Goal())
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	return true
}

// PauseGoalForTab suspends a running Goal without clearing it; ResumeGoalForTab
// restores it (with one extra budget slice when it was budget-paused).
func (a *App) PauseGoalForTab(tabID string) bool {
	tab := a.tabByID(tabID)
	if tab == nil {
		return false
	}
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	ctrl := a.controllerForTab(tab)
	return ctrl != nil && ctrl.PauseGoal()
}

// SetAutoApproveTools toggles YOLO/full-access tool auto-approval:
// approval-gated tool calls run without asking, while ask questions and plan
// approvals still wait for the user. Runtime-only — not written to config.
func (a *App) SetAutoApproveTools(on bool) {
	if on {
		a.SetToolApprovalModeForTab("", control.ToolApprovalYolo)
		return
	}
	a.SetToolApprovalModeForTab("", control.ToolApprovalAsk)
}

// SetBypass is the legacy Wails binding for SetAutoApproveTools.
func (a *App) SetBypass(on bool) {
	a.SetAutoApproveTools(on)
}

func (a *App) SetToolApprovalMode(mode string) {
	a.SetToolApprovalModeForTab("", mode)
}

// SetToolApprovalModeForTab returns the pending approval prompt ids the
// switch auto-allowed (see SetModeForTab).
func (a *App) SetToolApprovalModeForTab(tabID, mode string) []string {
	return a.setToolApprovalModeForTabInner(tabID, mode, false)
}

// SetApprovalTierForTab is the task-595 four-tier single-select on the
// approval-posture axis: ask|auto|yolo|autopilot, one click lands the axis
// exactly on the picked tier. Picking any of the three attended tiers while
// autopilot holds also leaves autopilot in the same call — on the mode bar the
// yolo tier is plain yolo, not an autopilot alias — while picking the
// autopilot tier delegates to the task-465 engagement (auto-assumes yolo).
// The plan/goal axis rides along untouched. Programmatic writers that must
// keep (autopilot, yolo) coherent keep using SetToolApprovalModeForTab.
func (a *App) SetApprovalTierForTab(tabID, tier string) []string {
	if strings.ToLower(strings.TrimSpace(tier)) == "autopilot" {
		a.SetCollaborationModeForTab(tabID, "autopilot")
		return nil
	}
	return a.setToolApprovalModeForTabInner(tabID, tier, true)
}

func (a *App) setToolApprovalModeForTabInner(tabID, mode string, tierSingleSelect bool) []string {
	tab := a.tabByID(tabID)
	if tab == nil {
		return nil
	}
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	mode = normalizeToolApprovalMode(mode)
	plan := tabModeHasPlan(a.tabRuntimeSnapshot(tab).currentMode())
	a.mu.Lock()
	if a.tabs[tab.ID] != tab {
		a.mu.Unlock()
		return nil
	}
	tab.toolApprovalMode = mode
	tab.mode = tabModeFromAxes(plan, mode == control.ToolApprovalYolo)
	// Task 325 reverse linkage (fail-closed): autopilot on + approval leaving
	// yolo must not persist, and blocking the approval switch here would also
	// trap heartbeat runs that legitimately set their task's mode — so the
	// unattended flag is the side that yields. The live controller keeps its
	// built-in autopilot posture until the next rebuild, but its approval mode
	// is updated below in the same call, so no build accepts the combination.
	// Task 595: the composer tier path additionally treats a yolo pick under a
	// holding autopilot as leaving the tier (single-select bar).
	autopilotClosed := closeAutopilotForOffYolo(tab, mode)
	// tierYoloLeave marks the deliberate yolo-pick leave: no surprise notice
	// (the mode bar moving is the feedback), guard cleanup still runs.
	tierYoloLeave := false
	if !autopilotClosed && tierSingleSelect {
		tierYoloLeave = closeAutopilotForTier(tab)
		autopilotClosed = tierYoloLeave
	}
	ctrl := tab.Ctrl
	tabIDForSave := tab.ID
	guardTopic := strings.TrimSpace(tab.TopicID)
	a.mu.Unlock()
	if autopilotClosed && !tierYoloLeave {
		a.noticeCodeForTab(tabIDForSave, event.LevelWarn, NoticeCodeAutopilotClosedOffYolo, autopilotClosedOffYoloText)
	}
	if autopilotClosed {
		// Task 326: symmetric cleanup — once autopilot is off there is no
		// session left for the guard to watch.
		a.clearAutopilotGuard(guardTopic)
	}
	drained := applyTabToolApprovalModeToController(ctrl, mode)
	a.mu.Lock()
	if a.tabs[tabIDForSave] == tab {
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	return drained
}

// SetSubagentPolicyForTab sets the per-session sub-agent delegation tier for a
// tab (light|balanced|aggressive). The value is persisted by the controller to
// the session's BranchMeta, so it survives restart. Returns an error on an
// invalid tier or a missing tab.
func (a *App) SetSubagentPolicyForTab(tabID, policy string) error {
	tab := a.tabByID(tabID)
	if tab == nil {
		return fmt.Errorf("tab not found")
	}
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	a.mu.Lock()
	if a.tabs[tab.ID] != tab {
		a.mu.Unlock()
		return fmt.Errorf("tab changed while setting subagent policy")
	}
	tab.subagentPolicy = policy
	ctrl := tab.Ctrl
	a.mu.Unlock()
	if ctrl != nil {
		if err := ctrl.SetSubagentPolicy(policy); err != nil {
			return err
		}
	}
	a.mu.Lock()
	if a.tabs[tab.ID] == tab {
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	return nil
}

type ToolView struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	ReadOnlyHint    bool   `json:"readOnlyHint,omitempty"`
	DestructiveHint bool   `json:"destructiveHint,omitempty"`
	SchemaError     string `json:"schemaError,omitempty"`
}

// subagentOverrideFor resolves a per-name subagent override with the same
// underscore/hyphen alias fallback the runtime dispatch uses
// (boot.SubagentModelKeys) — an exact-key read would show a legacy
// `security_review` config entry as "inherit default" while it still won at
// dispatch time.
func subagentOverrideFor(overrides map[string]string, name string) string {
	for _, key := range boot.SubagentModelKeys(name) {
		if v := strings.TrimSpace(overrides[key]); v != "" {
			return v
		}
	}
	return ""
}

// AvailableSubagentTools lists the tool names a subagent profile's
// "available tools" picker may offer. Scoped to compile-time builtins for
// v1 — MCP/plugin tools are per-session/per-connection and would need a new
// live-registry accessor on control.Capabilities to enumerate safely; a
// profile's allowed-tools already degrades gracefully (FilterRegistry drops
// unknown names silently) if extended to MCP names by hand later. Tools that
// are always excluded from every subagent regardless of an explicit
// allowlist (agent.AlwaysHiddenSubagentTools) are left out entirely — they'd
// be a selectable no-op otherwise.
func (a *App) AvailableSubagentTools() []ToolView {
	hidden := map[string]bool{}
	for _, name := range agent.AlwaysHiddenSubagentTools() {
		hidden[name] = true
	}
	entries := tool.BuiltinContractEntries()
	out := make([]ToolView, 0, len(entries))
	for _, e := range entries {
		if hidden[e.Name] {
			continue
		}
		out = append(out, ToolView{Name: e.Name, Description: e.Description, ReadOnlyHint: e.ReadOnly})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sameStringList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ModelInfo is one (provider, model) the bottom switcher can pick. Ref ("provider/
// model") is what SetModel takes; Provider/Model are for display.
type ModelInfo struct {
	Ref           string `json:"ref"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Current       bool   `json:"current"`
	ContextWindow int    `json:"contextWindow,omitempty"`
	Vision        bool   `json:"vision,omitempty"`
	DisplayName   string `json:"displayName,omitempty"`
}

type EffortInfo struct {
	Options   []provider.ReasoningOption `json:"options,omitempty"`
	Supported bool                       `json:"supported"`
	Current   string                     `json:"current"`
	Default   string                     `json:"default"`
	Levels    []string                   `json:"levels"`
	// AliasFold carries the MiMo identity mark (task effortfix2): display
	// surfaces fold the four honest tiers only for this family — never by
	// sniffing the vocabulary's shape.
	AliasFold bool `json:"aliasFold,omitempty"`
}

// Models flattens the configured providers into their (provider, model) pairs —
// the switcher's options — marking the active one. A vendor with a `models` list
// yields one entry per model, all sharing the same endpoint/key. Unconfigured
// providers are skipped. Result is non-nil: the frontend reads .length, so a nil
// slice (JSON null) would crash the switcher on an empty list.
func (a *App) Models() []ModelInfo {
	return a.ModelsForTab("")
}

// mergeExtensionModelInfos adds namespaced plugin models from the controller's
// merged provider catalog. Base descriptors are already represented by out;
// plugin refs need no provider-access gate because enabling the package grants
// access. A nil catalog leaves the config-backed list untouched.
func mergeExtensionModelInfos(out []ModelInfo, catalog []provider.Descriptor, curModel string) []ModelInfo {
	if len(catalog) == 0 {
		return out
	}
	seen := make(map[string]bool, len(out)+len(catalog))
	for _, info := range out {
		seen[info.Ref] = true
	}
	for _, d := range catalog {
		ref := strings.TrimSpace(d.Ref)
		owner := providerext.PluginRefOwner(ref)
		if ref == "" || owner == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		providerName := "plugin/" + owner
		model := strings.TrimPrefix(ref, providerName+"/")
		out = append(out, ModelInfo{Ref: ref, Provider: providerName, Model: model, Current: ref == curModel})
	}
	return out
}

// extensionModelDescriptor finds a plugin-namespaced ref in a controller's
// merged catalog: an exact match, or the prefix form where ref names the
// provider and the descriptor adds the model segment. Non-plugin refs never
// match — they belong to the config catalog.
func extensionModelDescriptor(catalog []provider.Descriptor, ref string) (provider.Descriptor, bool) {
	ref = strings.TrimSpace(ref)
	if providerext.PluginRefOwner(ref) == "" {
		return provider.Descriptor{}, false
	}
	for _, d := range catalog {
		if d.Ref == ref || strings.HasPrefix(d.Ref, ref+"/") {
			return d, true
		}
	}
	return provider.Descriptor{}, false
}

func modelProviderAccessAllowed(access []string, name string) bool {
	if access == nil {
		return true
	}
	name = strings.TrimSpace(name)
	for _, candidate := range access {
		if strings.TrimSpace(candidate) == name {
			return true
		}
	}
	return false
}

// providerCatalogForTab returns the tab controller's merged provider catalog
// (extension sidecar providers over the config base), or nil when the tab has
// no live controller or no sidecar declared providers.
func (a *App) providerCatalogForTab(tab *WorkspaceTab) []provider.Descriptor {
	if tab == nil {
		return nil
	}
	if ctrl := a.controllerForTab(tab); ctrl != nil {
		return ctrl.ProviderCatalog()
	}
	return nil
}

type activeRuntimeWork struct {
	running        bool
	pendingPrompt  bool
	backgroundJobs int
}

func controllerActiveRuntimeWork(ctrl control.SessionAPI) activeRuntimeWork {
	if ctrl == nil {
		return activeRuntimeWork{}
	}
	status := ctrl.RuntimeStatus()
	return activeRuntimeWork{
		running:        status.Running,
		pendingPrompt:  status.PendingPrompt,
		backgroundJobs: status.BackgroundJobs,
	}
}

func (w activeRuntimeWork) active() bool {
	return w.running || w.pendingPrompt || w.backgroundJobs > 0
}

func controllerHasActiveRuntimeWork(ctrl control.SessionAPI) bool {
	return controllerActiveRuntimeWork(ctrl).active()
}

// rebuildBusyError reports a rebuild rejected because the controller still has
// a running turn, pending prompt, or background jobs. Typed so the
// deferred-rebuild retry loop can keep waiting instead of giving up.
type rebuildBusyError struct {
	setting string
	work    activeRuntimeWork
}

func (e *rebuildBusyError) Error() string {
	return fmt.Sprintf(
		"active work is still running; running=%t; pending_prompt=%t; background_jobs=%d; finish or cancel the current turn, answer pending prompts, and stop background jobs before changing %s",
		e.work.running,
		e.work.pendingPrompt,
		e.work.backgroundJobs,
		e.setting,
	)
}

func rebuildControllerActiveWorkErrorFor(ctrl control.SessionAPI, setting string) error {
	work := controllerActiveRuntimeWork(ctrl)
	if !work.active() {
		return nil
	}
	return &rebuildBusyError{setting: setting, work: work}
}

type sessionLeaseBusyError struct {
	setting string
	err     error
}

func (e *sessionLeaseBusyError) Error() string {
	// The raw SessionLeaseError text carries the session path and the
	// holder's host-pid-writer id; every user-facing surface must render
	// this wrapper instead. An empty setting means the failure gated opening
	// the session itself (startup bind), not changing a setting on it.
	//
	// Task 272 L3: when the holder resolves to a concrete pid that is not
	// this process, name it and hand over an EXISTING next step — restart
	// reaps leftovers automatically (ReapOrphanSpawns), taskkill is there
	// for impatience. The old "close the other window" advice was dead-end
	// advice for the orphan-serve incidents ②③: there was no other window.
	setting := strings.TrimSpace(e.setting)
	base := "this session is already open in another Reasonix window or still running in the background; close the other window or open a copy"
	var leaseErr *agent.SessionLeaseError
	if errors.As(e.err, &leaseErr) && leaseErr != nil && leaseErr.Info != nil {
		if holderPID := leaseErr.Info.PID; holderPID > 0 {
			if holderPID == os.Getpid() {
				// Task 485: the holder resolves to THIS process — "another
				// Reasonix window" is a dead-end lead (the 2026-10-05 lease
				// leak logged 568 of these with no second window in
				// existence). The real owner is a tab or background runtime
				// of this very instance; P0/P1 keep such holds transient.
				base = fmt.Sprintf("this session is already open in this Reasonix instance (pid %d); switch to or close its tab, or wait for its background work to finish, then retry", holderPID)
			} else {
				base = fmt.Sprintf("this session is held by a leftover background process (pid %d); restart the desktop to reap it automatically, or run taskkill /PID %d, then reopen the session", holderPID, holderPID)
			}
		}
	}
	if setting == "" {
		return base
	}
	return fmt.Sprintf("%s before changing %s", base, setting)
}

func (e *sessionLeaseBusyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func userFacingSessionLeaseError(setting string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, agent.ErrSessionLeaseHeld) {
		// Task 272 G4: this wrap used to be the ONLY thing that happened —
		// the lease-held failure reached the UI and never the log, so the
		// orphan-serve hypothesis could be neither confirmed nor refuted from
		// desktop.log. Log it with the same content the user sees.
		slog.Warn("desktop: session lease held; refusing session access",
			"setting", setting, "holder", err.Error())
		return &sessionLeaseBusyError{setting: setting, err: err}
	}
	return err
}

// sessionPathAfterSnapshot returns where a controller rebuild should keep
// persisting after the old controller was snapshotted. Snapshotting is not
// path-neutral: a snapshot conflict can recover by retargeting the controller
// (and the tab's session lease, via handleTabSessionRecovered) to a recovery
// branch, so a prevPath captured before the snapshot may be stale. Reusing the
// stale path would bind the rebuilt controller — carrying the just-recovered
// transcript — back to the original file, turning every later save into a new
// conflict that derives yet another recovery branch. Falls back to fallback
// when the controller is gone or persistence is disabled (empty SessionPath).
func sessionPathAfterSnapshot(ctrl control.SessionAPI, fallback string) string {
	if ctrl == nil {
		return fallback
	}
	if path := strings.TrimSpace(ctrl.SessionPath()); path != "" {
		return path
	}
	return fallback
}

var (
	// sessionLeaseContentionRetryInterval and sessionLeaseContentionRetryAttempts
	// bound the retry window for lease or removal-guard acquisition that hits a
	// transient in-process holder. CleanupStaleRunning and catalog persistence
	// can hold the session lease or save lock briefly while a concurrent tab
	// bind or archive begins; those callers must not surface a spurious
	// "already open in another Reasonix window" error for ownership that is
	// genuinely free once the short operation finishes. A lease held by another
	// window or process stays held for its whole lifetime, so the bounded retry
	// still fails fast there.
	sessionLeaseContentionRetryInterval = 50 * time.Millisecond
	sessionLeaseContentionRetryAttempts = 2
)

// withSessionLeaseContentionRetry retries acquire while it fails with
// agent.ErrSessionLeaseHeld, absorbing sub-second contention windows created
// by transient in-process lease or save-lock holders. Any other error is
// returned immediately, and a lease that remains held after the bounded
// retries is reported as-is.
func withSessionLeaseContentionRetry[T any](acquire func() (T, error)) (T, error) {
	var zero T
	for attempt := 0; ; attempt++ {
		got, err := acquire()
		if err == nil {
			return got, nil
		}
		if !errors.Is(err, agent.ErrSessionLeaseHeld) || attempt >= sessionLeaseContentionRetryAttempts {
			if errors.Is(err, agent.ErrSessionLeaseHeld) {
				// Task 272 G4: the retry loop used to give up silently — a
				// lease that outlived the contention window (i.e. a real
				// foreign holder) left no trace in desktop.log.
				slog.Warn("desktop: session lease still held after contention retries",
					"attempts", attempt+1, "holder", err.Error())
			}
			return zero, err
		}
		time.Sleep(sessionLeaseContentionRetryInterval)
	}
}

func (a *App) ensureTabSessionLeaseForRebuild(tab *WorkspaceTab, path, setting string) error {
	transition, reserveErr := a.reserveSessionRuntimePath(tab, path)
	if reserveErr != nil {
		return userFacingSessionLeaseError(setting, reserveErr)
	}
	if _, err := withSessionLeaseContentionRetry(func() (struct{}, error) {
		if err := tab.ensureSessionLease(path); err != nil {
			if a.canReclaimCurrentProcessSessionLease(tab, path, err) {
				// Task 456: the reclaim decision may have passed because every
				// same-key holder in this process is a zombie (bound lease, no
				// controller, no build). Those zombies hold the OS lock the
				// reclaim needs, so release them first; with none released the
				// call is a no-op and the reclaim behaves exactly as before.
				a.releaseZombieSessionLeaseHoldersForKey(sessionRuntimeKey(path), tab)
				if lease, reclaimErr := agent.TryReclaimCurrentProcessSessionLease(path); reclaimErr == nil {
					tab.adoptSessionLease(lease)
					return struct{}{}, nil
				} else {
					err = reclaimErr
				}
			}
			return struct{}{}, err
		}
		return struct{}{}, nil
	}); err != nil {
		a.rollbackSessionRuntimePath(transition)
		return userFacingSessionLeaseError(setting, err)
	}
	a.commitSessionRuntimePath(transition)
	return nil
}

// leaseReclaimDecision answers whether a lease error may be reclaimed by this
// process (task 244 B5). nil Info keeps the historical "attempt the reclaim,
// the OS lock is the arbiter" path; a lease owned by this PID and writer is
// ours; anything else is a foreign runtime. With orphanReclaim on, a foreign
// lease whose recorded owner process no longer exists is a crash leftover
// (orphan) and may be taken back — a live foreign owner is still respected.
// Pure so both switch states are testable without a real lease.
func leaseReclaimDecision(info *agent.SessionLeaseInfo, ownPID int, ownWriter string, orphanReclaim bool, pidAlive func(int) bool) bool {
	if info == nil {
		return true
	}
	if info.PID == ownPID && info.WriterID == ownWriter {
		return true
	}
	if !orphanReclaim {
		return false
	}
	return !pidAlive(info.PID)
}

// experimentalOrphanLeaseReclaim reports whether orphan handling may reclaim
// this lease. It reads the merged experimental_orphan_handling switch (task
// 449, which folded task 244 B5 + B4 into one key) at call time (S4) so a
// settings toggle applies to the next reclaim without a restart; config.Load
// normalizes, so a legacy task-244 key still on is folded in by
// migrateOrphanHandlingMerge. Missing config = off.
func (a *App) experimentalOrphanLeaseReclaim() bool {
	cfg, err := config.Load()
	if err != nil {
		return false
	}
	return cfg.Agent.ExperimentalOrphanHandling
}

func (a *App) canReclaimCurrentProcessSessionLease(tab *WorkspaceTab, path string, err error) bool {
	key := sessionRuntimeKey(path)
	if tab == nil || key == "" || !errors.Is(err, agent.ErrSessionLeaseHeld) {
		return false
	}
	var leaseErr *agent.SessionLeaseError
	if !errors.As(err, &leaseErr) || leaseErr == nil {
		return false
	}
	// A readable info naming a foreign runtime is respected here; reclaim
	// would refuse it anyway. A nil Info (lease.json deleted by the user,
	// quarantined by AV, or torn by a crash) must still attempt the reclaim:
	// the OS lock is the arbiter there, and refusing on missing metadata
	// wedges a session nobody actually holds as permanently busy.
	// Task 244 B5: with the experiment on, a foreign lease whose recorded
	// owner process is DEAD is an orphan (crash leftover), not a holder —
	// taking it back is the registry's "reclaim orphans by instance identity
	// after restart". A live foreign owner keeps being respected, exactly as
	// before; the switch off keeps the original decision byte-for-byte.
	if leaseErr.Info != nil && !leaseReclaimDecision(leaseErr.Info, os.Getpid(), agent.SessionWriterID(), a.experimentalOrphanLeaseReclaim(), desktopProcessAlive) {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, candidate := range a.runtimeTabsLocked() {
		if candidate == nil || candidate == tab {
			continue
		}
		if candidate.sessionLeaseRuntimeKey() == key {
			// Task 456: a sibling that bound the lease but never published a
			// controller and has no build in flight is a zombie holder — the
			// incident's self-deadlock shape (this window's own runtime holds
			// the lease while the visible tab is refused). It cannot release
			// itself, so it must not veto the reclaim forever; the caller
			// releases it via releaseZombieSessionLeaseHoldersForKey before
			// reclaiming. A functional or still-building sibling vetoes.
			if !a.zombieLeaseHolderLocked(candidate, key) {
				return false
			}
			continue
		}
		if candidate.Ctrl != nil && sessionRuntimeKey(candidate.currentSessionPath()) == key {
			return false
		}
	}
	// A detached runtime's controller still holds the OS lock; refuse reclaim
	// even when PID matches (#6955). A detached zombie (no controller, no
	// build) is handled by the same release as above.
	if detached := a.detachedSessions[key]; detached != nil && detached.Ctrl != nil {
		return false
	}
	return true
}

// SetModel switches the active model and carries the current conversation into the
// new model's session, so the chat continues seamlessly and subsequent turns use
// the new model. No-op if name is already active or the controller is down.
func (a *App) SetModel(name string) error {
	return a.SetModelForTab("", name)
}

// persistTabModelIfCurrent repairs stale model metadata without letting an
// older default overwrite a newer explicit model switch. Model switches use
// the same runtimeRebuildMu, so whichever operation acquires it last owns the
// persisted provider identity.
func (a *App) persistTabModelIfCurrent(tab *WorkspaceTab, model string) error {
	model = strings.TrimSpace(model)
	if tab == nil || model == "" {
		return nil
	}
	a.runtimeRebuildMu.Lock()
	defer a.runtimeRebuildMu.Unlock()

	a.mu.RLock()
	if tab.removed || a.tabs[tab.ID] != tab {
		a.mu.RUnlock()
		return fmt.Errorf("tab %q changed while persisting model; retry", tab.ID)
	}
	if tab.Ctrl == nil || strings.TrimSpace(tab.model) != model {
		a.mu.RUnlock()
		return nil
	}
	a.mu.RUnlock()

	path := a.currentSessionPathFor(tab)
	if path == "" {
		return nil
	}
	if err := agent.SetBranchModelPreserveUpdated(path, model); err != nil {
		return fmt.Errorf("persist selected model: %w", err)
	}
	return nil
}

type modelSwitchTiming struct {
	Total          time.Duration
	LockWait       time.Duration
	Prepare        time.Duration
	Config         time.Duration
	Snapshot       time.Duration
	Build          time.Duration
	LeaseAndResume time.Duration
	SwapAndPersist time.Duration
	Outcome        string
}

// resolveTabModelRef validates a user-facing model name against the tab's
// config and returns the canonical "provider/model" ref plus the resolved
// entry, mirroring plugin-namespaced fallback and provider-access gating.
// Shared by the model-switch fast path and the build+swap path (task 148) so
// the two cannot drift on what counts as a switchable model. For
// plugin-namespaced refs the descriptor ref comes back as the canonical ref
// and pluginRef is true (the entry stays nil — extension sidecars own it).
func (a *App) resolveTabModelRef(tab *WorkspaceTab, workspaceRoot, name string) (string, *config.ProviderEntry, bool, error) {
	cfg, err := config.LoadForRoot(workspaceRoot)
	if err != nil {
		return "", nil, false, err
	}
	entry, ok := cfg.ResolveModel(name)
	pluginRef := false
	if !ok {
		// Plugin-namespaced refs belong to extension sidecars: validate them
		// against the tab controller's merged catalog instead of the config.
		if d, found := extensionModelDescriptor(a.providerCatalogForTab(tab), name); found {
			pluginRef = true
			ok = true
			name = d.Ref
		}
	}
	if !ok {
		return "", nil, false, fmt.Errorf("unknown model %q", name)
	}
	if !pluginRef {
		if !modelProviderAccessAllowed(cfg.Desktop.ProviderAccess, entry.Name) {
			return "", nil, false, fmt.Errorf("model %q is not available because provider %q is not added", name, entry.Name)
		}
		name = entry.Name + "/" + entry.Model
	}
	return name, entry, pluginRef, nil
}

// modelSwitchPersonaBoundary reports whether the switch crosses the official
// DeepSeek-V4-Pro persona boundary (task 602). The persona is prepended to the
// cache-stable system prompt at boot (config.ApplyOfficialDeepSeekV4ProPersona)
// and ReasoningLanguageForEntry pins its auto reasoning language — neither has
// an override seam, so crossing the boundary keeps the build+swap path, which
// re-derives the prompt exactly as boot does. Plugin-namespaced targets (nil
// entry) never apply the persona.
func modelSwitchPersonaBoundary(workspaceRoot, currentRef string, target *config.ProviderEntry) bool {
	cfg, err := config.LoadForRoot(workspaceRoot)
	if err != nil {
		return false // unreadable config declines the gate; resolution fails later either way
	}
	current := false
	if ref := strings.TrimSpace(currentRef); ref != "" {
		if e, ok := cfg.ResolveModel(ref); ok {
			current = config.AppliesOfficialDeepSeekV4ProPersona(e)
		}
	}
	targetApplies := target != nil && config.AppliesOfficialDeepSeekV4ProPersona(target)
	return current != targetApplies
}

// modelSwitchIdentity carries what the fast path rebinding writes into the
// controller and the tab: SetModelIdentity fields plus the tab label.
type modelSwitchIdentity struct {
	label      string
	balanceURL string
	balanceKey string
	imageInput *bool
}

// modelSwitchExtras derives the entry-scoped payload the hot switch needs
// (task 602): the agent override scalars plus the controller identity, exactly
// the surfaces a rebuild re-derives from the new entry (boot folds the same
// pricing/window/high-speed inputs into the executor and the controller).
func modelSwitchExtras(workspaceRoot, ref string, entry *config.ProviderEntry) (agent.ModelOverrideExtras, modelSwitchIdentity, error) {
	if entry == nil {
		// Plugin-namespaced ref: the sidecar owns the entry; the agent-side
		// resolver seam constructs the provider, zero extras keep the
		// construction scalars.
		return agent.ModelOverrideExtras{}, modelSwitchIdentity{}, nil
	}
	cfg, err := config.LoadForRoot(workspaceRoot)
	if err != nil {
		return agent.ModelOverrideExtras{}, modelSwitchIdentity{}, err
	}
	extras := agent.ModelOverrideExtras{
		Pricing:         entry.Price,
		ContextWindow:   entry.ContextWindow,
		MaxOutputTokens: entry.MaxOutputTokens,
		HighSpeedModels: boot.HighSpeedModelsFor(cfg, entry.HighSpeedModels),
	}
	// Label mirrors boot's derivation (entry.Model, plus the planner suffix
	// when a planner model is configured). The frozen image gate mirrors boot's
	// capability resolution; a catalog entry without modality data stays
	// conservative exactly as a rebuild would.
	label := entry.Model
	if planner := strings.TrimSpace(cfg.Agent.PlannerModel); planner != "" {
		if pe, ok := cfg.ResolveModel(planner); ok {
			label = entry.Model + " + planner " + pe.Model
		}
	}
	imageEnabled := config.NewModelCapabilityResolver().Resolve(entry).State == config.CapabilitySupported
	identity := modelSwitchIdentity{
		label:      label,
		balanceURL: entry.BalanceURL,
		balanceKey: entry.APIKey(),
		imageInput: &imageEnabled,
	}
	return extras, identity, nil
}

func (a *App) SetModelForTab(tabID, name string) (retErr error) {
	if name == "" {
		return nil
	}
	if a.isRemoteTab(tabID) {
		return a.SetRemoteTabModel(tabID, name)
	}
	if a.ctx == nil {
		return nil
	}
	tab := a.tabByID(tabID)
	if tab == nil {
		return nil
	}
	pendingSequence := a.deferredRebuildSequence(tab.ID)
	a.mu.RLock()
	currentModel := tab.model
	a.mu.RUnlock()
	if name == currentModel {
		return nil
	}
	timing := modelSwitchTiming{}
	totalStarted := time.Now()
	defer a.recordModelSwitchTiming(tab.ID, &timing, totalStarted, &retErr)
	// Same build+swap shape as rebuildSetting; hold the same lock so a settings
	// rebuild (manual or from the deferred-rebuild retry loop) and a model
	// switch cannot interleave on one tab.
	stageStarted := time.Now()
	a.runtimeRebuildMu.Lock()
	timing.LockWait = time.Since(stageStarted)
	defer a.runtimeRebuildMu.Unlock()
	stageStarted = time.Now()
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	// Task 148 per-request fast path (mirrors the task-334 effort seam in
	// SetEffortForTab): a same-provider target arms a session-scoped model
	// override on the running controller, so the next request carries the new
	// model with no runtime rebuild. Task 602 lifts the same-provider
	// restriction: every destination-scoped surface (protocol shaping, wire,
	// pricing, window scalars, controller identity) follows the override, so
	// cross-provider targets take this path too. An active turn is no longer
	// a rejection reason — the in-flight request stays frozen and the
	// override lands from the next request freeze — while the build+swap path
	// below keeps its active-work guard for everything that declines here
	// (no controller, persona-boundary crossings, recovery-forked sessions).
	stageStarted = time.Now()
	switchSnap := a.tabRuntimeSnapshot(tab)
	switchRef, switchEntry, _, err := a.resolveTabModelRef(tab, switchSnap.workspaceRoot, name)
	if err != nil {
		return err
	}
	timing.Config = time.Since(stageStarted)
	// Alias-folded same-model short circuit: the raw check above runs before
	// resolution; tab.model holds the canonical ref the tab currently runs.
	a.mu.RLock()
	sameModel := switchRef == tab.model
	a.mu.RUnlock()
	if sameModel {
		slog.Info("desktop: model switch", "tab", tabID, "path", "fast-same-model", "model", switchRef) // task 148: same-model exits before arming anything.
		return nil
	}
	if ctrl := a.controllerForTab(tab); ctrl != nil {
		if !modelSwitchPersonaBoundary(switchSnap.workspaceRoot, tab.model, switchEntry) {
			if setter, ok := ctrl.(interface {
				SetSessionModelOverride(string, agent.ModelOverrideExtras) bool
			}); ok {
				extras, identity, extrasErr := modelSwitchExtras(switchSnap.workspaceRoot, switchRef, switchEntry)
				if extrasErr != nil {
					return extrasErr
				}
				if setter.SetSessionModelOverride(switchRef, extras) {
					if idSetter, ok := ctrl.(interface {
						SetModelIdentity(ref, label, balanceURL, balanceKey string, imageInput *bool)
					}); ok {
						idSetter.SetModelIdentity(switchRef, identity.label, identity.balanceURL, identity.balanceKey, identity.imageInput)
					}
					a.mu.Lock()
					tab.model = switchRef
					tab.Label = identity.label
					a.saveTabsLocked()
					a.mu.Unlock()
					// Same sidecar rationale as the rebuild path: empty sessions do
					// not autosave a turn, so persisting the provider identity here
					// keeps a later startup on the provider the tab actually runs, and
					// keeps last-click-wins across overlapping switches.
					if path := a.currentSessionPathFor(tab); path != "" {
						if err := agent.SetBranchModelPreserveUpdated(path, switchRef); err != nil {
							return fmt.Errorf("persist selected model: %w", err)
						}
					}
					// A model switch changes the pricing context; discard the
					// session-local automatic wallet hint, same as the rebuild path.
					tab.clearRuntimeDisplayCurrency()
					slog.Info("desktop: model switch", "tab", tabID, "path", "fast-per-request", "model", switchRef) // task 148/602: fast path observability (paired against runtime build end path=fallback).
					return nil
				}
			}
		} else {
			// Task 602: the official DeepSeek-V4-Pro persona is baked into the
			// session's cache-stable system prompt and has no override seam —
			// crossing its boundary keeps the rebuild path, which re-derives
			// the prompt exactly as boot does.
			slog.Info("desktop: model switch", "tab", tabID, "path", "fallback-persona-boundary", "model", switchRef)
		}
	}
	prevPath := a.sessionPathForSettingsRebuild(tab)
	if a.controllerForTab(tab) == nil && prevPath != "" {
		a.attachExistingSessionRuntime(tab, prevPath, a.ctx)
	}
	if err := rebuildControllerActiveWorkErrorFor(a.controllerForTab(tab), "model"); err != nil {
		return err
	}
	if err := a.ensureTabControllerWorkspace(tab); err != nil {
		return err
	}
	prevPath = a.sessionPathForSettingsRebuild(tab)
	if a.controllerForTab(tab) == nil && prevPath != "" && a.attachExistingSessionRuntime(tab, prevPath, a.ctx) {
		prevPath = a.reconciledSessionPathForTab(tab)
		if prevPath == "" {
			prevPath = a.currentSessionPathFor(tab)
		}
		if err := rebuildControllerActiveWorkErrorFor(a.controllerForTab(tab), "model"); err != nil {
			return err
		}
	}
	timing.Prepare = time.Since(stageStarted)
	// Snapshot the tab profile under a.mu: SetModeForTab/SetGoalForTab and the
	// event sink write these fields under the lock while this rebuild runs
	// off-lock.
	stageStarted = time.Now()
	snap := a.tabRuntimeSnapshot(tab)
	runtime := snap.normalizedRuntime()
	// Same resolution as the fast path above (task 148): one helper so the
	// build+swap fallback cannot drift from what the fast path accepts.
	canonicalRef, entry, pluginRef, err := a.resolveTabModelRef(tab, snap.workspaceRoot, name)
	if err != nil {
		return err
	}
	name = canonicalRef
	effortOverride := cloneStringPtr(snap.effort)
	if effortOverride != nil && !pluginRef {
		normalized, err := config.NormalizeEffort(entry, config.EffortDisplay(&config.ProviderEntry{Effort: *effortOverride}))
		if err != nil {
			effortOverride = nil
		} else {
			effortOverride = &normalized
		}
	}
	timing.Config = time.Since(stageStarted)

	stageStarted = time.Now()
	var carried []provider.Message
	oldCtrl := a.controllerForTab(tab)
	if oldCtrl != nil {
		if prevPath == "" {
			prevPath = oldCtrl.SessionPath()
		}
		if err := a.ensureTabSessionLeaseForRebuild(tab, prevPath, "model"); err != nil {
			return err
		}
		if err := a.snapshotTabForAction(tab, "changing model"); err != nil {
			return err
		}
		prevPath = sessionPathAfterSnapshot(oldCtrl, prevPath)
		carried = oldCtrl.History()
	}
	timing.Snapshot = time.Since(stageStarted)

	// Preserve the shared plugin host across controller rebuilds — the tab
	// stays in the same workspace root, so MCP processes must not be restarted.
	sharedHost := a.lookupSharedHost(snap.sharedHostKey)

	stageStarted = time.Now()
	runtimeBuildStart := time.Now()
	slog.Info("desktop: runtime build begin", "tab", tabID, "trigger", "set-model", "model", name, "autopilot", tab.autopilot) // task 196: name who is building a runtime, so a startup burst can be attributed instead of inferred.
	defer logRuntimeBuildEnd("set-model", runtimeBuildStart, sharedHost != nil)                                                // task 334: begin→end pair (begin → function exit).
	newCtrl, err := boot.Build(a.bootContext(), boot.Options{
		RestartUpdater:             restartUpdaterAdapter{a},
		AutonomousUpdateController: newAutonomousUpdateController(a),
		WorktreeProjectOpener:      appWorktreeProjectOpener{app: a}, // 任务128：会话自主建项目——注册为项目并开后台 tab
		Model:                      name,
		Autopilot:                  tab.autopilot,
		MaxRuntime:                 tab.autopilotMaxRuntime,
		AutopilotApprovalGrace:     tab.autopilotApprovalGrace,
		AutopilotAskTimeoutEnabled: tab.autopilotAskTimeoutEnabled,
		AutopilotAskWait:           tab.autopilotAskWait,
		AutopilotAskAutoContinue:   tab.autopilotAskAutoContinue,
		RequireKey:                 false,
		StatsSource:                "desktop",
		TaskStore:                  a.taskStore(),
		OnConfigLoadWarnings:       a.configLoadWarningsHandler(),
		Sink:                       snap.sink,
		WorkspaceRoot:              snap.workspaceRoot,
		SessionDir:                 sessionDirForSnapshot(snap),
		EffortOverride:             cloneStringPtr(effortOverride),
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
		// Keep the private temporary directory across model switches (#7575).
		SessionTemp: sessionTempFromController(oldCtrl),
	})
	if err != nil {
		return err
	}
	timing.Build = time.Since(stageStarted)
	a.bindControllerDisplayRecorder(newCtrl)
	configureControllerRuntime(newCtrl, oldCtrl, runtime)

	stageStarted = time.Now()
	path := agent.ContinueSessionPath(prevPath, newCtrl.SessionDir(), newCtrl.Label())
	if err := a.ensureTabSessionLeaseForRebuild(tab, path, "model"); err != nil {
		newCtrl.Close()
		return err
	}
	restoredRuntime, err := resumeControllerRuntimeWithMessages(newCtrl, carried, path, runtime)
	if err != nil {
		newCtrl.Close()
		return err
	}
	timing.LeaseAndResume = time.Since(stageStarted)
	stageStarted = time.Now()
	a.mu.Lock()
	if err := a.authorizeTabReplacementLocked(tab, newCtrl, "switching model", "model-switch"); err != nil {
		// The tab was closed/replaced while we built the new controller off-lock;
		// adopting it now would leak the runtime onto an orphaned tab and pin the
		// session lease forever.
		a.mu.Unlock()
		newCtrl.Close()
		tab.releaseSessionLease()
		return err
	}
	tab.Ctrl = newCtrl
	tab.model = name
	tab.effort = cloneStringPtr(effortOverride)
	tab.Label = newCtrl.Label()
	applyNormalizedRuntimeToTabLocked(tab, restoredRuntime)
	// Supersede any in-flight startup build: it would otherwise finish later,
	// overwrite this controller, and release/steal the tab's session lease.
	a.supersedeTabBuildLocked(tab)
	a.saveTabsLocked()
	a.mu.Unlock()
	if oldCtrl != nil {
		oldCtrl.Close()
	}
	// A refresh queued during this build still owns its newer sequence.
	a.clearDeferredRebuildVersion(tab.ID, pendingSequence)
	a.persistTabSessionPath(tab, path)
	// Keep the provider identity in the session sidecar inside the same
	// runtimeRebuildMu transaction as the controller swap. Empty sessions do
	// not autosave a turn, so without this write a later startup can prefer the
	// outgoing provider from stale metadata. Serializing it here also preserves
	// last-click-wins when a new-session default switch overlaps an explicit
	// model selection.
	if path != "" {
		if err := agent.SetBranchModelPreserveUpdated(path, name); err != nil {
			return fmt.Errorf("persist selected model: %w", err)
		}
	}
	// A model switch changes the pricing context; discard the session-local
	// automatic wallet hint and let the next balance response rebind it.
	tab.clearRuntimeDisplayCurrency()
	a.notifyTabRuntimeRebuilt(tab)
	timing.SwapAndPersist = time.Since(stageStarted)
	return nil
}

func (a *App) Effort() EffortInfo {
	return a.EffortForTab("")
}

// effortForTabDirect is the unbounded effort read: provider entry resolution
// (per-root config snapshot plus session-binding reconcile per call; a full
// disk load before task 609) followed by capability mapping. Task 421 bounds
// it behind EffortForTab in effort_fetch.go; binding surfaces must go through
// EffortForTab so a stalled read cannot hang a tab switch.
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
	// applyProviderEffortConfig → rebuildSetting, which takes the lock itself.
	// switchStarted feeds the fast-path elapsed_ms lines (task 148) so an
	// effort-vs-model fast-path comparison reads from logs directly; the
	// fallback path keeps its own runtime build end line.
	switchStarted := time.Now()
	pendingSequence := a.deferredRebuildSequence(tab.ID)
	a.runtimeRebuildMu.Lock()
	defer a.runtimeRebuildMu.Unlock()
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	// Same-level short circuit, before any attach/workspace side effects:
	// switching to the depth this tab already runs must not pay for a full
	// runtime rebuild (nor even re-enter the attach/workspace paths). tab.effort
	// holds the depth the tab currently runs — seeded from the provider entry at
	// attach time and rewritten by every effort/model switch.
	if tab.effort != nil {
		if entry, err := a.currentProviderEntryForTab(tabID); err == nil {
			if effort, err := config.NormalizeEffort(entry, level); err == nil &&
				strings.EqualFold(strings.TrimSpace(*tab.effort), effort) {
				slog.Info("desktop: effort switch", "tab", tabID, "path", "fast-same-level", "level", effort, "elapsed_ms", time.Since(switchStarted).Milliseconds()) // task 334: fast path observability (paired against runtime build end path=fallback); elapsed_ms: task 148.
				return nil
			}
		}
	}
	// Per-request fast path: providers whose effort vocabulary is request-
	// scoped (provider.Request.EffortOverride) take the new depth on the next
	// call, so switching costs no rebuild at all. Providers that cannot vary
	// depth per request return false here and fall through to the build+swap
	// path below, which keeps re-anchoring semantics (recovery branches,
	// snapshot) identical to the model switch.
	if ctrl := a.controllerForTab(tab); ctrl != nil {
		if entry, err := a.currentProviderEntryForTab(tabID); err == nil {
			effort, nerr := config.NormalizeEffort(entry, level)
			if nerr != nil && !config.IsEffortNotConfigurable(nerr) {
				// Task 354: an unsupported level is a usage error — fail fast
				// with no build instead of paying the full rebuild for a level
				// the provider never offered. The capability-class
				// (not-configurable) error keeps falling through to rebuild.
				return nerr
			}
			if nerr == nil {
				if setter, ok := ctrl.(interface {
					SetSessionEffortOverride(string) bool
				}); ok && setter.SetSessionEffortOverride(effort) {
					a.mu.Lock()
					tab.effort = &effort
					a.mu.Unlock()
					slog.Info("desktop: effort switch", "tab", tabID, "path", "fast-per-request", "level", effort, "elapsed_ms", time.Since(switchStarted).Milliseconds()) // task 334: fast path observability; declines log on the agent side with reason; elapsed_ms: task 148.
					return nil
				}
			}
		}
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
		return err
	}
	a.bindControllerDisplayRecorder(newCtrl)
	configureControllerRuntime(newCtrl, oldCtrl, runtime)
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
// remain bound for one compatibility version as no-op wrappers: they never
// require an idle tab, never save a mode, and never rebuild an agent.
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
// parsing tab state and session metas. The values are fixed; nothing reads
// them to alter runtime behavior.
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

// DirEntry is one entry in the "@" file-reference menu.
type DirEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path,omitempty"`
	IsDir       bool   `json:"isDir"`
	DisplayName string `json:"displayName,omitempty"`
	DisplayPath string `json:"displayPath,omitempty"`
}

// FilePreview is a bounded, read-only file payload for the workspace side panel.
type FilePreview struct {
	Path      string `json:"path"`
	Body      string `json:"body"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated"`
	Binary    bool   `json:"binary"`
	Kind      string `json:"kind,omitempty"`
	Mime      string `json:"mime,omitempty"`
	URL       string `json:"url,omitempty"`
	Err       string `json:"err,omitempty"`
}

type WorkspaceChangeView struct {
	Path             string   `json:"path"`
	OldPath          string   `json:"oldPath,omitempty"`
	Sources          []string `json:"sources"`
	GitStatus        string   `json:"gitStatus,omitempty"`
	Turns            []int    `json:"turns,omitempty"`
	LatestPrompt     string   `json:"latestPrompt,omitempty"`
	LatestTime       int64    `json:"latestTime,omitempty"`
	CanSessionRevert bool     `json:"canSessionRevert,omitempty"`
}

type WorkspaceChangesView struct {
	Files        []WorkspaceChangeView `json:"files"`
	GitAvailable bool                  `json:"gitAvailable"`
	GitErr       string                `json:"gitErr,omitempty"`
	GitBranch    string                `json:"gitBranch,omitempty"`
}

type WorkspaceChangeDetailView struct {
	Diff      *string `json:"diff,omitempty"`
	Source    string  `json:"source,omitempty"`
	Added     int     `json:"added,omitempty"`
	Removed   int     `json:"removed,omitempty"`
	Binary    bool    `json:"binary,omitempty"`
	Truncated bool    `json:"truncated,omitempty"`
}

const filePreviewLimit = 2 * 1024 * 1024 // 2 MiB — full file preview for the workspace panel
const fileRefSearchLimit = 20

var previewMediaMIMEs = map[string]string{
	".bmp":  "image/bmp",
	".gif":  "image/gif",
	".jpeg": "image/jpeg",
	".jpg":  "image/jpeg",
	".pdf":  "application/pdf",
	".png":  "image/png",
	".svg":  "image/svg+xml",
	".webp": "image/webp",
}

func (a *App) noticeForTab(tabID, text string) {
	tab := a.tabByID(tabID)
	if tab != nil && tab.sink != nil {
		tab.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: text})
	}
}

func (a *App) warnForTab(tabID, text string) {
	tab := a.tabByID(tabID)
	if tab != nil && tab.sink != nil {
		tab.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: text})
	}
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
	// validation, branch-meta sidecar loads). A settled tab re-derives the
	// same binding on every read, so reconcileTabWithPinnedSessionMeta itself
	// now serves the memoized outcome (tab_reconcile_memo.go) and only walks
	// when an input that could move a binding changed.
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
	// EffortForTab (431-1350ms, multi-second AV outliers per task 421). The
	// snapshot reloads itself when the tracked config files change, so value
	// freshness matches a fresh load.
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
	// effortForTabDirect is deliberately not a segment: it is pure in-memory
	// table work, so any real cost shows up as total_ms exceeding the sum of
	// the segments here.
	logEffortReadBreakdown(tabID, time.Since(readStart), reconcileMs, snapshotMs, resolveMs,
		a.cfgSnapshotReloadCount(workspaceRoot) != reloadsBefore)
	return entry, nil
}

// onboardingKeyEnv is the default provider (deepseek) key from config.Default().
const onboardingKeyEnv = "DEEPSEEK_API_KEY"

// onboardingBalanceURL doubles as a zero-token connectivity + auth probe:
// billing.FetchWithClient surfaces 401/403 for a bad key.
const onboardingBalanceURL = "https://api.deepseek.com/user/balance"

var connectKeyBalanceFetch = billing.FetchWithClient

// NativeConfirmRequest is the payload for ConfirmAction — a native OS confirmation
// dialog that replaces web-style confirm() for destructive or important actions.
type NativeConfirmRequest struct {
	Title        string `json:"title"`
	Message      string `json:"message"`
	Detail       string `json:"detail"`
	ConfirmLabel string `json:"confirmLabel"`
	CancelLabel  string `json:"cancelLabel"`
	Destructive  bool   `json:"destructive"`
}

// ConfirmAction shows a native confirmation dialog and returns true when the user
// clicks the confirm button. For destructive actions the dialog type is Warning so
// the platform can apply its danger styling (red tint on macOS, etc.).
func (a *App) ConfirmAction(req NativeConfirmRequest) (bool, error) {
	if a.ctx == nil {
		return false, nil
	}
	dialogType := runtime.QuestionDialog
	if req.Destructive {
		dialogType = runtime.WarningDialog
	}
	confirm := req.ConfirmLabel
	if confirm == "" {
		confirm = "OK"
	}
	cancel := req.CancelLabel
	if cancel == "" {
		cancel = "Cancel"
	}
	title := req.Title
	if title == "" {
		title = req.Message
	}
	body := req.Message
	if req.Detail != "" {
		if body != "" {
			body += "\n\n" + req.Detail
		} else {
			body = req.Detail
		}
	}
	defaultBtn := confirm
	if req.Destructive {
		// On destructive actions, make cancel the default so Enter / Space
		// does NOT accidentally confirm. ESC always maps to CancelButton.
		defaultBtn = cancel
	}
	result, err := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type:          dialogType,
		Title:         title,
		Message:       body,
		Buttons:       []string{confirm, cancel},
		DefaultButton: defaultBtn,
		CancelButton:  cancel,
	})
	if err != nil {
		return false, err
	}
	return result == confirm, nil
}

func (a *App) NeedsOnboarding() bool {
	cfg, err := config.LoadForRootReadOnly(a.activeWorkspaceRoot())
	if err != nil {
		// Configuration errors already surface through the startup error banner.
		// Do not cover their recovery path with an onboarding gate.
		return false
	}
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		if !modelProviderAccessAllowed(cfg.Desktop.ProviderAccess, p.Name) || !p.Configured() || len(p.ChatModelList()) == 0 {
			continue
		}
		return false
	}
	return true
}

// ConnectKey validates apiKey against the balance endpoint, persists it to
// Reasonix's global .env, and rebuilds the controller so the new key takes effect.
func (a *App) ConnectKey(apiKey string) (string, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", fmt.Errorf("key is required")
	}
	ctx, cancel := context.WithTimeout(a.reqCtx(), 8*time.Second)
	defer cancel()
	if _, err := connectKeyBalanceFetch(ctx, nil, onboardingBalanceURL, apiKey); err != nil {
		return "", fmt.Errorf("validate: %w", err)
	}
	return a.AddOfficialProviderAccess("deepseek", apiKey)
}
