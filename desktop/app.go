package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"reasonix/internal/agent"
	"reasonix/internal/billing"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/extension/providerext"
	"reasonix/internal/notify"
	"reasonix/internal/plugin"
	"reasonix/internal/provider"
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
