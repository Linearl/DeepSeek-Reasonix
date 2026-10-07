package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"reasonix/desktop/internal/instanceidentity"
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
	"reasonix/internal/fileref"
	fileenc "reasonix/internal/fileutil/encoding"
	"reasonix/internal/i18n"
	"reasonix/internal/mcpdiag"
	"reasonix/internal/mcpregistry"
	"reasonix/internal/notify"
	"reasonix/internal/plugin"
	"reasonix/internal/proc"
	"reasonix/internal/provider"
	"reasonix/internal/repair"
	"reasonix/internal/servepool"
	"reasonix/internal/sessioncatalog"
	"reasonix/internal/sessiontemp"
	"reasonix/internal/skill"
	"reasonix/internal/store"
	"reasonix/internal/taskmonitor"
	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"
)

// sessionTempFromController returns the logical-session private temporary
// directory manager for a same-session controller rebuild. Nil when the
// controller is missing or is not a *control.Controller.
func sessionTempFromController(ctrl control.SessionAPI) *sessiontemp.Manager {
	c, ok := ctrl.(*control.Controller)
	if !ok || c == nil {
		return nil
	}
	return c.SessionTemp()
}

// eventChannel is the Wails runtime event name the frontend subscribes to for the
// agent's typed event stream. One channel carries every event kind; the payload's
// `kind` field discriminates — the desktop analogue of the serve transport's SSE
// `data:` frames.
const eventChannel = "agent:event"

const singleInstanceIDPrefix = instanceidentity.Prefix

func singleInstanceID() string { return instanceidentity.ForHome(config.ReasonixHomeDir()) }

// PromptHistoryEntry is one user prompt extracted from a session JSONL file.
// The frontend uses these for ↑/↓ prompt-history navigation.
type PromptHistoryEntry struct {
	Text        string `json:"text"`
	At          int64  `json:"at"` // unix ms
	SessionPath string `json:"sessionPath"`
	Turn        int    `json:"turn"`
}

// PromptHistoryResult is returned as one Wails value. It carries one loaded tape
// segment plus the cursor needed to keep walking toward older prompts.
type PromptHistoryResult struct {
	Entries     []PromptHistoryEntry `json:"entries"`
	Nonce       string               `json:"nonce"`
	OlderCursor string               `json:"olderCursor,omitempty"`
	HasOlder    bool                 `json:"hasOlder"`
}

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

	// Task 421: bounded effort re-fetch (see effort_fetch.go). effortCache
	// holds the last completed EffortInfo per tab ID ("" = the active-tab
	// form) so a read that blows the timeout serves this instead of stalling
	// the switch-tab ancillary batch. Display data only. The stub and the
	// limit override are test-only (catalogReconcileHook convention: set
	// before concurrent calls, zero/nil in production).
	effortReadStub          func(tabID string) EffortInfo
	effortReadLimitOverride time.Duration
	effortCacheMu           sync.Mutex
	effortCache             map[string]effortCacheEntry

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

	// updaterOperationMu guards the single native download/install operation.
	// Checks are read-only and may overlap; cache mutation and installation fail
	// fast when another updater operation is already active.
	updaterOperationMu sync.Mutex
	updaterOperationID string

	// deferredRebuild tracks tabs whose settings were saved but whose runtime
	// could not refresh because the session lease was held by another process.
	deferredRebuild deferredRebuildState

	// historySliceMu guards the windowed-history background bookkeeping:
	// single-flight display-index rebuilds for live sessions and the startup
	// index-migration worker's cancel handle. Never held while calling
	// controller or session methods.
	historySliceMu              sync.Mutex
	historyIndexRebuilds        map[string]chan struct{}
	historyIndexMigrationCancel context.CancelFunc
	historyDerived              historyDerivedCache

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
	lifecycle     desktopLifecycleRuntime
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

type desktopShellRuntimeState struct {
	coordinator   *desktopShellCoordinator
	linuxRecovery *linuxWebKitRecoveryCoordinator
	trayState     string
	trayReason    string
}

type skillRootsCache struct {
	key   string
	at    time.Time
	roots []SkillRootView
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
		remoteWindowOwnerID:  newRemoteWindowOwnerID(),
		topicState:           desktopTopicState,
		worktreeReservations: worktreeRuntimeReservations{
			cleanup: map[string]struct{}{},
			merge:   map[string]struct{}{},
		},
	}
	// Task 36 Phase 1: remote takeover requests prompt the user instead of
	// yielding silently. The sink owns the wails context at emit time.
	// Task 36 Phase 2: observe serve's device lease for runtime read-only.
	a.registerRemoteWriteAuthorityHook()
	RegisterTakeoverPromptSink(func(req takeoverDecisionReq) {
		runtimeEventsEmitFallback(a.ctx, "app:takeover-request", map[string]string{
			"marker": req.Marker,
			"path":   req.Path,
			"from":   req.From,
		})
	})
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
	// Task 225: the cascade-approval resolver needs the live App to find the
	// task source's controller. One desktop App per process.
	cascadeApp = a
	// Task 254 field report: capability-routed tool calls never cross the
	// agent's context-binding point, so restart_update / restart_and_update
	// would report themselves unavailable there. Register the process-wide
	// fallbacks next to cascadeApp — same one-App-per-process contract,
	// idempotent, and left nil in hosts (CLI/serve) that cannot swap their
	// own install.
	tool.SetFallbackAutonomousUpdateController(newAutonomousUpdateController(a))
	tool.SetFallbackRestartUpdater(restartUpdaterAdapter{a})
	// Task 203: the head-divergence notice needs to tell a real second tab
	// apart from an in-process race; the answer is a tab-registry fact, so the
	// desktop registers the probe once per process (same one-App contract as
	// cascadeApp above). Left nil in CLI/serve — the local class then only
	// logs, which is the conservative behaviour.
	control.SetConcurrentDualTabProbe(a.isConversationDualOpen)
	// Only the process that claimed the pre-Wails diagnostics lock consumes
	// lifecycle evidence. This remains correct on Linux where Wails invokes
	// OnStartup before its DBus single-instance handoff.
	initializeLifecycleDiagnostics(a)
	a.startWindowsWebView2StartupFallback(ctx)
	// Task 342: if the CDP lab switch was on at boot, watch for the browser
	// child process and publish the resolved loopback port. No-op when the
	// switch (or the platform) does not support the endpoint.
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
	installSystemQuitHook()
	// Task 211 P2: clear shadow project roots (registry entries pointing into the
	// app's own session storage) before anything reads the project tree.
	pruneShadowProjectRoots()
	// Serve pool gateway is opt-in via the Settings → 集成与连接 panel; only
	// start it on launch if the user previously enabled the toggle.
	if servepoolEnabled() {
		a.startServePool(ctx)
	}
	// Task 439: built-in zcode task bus, opt-in via the lab switch (铁律 2
	// default off). With the flag off startZcodeTaskBus returns before
	// touching the network; when on, the desktop hosts the bus MCP endpoint
	// itself (127.0.0.1:8787) — no external `reasonix serve`/vbs needed.
	if cfg, err := config.Load(); err == nil {
		a.startZcodeTaskBus(cfg)
	}
	a.startTray()
	a.enableDeferredRebuildRetry()
	a.startHistoryIndexMigration()
	// Task 333: forward the event-log rotation gate into the agent's save path.
	// The agent layer cannot import config (layering), so the host pushes the
	// normalized settings once at boot and again on every settings change.
	// Before any push the agent-side value equals the default config (manual),
	// so CLI/serve hosts and tests keep today's behavior.
	if cfg, err := config.Load(); err == nil {
		agent.SetEventsAutoRotation(
			config.EventsAutoRotationMode(cfg),
			config.EventsRotationFactor(cfg),
			config.EventsRotationCapMB(cfg),
		)
		// Task 196fix2: same load pushes the tunable graph-cache LRU capacity
		// (0/unset reads as the built-in 3) — the save-path cache must reflect
		// the configured value from the first save, not the first settings open.
		// Task 499: the byte ceilings ride the same push (0/unset reads as the
		// built-in 2048/1024 MiB) so the retained set is bounded from boot.
		agent.SetSessionGraphCacheCapacity(config.DagGraphCacheCapacity(cfg))
		agent.SetSessionGraphCacheMaxBytes(config.DagGraphCacheMaxMB(cfg) << 20)
		agent.SetSessionGraphCacheEntryMaxBytes(config.DagGraphCacheEntryMaxMB(cfg) << 20)
	} else {
		// Without the push the gate stays on the pre-push default (manual =
		// today's rotating gate): a config that says "off" would keep rotating
		// until a successful push. Log the divergence instead of hiding it
		// (review finding, 2026-09-28).
		slog.Warn("desktop: events rotation gate not pushed at boot (config load failed)", "err", err)
	}
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

	// Task 297: the cold-cache compact pass ticks unconditioned; the lab
	// switch is read live inside the loop, so OFF is one config read per
	// tick and ON applies without a restart.
	a.startColdCacheCompactLoop()
	// Task 308-O4: release detached/idle runtimes (gate: env minutes, default off).
	a.startDetachedIdleReleaseLoop()
	// Task 485 P1: release self-held orphan session leases (leaked-handle
	// backstop — the lock itself cannot arbitrate a handle this process lost).
	a.startSessionLeaseLeakSweeper()
	// Task 308-O3: apply the soft memory limit from config (live-capable via
	// SetGoMemLimitMB in settings; startup applies the stored value once).
	if cfg, _, err := a.loadDesktopUserConfigForView(); err == nil {
		applyGoMemLimit(cfg)
	}
	a.heartbeat = newHeartbeatEngine(a)
	// Task 244 B1: call-time evaluation (S4) — the burn guard reads the
	// saved switch on every run, so toggling it in settings applies without a
	// restart. Missing config = off.
	a.heartbeat.idleTerminate = func() bool {
		cfg, err := config.Load()
		if err != nil {
			return false
		}
		return cfg.Agent.ExperimentalAutonomousIdleTerminate
	}
	a.heartbeat.Start()
	// Expose the scheduler's admin surface to agent tools (task 201). The
	// adapter keeps the engine as the single source of truth for validation
	// and CAS writes; CLI/serve sessions without a desktop engine keep the
	// tools failing with a dedicated "no engine attached" error.
	builtin.SetHeartbeatManager(newHeartbeatManagerAdapter(a.heartbeat))

	a.sessionCollab = newSessionCollabPump(a)
	a.sessionCollab.Start()

	a.mu.Lock()
	a.tabsRestored = make(chan struct{})
	a.mu.Unlock()
	go a.restoreOrBuildTabs()
	a.registerHistoryIndexEvents()
	a.startSessionCatalog()
	a.goSafe("refreshBotRuntime", a.refreshBotRuntime)
	a.goSafe("sendStartupPing", a.sendStartupPing)
	a.goSafe("flushMetrics", a.flushMetrics)
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
	// Task 272 G3: this exact path ran silently in the incident — the launcher
	// timed out, booted a fresh instance, and it lost the single-instance lock
	// to the still-wedged old process, then exited without one log line. Name
	// the yield before showing the existing window; the 17-minute silent gap
	// must not repeat as a mystery.
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

// restoreOrBuildTabs restores the tabs from the last session, or creates a
// default Global tab on first launch.
func (a *App) restoreOrBuildTabs() {
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
	// is launched. Two persisted entries pointing at one session would come
	// back as two builds racing for the same lease — the loser wedges in
	// lease_blocked against its own sibling ("already open in another Reasonix
	// window" with no other window). Collapse duplicates first, then retire
	// stale lease records whose recorded holder process is dead, so the fresh
	// runtime starts against a clean record. Live foreign holders and active
	// handoff reservations are respected untouched.
	f.Tabs = dedupeRestoredTabEntries(f.Tabs)
	a.reconcileRestoredSessionKeys(f.Tabs)
	if len(f.Tabs) > 0 {
		toBuild := make([]*WorkspaceTab, 0, len(f.Tabs))
		for _, entry := range f.Tabs {
			// Task 186: a persisted tab sitting on one of the host's own directories
			// (left over from the era when the global workspace rendered as a project)
			// restores as a Global tab; otherwise its first turn would index the topic
			// under a project entry the next save strips, losing it on tab close.
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
			// sidecar: a typed /new or /clear rotates the session through the
			// controller without passing App.NewSession/ClearSession, so
			// entry.Goal can be stale. Session rotation writes a stopped
			// goal-state onto the fresh path; reading it here stops a restart
			// from re-seeding the cleared goal into the rotated session. A
			// session without a sidecar keeps the persisted goal (legacy).
			tab.goal = runningTabSessionGoal(strings.TrimSpace(entry.SessionPath), strings.TrimSpace(entry.Goal))
			tab.toolApprovalMode = normalizeToolApprovalMode(entry.ToolApprovalMode)
			if tab.toolApprovalMode == control.ToolApprovalAsk && tabModeHasAutoApproveTools(entry.Mode) {
				tab.toolApprovalMode = control.ToolApprovalYolo
			}
			// Task 49 A2 + task 465 (X4 断点 C): an unattended run continues
			// across a restart. Two persistence sources feed the flag — the
			// tab entry column (task 465; covers the bare autopilot toggle
			// that has no goal sidecar) and the goal-state sidecar (task 49;
			// covers a running unattended goal) — then the task-325 gate has
			// the final say: a persisted unattended run may only come back
			// under yolo, otherwise it stays interactive (the gate refuses
			// silently here; the run is bounded as a normal goal instead).
			// The sidecar decides whether THIS run was unattended; the
			// preferences only supply the bound, because the previous
			// deadline died with the process. Without a usable bound the run
			// stays interactive rather than resuming with no limit at all.
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
		// Task 405 Q2: throttle the startup build storm and build the active
		// tab first. Restoring N tabs fires N concurrent full boot.Builds;
		// same-key builds drag each other's mcp/tool-discovery stages out to
		// 21-38s spikes (2026-09-30 diagnosis: 4-way interleave measured
		// mcp=21.3s×4). A 2-slot semaphore keeps 2 builds in flight — a
		// resource guard, not a behavior change — while ordering puts the
		// tab the user is looking at first. Background tabs are never
		// dropped: they simply start as slots free up.
		restored := orderTabsActiveFirst(toBuild, a.activeTabID)
		sem := make(chan struct{}, startupBootConcurrency)
		for _, tab := range restored {
			a.startTabControllerBuildThrottled(tab, sem)
		}
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
		// Keep provider identity explicit at the new-session boundary. A bare
		// model id is ambiguous when two configured gateways expose the same
		// model, and a provider-only ref otherwise compares unequal to the
		// canonical ref stored on a running tab.
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
		if err := a.snapshotTab(t); err != nil {
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
		// Validate saved position against current screens. Wails v2 doesn't
		// expose per-screen origin (x,y offsets) so we can only do a basic
		// sanity check. Windows border insets (commonly x=-8,y=-8) are legal;
		// large off-screen positions (unplugged external display) re-center.
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

func (a *App) mcpAppsSandboxAvailable() bool { return a.mcpAppsSandbox.available() }

func (a *App) activeMCPRuntime() (*WorkspaceTab, control.SessionAPI, string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	tab := a.activeTabLocked()
	if tab == nil {
		return nil, nil, ""
	}
	return tab, tab.Ctrl, tab.WorkspaceRoot
}

func (a *App) controllerForTab(tab *WorkspaceTab) control.SessionAPI {
	if tab == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if tab.ID != "" && !a.ownsRuntimeTabLocked(tab) {
		return nil
	}
	return tab.Ctrl
}

// currentSessionPathFor is the locked form of tab.currentSessionPath: it
// snapshots Ctrl/SessionPath under a.mu, then queries the controller off-lock.
// Use it on paths that do not otherwise hold a.mu.
func (a *App) currentSessionPathFor(tab *WorkspaceTab) string {
	if tab == nil {
		return ""
	}
	a.mu.RLock()
	ctrl := tab.Ctrl
	fallback := strings.TrimSpace(tab.SessionPath)
	a.mu.RUnlock()
	if ctrl != nil {
		if path := strings.TrimSpace(ctrl.SessionPath()); path != "" {
			return path
		}
	}
	return fallback
}

// sessionDirForSnapshot mirrors tabSessionDir for callers that hold a
// tabRuntimeSnapshot instead of reading the live tab.
func sessionDirForSnapshot(s tabRuntimeSnapshot) string {
	if s.workspaceRoot != "" {
		return desktopSessionDir(s.workspaceRoot)
	}
	if s.ctrl != nil {
		if dir := s.ctrl.SessionDir(); dir != "" {
			return dir
		}
	}
	return desktopSessionDir("")
}

func readOnlyChannelErr() error {
	return fmt.Errorf("channel session is read-only")
}

func (a *App) snapshotTab(tab *WorkspaceTab) error {
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
	return ctrl.Snapshot()
}

func (a *App) snapshotTabForAction(tab *WorkspaceTab, action string) error {
	if err := a.snapshotTab(tab); err != nil {
		a.reportTabSnapshotError(tab, action, err)
		if strings.TrimSpace(action) == "" {
			return fmt.Errorf("save current session: %w", err)
		}
		return fmt.Errorf("save current session before %s: %w", action, err)
	}
	return nil
}

func (a *App) reportTabSnapshotError(tab *WorkspaceTab, action string, err error) {
	if err == nil {
		return
	}
	tabID := ""
	if tab != nil {
		tabID = tab.ID
	}
	slog.Warn("desktop: session snapshot failed", "tab", tabID, "action", action, "err", err)
	if tab == nil || tab.sink == nil {
		return
	}
	// Autosave fires once per turn; on a persistently failing disk that would
	// stream a chat warning after every turn. Rate-limit the user-facing
	// notice per tab (the slog line above always records every failure). Saves
	// triggered by an explicit action are one-shot and always surface.
	if action == "autosave" {
		tab.saveMu.Lock()
		now := time.Now()
		if !tab.lastAutosaveWarnAt.IsZero() && now.Sub(tab.lastAutosaveWarnAt) < autosaveWarnInterval {
			tab.saveMu.Unlock()
			return
		}
		tab.lastAutosaveWarnAt = now
		tab.saveMu.Unlock()
	}
	prefix := "Session autosave failed"
	if strings.TrimSpace(action) != "" && action != "autosave" {
		prefix = "Session save failed before " + action
	}
	tab.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: prefix + ": " + err.Error()})
}

func (a *App) reconciledSessionPathForTab(tab *WorkspaceTab) string {
	if tab == nil {
		return ""
	}
	path, _ := a.reconcileTabWithPinnedSessionMeta(tab)
	if ctrl := a.controllerForTab(tab); path == "" && ctrl != nil {
		path = ctrl.SessionPath()
	}
	return path
}

func (a *App) ensureTabControllerWorkspace(tab *WorkspaceTab) error {
	if tab == nil {
		return nil
	}
	tab.reconcileMu.Lock()
	defer tab.reconcileMu.Unlock()

	a.mu.RLock()
	current := a.tabs[tab.ID]
	ctrl := tab.Ctrl
	readOnly := tab.ReadOnly
	a.mu.RUnlock()
	if current != tab || ctrl == nil || readOnly {
		return nil
	}
	if controllerHasActiveRuntimeWork(ctrl) {
		return nil
	}
	path, hasBinding := a.reconcileTabWithPinnedSessionMeta(tab)
	desiredRoot := strings.TrimSpace(tab.WorkspaceRoot)
	ctrlRoot, rootOK := safeControllerWorkspaceRoot(ctrl)
	ctrlDir, dirOK := safeControllerSessionDir(ctrl)
	if !rootOK || !dirOK {
		return nil
	}
	if !hasBinding {
		if desiredRoot == "" || strings.TrimSpace(ctrlRoot) == "" || sameDesktopPath(ctrlRoot, desiredRoot) {
			return nil
		}
	}
	desiredDir := tabSessionDir(tab)
	rootMatches := desiredRoot == "" || sameDesktopPath(ctrlRoot, desiredRoot)
	dirMatches := desiredDir == "" || sameDesktopPath(ctrlDir, desiredDir)
	if !dirMatches && path != "" {
		if validPath, _, err := validateSessionPath(ctrlDir, path); err == nil && sessionRuntimeKey(validPath) == sessionRuntimeKey(path) {
			dirMatches = true
		}
	}
	if strings.TrimSpace(ctrlRoot) == "" && dirMatches {
		rootMatches = true
	}
	if tab.Scope == "global" {
		if strings.TrimSpace(ctrlRoot) == "" {
			rootMatches = true
		}
		if sameDesktopPath(ctrlDir, config.SessionDir()) || sameDesktopPath(ctrlDir, desktopSessionDir(globalWorkspaceRoot())) {
			dirMatches = true
		}
	}
	sessionMatches := path == "" || sessionRuntimeKey(ctrl.SessionPath()) == sessionRuntimeKey(path)
	if rootMatches && dirMatches && sessionMatches {
		return nil
	}
	if err := ctrl.Snapshot(); err != nil {
		return err
	}
	ctrl.Close()

	a.mu.Lock()
	var hostKey string
	if current := a.tabs[tab.ID]; current == tab {
		tab.Ctrl = nil
		tab.Ready = false
		clearTabStartupError(tab)
		tab.ActivityStatus = ""
		if tab.sink == nil {
			tab.sink = &tabEventSink{tabID: tab.ID, app: a, ctx: a.ctx}
		}
		hostKey = takeTabSharedHostKey(tab)
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	if hostKey != "" {
		a.releaseSharedHost(hostKey)
	}

	a.buildTabController(tab)
	if tab.Ctrl == nil {
		if tab.StartupErr != "" {
			return fmt.Errorf("workspace failed to restart with corrected root: %s", tab.StartupErr)
		}
		return fmt.Errorf("workspace failed to restart with corrected root")
	}
	return nil
}

func safeControllerWorkspaceRoot(ctrl control.SessionAPI) (root string, ok bool) {
	if ctrl == nil {
		return "", false
	}
	defer func() {
		if recover() != nil {
			root = ""
			ok = false
		}
	}()
	return ctrl.WorkspaceRoot(), true
}

func safeControllerSessionDir(ctrl control.SessionAPI) (dir string, ok bool) {
	if ctrl == nil {
		return "", false
	}
	defer func() {
		if recover() != nil {
			dir = ""
			ok = false
		}
	}()
	return ctrl.SessionDir(), true
}

// Approve answers a pending approval_request by ID: allow runs the call, session
// also remembers the grant for the rest of the session.
func (a *App) Approve(id string, allow, session, persist bool) {
	ctrl := a.ctrlByTabID("")
	if ctrl != nil {
		ctrl.Approve(id, allow, session, persist)
	}
}

// ApproveTab is like Approve but scoped to a specific tab.
func (a *App) ApproveTab(tabID, id string, allow, session, persist bool) {
	ctrl := a.ctrlForRuntimeTabID(tabID)
	if ctrl != nil {
		ctrl.Approve(id, allow, session, persist)
	}
}

// ResolvePlanDecision answers a Plan card while preserving whether the user
// chose to start execution, revise the plan, or exit without executing.
func (a *App) ResolvePlanDecision(id, action string) error {
	ctrl := a.ctrlByTabID("")
	if ctrl == nil {
		return fmt.Errorf("no active session")
	}
	return ctrl.ResolvePlanDecision(id, control.PlanDecisionAction(action))
}

// ResolvePlanDecisionTab is like ResolvePlanDecision but scoped to a runtime
// tab so a delayed bridge call cannot answer a prompt in another tab.
func (a *App) ResolvePlanDecisionTab(tabID, id, action string) error {
	ctrl := a.ctrlForRuntimeTabID(tabID)
	if ctrl == nil {
		return fmt.Errorf("no active session")
	}
	return ctrl.ResolvePlanDecision(id, control.PlanDecisionAction(action))
}

// ResolveRecovery answers an Auto Guard card. action is continue|revise. For
// revise, feedback is steered into the
// agent and the pending mutation is refused in the same operation.
func (a *App) ResolveRecovery(id, action, feedback string) error {
	return a.ResolveRecoveryTab("", id, action, feedback)
}

// ResolveRecoveryTab is like ResolveRecovery but scoped to a specific tab.
func (a *App) ResolveRecoveryTab(tabID, id, action, feedback string) error {
	ctrl := a.ctrlByTabID(tabID)
	if ctrl == nil {
		return fmt.Errorf("no active session")
	}
	return ctrl.ResolveRecovery(id, agent.RecoveryAction(action), feedback)
}

// SetRecoveryCheckpointEnabled is retained as a no-op Wails surface for older
// generated frontends. Auto Guard is always built into Auto.
func (a *App) SetRecoveryCheckpointEnabled(_ bool) {}

// SetRecoveryCheckpointEnabledTab is retained as a no-op Wails surface.
func (a *App) SetRecoveryCheckpointEnabledTab(_ string, _ bool) {}

// RecoveryCheckpointEnabled is retained for older generated frontends. Auto
// Guard is always built into Auto, so it always reports true.
func (a *App) RecoveryCheckpointEnabled() bool {
	return true
}

// RecoveryCheckpointEnabledTab is the tab-scoped compatibility alias.
func (a *App) RecoveryCheckpointEnabledTab(_ string) bool {
	return true
}

// ReplayPendingPrompts asks every tab's controller to re-emit any approval/ask
// prompt that is currently blocking its run loop. The frontend calls this once
// its event subscription is live (on load/reconnect) so a session that was
// already awaiting confirmation rebuilds its modal instead of showing a
// "waiting" status with no way to answer — and no way to stop.
func (a *App) ReplayPendingPrompts() {
	a.mu.RLock()
	tabs := a.runtimeTabsLocked()
	ctrls := make([]control.SessionAPI, 0, len(tabs))
	for _, t := range tabs {
		if t.Ctrl != nil {
			ctrls = append(ctrls, t.Ctrl)
		}
	}
	a.mu.RUnlock()
	for _, ctrl := range ctrls {
		ctrl.ReplayPendingPrompts()
	}
}

// ReplayPendingPromptsForTab re-emits only the prompt owned by tabID. Tab
// switches use this scoped form so a background session's ask/approval cannot
// depend on whichever tab happens to be backend-active when the replay RPC
// arrives. ReplayPendingPrompts remains bound for reconnect compatibility.
func (a *App) ReplayPendingPromptsForTab(tabID string) {
	ctrl := a.ctrlByTabID(tabID)
	if ctrl != nil {
		ctrl.ReplayPendingPrompts()
	}
}

// SetPlanMode toggles the plan-first workflow while preserving the current
// tool-approval posture and sandbox settings.
func (a *App) SetPlanMode(on bool) {
	a.setPlanModeForTab("", on)
}

func (a *App) setPlanModeForTab(tabID string, on bool) {
	if on {
		a.SetCollaborationModeForTab(tabID, "plan")
		return
	}
	a.SetCollaborationModeForTab(tabID, "normal")
}

// SetMode applies a composer gating mode ("plan" | "yolo" | "plan-yolo" |
// anything else =
// normal) in one call, so a turn submitted right after the switch can't race a
// half-applied plan/tool-auto-approval pair.
func (a *App) SetMode(mode string) {
	a.SetModeForTab("", mode)
}

// SetModeForTab returns the pending approval prompt ids the switch
// auto-allowed, so the frontend dismisses exactly those cards and keeps the
// ones the backend still holds (plan/memory/sandbox-escape never drain, and
// auto keeps approvals an allow policy would not cover — #6432).
func (a *App) SetModeForTab(tabID, mode string) []string {
	tab := a.tabByID(tabID)
	if tab == nil {
		return nil
	}
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	normalized := normalizeTabMode(mode)
	a.mu.Lock()
	if a.tabs[tab.ID] != tab {
		a.mu.Unlock()
		return nil
	}
	tab.mode = normalized
	tab.toolApprovalMode = normalizeToolApprovalMode(tab.toolApprovalMode)
	if tabModeHasAutoApproveTools(normalized) {
		tab.toolApprovalMode = control.ToolApprovalYolo
	} else if tab.toolApprovalMode == control.ToolApprovalYolo {
		tab.toolApprovalMode = control.ToolApprovalAsk
	}
	ctrl := tab.Ctrl
	approvalMode := tab.toolApprovalMode
	tabIDForSave := tab.ID
	a.mu.Unlock()
	drained := applyTabModeToController(ctrl, normalized)
	drained = append(drained, applyTabToolApprovalModeToController(ctrl, approvalMode)...)
	a.mu.Lock()
	if a.tabs[tabIDForSave] == tab {
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	return drained
}

// modeApplier / toolApprovalApplier are the drained-id-reporting variants of
// SessionAPI's SetMode / SetToolApprovalMode. Asserted optionally so test
// fakes implementing the plain SessionAPI keep compiling (they report nil).
type modeApplier interface {
	ApplyMode(plan, autoApproveTools bool) []string
}

type toolApprovalApplier interface {
	ApplyToolApprovalMode(mode string) []string
}

func applyTabModeToController(ctrl control.SessionAPI, mode string) []string {
	if ctrl == nil {
		return nil
	}
	plan, yolo := false, false
	switch normalizeTabMode(mode) {
	case "plan":
		plan = true
	case "yolo":
		yolo = true
	case "plan-yolo":
		plan, yolo = true, true
	}
	if applier, ok := ctrl.(modeApplier); ok {
		return applier.ApplyMode(plan, yolo)
	}
	ctrl.SetMode(plan, yolo)
	return nil
}

func applyTabToolApprovalModeToController(ctrl control.SessionAPI, mode string) []string {
	if ctrl == nil {
		return nil
	}
	mode = normalizeToolApprovalMode(mode)
	if applier, ok := ctrl.(toolApprovalApplier); ok {
		return applier.ApplyToolApprovalMode(mode)
	}
	ctrl.SetToolApprovalMode(mode)
	return nil
}

func normalizeCollaborationMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "plan":
		return "plan"
	case "goal":
		return "goal"
	case "autopilot":
		return "autopilot"
	default:
		return "normal"
	}
}

func (a *App) SetCollaborationMode(mode string) {
	a.SetCollaborationModeForTab("", mode)
}

// SetComposerProfileForTab applies the controller-facing profile axes under one
// turn gate. Frontends use this before submit and after controller rebuilds so a
// turn cannot observe collaboration, approval, and goal from different UI
// generations.
func (a *App) SetComposerProfileForTab(tabID, collaborationMode, toolApprovalMode, goal string) ([]string, error) {
	if a.isRemoteTab(tabID) {
		return []string{}, nil
	}
	collaborationMode = normalizeCollaborationMode(collaborationMode)
	toolApprovalMode = normalizeToolApprovalMode(toolApprovalMode)
	goal = strings.TrimSpace(goal)

	tab := a.tabByID(tabID)
	if tab == nil {
		return []string{}, fmt.Errorf("tab is no longer available")
	}
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()

	a.mu.Lock()
	if a.tabs[tab.ID] != tab {
		a.mu.Unlock()
		return []string{}, fmt.Errorf("tab is no longer available")
	}
	tab.toolApprovalMode = toolApprovalMode
	// Task 325 reverse linkage: a composer profile that would pair autopilot
	// with a non-yolo approval posture turns autopilot off instead (same
	// fail-closed direction as SetToolApprovalModeForTab).
	autopilotClosed := closeAutopilotForOffYolo(tab, toolApprovalMode)
	// Task 326: same symmetric cleanup as the approval-switch path below.
	guardTopic := strings.TrimSpace(tab.TopicID)
	if goal != "" {
		tab.goal = goal
		tab.mode = tabModeFromAxes(false, toolApprovalMode == control.ToolApprovalYolo)
	} else {
		tab.goal = ""
		tab.mode = tabModeFromAxes(collaborationMode == "plan", toolApprovalMode == control.ToolApprovalYolo)
	}
	ctrl := tab.Ctrl
	mode := tab.mode
	goal = tab.goal
	tabIDForSave := tab.ID
	a.mu.Unlock()

	if autopilotClosed {
		a.noticeCodeForTab(tabIDForSave, event.LevelWarn, NoticeCodeAutopilotClosedOffYolo, autopilotClosedOffYoloText)
		a.clearAutopilotGuard(guardTopic)
	}
	if ctrl != nil {
		ctrl.SetPlanMode(tabModeHasPlan(mode))
	}
	drained := applyTabToolApprovalModeToController(ctrl, toolApprovalMode)
	syncTabGoalToController(ctrl, goal)

	a.mu.Lock()
	if a.tabs[tabIDForSave] == tab {
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	if drained == nil {
		return []string{}, nil
	}
	return drained, nil
}

func (a *App) SetCollaborationModeForTab(tabID, mode string) {
	tab := a.tabByID(tabID)
	if tab == nil {
		return
	}
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	mode = normalizeCollaborationMode(mode)
	approvalMode := a.tabRuntimeSnapshot(tab).currentToolApprovalMode()
	a.mu.Lock()
	if a.tabs[tab.ID] != tab {
		a.mu.Unlock()
		return
	}
	// Task 465 two-axis matrix: plan/goal/normal are the SECOND axis (task
	// dimension 常规/计划/目标) and no longer touch the first axis (approval
	// posture + autopilot flag) — the axes are independent, so a dim-2 switch
	// seeds the autopilot fields from the tab's current values instead of
	// zeros. Only the "autopilot" tier and the 325 reverse linkage rewrite them.
	autopilotOn, autopilotRuntime, autopilotGrace := tab.autopilot, tab.autopilotMaxRuntime, tab.autopilotApprovalGrace
	autopilotAskEnabled, autopilotAskWait := tab.autopilotAskTimeoutEnabled, tab.autopilotAskWait
	var autopilotAskAutoContinue bool
	// Task 326: remember the guard's owner identity and whether autopilot was on
	// before this switch, so the guard is ensured/disabled right after the lock
	// is released (the engine takes its own locks).
	wasAutopilot := tab.autopilot
	guardOwner := autopilotGuardOwner{
		TopicID:       strings.TrimSpace(tab.TopicID),
		Scope:         tab.Scope,
		WorkspaceRoot: tab.WorkspaceRoot,
	}
	// Task 325 legacy refusal flag: the tier switch no longer refuses (task 465
	// auto-satisfies), so this stays false on the composer path; the field
	// remains in the X4 判据锚 log line for schema stability.
	autopilotRefused := false
	// Task 465: the tier switch auto-satisfied the yolo precondition instead of
	// refusing; assumedYolo marks that decision for the log/notice record.
	assumedYolo := false
	switch mode {
	case "plan":
		tab.mode = tabModeFromAxes(true, approvalMode == control.ToolApprovalYolo)
		tab.goal = ""
	case "goal":
		tab.mode = tabModeFromAxes(false, approvalMode == control.ToolApprovalYolo)
	case "autopilot":
		// The bound comes from the [desktop] preferences; without one autopilot
		// stays off - the same refusal the CLI makes - and the tier falls back
		// rather than starting an unbounded unattended run.
		prefOn, prefRuntime, prefGrace, prefAskEnabled, prefAskWait, prefAskAutoContinue := desktopAutopilotDefaults()
		effectiveApproval := approvalMode
		if prefOn && !autopilotGateAllowed(approvalMode) {
			effectiveApproval = control.ToolApprovalYolo
			assumedYolo = true
		}
		autopilotOn, autopilotRuntime, autopilotGrace, autopilotAskEnabled, autopilotAskWait, autopilotAskAutoContinue = gateRestoredAutopilotDefaults(prefOn, prefRuntime, prefGrace, prefAskEnabled, prefAskWait, prefAskAutoContinue, effectiveApproval)
		if autopilotOn {
			// dim-2 (plan/goal) survives the tier switch: goal × autopilot is a
			// legal product state (the goal guard works unattended too), so the
			// old "autopilot clears goal/plan" behavior is gone. approval is
			// pinned to yolo (autopilot implies yolo).
			tab.mode = tabModeFromAxes(tabModeHasPlan(tab.mode), true)
			tab.toolApprovalMode = control.ToolApprovalYolo
		} else {
			// Preference off: leave the posture untouched and drop back out of
			// the tier (bounds stay zeroed — an unbounded unattended run is
			// never started implicitly).
			tab.mode = tabModeFromAxes(tabModeHasPlan(tab.mode), approvalMode == control.ToolApprovalYolo)
			autopilotOn, autopilotRuntime, autopilotGrace = false, 0, 0
			autopilotAskEnabled, autopilotAskWait = false, 0
		}
		// X4 判据锚: every input and the outcome in one line, so "did my
		// autopilot toggle actually land" is answerable from desktop.log alone.
		// assumed_yolo is the task-465 decision record.
		slog.Info("desktop: autopilot toggle", "tab", tab.ID, "preference_on", prefOn, "approval_mode", approvalMode,
			"applied", autopilotOn, "refused_requires_yolo", autopilotRefused, "assumed_yolo", assumedYolo, "max_runtime", autopilotRuntime.String())
	default:
		tab.mode = tabModeFromAxes(false, approvalMode == control.ToolApprovalYolo)
		tab.goal = ""
	}
	tab.autopilot = autopilotOn
	tab.autopilotMaxRuntime = autopilotRuntime
	tab.autopilotApprovalGrace = autopilotGrace
	tab.autopilotAskTimeoutEnabled = autopilotAskEnabled
	tab.autopilotAskWait = autopilotAskWait
	tab.autopilotAskAutoContinue = autopilotAskAutoContinue
	ctrl := tab.Ctrl
	goal := tab.goal
	plan := tabModeHasPlan(tab.mode)
	tabIDForSave := tab.ID
	a.mu.Unlock()
	// Task 326: only the real edges fire — autopilot off→on creates the guard
	// (idempotent, keyed by topic), on→off clears it. Task 465 dim-2 switches
	// seed autopilotOn from the tab, so they are no-ops here by construction.
	if autopilotOn && !wasAutopilot {
		a.ensureAutopilotGuard(guardOwner)
	} else if !autopilotOn && wasAutopilot && guardOwner.TopicID != "" {
		a.clearAutopilotGuard(guardOwner.TopicID)
	}
	if autopilotRefused {
		// Task 325: the refusal tells the user exactly which switch to flip.
		a.noticeCodeForTab(tabIDForSave, event.LevelWarn, NoticeCodeAutopilotRequiresYolo, autopilotRequiresYoloText)
	}
	if assumedYolo && autopilotOn {
		// Task 465 decision record, user-visible half: the tier switch moved
		// approval to yolo by itself, so the user is told that happened (the
		// slog line above is the desktop.log half of the record).
		a.noticeCodeForTab(tabIDForSave, event.LevelInfo, NoticeCodeAutopilotAssumedYolo, autopilotAssumedYoloText)
		// The live controller must carry the posture the tab now promises,
		// or the current turn keeps stacking ask prompts under an unattended
		// flag. Drained ids have no return channel on this void wire call:
		// plain tool cards are dismissed frontend-side (the composer only had
		// a kind=="tool" card if the posture just moved off yolo), and
		// plan/sandbox-escape cards never drain under yolo (#6432).
		_ = applyTabToolApprovalModeToController(ctrl, control.ToolApprovalYolo)
	}
	if ctrl != nil {
		ctrl.SetPlanMode(plan)
		syncTabGoalToController(ctrl, goal)
	}
	a.mu.Lock()
	if a.tabs[tabIDForSave] == tab {
		a.saveTabsLocked()
	}
	a.mu.Unlock()
}

// QuestionAnswer is the frontend's reply to one question in an ask_request.
type QuestionAnswer struct {
	QuestionID string   `json:"questionId"`
	Selected   []string `json:"selected"`
}

// AnswerQuestion resolves a pending ask_request (the `ask` tool) by ID with the
// user's selections per question.
func (a *App) AnswerQuestion(id string, answers []QuestionAnswer) {
	a.AnswerQuestionForTab("", id, answers)
}

func (a *App) AnswerQuestionForTab(tabID, id string, answers []QuestionAnswer) {
	ctrl := a.ctrlByTabID(tabID)
	if ctrl == nil {
		return
	}
	out := make([]event.AskAnswer, len(answers))
	for i, an := range answers {
		out[i] = event.AskAnswer{QuestionID: an.QuestionID, Selected: an.Selected}
	}
	ctrl.AnswerQuestion(id, out)
}

// Compact runs a plain compaction pass (the "compact now" button). Focus-guided
// compaction goes through Submit("/compact <focus>") instead.
func (a *App) Compact() error {
	return a.CompactForTab("")
}

// CompactForTab compacts the requested tab without depending on which tab is
// focused when the asynchronous frontend call reaches the backend.
func (a *App) CompactForTab(tabID string) error {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return readOnlyChannelErr()
	}
	if ctrl == nil {
		return nil
	}
	if err := a.ensureTabControllerWorkspace(tab); err != nil {
		return err
	}
	ctrl = a.controllerForTab(tab)
	if ctrl == nil {
		return nil
	}
	return ctrl.Compact(a.ctx, "")
}

// workspaceNotReadyErr names why a session action arrived before the tab's
// controller existed: still starting, or failed to start. Silently returning
// nil here swallowed the click with no feedback (#3938).
//
// This is the bound-method form: StartupErr is written under a.mu by the
// build goroutine while Submit-family calls race it, so read it under the
// lock. Callers must not hold a.mu.
func (a *App) workspaceNotReadyErr(tab *WorkspaceTab) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.workspaceNotReadyErrLocked(tab)
}

func (a *App) workspaceNotReadyErrLocked(tab *WorkspaceTab) error {
	startupErr := ""
	var issue *SessionRuntimeIssue
	if tab != nil {
		startupErr = tab.StartupErr
		issue = a.sessionRuntimeViewLocked(tab).Issue
	}
	if strings.TrimSpace(startupErr) != "" {
		return fmt.Errorf("workspace failed to start: %s", startupErr)
	}
	if issue != nil && strings.TrimSpace(issue.Message) != "" {
		return fmt.Errorf("workspace failed to start: %s", issue.Message)
	}
	return fmt.Errorf("workspace is still starting")
}

// tabIsReadOnly reads tab.ReadOnly under a.mu; setTabReadOnly can flip it
// concurrently with Submit-family bound calls. Callers must not hold a.mu.
func (a *App) tabIsReadOnly(tab *WorkspaceTab) bool {
	if tab == nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return tab.ReadOnly
}

// applyNewSessionDefaultModel makes a freshly rotated or reused blank session
// obey the same default as EnsureBlankTab. Existing conversations keep their
// saved model until the user starts a new one.
func (a *App) applyNewSessionDefaultModel(tab *WorkspaceTab) error {
	if tab == nil {
		return nil
	}
	a.mu.RLock()
	scope := tab.Scope
	root := tab.WorkspaceRoot
	a.mu.RUnlock()
	if strings.TrimSpace(scope) != "project" {
		scope = "global"
		root = ""
	}
	defaultModel, _, _ := desktopNewSessionDefaults(scope, root)
	return a.alignReusableBlankTabModel(tab, defaultModel)
}

func (a *App) assignFreshSessionTopic(tab *WorkspaceTab) {
	if tab == nil {
		return
	}
	topicID := newTopicID()
	a.mu.Lock()
	scope := tab.Scope
	workspaceRoot := tab.WorkspaceRoot
	tab.TopicID = topicID
	tab.TopicTitle = defaultTopicTitle
	tab.topicTitleSource = topicTitleSourceAuto
	if current := a.tabs[tab.ID]; current == tab {
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	if strings.TrimSpace(scope) == "global" {
		workspaceRoot = ""
	} else {
		workspaceRoot = normalizeProjectRoot(workspaceRoot)
	}
	// NewSession already rotated the runtime to a fresh session. If the sidebar
	// topic index repair fails here, keep the session usable and let persisted
	// session metadata repair the topic index later instead of surfacing a false
	// "new session failed" error to the frontend.
	// Task 550 ①: the failure is no longer silent — it is logged with the
	// structured identity of the topic and counted, so a tab whose sidebar row
	// later disagrees with the index has a retrievable cause.
	if err := ensureTopicIndexedWithCreatedAt(scope, workspaceRoot, topicID, defaultTopicTitle, topicTitleSourceAuto, time.Now().UnixMilli()); err != nil {
		a.topicIndexWriteFailures.Add(1)
		slog.Warn("desktop: new-session topic index write failed; sidebar row waits for session metadata repair",
			"topic_id", topicID, "scope", scope, "workspace_root", workspaceRoot, "err", err)
	}
}

func (a *App) ensureTabTopicIndexedForUserTurn(tab *WorkspaceTab) {
	if tab == nil {
		return
	}
	topicID := newTopicID()
	a.mu.Lock()
	if strings.TrimSpace(tab.TopicID) != "" {
		a.mu.Unlock()
		return
	}
	scope := tab.Scope
	workspaceRoot := tab.WorkspaceRoot
	tab.TopicID = topicID
	tab.TopicTitle = defaultTopicTitle
	tab.topicTitleSource = topicTitleSourceAuto
	if current := a.tabs[tab.ID]; current == tab {
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	if strings.TrimSpace(scope) == "global" {
		scope = "global"
		workspaceRoot = ""
	} else {
		scope = "project"
		workspaceRoot = normalizeProjectRoot(workspaceRoot)
	}

	// Task 550 ①: same no-silent-failure contract as assignFreshSessionTopic.
	if err := ensureTopicIndexedWithCreatedAt(scope, workspaceRoot, topicID, defaultTopicTitle, topicTitleSourceAuto, time.Now().UnixMilli()); err != nil {
		a.topicIndexWriteFailures.Add(1)
		slog.Warn("desktop: first-turn topic index write failed; sidebar row waits for session metadata repair",
			"topic_id", topicID, "scope", scope, "workspace_root", workspaceRoot, "err", err)
	}
	path := a.currentSessionPathFor(tab)
	a.persistTabSessionPath(tab, path)
	a.emitProjectTreeChangedForSessionDirs(sessionDirectoryForPath(path))
}

func messagesHaveConversationContent(messages []provider.Message) bool {
	for _, msg := range messages {
		if msg.Role != provider.RoleSystem {
			return true
		}
	}
	return false
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
		if err := item.ctrl.Snapshot(); err != nil {
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

// invalidatePromptHistoryCache resets the lazy prompt-history tape so the next
// ScanPromptHistory call rebuilds session order and reloads sessions on demand.
// Called from every session-mutating path: NewSession, ClearSession,
// DeleteSession, RestoreSession, PurgeTrashedSession, RenameSession.
func (a *App) invalidatePromptHistoryCache() {
	a.promptHistoryMu.Lock()
	a.promptHistoryTape = nil
	a.promptHistoryMu.Unlock()
}

const (
	promptHistoryPageLimit    = 50
	promptHistoryMaxPageLimit = 200
)

type promptHistoryRequest struct {
	Nonce  string `json:"nonce,omitempty"`
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	legacy bool
}

type promptHistoryCursor struct {
	Nonce   string `json:"n"`
	Session int    `json:"s"`
	Offset  int    `json:"o"`
}

type promptHistoryTape struct {
	nonce       string
	dir         string
	currentPath string
	displays    sessionDisplayMap
	sessions    []promptHistorySessionFile
	loaded      map[string][]PromptHistoryEntry
}

// ScanPromptHistory returns the next prompt-history tape segment. The request is
// a JSON string so the Wails binding stays one-argument while the protocol can
// carry a cursor and page limit. Older clients may still pass a bare nonce; that
// path keeps the old cache-hit behavior.
func (a *App) ScanPromptHistory(rawRequest string) (PromptHistoryResult, error) {
	req := parsePromptHistoryRequest(rawRequest)
	dir := a.activeSessionDir()
	sessionPath := a.activeSessionPath(dir)

	a.promptHistoryMu.Lock()
	tape, err := a.promptHistoryTapeForLocked(dir, sessionPath)
	if err != nil {
		a.promptHistoryMu.Unlock()
		return PromptHistoryResult{}, err
	}
	if req.legacy && req.Nonce != "" && req.Nonce == tape.nonce {
		a.promptHistoryMu.Unlock()
		return PromptHistoryResult{Entries: nil, Nonce: req.Nonce}, nil
	}
	result := tape.readOlder(req.Cursor, promptHistoryLimit(req.Limit))
	a.promptHistoryMu.Unlock()
	return result, nil
}

func parsePromptHistoryRequest(raw string) promptHistoryRequest {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return promptHistoryRequest{}
	}
	if strings.HasPrefix(raw, "{") {
		var req promptHistoryRequest
		if err := json.Unmarshal([]byte(raw), &req); err == nil {
			return req
		}
	}
	return promptHistoryRequest{Nonce: raw, legacy: true}
}

func promptHistoryLimit(limit int) int {
	if limit <= 0 {
		return promptHistoryPageLimit
	}
	if limit > promptHistoryMaxPageLimit {
		return promptHistoryMaxPageLimit
	}
	return limit
}

func (a *App) promptHistoryTapeForLocked(dir, sessionPath string) (*promptHistoryTape, error) {
	currentPath := ""
	if path, _, err := validateSessionPath(dir, sessionPath); err == nil {
		currentPath = path
	}
	if a.promptHistoryTape != nil && a.promptHistoryTape.dir == dir && a.promptHistoryTape.currentPath == currentPath {
		return a.promptHistoryTape, nil
	}
	tape, err := newPromptHistoryTape(dir, currentPath)
	if err != nil {
		return nil, err
	}
	a.promptHistoryTape = tape
	return tape, nil
}

func (a *App) scanPromptHistoryFromDir(dir string) ([]PromptHistoryEntry, error) {
	tape, err := newPromptHistoryTape(dir, "")
	if err != nil {
		return nil, err
	}
	return tape.readAll(), nil
}

func newPromptHistoryTape(dir, currentPath string) (*promptHistoryTape, error) {
	tape := &promptHistoryTape{
		nonce:       rand.Text(),
		dir:         dir,
		currentPath: currentPath,
		displays:    loadSessionDisplays(dir),
		loaded:      map[string][]PromptHistoryEntry{},
	}
	sessions, err := promptHistorySessionFiles(dir)
	if err != nil {
		return nil, err
	}
	if currentPath != "" {
		currentPath = filepath.Clean(currentPath)
		currentSession := promptHistorySessionFile{}
		currentIndex := -1
		for i, session := range sessions {
			if filepath.Clean(session.path) == currentPath {
				currentSession = session
				currentIndex = i
				break
			}
		}
		if currentIndex >= 0 {
			sessions = append([]promptHistorySessionFile{currentSession}, append(sessions[:currentIndex], sessions[currentIndex+1:]...)...)
		} else if info, err := os.Stat(currentPath); err == nil && !info.IsDir() {
			sessions = append([]promptHistorySessionFile{{
				path: currentPath,
			}}, sessions...)
		}
	}
	tape.sessions = sessions
	return tape, nil
}

func (t *promptHistoryTape) readOlder(cursor string, limit int) PromptHistoryResult {
	c := promptHistoryCursor{Nonce: t.nonce}
	if decoded, ok := decodePromptHistoryCursor(cursor); ok && decoded.Nonce == t.nonce {
		c = decoded
	}
	if c.Session < 0 {
		c.Session = 0
	}
	if c.Offset < 0 {
		c.Offset = 0
	}

	out := make([]PromptHistoryEntry, 0, limit)
	sessionIndex := c.Session
	offset := c.Offset
	for sessionIndex < len(t.sessions) && len(out) < limit {
		entries, err := t.entriesForSession(sessionIndex)
		if err != nil || offset >= len(entries) {
			sessionIndex++
			offset = 0
			continue
		}

		end := min(len(entries), offset+limit-len(out))
		out = append(out, entries[offset:end]...)
		offset = end
		if offset >= len(entries) && len(out) < limit {
			sessionIndex++
			offset = 0
		}
	}

	if sessionIndex < len(t.sessions) {
		if entries, ok := t.loaded[t.sessions[sessionIndex].path]; ok && offset >= len(entries) {
			sessionIndex++
			offset = 0
		}
	}
	hasOlder := sessionIndex < len(t.sessions)
	olderCursor := ""
	if hasOlder {
		olderCursor = encodePromptHistoryCursor(promptHistoryCursor{Nonce: t.nonce, Session: sessionIndex, Offset: offset})
	}
	return PromptHistoryResult{Entries: out, Nonce: t.nonce, OlderCursor: olderCursor, HasOlder: hasOlder}
}

func (t *promptHistoryTape) readAll() []PromptHistoryEntry {
	out := []PromptHistoryEntry{}
	cursor := ""
	for {
		page := t.readOlder(cursor, promptHistoryMaxPageLimit)
		out = append(out, page.Entries...)
		if !page.HasOlder || page.OlderCursor == "" {
			return out
		}
		cursor = page.OlderCursor
	}
}

func (t *promptHistoryTape) entriesForSession(index int) ([]PromptHistoryEntry, error) {
	if index < 0 || index >= len(t.sessions) {
		return nil, nil
	}
	path := t.sessions[index].path
	if entries, ok := t.loaded[path]; ok {
		return entries, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		t.loaded[path] = nil
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	entries, err := scanPromptHistoryFile(path, info, sessionDisplayResolverFromMap(t.displays, path))
	if err != nil {
		t.loaded[path] = nil
		return nil, err
	}
	t.loaded[path] = entries
	return entries, nil
}

func encodePromptHistoryCursor(cursor promptHistoryCursor) string {
	b, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodePromptHistoryCursor(value string) (promptHistoryCursor, bool) {
	if strings.TrimSpace(value) == "" {
		return promptHistoryCursor{}, false
	}
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return promptHistoryCursor{}, false
	}
	var cursor promptHistoryCursor
	if err := json.Unmarshal(b, &cursor); err != nil {
		return promptHistoryCursor{}, false
	}
	return cursor, true
}

func scanPromptHistoryFile(path string, info os.FileInfo, resolveUserContent func(string) string) ([]PromptHistoryEntry, error) {
	entries, err := collectPromptHistoryEntries(path, info, resolveUserContent)
	if err != nil {
		return nil, err
	}
	sortPromptHistoryNewestFirst(entries)
	return entries, nil
}

type promptHistorySessionFile struct {
	path string
}

func promptHistorySessionFiles(dir string) ([]promptHistorySessionFile, error) {
	infos, err := agent.ListSessionOrder(dir)
	if err != nil {
		return nil, err
	}
	sessions := make([]promptHistorySessionFile, 0, len(infos))
	for _, info := range infos {
		sessions = append(sessions, promptHistorySessionFile{path: info.Path})
	}
	return sessions, nil
}

func promptHistoryEntryNewer(a, b PromptHistoryEntry) bool {
	if a.At != b.At {
		return a.At > b.At
	}
	if a.SessionPath != b.SessionPath {
		return a.SessionPath > b.SessionPath
	}
	return a.Turn > b.Turn
}

func sortPromptHistoryNewestFirst(entries []PromptHistoryEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return promptHistoryEntryNewer(entries[i], entries[j])
	})
}

func collectPromptHistoryEntries(path string, info os.FileInfo, resolveUserContent func(string) string) ([]PromptHistoryEntry, error) {
	var out []PromptHistoryEntry
	emit := func(entry PromptHistoryEntry) {
		out = append(out, entry)
	}
	// Sessions with an event log must replay it: the .jsonl checkpoint stops
	// gaining turns between checkpoints, so scanning it directly would freeze
	// ↑-recall at each session's last checkpoint.
	if handled, err := collectEventLogUserPrompts(path, info, resolveUserContent, emit); handled {
		return out, err
	}
	err := collectJSONLUserPrompts(path, info, resolveUserContent, emit)
	return out, err
}

func collectEventLogUserPrompts(path string, info os.FileInfo, resolveUserContent func(string) string, emit func(PromptHistoryEntry)) (bool, error) {
	logPath := store.SessionEventLog(path)
	if logPath == "" {
		return false, nil
	}
	if logInfo, err := os.Stat(logPath); err != nil || logInfo.IsDir() || logInfo.Size() == 0 {
		return false, nil
	}
	users, err := agent.LoadSessionUserMessages(path)
	if err != nil {
		return true, err
	}
	fallbackAt := promptHistoryFallbackMillis(path, info)
	turn := 0
	for _, user := range users {
		if !agent.IsUserAuthoredTurnMessage(user.Message) {
			continue
		}
		text := sessionUserPromptText(user.Message, resolveUserContent)
		if text == "" {
			continue
		}
		at := fallbackAt
		if !user.At.IsZero() {
			at = user.At.UnixMilli()
		}
		emit(PromptHistoryEntry{
			Text:        text,
			At:          at,
			SessionPath: path,
			Turn:        turn,
		})
		turn++
	}
	return true, nil
}

func collectJSONLUserPrompts(path string, info os.FileInfo, resolveUserContent func(string) string, emit func(PromptHistoryEntry)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	fallbackAt := promptHistoryFallbackMillis(path, info)

	dec := json.NewDecoder(f)
	turn := 0
	for {
		var rec previewEventRecord
		if err := dec.Decode(&rec); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil // partial results are better than none
		}
		// Format compatibility:
		// 1) Legacy event format: {"kind":"user.message","text":"..."}
		// 2) Early event format:   {"type":"user.message","text":"..."}
		// 3) Current provider.Message format: {"role":"user","content":"..."}
		var message provider.Message
		kindOrType := strings.TrimSpace(rec.Kind)
		if kindOrType == "" {
			kindOrType = strings.TrimSpace(rec.Type)
		}
		if kindOrType == "user.message" {
			message = provider.Message{Role: provider.RoleUser, Content: strings.TrimSpace(rec.Text)}
		} else if strings.TrimSpace(rec.Role) == "user" {
			message = provider.Message{
				Role: provider.RoleUser, Origin: rec.Origin,
				Content: strings.TrimSpace(rec.Content), RawContent: strings.TrimSpace(rec.RawContent),
			}
		}
		if message.Content != "" {
			if !agent.IsUserAuthoredTurnMessage(message) {
				continue
			}
			text := sessionUserPromptText(message, resolveUserContent)
			if text == "" {
				continue
			}
			at := fallbackAt
			if eventAt, ok := promptHistoryEventMillis(rec); ok {
				at = eventAt
			}
			entry := PromptHistoryEntry{
				Text:        text,
				At:          at,
				SessionPath: path,
				Turn:        turn,
			}
			emit(entry)
			turn++
		}
	}
	return nil
}

func sessionUserPromptText(message provider.Message, resolveUserContent func(string) string) string {
	if strings.TrimSpace(message.RawContent) != "" {
		return strings.TrimSpace(agent.UserMessageText(message))
	}
	return strings.TrimSpace(resolveUserContent(strings.TrimSpace(message.Content)))
}

func promptHistoryFallbackMillis(path string, info os.FileInfo) int64 {
	if meta, ok, err := agent.LoadBranchMeta(path); err == nil && ok && !meta.UpdatedAt.IsZero() {
		return meta.UpdatedAt.UnixMilli()
	}
	if info != nil {
		return info.ModTime().UnixMilli()
	}
	return 0
}

func promptHistoryEventMillis(rec previewEventRecord) (int64, bool) {
	for _, raw := range []json.RawMessage{
		rec.TS,
		rec.Time,
		rec.Timestamp,
		rec.CreatedAt,
		rec.CreatedAtSnake,
		rec.UpdatedAt,
		rec.UpdatedAtSnake,
	} {
		if at, ok := parseJSONTimestampMillis(raw); ok {
			return at, true
		}
	}
	return 0, false
}

func parseJSONTimestampMillis(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return 0, false
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		s = strings.TrimSpace(s)
		if s == "" {
			return 0, false
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return normalizeTimestampMillis(n)
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return normalizeTimestampMillisFloat(f)
		}
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t.UnixMilli(), true
		}
		return 0, false
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var n json.Number
	if err := dec.Decode(&n); err != nil {
		return 0, false
	}
	if i, err := strconv.ParseInt(n.String(), 10, 64); err == nil {
		return normalizeTimestampMillis(i)
	}
	if f, err := strconv.ParseFloat(n.String(), 64); err == nil {
		return normalizeTimestampMillisFloat(f)
	}
	return 0, false
}

func normalizeTimestampMillis(v int64) (int64, bool) {
	if v <= 0 {
		return 0, false
	}
	switch {
	case v >= 1_000_000_000_000_000_000:
		return v / 1_000_000, true // nanoseconds
	case v >= 1_000_000_000_000_000:
		return v / 1_000, true // microseconds
	case v >= 100_000_000_000:
		return v, true // milliseconds
	case v >= 1_000_000_000:
		return v * 1_000, true // seconds
	default:
		return 0, false
	}
}

func normalizeTimestampMillisFloat(v float64) (int64, bool) {
	if v <= 0 {
		return 0, false
	}
	switch {
	case v >= 1_000_000_000_000_000_000:
		return int64(v / 1_000_000), true
	case v >= 1_000_000_000_000_000:
		return int64(v / 1_000), true
	case v >= 100_000_000_000:
		return int64(v), true
	case v >= 1_000_000_000:
		return int64(v * 1_000), true
	default:
		return 0, false
	}
}

// PickWorkspace opens a folder chooser and, on a pick, opens a new project tab
// scoped to that folder. Returns the chosen path ("" if cancelled).
func (a *App) PickWorkspace() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	cur, _ := os.Getwd()
	a.mu.RLock()
	if tab := a.activeTabLocked(); tab != nil && tab.WorkspaceRoot != "" {
		cur = tab.WorkspaceRoot
	}
	a.mu.RUnlock()
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Choose working folder",
		DefaultDirectory: dialogDefaultDirectory(cur),
	})
	if err != nil || dir == "" {
		return "", err
	}
	return a.SwitchWorkspace(dir)
}

func dialogDefaultDirectory(preferred string) string {
	if dir := nearestExistingDirectory(preferred); dir != "" {
		return dir
	}
	if cwd, err := os.Getwd(); err == nil {
		if dir := nearestExistingDirectory(cwd); dir != "" {
			return dir
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if dir := nearestExistingDirectory(home); dir != "" {
			return dir
		}
	}
	return ""
}

func nearestExistingDirectory(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	for {
		info, err := os.Stat(path)
		if err == nil {
			if info.IsDir() {
				return path
			}
			path = filepath.Dir(path)
			continue
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
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
			if err := a.snapshotTab(tab); err != nil {
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
	users, err := agent.LoadSessionUserMessages(sessionPath)
	if err != nil || len(users) == 0 {
		return msgs
	}
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
				} else if loaded, err := agent.LoadSession(path); err == nil && loaded != nil {
					msgs = loaded.Snapshot()
				}
			} else if loaded, err := agent.LoadSession(path); err == nil && loaded != nil {
				msgs = loaded.Snapshot()
			}
		} else if loaded, err := agent.LoadSession(path); err == nil && loaded != nil {
			msgs = loaded.Snapshot()
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
	autopilotClosed := closeAutopilotForOffYolo(tab, mode)
	ctrl := tab.Ctrl
	tabIDForSave := tab.ID
	guardTopic := strings.TrimSpace(tab.TopicID)
	a.mu.Unlock()
	if autopilotClosed {
		a.noticeCodeForTab(tabIDForSave, event.LevelWarn, NoticeCodeAutopilotClosedOffYolo, autopilotClosedOffYoloText)
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

// CapabilitiesView is the MCP & Skills drawer's data: connected/failed MCP
// servers and the discoverable skills, the GUI counterpart to `/mcp` + `/skill`.
type CapabilitiesView struct {
	Servers    []ServerView    `json:"servers"`
	Skills     []SkillView     `json:"skills"`
	SkillRoots []SkillRootView `json:"skillRoots"`
	Plugins    []PluginView    `json:"plugins"`
}

// SkillsSettingsView is the skills management page's data, split from MCP
// status so opening MCP settings does not scan skill roots.
type SkillsSettingsView struct {
	Skills                  []SkillView     `json:"skills"`
	SkillRoots              []SkillRootView `json:"skillRoots"`
	AllowImplicitInvocation bool            `json:"allowImplicitInvocation"`
}

// ServerView is one MCP server for the drawer. Status is "connected" (with
// tool/prompt/resource counts), "deferred" (enabled but idle), "failed" (with
// the connection error), "initializing" (background startup in progress), or
// "disabled".
//
// Product fields for the simplified MCP panel are Enabled/Installed/
// Availability/RuntimeState/ToolCount/ToolList/Action. Legacy AutoStart, Tier,
// and StartIntent remain for one major as derived compatibility fields only.
type ServerView struct {
	Name                   string         `json:"name"`
	Transport              string         `json:"transport"`
	Status                 string         `json:"status"`
	HostProfile            string         `json:"hostProfile,omitempty"`
	ElicitationNegotiated  bool           `json:"elicitationNegotiated,omitempty"`
	AppsNegotiated         bool           `json:"appsNegotiated,omitempty"`
	StartIntent            string         `json:"startIntent,omitempty"` // deprecated: derived from Enabled
	RuntimeState           string         `json:"runtimeState,omitempty"`
	ProtocolVersion        string         `json:"protocolVersion,omitempty"`
	SessionState           string         `json:"sessionState,omitempty"`
	ReconnectAttempts      int            `json:"reconnectAttempts,omitempty"`
	ErrorKind              string         `json:"errorKind,omitempty"`
	Availability           string         `json:"availability,omitempty"`
	Enabled                bool           `json:"enabled"`
	Installed              bool           `json:"installed"`
	Action                 string         `json:"action,omitempty"`
	Source                 string         `json:"source,omitempty"`
	ConfigSource           string         `json:"configSource,omitempty"`
	BuiltIn                bool           `json:"builtIn,omitempty"`
	Configured             bool           `json:"configured,omitempty"`
	AutoStart              bool           `json:"autoStart"` // deprecated: same as Enabled
	Tier                   string         `json:"tier,omitempty"`
	Command                string         `json:"command,omitempty"`
	Args                   []string       `json:"args,omitempty"`
	URL                    string         `json:"url,omitempty"`
	EnvKeys                []string       `json:"envKeys,omitempty"`
	HeaderKeys             []string       `json:"headerKeys,omitempty"`
	Tools                  int            `json:"tools"`
	ToolCount              int            `json:"toolCount"`
	Prompts                int            `json:"prompts"`
	Resources              int            `json:"resources"`
	HasTools               bool           `json:"hasTools,omitempty"`
	Error                  string         `json:"error,omitempty"`
	ToolList               []ToolView     `json:"toolList"`
	CallTimeoutSeconds     int            `json:"callTimeoutSeconds,omitempty"`
	ToolTimeoutSeconds     map[string]int `json:"toolTimeoutSeconds,omitempty"`
	RequiresLaunchApproval bool           `json:"requiresLaunchApproval,omitempty"`
	AuthStatus             string         `json:"authStatus,omitempty"`
	AuthURL                string         `json:"authUrl,omitempty"`
	AuthConfigured         bool           `json:"authConfigured,omitempty"`
	ManagedByPlugin        string         `json:"managedByPlugin,omitempty"`
}

type ToolView struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	ReadOnlyHint    bool   `json:"readOnlyHint,omitempty"`
	DestructiveHint bool   `json:"destructiveHint,omitempty"`
	SchemaError     string `json:"schemaError,omitempty"`
}

// SkillView is one discoverable skill for the drawer. Also backs the
// Subagents settings surface: the frontend filters this same list to
// RunAs=="subagent" rather than calling a second, redundant endpoint.
type SkillView struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Scope        string   `json:"scope"`
	SourceDir    string   `json:"sourceDir,omitempty"`
	RunAs        string   `json:"runAs"`
	Enabled      bool     `json:"enabled"`
	Plugin       string   `json:"plugin,omitempty"`
	Model        string   `json:"model,omitempty"`
	Effort       string   `json:"effort,omitempty"`
	AllowedTools []string `json:"allowedTools,omitempty"`
	// ReadOnly mirrors frontmatter read-only; omitted/false keeps the legacy
	// writable default for older profiles.
	ReadOnly bool   `json:"readOnly,omitempty"`
	Color    string `json:"color,omitempty"`
	// Invocation is the user-facing slash name; InvocationMode preserves the
	// frontmatter policy used by the subagent profile editor.
	Invocation     string `json:"invocation,omitempty"`
	InvocationMode string `json:"invocationMode,omitempty"`
	// Body is the skill's full markdown body (post-frontmatter) — the
	// subagent profile editor pre-fills its system-prompt field from this.
	Body string `json:"body,omitempty"`
	// ConfiguredModel/ConfiguredEffort are the per-name overrides from
	// cfg.Agent.SubagentModels/SubagentEfforts (internal/boot's
	// subagentModelRef/subagentEffortRef read the same map at dispatch time).
	// This is the only lever for a built-in subagent's model/effort, since
	// built-ins have no editable frontmatter file to carry Model/Effort.
	ConfiguredModel  string `json:"configuredModel,omitempty"`
	ConfiguredEffort string `json:"configuredEffort,omitempty"`
}

type SkillRootSkillView struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Scope        string   `json:"scope"`
	RunAs        string   `json:"runAs"`
	Plugin       string   `json:"plugin,omitempty"`
	Model        string   `json:"model,omitempty"`
	Effort       string   `json:"effort,omitempty"`
	AllowedTools []string `json:"allowedTools,omitempty"`
	Color        string   `json:"color,omitempty"`
	Invocation   string   `json:"invocation,omitempty"`
}

// SkillRootView is one skill discovery root for the drawer's Sources section.
type SkillRootView struct {
	Dir        string               `json:"dir"`
	Scope      string               `json:"scope"`
	Priority   int                  `json:"priority"`
	Status     string               `json:"status"`
	Enabled    bool                 `json:"enabled"`
	Configured bool                 `json:"configured"`
	Removable  bool                 `json:"removable"`
	Skills     int                  `json:"skills"`
	SkillItems []SkillRootSkillView `json:"skillItems,omitempty"`
	Warning    string               `json:"warning,omitempty"`
}

// Capabilities projects the session's MCP servers (connected + failed) and skills
// for the MCP & Skills drawer. Non-nil slices so the frontend can map over them.
func (a *App) Capabilities() CapabilitiesView {
	skills := a.SkillsSettings()
	return CapabilitiesView{
		Servers:    a.MCPServers(),
		Skills:     skills.Skills,
		SkillRoots: skills.SkillRoots,
		Plugins:    a.Plugins(),
	}
}

// MCPServers returns only MCP server status for settings pages that do not need
// skill discovery.
func (a *App) MCPServers() []ServerView {
	return a.mcpServersView()
}

type MCPMarketplaceEntryView struct {
	Name              string   `json:"name"`
	SuggestedName     string   `json:"suggestedName"`
	Title             string   `json:"title,omitempty"`
	Description       string   `json:"description,omitempty"`
	Version           string   `json:"version,omitempty"`
	RepositoryURL     string   `json:"repositoryUrl,omitempty"`
	Installable       bool     `json:"installable"`
	UnavailableReason string   `json:"unavailableReason,omitempty"`
	Transport         string   `json:"transport,omitempty"`
	Command           string   `json:"command,omitempty"`
	Args              []string `json:"args"`
	URL               string   `json:"url,omitempty"`
}

type MCPMarketplaceView struct {
	Servers []MCPMarketplaceEntryView `json:"servers"`
	Cached  bool                      `json:"cached"`
	Warning string                    `json:"warning,omitempty"`
}

// MCPMarketplace explicitly queries the official MCP Registry. It is only
// called from the settings marketplace; startup and tool discovery never touch
// the network. A query-specific cache keeps the page useful during a registry
// outage without treating cached entries as installed servers.
func (a *App) MCPMarketplace(query string) (MCPMarketplaceView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := mcpregistry.New(mcpRegistryCachePath()).Search(ctx, query, 50)
	if err != nil {
		return MCPMarketplaceView{Servers: []MCPMarketplaceEntryView{}}, err
	}
	view := MCPMarketplaceView{
		Servers: make([]MCPMarketplaceEntryView, 0, len(result.Entries)),
		Cached:  result.Cached,
		Warning: result.Warning,
	}
	for _, entry := range result.Entries {
		view.Servers = append(view.Servers, mcpMarketplaceEntryView(entry))
	}
	return view, nil
}

// MCPMarketplaceResolve re-fetches one Registry entry immediately before the
// settings UI installs it. Offline cache remains useful for browsing, but it is
// never accepted as installation metadata.
func (a *App) MCPMarketplaceResolve(registryName string) (MCPMarketplaceEntryView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	entry, _, err := mcpregistry.New(mcpRegistryCachePath()).Resolve(ctx, registryName)
	if err != nil {
		return MCPMarketplaceEntryView{}, err
	}
	if _, err := entry.PluginEntry(""); err != nil {
		return MCPMarketplaceEntryView{}, err
	}
	return mcpMarketplaceEntryView(entry), nil
}

func mcpRegistryCachePath() string {
	if cacheDir := config.CacheDir(); cacheDir != "" {
		return filepath.Join(cacheDir, "mcp-registry-v0.1.json")
	}
	return ""
}

func mcpMarketplaceEntryView(entry mcpregistry.Entry) MCPMarketplaceEntryView {
	return MCPMarketplaceEntryView{
		Name:              entry.Name,
		SuggestedName:     entry.SuggestedName,
		Title:             entry.Title,
		Description:       entry.Description,
		Version:           entry.Version,
		RepositoryURL:     entry.RepositoryURL,
		Installable:       entry.Installable,
		UnavailableReason: entry.UnavailableReason,
		Transport:         entry.Transport,
		Command:           entry.Command,
		Args:              append([]string{}, entry.Args...),
		URL:               entry.URL,
	}
}

// lockRuntimeMutation serializes controller rebuild/teardown operations and
// freezes runtime admission so a captured controller or Host cannot be replaced
// or closed in flight. The caller must not hold App.mu; the lock order is
// runtimeRebuildMu -> runtimeAdmissionMu -> App/Host/Registry.
func (a *App) lockRuntimeMutation(operation string) func() {
	if hook := a.runtimeMutationBeforeLockHook; hook != nil {
		hook(operation)
	}
	a.runtimeRebuildMu.Lock()
	a.runtimeAdmissionMu.Lock()
	return func() {
		a.runtimeAdmissionMu.Unlock()
		a.runtimeRebuildMu.Unlock()
	}
}

// AuthorizeAndConnectMCPServer is retained for older generated Wails clients.
// Project configuration is trusted by default now, so the normal path simply
// reconnects the effective entry. Explicitly gated host specs still record
// their exact launch grant before reconnecting.
func (a *App) AuthorizeAndConnectMCPServer(name string) error {
	defer a.lockMCPMutation("authorize-connect")()

	tab, ctrl, root := a.activeMCPRuntime()
	if tab == nil || ctrl == nil {
		return fmt.Errorf("no active session")
	}
	host, releaseGates, err := a.lockMCPHostTurnGates("MCP authorization", ctrl)
	if err != nil {
		return err
	}
	defer releaseGates()
	entry, found, err := desktopEffectiveMCPServer(root, name)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no configured MCP server named %q", name)
	}
	spec, err := a.mcpLaunchSpec(root, name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if spec.RequireLaunchApproval {
		if err := plugin.AuthorizeProjectSpecLaunch(ctx, spec); err != nil {
			return err
		}
	}

	controllers := a.mcpControllersSharingHost(host, name, ctrl)
	for i := range controllers {
		if controllers[i].ctrl == ctrl {
			controllers[i].enabled = true
		}
	}
	// Drop any previous identity, then start the effective configured server
	// once and refresh every enabled registry sharing this Host.
	disconnectMCPServerControllers(name, ctrl, controllers)
	if host != nil {
		host.ClearFailure(name)
	}
	if err := reconnectMCPServerControllers(entry, controllers); err != nil {
		recordMCPFailure(ctrl, entry, err)
		return err
	}
	a.mu.Lock()
	delete(tab.disabledMCP, name)
	a.mu.Unlock()
	return nil
}

type mcpControllerTarget struct {
	ctrl    control.SessionAPI
	enabled bool
}

// lockMCPHostTurnGates freezes every runtime sharing ctrl's Host. Callers hold
// lockMCPMutation, so runtimeAdmissionMu's write side already prevents new turn
// admissions, builds, and teardown while this helper snapshots and gates the
// existing runtimes.
func (a *App) lockMCPHostTurnGates(setting string, ctrl control.SessionAPI) (*plugin.Host, func(), error) {
	if ctrl == nil {
		return nil, nil, fmt.Errorf("no active session")
	}
	host := ctrl.Host()
	release, err := a.lockRuntimeTurnGates(setting, func(tab *WorkspaceTab) bool {
		if host == nil {
			return tab.Ctrl == ctrl
		}
		return tab.Ctrl != nil && tab.Ctrl.Host() == host
	})
	return host, release, err
}

func disconnectMCPServerControllers(name string, preferred control.SessionAPI, controllers []mcpControllerTarget) bool {
	for _, target := range controllers {
		target.ctrl.UnregisterMCPServerTools(name)
	}
	disconnected := false
	if preferred != nil {
		disconnected = preferred.DisconnectMCPServer(name)
	}
	// Every controller owns an independent capability runtime even when the Host
	// process is shared. Reconcile each one after the preferred controller drops
	// the client so remove/update/rollback cannot leave sibling tabs with stale
	// specs or live-tool snapshots.
	for _, target := range controllers {
		if target.ctrl == preferred {
			continue
		}
		disconnected = target.ctrl.DisconnectMCPServer(name) || disconnected
	}
	return disconnected
}

func (a *App) clearMCPServerTabState(name string, controllers []mcpControllerTarget) {
	selected := make(map[control.SessionAPI]bool, len(controllers))
	for _, target := range controllers {
		selected[target.ctrl] = true
	}
	a.mu.Lock()
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || !selected[tab.Ctrl] {
			continue
		}
		delete(tab.disabledMCP, name)
		tab.mcpOrder = removeServerOrder(tab.mcpOrder, name)
	}
	a.mu.Unlock()
}

// reconnectMCPServerControllers establishes one shared client, then refreshes
// every enabled controller's provider-visible Registry. Disabled tabs remain
// suspended and reconnect only when explicitly enabled.
func reconnectMCPServerControllers(entry config.PluginEntry, controllers []mcpControllerTarget) error {
	var startErrors []error
	connectedTarget := -1
	for i, target := range controllers {
		if !target.enabled {
			continue
		}
		if _, err := target.ctrl.ConnectMCPServer(entry); err != nil {
			startErrors = append(startErrors, err)
			continue
		}
		connectedTarget = i
		break
	}
	if connectedTarget < 0 {
		// All tabs may have disabled this server. Keeping it disconnected
		// preserves their explicit state.
		return errors.Join(startErrors...)
	}

	var refreshErrors []error
	for i, target := range controllers {
		if !target.enabled || i == connectedTarget {
			continue
		}
		if _, err := target.ctrl.ConnectMCPServer(entry); err != nil {
			refreshErrors = append(refreshErrors, err)
		}
	}
	return errors.Join(refreshErrors...)
}

// mcpControllersSharingHost snapshots visible and detached runtimes before
// calling controller methods. App.mu is never held across Host/controller
// locks or network work. preferred (normally the active tab) is returned first.
func (a *App) mcpControllersSharingHost(host *plugin.Host, name string, preferred control.SessionAPI) []mcpControllerTarget {
	if host == nil {
		enabled := true
		a.mu.RLock()
		for _, tab := range a.runtimeTabsLocked() {
			if tab != nil && tab.Ctrl == preferred {
				_, disabled := tab.disabledMCP[name]
				enabled = !disabled
				break
			}
		}
		a.mu.RUnlock()
		return []mcpControllerTarget{{ctrl: preferred, enabled: enabled}}
	}
	a.mu.RLock()
	candidates := make([]mcpControllerTarget, 0, len(a.tabs)+len(a.detachedSessions))
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || tab.Ctrl == nil {
			continue
		}
		_, disabled := tab.disabledMCP[name]
		candidates = append(candidates, mcpControllerTarget{ctrl: tab.Ctrl, enabled: !disabled})
	}
	a.mu.RUnlock()

	byController := make(map[control.SessionAPI]int, len(candidates))
	targets := make([]mcpControllerTarget, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ctrl.Host() != host {
			continue
		}
		if idx, ok := byController[candidate.ctrl]; ok {
			targets[idx].enabled = targets[idx].enabled || candidate.enabled
			continue
		}
		byController[candidate.ctrl] = len(targets)
		targets = append(targets, candidate)
	}
	if len(targets) == 0 {
		return []mcpControllerTarget{{ctrl: preferred, enabled: true}}
	}
	if idx, ok := byController[preferred]; ok && idx > 0 {
		targets[0], targets[idx] = targets[idx], targets[0]
	}
	return targets
}

// lockRuntimeTurnGates locks the turn gate of every runtime tab selected by
// affected (nil selects all visible and detached runtime tabs) in stable tab-ID
// order, then verifies under the gates that no gated controller has active
// runtime work. Callers must hold runtimeRebuildMu and the write side of
// runtimeAdmissionMu (normally through lockMCPMutation), which freezes new turn
// admission, controller builds, and runtime teardown before this snapshot.
// On success the returned release func unlocks the per-tab gates in reverse
// order; on error every gate acquired here is already unlocked.
func (a *App) lockRuntimeTurnGates(setting string, affected func(*WorkspaceTab) bool) (func(), error) {
	a.mu.RLock()
	all := a.runtimeTabsLocked()
	tabs := make([]*WorkspaceTab, 0, len(all))
	for _, tab := range all {
		if tab == nil || (affected != nil && !affected(tab)) {
			continue
		}
		tabs = append(tabs, tab)
	}
	a.mu.RUnlock()
	sort.Slice(tabs, func(i, j int) bool { return tabs[i].ID < tabs[j].ID })
	locked := 0
	release := func() {
		for i := locked - 1; i >= 0; i-- {
			tabs[i].turnStartMu.Unlock()
		}
	}
	for _, tab := range tabs {
		tab.turnStartMu.Lock()
		locked++
	}
	// Read tab.Ctrl under a.mu rather than through controllerForTab: detached
	// runtimes live in detachedSessions, not a.tabs, and their work counts too.
	a.mu.RLock()
	for _, tab := range tabs {
		if err := rebuildControllerActiveWorkErrorFor(tab.Ctrl, setting); err != nil {
			a.mu.RUnlock()
			release()
			return nil, err
		}
	}
	a.mu.RUnlock()
	return release, nil
}

// disconnectMCPServerAllRuntimes removes an uninstalled MCP server from every
// live runtime: all visible and detached runtime tabs, across every shared
// Host — a global plugin uninstall must not leave sibling tabs exposing stale
// provider-visible tools or other workspaces running the removed server.
// DisconnectMCPServer stops the shared client once per Host and drops the tool
// prefix from every other controller's registry.
func (a *App) disconnectMCPServerAllRuntimes(serverName string) bool {
	a.mu.RLock()
	ctrls := make([]control.SessionAPI, 0, len(a.tabs)+len(a.detachedSessions))
	seen := make(map[control.SessionAPI]bool, len(a.tabs)+len(a.detachedSessions))
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || tab.Ctrl == nil || seen[tab.Ctrl] {
			continue
		}
		seen[tab.Ctrl] = true
		ctrls = append(ctrls, tab.Ctrl)
	}
	a.mu.RUnlock()
	disconnected := false
	for _, ctrl := range ctrls {
		if ctrl.DisconnectMCPServer(serverName) {
			disconnected = true
		}
	}
	return disconnected
}

// SkillsSettings returns the skills management snapshot without MCP status.
func (a *App) SkillsSettings() SkillsSettingsView {
	out := SkillsSettingsView{Skills: []SkillView{}, SkillRoots: []SkillRootView{}, AllowImplicitInvocation: true}
	a.mu.RLock()
	tab := a.activeTabLocked()
	var ctrl control.SessionAPI
	workspaceRoot := "."
	if tab != nil {
		ctrl = tab.Ctrl
		if strings.TrimSpace(tab.WorkspaceRoot) != "" {
			workspaceRoot = tab.WorkspaceRoot
		}
	}
	a.mu.RUnlock()
	if ctrl == nil {
		return out
	}

	disabled := map[string]bool{}
	var configuredModels, configuredEfforts map[string]string
	if cfg, err := config.LoadForRootReadOnly(workspaceRoot); err == nil {
		out.AllowImplicitInvocation = cfg.ImplicitSkillInvocationEnabled()
		for _, name := range cfg.Skills.DisabledSkills {
			if key := config.SkillNameKey(name); key != "" {
				disabled[key] = true
			}
		}
		configuredModels = cfg.Agent.SubagentModels
		configuredEfforts = cfg.Agent.SubagentEfforts
	}
	out.SkillRoots = a.cachedSkillRootsView(workspaceRoot)
	for _, s := range ctrl.AllSkills() {
		view := SkillView{
			Name: s.Name, Description: s.Description,
			Scope: string(s.Scope), SourceDir: skillSourceDir(s, out.SkillRoots), RunAs: string(s.RunAs),
			Enabled:          !disabled[config.SkillNameKey(s.Name)],
			Plugin:           s.Plugin,
			Model:            s.Model,
			Effort:           s.Effort,
			AllowedTools:     append([]string{}, s.AllowedTools...),
			ReadOnly:         s.ReadOnly,
			Color:            s.Color,
			Invocation:       "/" + s.SlashName(),
			InvocationMode:   s.Invocation,
			ConfiguredModel:  subagentOverrideFor(configuredModels, s.Name),
			ConfiguredEffort: subagentOverrideFor(configuredEfforts, s.Name),
		}
		// Body feeds only the Subagents editor's prompt prefill. Inline skills
		// fold references/ into Body at load time (hundreds of KB for a rich
		// skill library), and every Capabilities/Settings fetch would ship all
		// of it across the JSON bridge for nothing.
		if s.RunAs == skill.RunSubagent {
			view.Body = s.Body
		}
		out.Skills = append(out.Skills, view)
	}
	return out
}

// SetSkillImplicitInvocation persists whether the model may discover and
// invoke skills automatically, then rebuilds the active runtime. Explicit
// /skill invocation and skill management remain available in either mode.
func (a *App) SetSkillImplicitInvocation(enabled bool) error {
	err := a.applySkillConfigChange("disable_implicit_invocation", "skills policy", func(c *config.Config) error {
		c.SetSkillImplicitInvocation(enabled)
		return nil
	})
	if err == nil {
		a.invalidateSkillRootsCache()
	}
	return err
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

func (a *App) mcpServersView() []ServerView {
	out := []ServerView{}
	a.mu.RLock()
	tab := a.activeTabLocked()
	if tab == nil {
		a.mu.RUnlock()
		return out
	}
	ctrl := tab.Ctrl
	disabled := make(map[string]ServerView, len(tab.disabledMCP))
	maps.Copy(disabled, tab.disabledMCP)
	order := append([]string(nil), tab.mcpOrder...)
	workspaceRoot := tab.WorkspaceRoot
	tabID := tab.ID
	a.mu.RUnlock()
	if ctrl == nil {
		return out
	}
	seen := map[string]bool{}
	connected := map[string]bool{}
	retainedDisabled := map[string]ServerView{}
	configured := map[string]config.PluginEntry{}
	managedByPlugin := map[string]string{}
	var configuredEntries []config.PluginEntry
	if cfg, err := config.LoadForRoot(workspaceRoot); err == nil {
		configuredEntries = append(configuredEntries, cfg.Plugins...)
		for _, p := range configuredEntries {
			configured[p.Name] = p
			if owner, ok := cfg.PluginPackageOwner(p.Name); ok {
				managedByPlugin[p.Name] = owner
			}
		}
	}
	if h := ctrl.Host(); h != nil {
		for _, s := range h.Servers() {
			if disabledView, ok := disabled[s.Name]; ok {
				disabledView.Status = "disabled"
				disabledView.RuntimeState = "idle"
				disabledView.StartIntent = "off"
				disabledView.Error = ""
				if p, ok := configured[s.Name]; ok {
					disabledView = withPluginConfigInWorkspace(disabledView, p, workspaceRoot)
				}
				out = append(out, disabledView)
				retainedDisabled[s.Name] = disabledView
				seen[s.Name] = true
				delete(disabled, s.Name)
				continue
			}
			seen[s.Name] = true
			connected[s.Name] = true
			view := pluginServerToView(s)
			if p, ok := configured[s.Name]; ok {
				view = withPluginConfigInWorkspace(view, p, workspaceRoot)
			}
			out = append(out, view)
		}
		for _, f := range h.Failures() {
			seen[f.Name] = true
			view := ServerView{
				Name: f.Name, Transport: f.Transport, Status: "failed", RuntimeState: "issue", Error: f.Error,
				RequiresLaunchApproval: f.RequiresLaunchApproval,
			}
			if p, ok := configured[f.Name]; ok {
				view = withPluginConfigInWorkspace(view, p, workspaceRoot)
			}
			out = append(out, view)
		}
		for _, name := range h.ConnectingServers() {
			if seen[name] {
				continue
			}
			seen[name] = true
			view := ServerView{Name: name, Status: "initializing", RuntimeState: "connecting"}
			if p, ok := configured[name]; ok {
				view = withPluginConfigInWorkspace(view, p, workspaceRoot)
			}
			out = append(out, view)
		}
	}
	// Configured servers that are neither connected, connecting, nor failed are
	// idle: disabled/off or automatic background startup waiting for its next kick.
	if len(configuredEntries) > 0 {
		for _, p := range configuredEntries {
			if seen[p.Name] {
				continue
			}
			if s, ok := disabled[p.Name]; ok {
				s.Status = "disabled"
				s.RuntimeState = "idle"
				s.StartIntent = "off"
				s = withPluginConfigInWorkspace(s, p, workspaceRoot)
				s.Error = ""
				out = append(out, s)
				retainedDisabled[p.Name] = s
				seen[p.Name] = true
				delete(disabled, p.Name)
				continue
			}
			status := "disabled"
			startIntent := "off"
			if mcpEntryEnabled(p, workspaceRoot) {
				status = "deferred"
				startIntent = "automatic"
			}
			out = append(out, withPluginConfigInWorkspace(ServerView{Name: p.Name, Status: status, StartIntent: startIntent, RuntimeState: "idle"}, p, workspaceRoot))
			seen[p.Name] = true
		}
	}
	out = orderServerViews(out, order)
	for i := range out {
		out[i].ManagedByPlugin = managedByPlugin[out[i].Name]
		out[i] = finalizeServerView(out[i])
	}

	a.mu.Lock()
	if tab, ok := a.tabs[tabID]; ok {
		for name := range connected {
			delete(retainedDisabled, name)
		}
		tab.disabledMCP = retainedDisabled
		tab.mcpOrder = mergeServerOrder(tab.mcpOrder, out)
	}
	a.mu.Unlock()
	return out
}

func mcpEntryEnabled(p config.PluginEntry, workspace string) bool {
	enabled, err := config.DefaultMCPActivationStore().IsEnabled(p, workspace)
	if err != nil {
		return p.ShouldAutoStart()
	}
	return enabled
}

func mcpRuntimeState(status string) string {
	switch status {
	case "connected":
		return "ready"
	case "initializing":
		return "connecting"
	case "failed":
		return "issue"
	default:
		return "idle"
	}
}

func mcpAvailability(v ServerView) string {
	if !v.Enabled {
		return "disabled"
	}
	switch v.RuntimeState {
	case "ready":
		return "connected"
	case "connecting":
		return "starting"
	case "issue":
		if v.RequiresLaunchApproval {
			return "project_auth_changed"
		}
		if v.AuthStatus == "required" || v.AuthStatus == "possible" {
			return "auth_required"
		}
		return "start_failed"
	default:
		// Idle enabled servers are available on demand, not "disconnected".
		return "available_on_demand"
	}
}

func mcpActionForView(v ServerView) string {
	if v.RequiresLaunchApproval {
		return "authorize"
	}
	if v.AuthStatus == "required" {
		return "authenticate"
	}
	if v.RuntimeState == "issue" {
		return "retry"
	}
	return "none"
}

func finalizeServerView(v ServerView) ServerView {
	if v.ToolList == nil {
		v.ToolList = []ToolView{}
	}
	if v.Args == nil {
		v.Args = []string{}
	}
	if v.EnvKeys == nil {
		v.EnvKeys = []string{}
	}
	if v.HeaderKeys == nil {
		v.HeaderKeys = []string{}
	}
	v.ToolCount = v.Tools
	if v.ToolCount == 0 && len(v.ToolList) > 0 {
		v.ToolCount = len(v.ToolList)
		v.Tools = v.ToolCount
	}
	v.Installed = v.Configured || v.BuiltIn || v.Status != ""
	if v.Source == "" {
		switch {
		case v.BuiltIn:
			v.Source = "builtin"
		case v.ManagedByPlugin != "":
			v.Source = "plugin"
		case v.Configured:
			v.Source = "user"
		}
	}
	if v.RuntimeState == "" {
		v.RuntimeState = mcpRuntimeState(v.Status)
	}
	v.Availability = mcpAvailability(v)
	if v.Action == "" {
		v.Action = mcpActionForView(v)
	}
	// Keep deprecated fields derived from the new product state.
	v.AutoStart = v.Enabled
	if !v.Enabled {
		v.StartIntent = "off"
	} else if v.StartIntent == "" {
		v.StartIntent = "automatic"
	}
	return v
}

func withPluginConfig(v ServerView, p config.PluginEntry) ServerView {
	return withPluginConfigInWorkspace(v, p, "")
}

func withPluginConfigInWorkspace(v ServerView, p config.PluginEntry, workspace string) ServerView {
	tt := p.Type
	if tt == "" {
		tt = "stdio"
	}
	v.Transport = tt
	v.Configured = true
	v.Installed = true
	v.Source, v.ConfigSource = mcpServerSource(p.Source)
	v.Enabled = mcpEntryEnabled(p, workspace)
	v.AutoStart = v.Enabled
	v.Tier = p.ResolvedTier()
	if v.StartIntent == "" {
		if v.Enabled {
			v.StartIntent = "automatic"
		} else {
			v.StartIntent = "off"
		}
	}
	if !v.Enabled || v.Status == "disabled" {
		v.Status = "disabled"
		v.StartIntent = "off"
		v.RuntimeState = "idle"
	}
	if v.RuntimeState == "" {
		v.RuntimeState = mcpRuntimeState(v.Status)
	}
	v.Command = p.Command
	v.Args = append([]string(nil), p.Args...)
	v.URL = p.URL
	v.CallTimeoutSeconds = p.CallTimeoutSeconds
	v.ToolTimeoutSeconds = cloneStringIntMap(p.ToolTimeoutSeconds)
	// Configured MCP entries are explicit installs, including project sources.
	v.RequiresLaunchApproval = false
	v.AuthConfigured = mcpdiag.HasAuthConfig(p.Headers, p.Env, p.URL)
	v.EnvKeys = nil
	v.HeaderKeys = nil
	if len(p.Env) > 0 {
		v.EnvKeys = make([]string, 0, len(p.Env))
		for k := range p.Env {
			v.EnvKeys = append(v.EnvKeys, k)
		}
		sort.Strings(v.EnvKeys)
	}
	if len(p.Headers) > 0 {
		v.HeaderKeys = make([]string, 0, len(p.Headers))
		for k := range p.Headers {
			v.HeaderKeys = append(v.HeaderKeys, k)
		}
		sort.Strings(v.HeaderKeys)
	}
	auth := mcpdiag.DiagnoseAuth(v.Transport, v.Status, v.Error, v.URL, v.AuthConfigured)
	v.AuthStatus = auth.Status
	v.AuthURL = auth.URL
	return v
}

func mcpServerSource(source config.MCPConfigSource) (kind, configSource string) {
	switch source {
	case config.MCPSourceProjectConfig:
		return "project", "reasonix.toml"
	case config.MCPSourceProjectMCPJSON:
		return "project", ".mcp.json"
	case config.MCPSourcePluginPackage:
		return "plugin", "plugin"
	case config.MCPSourceLegacyUser:
		return "user", "legacy config"
	case config.MCPSourceUserConfig:
		return "user", "config.toml"
	default:
		return "", ""
	}
}

const skillRootsCacheTTL = 10 * time.Second

func (a *App) cachedSkillRootsView(workspaceRoots ...string) []SkillRootView {
	workspaceRoot := "."
	if len(workspaceRoots) > 0 {
		workspaceRoot = workspaceRoots[0]
	}
	workspaceRoot = normalizeWorkspaceRoot(workspaceRoot)
	cfg, _ := config.LoadForRootReadOnly(workspaceRoot)
	userCfg := config.LoadForEdit(config.UserConfigPath())
	key := skillRootsCacheKey(workspaceRoot, cfg, userCfg)

	now := time.Now()
	a.skillRootsMu.Lock()
	if a.skillRootsCache.key == key && now.Sub(a.skillRootsCache.at) < skillRootsCacheTTL {
		roots := cloneSkillRootViews(a.skillRootsCache.roots)
		a.skillRootsMu.Unlock()
		return roots
	}
	a.skillRootsMu.Unlock()

	roots := skillRootsViewFrom(workspaceRoot, cfg, userCfg)

	a.skillRootsMu.Lock()
	a.skillRootsCache = skillRootsCache{
		key:   key,
		at:    now,
		roots: cloneSkillRootViews(roots),
	}
	a.skillRootsMu.Unlock()
	return roots
}

func (a *App) invalidateSkillRootsCache() {
	a.skillRootsMu.Lock()
	a.skillRootsCache = skillRootsCache{}
	a.skillRootsMu.Unlock()
}

func skillRootsView() []SkillRootView {
	cwd, _ := os.Getwd()
	cfg, _ := config.Load()
	userCfg := config.LoadForEdit(config.UserConfigPath())
	return skillRootsViewFrom(cwd, cfg, userCfg)
}

func skillRootsViewFrom(workspaceRoot string, cfg, userCfg *config.Config) []SkillRootView {
	workspaceRoot = normalizeWorkspaceRoot(workspaceRoot)
	var custom []string
	var excluded []string
	maxDepth := 3
	if cfg != nil {
		custom = cfg.SkillCustomPaths()
		excluded = cfg.SkillExcludedPaths()
		maxDepth = cfg.SkillMaxDepth()
	}
	var pluginPaths map[string][]string
	var pluginAgentPaths map[string][]string
	if cfg != nil {
		pluginPaths = cfg.PluginPackageSkillOwners()
		pluginAgentPaths = cfg.PluginPackageAgentOwners()
	}
	st := skill.New(skill.Options{ProjectRoot: workspaceRoot, CustomPaths: custom, PluginPaths: pluginPaths, PluginAgentPaths: pluginAgentPaths, ExcludedPaths: excluded, MaxDepth: maxDepth, DisableBuiltins: true, Stderr: io.Discard})
	counts := map[string]int{}
	skillItems := map[string][]SkillRootSkillView{}
	roots := st.Roots()
	for _, sk := range st.SlashList() {
		root := skillDisplayRoot(sk, roots)
		counts[root]++
		skillItems[root] = append(skillItems[root], SkillRootSkillView{
			Name:         sk.Name,
			Description:  sk.Description,
			Scope:        string(sk.Scope),
			RunAs:        string(sk.RunAs),
			Plugin:       sk.Plugin,
			Model:        sk.Model,
			Effort:       sk.Effort,
			AllowedTools: append([]string{}, sk.AllowedTools...),
			Color:        sk.Color,
			Invocation:   "/" + sk.SlashName(),
		})
	}
	for root := range skillItems {
		sort.Slice(skillItems[root], func(i, j int) bool {
			return skillItems[root][i].Invocation < skillItems[root][j].Invocation
		})
	}
	userConfigured := map[string]bool{}
	if userCfg != nil {
		for _, p := range userCfg.Skills.Paths {
			userConfigured[canonicalSkillPathForRoot(p, workspaceRoot)] = true
		}
	}
	effectiveConfigured := map[string]bool{}
	effectiveExcluded := map[string]bool{}
	if cfg != nil {
		for _, p := range cfg.Skills.Paths {
			effectiveConfigured[canonicalSkillPathForRoot(p, workspaceRoot)] = true
		}
		for _, p := range cfg.Skills.ExcludedPaths {
			effectiveExcluded[canonicalSkillPathForRoot(p, workspaceRoot)] = true
		}
	}
	out := []SkillRootView{}
	seenRoots := map[string]int{}
	for _, r := range roots {
		dir := canonicalSkillPathForRoot(r.Dir, workspaceRoot)
		view := SkillRootView{
			Dir:        r.Dir,
			Scope:      string(r.Scope),
			Priority:   r.Priority + 1,
			Status:     string(r.Status),
			Enabled:    true,
			Configured: r.Scope == skill.ScopeCustom && (userConfigured[dir] || effectiveConfigured[dir]),
			Removable:  true,
			Skills:     counts[dir],
			SkillItems: skillItems[dir],
		}
		if idx, ok := seenRoots[dir]; ok {
			out[idx] = mergeDuplicateSkillRootView(out[idx], view)
			continue
		}
		seenRoots[dir] = len(out)
		out = append(out, view)
	}
	if cfg != nil {
		for _, p := range cfg.Skills.Paths {
			if rootActive(out, p, workspaceRoot) {
				continue
			}
			dir := canonicalSkillPathForRoot(p, workspaceRoot)
			enabled := !effectiveExcluded[dir]
			status := "inactive"
			warning := "configured in project/user config but not active in this workspace"
			if !enabled {
				status = "disabled"
				warning = ""
			}
			appendSkillRootView(&out, &seenRoots, SkillRootView{
				Dir: dir, Scope: string(skill.ScopeCustom), Status: status, Enabled: enabled,
				Configured: true, Removable: true, Warning: warning,
			}, workspaceRoot)
		}
		for _, p := range cfg.Skills.ExcludedPaths {
			if rootActive(out, p, workspaceRoot) {
				continue
			}
			dir := canonicalSkillPathForRoot(p, workspaceRoot)
			scope := skillRootScopeForPath(p, workspaceRoot)
			appendSkillRootView(&out, &seenRoots, SkillRootView{
				Dir: dir, Scope: string(scope), Status: "disabled", Enabled: false,
				Configured: scope == skill.ScopeCustom || effectiveConfigured[dir], Removable: true,
			}, workspaceRoot)
		}
	}
	if userCfg != nil {
		userExcluded := map[string]bool{}
		for _, p := range userCfg.Skills.ExcludedPaths {
			userExcluded[canonicalSkillPathForRoot(p, workspaceRoot)] = true
		}
		for _, p := range userCfg.Skills.Paths {
			if rootActive(out, p, workspaceRoot) {
				continue
			}
			enabled := !userExcluded[canonicalSkillPathForRoot(p, workspaceRoot)]
			status := "inactive"
			warning := "configured in user config but not active in this workspace; project [skills].paths may override it"
			if !enabled {
				status = "disabled"
				warning = ""
			}
			appendSkillRootView(&out, &seenRoots, SkillRootView{
				Dir:        canonicalSkillPathForRoot(p, workspaceRoot),
				Scope:      string(skill.ScopeCustom),
				Status:     status,
				Enabled:    enabled,
				Configured: true,
				Removable:  true,
				Warning:    warning,
			}, workspaceRoot)
		}
		for _, p := range userCfg.Skills.ExcludedPaths {
			if rootActive(out, p, workspaceRoot) || userConfigured[canonicalSkillPathForRoot(p, workspaceRoot)] {
				continue
			}
			scope := skillRootScopeForPath(p, workspaceRoot)
			appendSkillRootView(&out, &seenRoots, SkillRootView{
				Dir: canonicalSkillPathForRoot(p, workspaceRoot), Scope: string(scope), Status: "disabled", Enabled: false,
				Configured: scope == skill.ScopeCustom, Removable: true,
			}, workspaceRoot)
		}
	}
	return out
}

func appendSkillRootView(out *[]SkillRootView, seen *map[string]int, view SkillRootView, workspaceRoot string) {
	dir := canonicalSkillPathForRoot(view.Dir, workspaceRoot)
	if idx, ok := (*seen)[dir]; ok {
		(*out)[idx] = mergeDuplicateSkillRootView((*out)[idx], view)
		return
	}
	(*seen)[dir] = len(*out)
	*out = append(*out, view)
}

func mergeDuplicateSkillRootView(existing, duplicate SkillRootView) SkillRootView {
	existing.Configured = existing.Configured || duplicate.Configured
	existing.Removable = existing.Removable || duplicate.Removable
	if existing.Status != "ok" && duplicate.Status == "ok" {
		existing.Status = duplicate.Status
		existing.Enabled = duplicate.Enabled
	}
	if existing.Skills == 0 && duplicate.Skills > 0 {
		existing.Skills = duplicate.Skills
		existing.SkillItems = duplicate.SkillItems
	}
	if existing.Warning == "" {
		existing.Warning = duplicate.Warning
	}
	return existing
}

func skillRootsCacheKey(workspaceRoot string, cfg, userCfg *config.Config) string {
	type cacheKey struct {
		CWD       string   `json:"cwd"`
		Custom    []string `json:"custom"`
		Plugins   []string `json:"plugins"`
		Excluded  []string `json:"excluded"`
		MaxDepth  int      `json:"maxDepth"`
		UserPaths []string `json:"userPaths"`
	}
	workspaceRoot = normalizeWorkspaceRoot(workspaceRoot)
	key := cacheKey{CWD: canonicalSkillPathForRoot(workspaceRoot, workspaceRoot), MaxDepth: 3}
	if cfg != nil {
		key.Custom = canonicalSkillPathsForRoot(cfg.SkillCustomPaths(), workspaceRoot)
		for path, owners := range cfg.PluginPackageSkillOwners() {
			for _, owner := range owners {
				key.Plugins = append(key.Plugins, canonicalSkillPathForRoot(path, workspaceRoot)+"\x00"+owner)
			}
		}
		sort.Strings(key.Plugins)
		key.Excluded = canonicalSkillPathsForRoot(cfg.SkillExcludedPaths(), workspaceRoot)
		key.MaxDepth = cfg.SkillMaxDepth()
	}
	if userCfg != nil {
		key.UserPaths = canonicalSkillPathsForRoot(userCfg.Skills.Paths, workspaceRoot)
	}
	b, err := json.Marshal(key)
	if err != nil {
		return fmt.Sprintf("%s|%v|%v|%v|%d|%v", key.CWD, key.Custom, key.Plugins, key.Excluded, key.MaxDepth, key.UserPaths)
	}
	return string(b)
}

func canonicalSkillPathsForRoot(paths []string, workspaceRoot string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, canonicalSkillPathForRoot(p, workspaceRoot))
	}
	sort.Strings(out)
	return out
}

func normalizeWorkspaceRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" || root == "." {
		if cwd, err := os.Getwd(); err == nil {
			return filepath.Clean(cwd)
		}
		return "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(root)
}

// canonicalSkillPathForRoot mirrors skill.Store's path resolution while keeping
// comparisons independent of the desktop process CWD. Config may intentionally
// contain relative paths; those are relative to the active workspace.
func canonicalSkillPathForRoot(path, workspaceRoot string) string {
	path = config.ExpandVars(strings.TrimSpace(path))
	if path == "" {
		return ""
	}
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				path = home
			} else {
				path = filepath.Join(home, path[2:])
			}
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(normalizeWorkspaceRoot(workspaceRoot), path)
	}
	return config.CanonicalSkillPath(path)
}

func cloneSkillRootViews(in []SkillRootView) []SkillRootView {
	out := make([]SkillRootView, len(in))
	for i, r := range in {
		out[i] = r
		out[i].SkillItems = append([]SkillRootSkillView(nil), r.SkillItems...)
	}
	return out
}

func rootActive(roots []SkillRootView, path string, workspaceRoots ...string) bool {
	workspaceRoot := "."
	if len(workspaceRoots) > 0 {
		workspaceRoot = workspaceRoots[0]
	}
	want := canonicalSkillPathForRoot(path, workspaceRoot)
	for _, r := range roots {
		if canonicalSkillPathForRoot(r.Dir, workspaceRoot) == want {
			return true
		}
	}
	return false
}

// PickSkillFolder opens a directory picker for adding custom skill roots. It only
// returns a path; AddSkillPath performs normalization and writes config.
func (a *App) PickSkillFolder() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	cur, _ := os.Getwd()
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Choose skills folder",
		DefaultDirectory: dialogDefaultDirectory(cur),
	})
	if err != nil || dir == "" {
		return "", err
	}
	return normalizeSkillPath(dir), nil
}

// PickPluginFolder opens a directory picker for choosing a local plugin package
// source. It returns the selected directory path; plugin install/plan performs
// manifest validation and decides whether to copy or link the package.
func (a *App) PickPluginFolder() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	cur := a.activeWorkspaceRoot()
	if strings.TrimSpace(cur) == "" {
		cur, _ = os.Getwd()
	}
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Choose plugin folder",
		DefaultDirectory: dialogDefaultDirectory(cur),
	})
	if err != nil || dir == "" {
		return "", err
	}
	return filepath.Clean(dir), nil
}

// AddSkillPath adds a custom skill root to the user config and rebuilds the
// controller so the skills index and slash menu reflect it immediately.
func (a *App) AddSkillPath(path string) error {
	path = normalizeSkillPath(path)
	workspaceRoot := a.activeWorkspaceRoot()
	field := "paths"
	if isConventionSkillRoot(path, workspaceRoot) {
		field = "excluded_paths"
	}
	err := a.applySkillConfigChange(field, "skills source", func(c *config.Config) error {
		if isConventionSkillRoot(path, workspaceRoot) {
			return c.RestoreSkillPath(path)
		}
		return c.AddSkillPath(path)
	})
	if err == nil {
		a.invalidateSkillRootsCache()
	}
	return err
}

// RemoveSkillPath removes a skill source from the user config and rebuilds. For
// convention roots, it records a pseudo-delete in excluded_paths.
func (a *App) RemoveSkillPath(path string) error {
	path = normalizeSkillPath(path)
	workspaceRoot := a.activeWorkspaceRoot()
	field := "paths"
	if isConventionSkillRoot(path, workspaceRoot) {
		field = "excluded_paths"
	}
	err := a.applySkillConfigChange(field, "skills source", func(c *config.Config) error {
		removed, err := c.RemoveSkillPath(path)
		if err != nil || removed {
			return err
		}
		return c.ExcludeSkillPath(path)
	})
	if err == nil {
		a.invalidateSkillRootsCache()
	}
	return err
}

// SetSkillPathEnabled persists a reversible source toggle and rebuilds the
// controller so the source is immediately included or excluded from discovery.
func (a *App) SetSkillPathEnabled(path string, enabled bool) error {
	path = normalizeSkillPath(path)
	workspaceRoot := a.activeWorkspaceRoot()
	field := "paths"
	if isConventionSkillRoot(path, workspaceRoot) {
		field = "excluded_paths"
	}
	err := a.applySkillConfigChange(field, "skills source", func(c *config.Config) error {
		return c.SetSkillPathEnabled(path, enabled)
	})
	if err == nil {
		a.invalidateSkillRootsCache()
	}
	return err
}

// RefreshSkills rebuilds the controller without changing config, reloading skill
// discovery, the system prompt index, and slash completions.
func (a *App) RefreshSkills() error {
	a.invalidateSkillRootsCache()
	if err := a.rebuild(); err != nil {
		// The skill cache is already invalidated; refresh the runtime once the
		// other window releases the session lease.
		if _, ok := a.deferredRebuildWarning("skills", err); ok {
			return nil
		}
		return err
	}
	return nil
}

// ReloadCommands rescans command directories and hot-swaps without restarting
// the controller — no MCP disconnect, no hook rerun.
func (a *App) ReloadCommands() error {
	if a.ctx == nil {
		return nil
	}
	_, ctrl := a.activeTabAndCtrl()
	if ctrl == nil {
		return fmt.Errorf("no active session")
	}
	if ctrl.Running() {
		return fmt.Errorf("wait for the current turn to finish, then retry")
	}
	return ctrl.ReloadCommands(a.ctx)
}

// SetSkillEnabled persists a skill toggle and rebuilds the controller so the
// prompt index, slash menu, and skill tools reflect it immediately.
func (a *App) SetSkillEnabled(name string, enabled bool) error {
	err := a.applySkillConfigChange("disabled_skills", "skill", func(c *config.Config) error {
		return c.SetSkillEnabled(name, enabled)
	})
	if err == nil {
		a.invalidateSkillRootsCache()
	}
	return err
}

func normalizeSkillPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				path = home
			} else {
				path = filepath.Join(home, path[2:])
			}
		}
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	info, err := os.Stat(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if info.Mode().IsRegular() {
		if filepath.Base(path) == skill.SkillFile {
			return filepath.Clean(filepath.Dir(filepath.Dir(path)))
		}
		return filepath.Clean(filepath.Dir(path))
	}
	if info.IsDir() {
		if _, err := os.Stat(filepath.Join(path, skill.SkillFile)); err == nil {
			return filepath.Clean(filepath.Dir(path))
		}
	}
	return filepath.Clean(path)
}

func isConventionSkillRoot(path, workspaceRoot string) bool {
	want := canonicalSkillPathForRoot(path, workspaceRoot)
	if want == "" {
		return false
	}
	bases := []string{normalizeWorkspaceRoot(workspaceRoot)}
	if home, err := os.UserHomeDir(); err == nil {
		bases = append(bases, home)
	}
	for _, base := range bases {
		base = strings.TrimSpace(base)
		if base == "" {
			continue
		}
		for _, dir := range config.ConventionDirs {
			if want == canonicalSkillPathForRoot(filepath.Join(base, dir, skill.SkillsDirname), workspaceRoot) {
				return true
			}
		}
	}
	return false
}

func skillRootScopeForPath(path, workspaceRoot string) skill.Scope {
	want := canonicalSkillPathForRoot(path, workspaceRoot)
	if home, err := os.UserHomeDir(); err == nil {
		for _, dir := range config.ConventionDirs {
			if want == canonicalSkillPathForRoot(filepath.Join(home, dir, skill.SkillsDirname), workspaceRoot) {
				return skill.ScopeGlobal
			}
		}
	}
	if isConventionSkillRoot(path, workspaceRoot) {
		return skill.ScopeProject
	}
	return skill.ScopeCustom
}

func skillRootPath(path string) string {
	if filepath.Base(path) == skill.SkillFile {
		return filepath.Dir(path)
	}
	return path
}

func skillDisplayRoot(sk skill.Skill, roots []skill.Root) string {
	cleanPath := filepath.Clean(sk.Path)
	for _, r := range roots {
		if r.Scope != sk.Scope {
			continue
		}
		cleanRoot := filepath.Clean(r.Dir)
		prefix := cleanRoot + string(filepath.Separator)
		if cleanPath == cleanRoot || strings.HasPrefix(cleanPath, prefix) {
			return config.CanonicalSkillPath(r.Dir)
		}
	}
	return config.CanonicalSkillPath(filepath.Dir(skillRootPath(sk.Path)))
}

func skillSourceDir(sk skill.Skill, roots []SkillRootView) string {
	path := strings.TrimSpace(sk.Path)
	if path == "" || strings.HasPrefix(path, "(builtin") {
		return ""
	}
	cleanPath := config.CanonicalSkillPath(path)
	bestDir := ""
	bestLen := -1
	for _, root := range roots {
		if root.Scope != "" && root.Scope != string(sk.Scope) {
			continue
		}
		cleanRoot := config.CanonicalSkillPath(root.Dir)
		if cleanRoot == "" {
			continue
		}
		prefix := cleanRoot + string(filepath.Separator)
		if cleanPath != cleanRoot && !strings.HasPrefix(cleanPath, prefix) {
			continue
		}
		if len(cleanRoot) > bestLen {
			bestDir = root.Dir
			bestLen = len(cleanRoot)
		}
	}
	if bestDir != "" {
		return bestDir
	}
	return config.CanonicalSkillPath(filepath.Dir(skillRootPath(path)))
}

// MCPServerInput is the drawer's "add server" form. Transport is "stdio" (Command
// + Args + Env) or "http"/"sse" (URL). Mirrors config.PluginEntry's writable shape.
type MCPServerInput struct {
	Name               string            `json:"name"`
	Transport          string            `json:"transport"`
	Command            string            `json:"command"`
	Args               []string          `json:"args"`
	URL                string            `json:"url"`
	Env                map[string]string `json:"env"`
	Headers            map[string]string `json:"headers"`
	AutoStart          *bool             `json:"autoStart"`
	CallTimeoutSeconds *int              `json:"callTimeoutSeconds"`
	ToolTimeoutSeconds map[string]int    `json:"toolTimeoutSeconds"`
}

func mcpServerInputEntry(in MCPServerInput) config.PluginEntry {
	entry := config.PluginEntry{
		Name:               strings.TrimSpace(in.Name),
		Type:               normalizeMCPTransport(in.Transport),
		Command:            strings.TrimSpace(in.Command),
		Args:               append([]string(nil), in.Args...),
		URL:                strings.TrimSpace(in.URL),
		Env:                in.Env,
		Headers:            in.Headers,
		AutoStart:          in.AutoStart,
		CallTimeoutSeconds: mcpIntValue(in.CallTimeoutSeconds),
		ToolTimeoutSeconds: cloneStringIntMap(in.ToolTimeoutSeconds),
		Source:             config.MCPSourceUserConfig,
	}
	entry, _ = config.NormalizePluginCommandLine(entry)
	return entry
}

// InstallMCPServer is the desktop's high-level install transaction. A normal
// handshake failure leaves no config behind; authentication-required servers
// are retained so the user can complete OAuth and retry. Only a ready result is
// published to every controller sharing the Host.
func (a *App) InstallMCPServer(in MCPServerInput) (plugin.MCPInstallResult, error) {
	defer a.lockMCPMutation("add")()

	_, ctrl, root := a.activeMCPRuntime()
	if ctrl == nil {
		return plugin.MCPInstallResult{}, fmt.Errorf("no active session")
	}
	host, releaseGates, err := a.lockMCPHostTurnGates("MCP server", ctrl)
	if err != nil {
		return plugin.MCPInstallResult{}, err
	}
	defer releaseGates()

	entry := mcpServerInputEntry(in)
	if entry.Name == "" {
		return plugin.InstallResultForError(entry.Name, fmt.Errorf("MCP server name is required")), nil
	}
	if _, found, lookupErr := desktopEffectiveMCPServer(root, entry.Name); lookupErr != nil {
		return plugin.MCPInstallResult{}, lookupErr
	} else if found {
		return plugin.InstallResultForError(entry.Name, fmt.Errorf("MCP server %q is already installed", entry.Name)), nil
	}

	controllers := a.mcpControllersSharingHost(host, entry.Name, ctrl)
	toolCount, connectErr := ctrl.ConnectMCPServer(entry)
	if connectErr != nil {
		result := plugin.InstallResultForError(entry.Name, connectErr)
		if result.State != "action_required" {
			if host != nil {
				host.ClearFailure(entry.Name)
			}
			return result, nil
		}
		if err := a.saveDesktopMCPServer(root, entry); err != nil {
			return plugin.MCPInstallResult{}, err
		}
		if err := persistMCPInstallActivation(entry, root); err != nil {
			_, rollbackErr := a.removeDesktopMCPServer(root, entry.Name)
			if host != nil {
				host.ClearFailure(entry.Name)
			}
			return plugin.MCPInstallResult{}, errors.Join(err, rollbackErr)
		}
		a.bumpExtensionGeneration()
		recordMCPFailure(ctrl, entry, connectErr)
		return result, nil
	}
	var publishErrs []error
	for _, target := range controllers {
		if target.ctrl == ctrl || !target.enabled {
			continue
		}
		if _, err := target.ctrl.ConnectMCPServer(entry); err != nil {
			publishErrs = append(publishErrs, err)
		}
	}
	if err := errors.Join(publishErrs...); err != nil {
		disconnectMCPServerControllers(entry.Name, ctrl, controllers)
		return plugin.MCPInstallResult{}, fmt.Errorf("publish MCP tools: %w", err)
	}
	if err := a.saveDesktopMCPServer(root, entry); err != nil {
		disconnectMCPServerControllers(entry.Name, ctrl, controllers)
		return plugin.MCPInstallResult{}, err
	}
	if err := persistMCPInstallActivation(entry, root); err != nil {
		disconnectMCPServerControllers(entry.Name, ctrl, controllers)
		_, rollbackErr := a.removeDesktopMCPServer(root, entry.Name)
		// The first disconnect happened while the just-saved config still
		// existed, so controller runtimes retained it as disabled. Reconcile once
		// more after rollback removes the config to prevent a phantom proxy entry.
		disconnectMCPServerControllers(entry.Name, ctrl, controllers)
		return plugin.MCPInstallResult{}, errors.Join(err, rollbackErr)
	}
	a.bumpExtensionGeneration()
	return plugin.ReadyInstallResult(entry.Name, toolCount), nil
}
func persistMCPInstallActivation(entry config.PluginEntry, root string) error {
	store := config.DefaultMCPActivationStore()
	if !entry.ShouldAutoStart() {
		return store.ClearServer(entry, root)
	}
	return store.SetServerEnabled(entry, root, true)
}

// AddMCPServer is retained for old generated Wails clients. New clients use
// InstallMCPServer so authentication and retry states remain structured.
func (a *App) AddMCPServer(in MCPServerInput) (int, error) {
	result, err := a.InstallMCPServer(in)
	if err != nil {
		return 0, err
	}
	if result.State != "ready" {
		return 0, fmt.Errorf("%s", result.Message)
	}
	return result.ToolCount, nil
}

// UpdateMCPServer edits a persisted external MCP server. The name is the stable
// identity; callers must remove + add if they want to rename a server.
func (a *App) UpdateMCPServer(name string, in MCPServerInput) error {
	defer a.lockMCPMutation("update")()

	tab, ctrl, root := a.activeMCPRuntime()
	if tab == nil || ctrl == nil {
		return fmt.Errorf("no active session")
	}
	host, releaseGates, err := a.lockMCPHostTurnGates("MCP server", ctrl)
	if err != nil {
		return err
	}
	defer releaseGates()
	controllers := a.mcpControllersSharingHost(host, name, ctrl)
	if strings.TrimSpace(in.Name) != "" && strings.TrimSpace(in.Name) != name {
		return fmt.Errorf("renaming MCP servers is not supported; remove and add a new server")
	}
	updated, found, err := a.desktopMCPServerForEdit(root, name)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no configured MCP server named %q", name)
	}
	original := updated
	updated.Type = normalizeMCPTransport(in.Transport)
	updated.Command = strings.TrimSpace(in.Command)
	updated.Args = append([]string(nil), in.Args...)
	updated.URL = strings.TrimSpace(in.URL)
	updated.Tier = ""
	if in.Env != nil {
		updated.Env = in.Env
	}
	if in.Headers != nil {
		updated.Headers = in.Headers
	}
	if in.AutoStart != nil {
		value := *in.AutoStart
		updated.AutoStart = &value
	}
	if in.CallTimeoutSeconds != nil {
		updated.CallTimeoutSeconds = *in.CallTimeoutSeconds
	}
	if in.ToolTimeoutSeconds != nil {
		updated.ToolTimeoutSeconds = cloneStringIntMap(in.ToolTimeoutSeconds)
	}
	updated, _ = config.NormalizePluginCommandLine(updated)
	if updated.Type == "stdio" {
		updated.URL = ""
	} else {
		updated.Command = ""
		updated.Args = nil
	}
	enabled := false
	for _, target := range controllers {
		enabled = enabled || target.enabled
	}
	if !enabled {
		return a.saveDesktopMCPServerAndBump(root, updated)
	}
	spec, specErr := a.mcpLaunchSpecForEntry(root, updated)
	if specErr != nil {
		return specErr
	}
	if spec.RequireLaunchApproval {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := plugin.AuthorizeProjectSpecLaunch(ctx, spec); err != nil {
			return err
		}
	}
	disconnectMCPServerControllers(name, ctrl, controllers)
	if err := reconnectMCPServerControllers(updated, controllers); err != nil {
		rollbackErr := reconnectMCPServerControllers(original, controllers)
		recordMCPFailure(ctrl, updated, err)
		return errors.Join(err, rollbackErr)
	}
	if err := a.saveDesktopMCPServer(root, updated); err != nil {
		disconnectMCPServerControllers(name, ctrl, controllers)
		rollbackErr := reconnectMCPServerControllers(original, controllers)
		return errors.Join(err, rollbackErr)
	}
	a.bumpExtensionGeneration()
	return nil
}

// RemoveMCPServer disconnects a live server and drops it from config (the row's ✕).
// Uninstall also clears durable activation overrides for that server.
func (a *App) RemoveMCPServer(name string) error {
	defer a.lockMCPMutation("remove")()

	tab, ctrl, root := a.activeMCPRuntime()
	if tab == nil || ctrl == nil {
		return fmt.Errorf("no active session")
	}
	host, releaseGates, err := a.lockMCPHostTurnGates("MCP server", ctrl)
	if err != nil {
		return err
	}
	defer releaseGates()
	controllers := a.mcpControllersSharingHost(host, name, ctrl)
	if err := ensureMCPServerDirectlyWritable(root, name); err != nil {
		return err
	}
	entry, hasEntry, _ := desktopEffectiveMCPServer(root, name)
	removed, err := a.removeDesktopMCPServer(root, name)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("no removable MCP server named %q", name)
	}
	if hasEntry {
		_ = config.DefaultMCPActivationStore().ClearServer(entry, root)
	}
	authCleanupErr := reconcileRemovedMCPAuthentication(name, a.mcpWorkspaceRoots(root))
	disconnectMCPServerControllers(name, ctrl, controllers)
	if host != nil {
		host.ClearFailure(name)
	}
	restoreMCPServerFallbacks(name, controllers)
	a.clearMCPServerTabState(name, controllers)
	a.bumpExtensionGeneration()
	return authCleanupErr
}

// restoreMCPServerFallbacks makes a lower-priority declaration immediately
// available after its project override is removed. Registration is cache-first:
// it restores cached tools or a connect placeholder without starting a process.
func restoreMCPServerFallbacks(name string, controllers []mcpControllerTarget) {
	for _, target := range controllers {
		root := target.ctrl.WorkspaceRoot()
		cfg, err := config.LoadForRoot(root)
		if err != nil {
			slog.Warn("desktop: reload MCP fallback after remove", "name", name, "workspace", root, "err", err)
			continue
		}
		entry, found := findPluginEntry(cfg.Plugins, name)
		if !found || !mcpEntryEnabled(entry, root) {
			continue
		}
		if _, err := target.ctrl.RegisterMCPServerOnDemand(entry); err != nil {
			slog.Warn("desktop: restore MCP fallback after remove", "name", name, "workspace", root, "err", err)
		}
	}
}

// ReconnectMCPServer disconnects the server if it is already connected (to force
// a fresh handshake and tool re-registration), then reconnects.  Failures are
// recorded on the Host so the UI can render them.
func (a *App) ReconnectMCPServer(name string) error {
	defer a.lockMCPMutation("reconnect")()

	tab, ctrl, root := a.activeMCPRuntime()
	if tab == nil || ctrl == nil {
		return fmt.Errorf("no active session")
	}
	host, releaseGates, err := a.lockMCPHostTurnGates("MCP server", ctrl)
	if err != nil {
		return err
	}
	defer releaseGates()
	entry, found, err := desktopEffectiveMCPServer(root, name)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no configured MCP server named %q", name)
	}
	controllers := a.mcpControllersSharingHost(host, name, ctrl)
	for i := range controllers {
		if controllers[i].ctrl == ctrl {
			controllers[i].enabled = true
		}
	}
	disconnectMCPServerControllers(name, ctrl, controllers)
	if host != nil {
		host.ClearFailure(name)
	}
	if err := reconnectMCPServerControllers(entry, controllers); err != nil {
		recordMCPFailure(ctrl, entry, err)
		return err
	}
	a.mu.Lock()
	delete(tab.disabledMCP, name)
	a.mu.Unlock()
	a.bumpExtensionGeneration()
	return nil
}

// SetMCPServerEnabled is the durable enable/disable switch for an installed MCP
// server. It writes $REASONIX_HOME/mcp-activation.json and updates the live
// registry: disable removes tools and may stop the process; enable restores
// cached tools and starts the process only on the next real tool call.
func (a *App) SetMCPServerEnabled(name string, enabled bool) error {
	defer a.lockMCPMutation("set-enabled")()

	tab, ctrl, root := a.activeMCPRuntime()
	if tab == nil || ctrl == nil {
		return fmt.Errorf("no active session")
	}
	a.mu.RLock()
	hostKey := tab.SharedHostKey
	a.mu.RUnlock()
	if err := rebuildControllerActiveWorkErrorFor(ctrl, "MCP server"); err != nil {
		return err
	}
	configuredEntry, hasConfiguredEntry, err := desktopEffectiveMCPServer(root, name)
	if err != nil {
		return err
	}
	if !hasConfiguredEntry {
		return fmt.Errorf("no configured MCP server named %q", name)
	}
	activationStore := config.DefaultMCPActivationStore()
	scope, workspaceFP, source, owner := config.ActivationIdentity(configuredEntry, root)
	previousEnabled, previousFound, err := activationStore.Lookup(scope, workspaceFP, source, owner, configuredEntry.Name)
	if err != nil {
		return err
	}
	if err := activationStore.SetServerEnabled(configuredEntry, root, enabled); err != nil {
		return err
	}
	a.bumpExtensionGeneration()
	if enabled {
		// Restore cached tools (or a cache-miss connect stub) without forcing a
		// process start. Explicit install/retry remains the readiness-probed path.
		_, err := a.registerConfiguredMCPServerForTab(tab, name)
		if err == nil {
			a.mu.Lock()
			delete(tab.disabledMCP, name)
			a.mu.Unlock()
			return nil
		}
		var rollbackErr error
		if previousFound {
			rollbackErr = activationStore.SetServerEnabled(configuredEntry, root, previousEnabled)
		} else {
			rollbackErr = activationStore.ClearServer(configuredEntry, root)
		}
		return errors.Join(err, rollbackErr)
	}
	if s, ok := findMCPServerView(ctrl, name); ok {
		s.Status = "disabled"
		s.Enabled = false
		s.Error = ""
		s = finalizeServerView(s)
		a.mu.Lock()
		if tab.disabledMCP == nil {
			tab.disabledMCP = map[string]ServerView{}
		}
		tab.disabledMCP[name] = s
		tab.mcpOrder = mergeServerOrder(tab.mcpOrder, []ServerView{s})
		a.mu.Unlock()
	} else {
		s := finalizeServerView(withPluginConfig(ServerView{Name: name, Status: "disabled", Enabled: false}, configuredEntry))
		a.mu.Lock()
		if tab.disabledMCP == nil {
			tab.disabledMCP = map[string]ServerView{}
		}
		tab.disabledMCP[name] = s
		tab.mcpOrder = mergeServerOrder(tab.mcpOrder, []ServerView{s})
		a.mu.Unlock()
	}
	if hostKey != "" {
		ctrl.UnregisterMCPServerTools(name)
	} else {
		ctrl.DisconnectMCPServer(name)
	}
	return nil
}

func (a *App) registerConfiguredMCPServerForTab(tab *WorkspaceTab, name string) (int, error) {
	a.mu.RLock()
	var ctrl control.SessionAPI
	root := ""
	if tab != nil {
		ctrl = tab.Ctrl
		root = tab.WorkspaceRoot
	}
	a.mu.RUnlock()
	if ctrl == nil {
		return 0, fmt.Errorf("no active session")
	}
	cfg, err := config.LoadForRoot(root)
	if err != nil {
		return 0, err
	}
	for _, p := range cfg.Plugins {
		if p.Name == name {
			return ctrl.RegisterMCPServerOnDemand(p)
		}
	}
	return 0, fmt.Errorf("no configured MCP server named %q", name)
}

// SetMCPServerTier is kept for old desktop bindings. New config writes drop the
// retired tier field.
func (a *App) SetMCPServerTier(name, tier string) error {
	defer a.lockMCPMutation("set-tier")()

	tier = normalizeMCPTier(tier)
	tab, ctrl, root := a.activeMCPRuntime()
	if tab != nil {
		if err := rebuildControllerActiveWorkErrorFor(ctrl, "MCP server"); err != nil {
			return err
		}
	}
	updated, found, err := a.desktopMCPServerForEdit(root, name)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no configured MCP server named %q", name)
	}
	updated.Tier = tier
	if !updated.ShouldAutoStart() {
		on := true
		updated.AutoStart = &on
	}
	if err := a.saveDesktopMCPServer(root, updated); err != nil {
		return err
	}
	a.bumpExtensionGeneration()
	if tab != nil && ctrl != nil && !mcpConnected(ctrl, name) {
		if _, err := ctrl.ConnectMCPServer(updated); err != nil {
			recordMCPFailure(ctrl, updated, err)
			return nil
		}
		a.mu.Lock()
		delete(tab.disabledMCP, name)
		a.mu.Unlock()
	}
	return nil
}

func (a *App) desktopMCPServerForEdit(root, name string) (config.PluginEntry, bool, error) {
	// Edit the same effective declaration the runtime selected. The entry's
	// provenance is retained so saveDesktopMCPServer writes it back to the
	// owning project/global file instead of promoting it across scopes.
	return desktopEffectiveMCPServer(root, name)
}

// desktopEffectiveMCPServer returns the same merged entry the runtime starts.
// Its provenance identifies the exact project or global declaration that edit
// and remove operations must mutate.
func desktopEffectiveMCPServer(root, name string) (config.PluginEntry, bool, error) {
	cfg, err := config.LoadForRoot(root)
	if err != nil {
		return config.PluginEntry{}, false, err
	}
	p, ok := findPluginEntry(cfg.Plugins, name)
	return p, ok, nil
}

func (a *App) saveDesktopMCPServer(root string, entry config.PluginEntry) error {
	if err := ensureMCPServerDirectlyWritable(root, entry.Name); err != nil {
		return err
	}
	_, err := config.UpsertPluginInSourceForRoot(root, entry)
	return err
}

func ensureMCPServerDirectlyWritable(root, name string) error {
	cfg, err := config.LoadForRoot(root)
	if err != nil {
		return err
	}
	if owner, ok := cfg.PluginPackageOwner(name); ok {
		return fmt.Errorf("MCP server %q is managed by plugin %q; disable or remove the plugin instead", name, owner)
	}
	return nil
}

func (a *App) removeDesktopMCPServer(root, name string) (bool, error) {
	_, removed, _, err := config.RemovePluginFromEffectiveSourceForRoot(root, name)
	return removed, err
}

func findPluginEntry(entries []config.PluginEntry, name string) (config.PluginEntry, bool) {
	for _, p := range entries {
		if p.Name == name {
			return p, true
		}
	}
	return config.PluginEntry{}, false
}

func normalizeMCPTier(tier string) string {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "eager":
		return "eager"
	case "background", "lazy":
		return "background"
	case "":
		return "background"
	default:
		return "background"
	}
}

func normalizeMCPTransport(transport string) string {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "http", "streamable-http":
		return "http"
	case "sse":
		return "sse"
	case "", "stdio":
		return "stdio"
	default:
		return strings.ToLower(strings.TrimSpace(transport))
	}
}

func mcpIntValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func cloneStringIntMap(values map[string]int) map[string]int {
	if values == nil {
		return nil
	}
	out := make(map[string]int, len(values))
	maps.Copy(out, values)
	return out
}

func mcpConnected(ctrl control.SessionAPI, name string) bool {
	if ctrl == nil || ctrl.Host() == nil {
		return false
	}
	for _, s := range ctrl.Host().Servers() {
		if s.Name == name {
			return true
		}
	}
	return false
}

func mcpFailed(ctrl control.SessionAPI, name string) bool {
	if ctrl == nil || ctrl.Host() == nil {
		return false
	}
	for _, f := range ctrl.Host().Failures() {
		if f.Name == name {
			return true
		}
	}
	return false
}

func recordMCPFailure(ctrl control.SessionAPI, e config.PluginEntry, err error) {
	if ctrl == nil || ctrl.Host() == nil || err == nil {
		return
	}
	exp := e.ExpandedPlugin()
	ctrl.Host().RecordFailure(plugin.Spec{
		Name:    exp.Name,
		Type:    exp.Type,
		Command: exp.Command,
		Args:    exp.Args,
		Env:     exp.Env,
		URL:     exp.URL,
		Headers: exp.Headers,
	}, err)
}

func findMCPServerView(ctrl control.SessionAPI, name string) (ServerView, bool) {
	if ctrl == nil || ctrl.Host() == nil {
		return ServerView{}, false
	}
	for _, s := range ctrl.Host().Servers() {
		if s.Name == name {
			return pluginServerToView(s), true
		}
	}
	for _, f := range ctrl.Host().Failures() {
		if f.Name == name {
			return ServerView{
				Name: f.Name, Transport: f.Transport, Status: "failed", Error: f.Error,
				RequiresLaunchApproval: f.RequiresLaunchApproval,
			}, true
		}
	}
	return ServerView{}, false
}

func pluginToolsToView(tools []plugin.ToolInfo) []ToolView {
	if len(tools) == 0 {
		return []ToolView{}
	}
	out := make([]ToolView, 0, len(tools))
	for _, t := range tools {
		out = append(out, ToolView{
			Name: t.Name, Description: t.Description, ReadOnlyHint: t.ReadOnlyHint, DestructiveHint: t.DestructiveHint, SchemaError: t.SchemaError,
		})
	}
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

func orderServerViews(servers []ServerView, order []string) []ServerView {
	pos := make(map[string]int, len(order))
	for i, name := range order {
		pos[name] = i
	}
	sort.SliceStable(servers, func(i, j int) bool {
		pi, iok := pos[servers[i].Name]
		pj, jok := pos[servers[j].Name]
		switch {
		case iok && jok:
			return pi < pj
		case iok:
			return true
		case jok:
			return false
		default:
			return false
		}
	})
	return servers
}

func mergeServerOrder(order []string, servers []ServerView) []string {
	seen := make(map[string]bool, len(order)+len(servers))
	next := make([]string, 0, len(order)+len(servers))
	for _, name := range order {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		next = append(next, name)
	}
	for _, s := range servers {
		if s.Name == "" || seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		next = append(next, s.Name)
	}
	return next
}

func removeServerOrder(order []string, name string) []string {
	if name == "" || len(order) == 0 {
		return order
	}
	next := order[:0]
	for _, n := range order {
		if n != name {
			next = append(next, n)
		}
	}
	return next
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
	// model with no runtime rebuild. An active turn is no longer a rejection
	// reason — the in-flight request stays frozen and the override lands from
	// the next request freeze — while the build+swap path below keeps its
	// active-work guard for everything that declines here (no controller,
	// cross-provider targets, recovery-forked sessions).
	stageStarted = time.Now()
	switchSnap := a.tabRuntimeSnapshot(tab)
	switchRef, _, _, err := a.resolveTabModelRef(tab, switchSnap.workspaceRoot, name)
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
		if setter, ok := ctrl.(interface {
			SetSessionModelOverride(string) bool
		}); ok && setter.SetSessionModelOverride(switchRef) {
			a.mu.Lock()
			tab.model = switchRef
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
			slog.Info("desktop: model switch", "tab", tabID, "path", "fast-per-request", "model", switchRef) // task 148: fast path observability (paired against runtime build end path=fallback).
			return nil
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
// (a full config disk load plus session-binding reconcile per call) followed
// by capability mapping. Task 421 bounds it behind EffortForTab in
// effort_fetch.go; binding surfaces must go through EffortForTab so a stalled
// read cannot hang a tab switch.
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

func trimUTF8PartialSuffix(data []byte) []byte {
	if utf8.Valid(data) {
		return data
	}
	for i := len(data) - 1; i >= 0 && len(data)-i <= utf8.UTFMax; i-- {
		if !utf8.RuneStart(data[i]) {
			continue
		}
		if !utf8.Valid(data[:i]) || utf8.FullRune(data[i:]) {
			return data
		}
		return data[:i]
	}
	return data
}

func previewMediaKind(path string) (kind string, mime string) {
	mime = previewMediaMIMEs[strings.ToLower(filepath.Ext(path))]
	if mime == "" {
		return "", ""
	}
	if strings.HasPrefix(mime, "image/") {
		return "image", mime
	}
	if mime == "application/pdf" {
		return "pdf", mime
	}
	return "", ""
}

func workspaceEntryRel(rel, name string) string {
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	if rel == "" || rel == "." {
		return name
	}
	return rel + "/" + name
}

func skipWorkspaceEntry(rel, name string, isDir bool) bool {
	return fileref.SkipEntryForPanel(workspaceEntryRel(rel, name), name, isDir)
}

func (a *App) activeWorkspaceBase() (string, error) {
	return workspaceBaseFromRoot(a.activeWorkspaceRoot())
}

func (a *App) workspaceTargetForTab(tabID string) (string, control.SessionAPI, bool) {
	tabID = strings.TrimSpace(tabID)
	a.mu.RLock()
	defer a.mu.RUnlock()
	tab := a.tabByIDLocked(tabID)
	if tab == nil {
		if tabID == "" {
			return ".", nil, true
		}
		return "", nil, false
	}
	return tab.WorkspaceRoot, tab.Ctrl, true
}

func workspaceBaseFromRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" || root == "." {
		return os.Getwd()
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return filepath.Clean(root), nil
}

func workspacePathForBase(base, rel string) (string, bool, error) {
	base = filepath.Clean(base)
	if rel == "" {
		return "", false, os.ErrInvalid
	}
	path := rel
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, rel)
	}
	path = filepath.Clean(path)
	r, err := filepath.Rel(base, path)
	if err != nil {
		return "", false, err
	}
	if r == ".." || strings.HasPrefix(r, ".."+string(os.PathSeparator)) {
		return "", false, os.ErrPermission
	}
	return path, true, nil
}

// ListDir lists one directory level (directories first, then files, each
// alphabetical) for the "@" file-reference menu. rel resolves against the active
// tab workspace. The menu navigates one level at a time, never recursively —
// bounded for huge trees.
func (a *App) ListDir(rel string) []DirEntry {
	return a.ListDirForTab("", rel)
}

// ListDirForTab is the tab-scoped variant used by multi-tab frontend surfaces.
func (a *App) ListDirForTab(tabID, rel string) []DirEntry {
	root, ctrl, ok := a.workspaceTargetForTab(tabID)
	if !ok {
		return []DirEntry{}
	}
	if browser := externalFolderRefBrowserFromController(ctrl); browser != nil {
		if entries, handled := browser.ListExternalFolderRefDir(rel); handled {
			return externalFolderDirEntries(entries)
		}
	}
	base, err := workspaceBaseFromRoot(root)
	if err != nil {
		return []DirEntry{}
	}
	dir := base
	if rel != "" {
		path, ok, err := workspacePathForBase(base, rel)
		if err != nil || !ok {
			return []DirEntry{}
		}
		dir = path
	}
	es, err := os.ReadDir(dir)
	if err != nil {
		return []DirEntry{}
	}
	dirs, files := []DirEntry{}, []DirEntry{}
	for _, e := range es {
		name := e.Name()
		if skipWorkspaceEntry(rel, name, e.IsDir()) {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, DirEntry{Name: name, IsDir: true})
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, DirEntry{Name: name, IsDir: false})
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name) })
	return append(dirs, files...)
}

// SearchFileRefs finds workspace files by basename for bare "@token" completion.
func (a *App) SearchFileRefs(query string) []DirEntry {
	return a.SearchFileRefsForTab("", query)
}

// SearchFileRefsForTab is the tab-scoped variant used by multi-tab frontend surfaces.
func (a *App) SearchFileRefsForTab(tabID, query string) []DirEntry {
	root, ctrl, ok := a.workspaceTargetForTab(tabID)
	if !ok {
		return []DirEntry{}
	}
	base, err := workspaceBaseFromRoot(root)
	if err != nil {
		return []DirEntry{}
	}
	results := fileref.Search(base, query, fileRefSearchLimit)
	out := make([]DirEntry, 0, len(results))
	for _, r := range results {
		out = append(out, DirEntry{Name: r.Path, IsDir: r.IsDir})
	}
	if browser := externalFolderRefBrowserFromController(ctrl); browser != nil {
		out = append(out, externalFolderDirEntries(browser.SearchExternalFolderRefs(query, fileRefSearchLimit))...)
	}
	return out
}

type externalFolderRefBrowser interface {
	ListExternalFolderRefDir(tokenPath string) ([]control.ExternalFolderRefEntry, bool)
	SearchExternalFolderRefs(query string, limit int) []control.ExternalFolderRefEntry
	ExternalFolderRefLocalPath(tokenPath string) (path, displayPath string, ok bool)
}

func externalFolderRefBrowserFromController(ctrl control.SessionAPI) externalFolderRefBrowser {
	if browser, ok := ctrl.(externalFolderRefBrowser); ok {
		return browser
	}
	return nil
}

func externalFolderDirEntries(entries []control.ExternalFolderRefEntry) []DirEntry {
	out := make([]DirEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, DirEntry{
			Name:        e.Name,
			Path:        e.Path,
			IsDir:       e.IsDir,
			DisplayName: e.DisplayName,
			DisplayPath: e.DisplayPath,
		})
	}
	return out
}

func (a *App) workspaceOrExternalPathForTab(tabID, rel string) (string, bool, error) {
	root, ctrl, ok := a.workspaceTargetForTab(tabID)
	if !ok {
		return "", false, os.ErrNotExist
	}
	if browser := externalFolderRefBrowserFromController(ctrl); browser != nil {
		if path, _, ok := browser.ExternalFolderRefLocalPath(rel); ok {
			return path, true, nil
		}
	}
	base, err := workspaceBaseFromRoot(root)
	if err != nil {
		return "", false, err
	}
	return workspacePathForBase(base, rel)
}

// ReadFile returns a small text preview for a file under the current workspace
// or a session-authorized external folder ref.
func (a *App) ReadFile(rel string) FilePreview {
	return a.ReadFileForTab("", rel)
}

// ReadFileForTab returns a preview resolved against the requested tab.
func (a *App) ReadFileForTab(tabID, rel string) FilePreview {
	out := FilePreview{Path: rel}
	path, ok, err := a.workspaceOrExternalPathForTab(tabID, rel)
	if err != nil || !ok {
		out.Err = "invalid path"
		return out
	}
	info, err := os.Stat(path)
	if err != nil {
		out.Err = err.Error()
		return out
	}
	if info.IsDir() {
		out.Err = "path is a directory"
		return out
	}
	if !info.Mode().IsRegular() {
		out.Err = "path is not a regular file"
		return out
	}
	out.Size = info.Size()
	if kind, mime := previewMediaKind(path); kind != "" {
		token := a.ensureMediaTokenStore().create(path, info.Name(), mime, kind, info.Size(), info.ModTime())
		out.Kind = kind
		out.Mime = mime
		out.URL = "/__reasonix_workspace_media/" + token + "/" + url.PathEscape(info.Name())
		return out
	}
	f, err := os.Open(path)
	if err != nil {
		out.Err = err.Error()
		return out
	}
	defer f.Close()

	buf := make([]byte, filePreviewLimit+1)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		out.Err = err.Error()
		return out
	}
	data := buf[:n]
	if len(data) > filePreviewLimit {
		data = data[:filePreviewLimit]
		out.Truncated = true
	}

	// Check for BOM first (just the first 2-3 bytes — always complete
	// even at a truncation boundary). BOM-prefixed files skip the NUL
	// check since UTF-16 normally contains 0x00 for ASCII characters.
	bomKind := fileenc.DetectQuick(data)
	if bomKind != fileenc.UTF8 {
		enc, _ := fileenc.Detect(data)
		if enc == fileenc.LossyUTF8 {
			out.Binary = true
			return out
		}
		decoded := fileenc.Decode(data, enc)
		out.Body = string(decoded)
		return out
	}

	// No BOM — NUL in raw bytes is a binary signal.
	if bytes.Contains(data, []byte{0}) {
		out.Binary = true
		return out
	}

	// Trim any partial multi-byte rune at the truncation boundary BEFORE
	// encoding detection. Without this, a large UTF-8 file truncated
	// mid-character would fail utf8.Valid and be misdetected as GB18030
	// or LossyUTF8, producing mojibake or a false binary classification.
	if out.Truncated {
		data = trimUTF8PartialSuffix(data)
	}
	enc, _ := fileenc.Detect(data)
	if enc == fileenc.LossyUTF8 {
		out.Binary = true
		return out
	}
	out.Body = string(fileenc.Decode(data, enc))
	return out
}

// OpenWorkspacePath opens a workspace or authorized external-ref file/folder in
// the OS default app.
func (a *App) OpenWorkspacePath(rel string) error {
	return a.OpenWorkspacePathForTab("", rel)
}

// OpenWorkspacePathForTab opens a path resolved against the requested tab.
func (a *App) OpenWorkspacePathForTab(tabID, rel string) error {
	path, ok, err := a.workspaceOrExternalPathForTab(tabID, rel)
	if err != nil || !ok {
		return os.ErrInvalid
	}
	return openWorkspacePath(path)
}

// RevealWorkspacePath shows a workspace or authorized external-ref file in the
// native file manager.
func (a *App) RevealWorkspacePath(rel string) error {
	return a.RevealWorkspacePathForTab("", rel)
}

// RevealWorkspacePathForTab reveals a path resolved against the requested tab.
func (a *App) RevealWorkspacePathForTab(tabID, rel string) error {
	path, ok, err := a.workspaceOrExternalPathForTab(tabID, rel)
	if err != nil || !ok {
		return os.ErrInvalid
	}
	return revealPath(path)
}

// RevealPath shows an arbitrary absolute path in the native file manager.
func (a *App) RevealPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return os.ErrInvalid
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return revealPath(path)
}

var revealPath = defaultRevealPath

func defaultRevealPath(path string) error {
	switch goruntime.GOOS {
	case "darwin":
		return proc.VisibleCommand("open", "-R", path).Start()
	case "windows":
		// explorer.exe lives in %SystemRoot%, which isn't always on PATH (the
		// launch environment can strip it), so resolve it directly rather than
		// relying on a PATH lookup.
		explorer := "explorer.exe"
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = os.Getenv("windir")
		}
		if root != "" {
			explorer = filepath.Join(root, "explorer.exe")
		}
		return proc.VisibleCommand(explorer, "/select,", path).Start()
	default:
		dir := path
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			dir = filepath.Dir(path)
		}
		return proc.VisibleCommand("xdg-open", dir).Start()
	}
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
	if tab := a.tabByID(tabID); tab != nil {
		a.reconcileTabWithPinnedSessionMeta(tab)
	}
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
	cfg, err := config.LoadForRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(ref) == "" {
		ref = cfg.DefaultModel
	}
	config.NormalizeLegacyMimoCustomProvidersForRefs(cfg, ref)
	resolved, _, ok := cfg.ResolveModelWithFallback(ref)
	if !ok {
		return nil, fmt.Errorf("unknown model %q", ref)
	}
	entry, ok := cfg.ResolveModel(resolved)
	if !ok {
		return nil, fmt.Errorf("unknown model %q", resolved)
	}
	if effortOverride != nil {
		entry.Effort = *effortOverride
	}
	return entry, nil
}

func (a *App) withActiveWorkspace(fn func() (string, error)) (string, error) {
	var result string
	err := a.withActiveWorkspaceDo(func() error {
		var err error
		result, err = fn()
		return err
	})
	return result, err
}

func (a *App) withActiveWorkspaceDo(fn func() error) error {
	root := a.activeWorkspaceRoot()
	if root != "" && root != "." {
		prev, err := os.Getwd()
		if err != nil {
			return err
		}
		if err := os.Chdir(root); err != nil {
			return err
		}
		defer func() { _ = os.Chdir(prev) }()
	}
	return fn()
}

// SavePastedImage stores a browser clipboard image data URL under the active
// tab's workspace .reasonix/attachments and returns the relative @-reference path.
func (a *App) SavePastedImage(dataURL string) (string, error) {
	return a.withActiveWorkspace(func() (string, error) {
		return control.SaveImageDataURLIn(a.activeWorkspaceRoot(), dataURL)
	})
}

// SaveClipboardImage reads the native OS clipboard image under the active tab's
// workspace .reasonix/attachments and returns the relative @-reference path.
func (a *App) SaveClipboardImage() (string, error) {
	return a.withActiveWorkspace(control.SaveClipboardImage)
}

// SavePastedFile stores a dropped non-image file (the browser exposes its bytes
// as a data URL but not a real path) under the active tab's workspace
// .reasonix/attachments and returns the relative @-reference path.
func (a *App) SavePastedFile(name, dataURL string) (string, error) {
	return a.withActiveWorkspace(func() (string, error) {
		return control.SaveAttachmentDataURL(name, dataURL)
	})
}

// PickExportFile opens the native save dialog and returns the selected path. It
// returns "" when the user cancels.
func (a *App) PickExportFile(defaultFilename, mimeType string) (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	defaultFilename = safeExportFilename(defaultFilename)
	ext := strings.ToLower(filepath.Ext(defaultFilename))
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:                "Export session",
		DefaultDirectory:     dialogDefaultDirectory(a.activeWorkspaceRoot()),
		DefaultFilename:      defaultFilename,
		CanCreateDirectories: true,
		Filters:              exportFileFilters(mimeType, ext),
	})
	if err != nil || path == "" {
		return "", err
	}
	if ext != "" && filepath.Ext(path) == "" {
		path += ext
	}
	return path, nil
}

// SaveExportFile writes an exported session payload to a path previously picked
// by PickExportFile. An empty path is treated as a cancelled export.
func (a *App) SaveExportFile(path, payload string, base64Encoded bool) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	var data []byte
	var err error
	if base64Encoded {
		data, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return fmt.Errorf("decode export payload: %w", err)
		}
	} else {
		data = []byte(payload)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return exportOperationError("save export file", path, err)
	}
	return nil
}

// SaveExportImageFiles writes one or more base64-encoded image parts. A single
// image keeps the native save dialog's normal overwrite semantics. Multi-part
// exports use numbered sibling paths and never overwrite an existing sibling;
// every payload is staged before any target is committed, and a failed commit
// removes only files created by this call.
func (a *App) SaveExportImageFiles(path string, payloads []string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if len(payloads) == 0 {
		return errors.New("no image payloads to export")
	}
	if len(payloads) == 1 {
		return a.SaveExportFile(path, payloads[0], true)
	}

	targets := make([]string, len(payloads))
	for i := range payloads {
		targets[i] = numberedExportPath(path, i, len(payloads))
	}

	return saveExclusiveExportPayloads(targets, len(payloads), func(index int) ([]byte, error) {
		decoded, err := base64.StdEncoding.DecodeString(payloads[index])
		if err != nil {
			return nil, fmt.Errorf("decode export image part %d: %w", index+1, err)
		}
		return decoded, nil
	})
}

type stagedExportFile struct {
	targetPath string
	tempPath   string
}

type committedExportFile struct {
	path string
	info os.FileInfo
}

const exportTempCreateAttempts = 100

func numberedExportPath(path string, partIndex, partCount int) string {
	if partCount <= 1 {
		return path
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	return fmt.Sprintf("%s-%d-of-%d%s", stem, partIndex+1, partCount, ext)
}

func saveExclusiveExportFiles(targets []string, payloads [][]byte) error {
	return saveExclusiveExportPayloads(targets, len(payloads), func(index int) ([]byte, error) {
		return payloads[index], nil
	})
}

func saveExclusiveExportPayloads(targets []string, payloadCount int, payloadAt func(int) ([]byte, error)) error {
	if len(targets) == 0 || len(targets) != payloadCount || payloadAt == nil {
		return errors.New("invalid export image batch")
	}
	for _, target := range targets {
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("export file already exists: %s", filepath.Base(target))
		} else if !errors.Is(err, os.ErrNotExist) {
			return exportOperationError("inspect export target", target, err)
		}
	}

	staged := make([]stagedExportFile, 0, len(targets))
	defer func() {
		for _, file := range staged {
			_ = os.Remove(file.tempPath)
		}
	}()
	for i, target := range targets {
		payload, err := payloadAt(i)
		if err != nil {
			return err
		}
		file, finalMode, err := createExportTempFile(filepath.Dir(target))
		if err != nil {
			return exportOperationError("stage export file", target, err)
		}
		tempPath := file.Name()
		staged = append(staged, stagedExportFile{targetPath: target, tempPath: tempPath})
		if _, err = file.Write(payload); err == nil {
			err = file.Sync()
		}
		// Keep staged payloads private while they are incomplete, then restore
		// the same umask-adjusted mode used by SaveExportFile before publishing.
		if err == nil {
			err = file.Chmod(finalMode)
		}
		if err == nil {
			err = file.Sync()
		}
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return exportOperationError("stage export file", target, err)
		}
	}

	committed := make([]committedExportFile, 0, len(staged))
	for _, file := range staged {
		info, err := commitStagedExportFile(file.tempPath, file.targetPath)
		if err != nil {
			rollbackCommittedExportFiles(committed)
			return exportOperationError("save export file", file.targetPath, err)
		}
		committed = append(committed, committedExportFile{path: file.targetPath, info: info})
	}
	return nil
}

// createExportTempFile reserves a cryptographically random sibling path with
// the same requested mode as a normal export. It immediately narrows the mode
// while bytes are staged; the caller restores finalMode only after the payload
// has been completely written and synced.
func createExportTempFile(dir string) (*os.File, os.FileMode, error) {
	for range exportTempCreateAttempts {
		var suffix [12]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return nil, 0, fmt.Errorf("generate export temp name: %w", err)
		}
		path := filepath.Join(dir, ".reasonix-export-"+hex.EncodeToString(suffix[:]))
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		info, err := file.Stat()
		if err == nil {
			err = file.Chmod(0o600)
		}
		if err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return nil, 0, err
		}
		return file, info.Mode().Perm(), nil
	}
	return nil, 0, errors.New("could not reserve a unique export temp file")
}

func commitStagedExportFile(tempPath, targetPath string) (os.FileInfo, error) {
	stagedInfo, err := os.Lstat(tempPath)
	if err != nil {
		return nil, err
	}
	// A hard link publishes a fully written staged file atomically and fails if
	// the target already exists. Some filesystems do not support hard links, so
	// fall back to an exclusive create while preserving the no-overwrite rule.
	if err := os.Link(tempPath, targetPath); err == nil {
		current, statErr := os.Lstat(targetPath)
		if statErr != nil {
			removeExportFileIfSame(targetPath, stagedInfo)
			return nil, statErr
		}
		if !os.SameFile(current, stagedInfo) {
			return nil, errors.New("export target changed while it was being saved")
		}
		return stagedInfo, nil
	}

	source, err := os.Open(tempPath)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	info, statErr := target.Stat()
	if statErr == nil {
		_, err = io.Copy(target, source)
	}
	if err == nil && statErr == nil {
		err = target.Sync()
	}
	if closeErr := target.Close(); err == nil && statErr == nil {
		err = closeErr
	}
	if statErr != nil {
		err = statErr
	}
	if err != nil {
		removeExportFileIfSame(targetPath, info)
		return nil, err
	}
	return info, nil
}

func rollbackCommittedExportFiles(files []committedExportFile) {
	for _, file := range files {
		removeExportFileIfSame(file.path, file.info)
	}
}

func removeExportFileIfSame(path string, created os.FileInfo) {
	if created == nil {
		return
	}
	current, err := os.Lstat(path)
	if err == nil && os.SameFile(current, created) {
		_ = os.Remove(path)
	}
}

func exportOperationError(operation, path string, err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return fmt.Errorf("%s %s: %w", operation, filepath.Base(path), pathErr.Err)
	}
	return fmt.Errorf("%s %s: %w", operation, filepath.Base(path), err)
}

func safeExportFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "reasonix-session.md"
	}
	return filepath.Base(name)
}

func exportFileFilters(mimeType, ext string) []runtime.FileFilter {
	switch mimeType {
	case "text/markdown":
		return []runtime.FileFilter{{DisplayName: "Markdown (*.md)", Pattern: "*.md"}}
	case "application/json":
		return []runtime.FileFilter{{DisplayName: "JSON (*.json)", Pattern: "*.json"}}
	case "application/pdf":
		return []runtime.FileFilter{{DisplayName: "PDF (*.pdf)", Pattern: "*.pdf"}}
	case "image/png":
		return []runtime.FileFilter{{DisplayName: "PNG image (*.png)", Pattern: "*.png"}}
	}
	if ext != "" {
		return []runtime.FileFilter{{DisplayName: strings.ToUpper(strings.TrimPrefix(ext, ".")) + " files (*" + ext + ")", Pattern: "*" + ext}}
	}
	return []runtime.FileFilter{{DisplayName: "All files (*.*)", Pattern: "*.*"}}
}

// AttachmentDataURL returns a safe data URL for a stored image attachment.
func (a *App) AttachmentDataURL(path string) (string, error) {
	return a.withActiveWorkspace(func() (string, error) {
		return control.ImageDataURLIn(a.activeWorkspaceRoot(), path)
	})
}

// DroppedItem is one OS-dropped file resolved into a composer context entry: an
// in-tree file becomes a workspace @reference (read in place, no copy), while an
// outside directory becomes a session-scoped workspace @reference; an image or
// out-of-tree file is copied into .reasonix/attachments.
type DroppedItem struct {
	Kind        string `json:"kind"` // "workspace" | "attachment"
	Path        string `json:"path"`
	IsDir       bool   `json:"isDir,omitempty"`
	DisplayPath string `json:"displayPath,omitempty"`
	PreviewURL  string `json:"previewUrl,omitempty"`
}

// AttachDropped turns an absolute path from the native file-drop bridge into a
// composer context entry. Images are stored as attachments so the chip shows a
// thumbnail; in-workspace files are referenced relatively (no copy); directories
// outside the workspace are registered as current-session folder references;
// files outside the workspace are copied into .reasonix/attachments.
func (a *App) AttachDropped(path string) (DroppedItem, error) {
	var item DroppedItem
	err := a.withActiveWorkspaceDo(func() error {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if isImageExt(path) {
			if rel, err := control.SaveImageFileIn(a.activeWorkspaceRoot(), path); err == nil {
				preview, _ := control.ImageDataURLIn(a.activeWorkspaceRoot(), rel)
				item = DroppedItem{Kind: "attachment", Path: rel, PreviewURL: preview}
				return nil
			}
		}
		if rel, ok := workspaceRelativeIn(path, a.activeWorkspaceRoot()); ok {
			item = DroppedItem{Kind: "workspace", Path: rel, IsDir: info.IsDir()}
			return nil
		}
		if info.IsDir() {
			tab, ctrl := a.tabAndCtrlByID("")
			if err := a.ensureTabControllerWorkspace(tab); err != nil {
				return err
			}
			if tab != nil {
				ctrl = a.controllerForTab(tab)
			}
			if ctrl == nil {
				return fmt.Errorf("workspace is not ready")
			}
			token, displayPath, err := ctrl.RegisterExternalFolderRef(path)
			if err != nil {
				return err
			}
			item = DroppedItem{Kind: "workspace", Path: token, IsDir: true, DisplayPath: displayPath}
			return nil
		}
		rel, err := control.SaveAttachmentFileIn(a.activeWorkspaceRoot(), path)
		if err != nil {
			return err
		}
		item = DroppedItem{Kind: "attachment", Path: rel}
		return nil
	})
	if err != nil {
		return DroppedItem{}, err
	}
	return item, nil
}

func isImageExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

func workspaceRelativeIn(path, workspaceRoot string) (string, bool) {
	root := workspaceRoot
	if !filepath.IsAbs(root) {
		abs, err := filepath.Abs(root)
		if err != nil {
			return "", false
		}
		root = abs
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
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
