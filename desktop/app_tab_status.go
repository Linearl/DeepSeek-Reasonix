package main

import (
	"os"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/billing"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/evidence"
)

// tabAndCtrlForUsage resolves the tab/controller pair for the gauge reads.
// tl1500 刀①: the active-tab cases (empty tabID = ContextUsage, or the
// explicit current-tab ID the frontend poll sends) answer from the binding
// snapshot, so a write-lock holder cannot queue the 60s usage poll and the
// right-rail gauge behind the save family (诊断报告 §2.3: 指标全「-」的机理).
// Other explicit tab IDs keep the old blocking read — non-active-tab status
// reads are not the convoy surface.
func (a *App) tabAndCtrlForUsage(tabID string) (*WorkspaceTab, control.SessionAPI) {
	snap, _ := a.bindingSnapshotNow()
	if snap != nil && snap.present {
		if tabID == "" || snap.activeTabID == tabID {
			return snap.tab, snap.ctrl
		}
	}
	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	var ctrl control.SessionAPI
	if tab != nil {
		ctrl = tab.Ctrl
	}
	a.mu.RUnlock()
	return tab, ctrl
}

// ContextUsage returns the latest context-window gauge numbers.
func (a *App) ContextUsage() ContextInfo {
	return a.ContextUsageForTab("")
}

func (a *App) ContextUsageForTab(tabID string) ContextInfo {
	tab, ctrl := a.tabAndCtrlForUsage(tabID)
	var info ContextInfo
	var snap tabTelemetrySnapshot
	if tab != nil {
		// Re-key first: a controller-side rotation (typed /new) may have
		// swapped sessions without the App noticing, and the stale totals
		// would otherwise be reported — and then persisted — under the new session (#5850).
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
	// Task 882: the controller-derived fields run bounded (2s cap + cached
	// fallback, see context_usage_fetch.go) - this binding is the "ancillary
	// context" leg of the switch-tab batch, and its O(transcript) recompute
	// used to hold the whole switch for seconds on long sessions.
	compute := a.boundedContextUsageCompute(tabID, ctrl)
	info.Used = compute.used
	info.Window = compute.window
	info.CompactRatio = compute.compactRatio
	info.Maintenance = compute.maintenance
	info.ContextBudget = compute.contextBudget
	return info
}

// BalanceInfo is the wallet-balance readout for the status bar. Available is true
// only when a balance was fetched; Display is the exact formatted amount (e.g.
// "¥110.00") and is "" when the active provider declares no balance_url — the frontend then omits the readout. Err carries a fetch failure for an optional tooltip. Wallet balances are displayed in their original currencies; no conversion or cross-currency sum is performed.
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
// controller is down, or the fetch fails — so the status bar simply shows nothing rather than an error.
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
	// SessionGeneration mirrors tab.SessionGeneration on the "tab:meta" push
	// (tl1403): a recovery fork/promote moves the controller onto a new session
	// file, and the pushed meta carries the bumped generation as the deliberate
	// surface-move marker — the frontend accepts a path-changing push only when
	// this generation strictly advances, so stale pushes from the outgoing
	// session can never clobber a settled tab. Additive omitempty field.
	SessionGeneration    uint64             `json:"sessionGeneration,omitempty"`
	Cwd                   string             `json:"cwd"`
	WorkspaceRoot         string             `json:"workspaceRoot,omitempty"`
	WorkspaceName         string             `json:"workspaceName,omitempty"`
	WorkspacePath         string             `json:"workspacePath,omitempty"`
	GitBranch             string             `json:"gitBranch,omitempty"`
	ImageInputEnabled     bool               `json:"imageInputEnabled"`
	AudioInputEnabled     bool               `json:"audioInputEnabled"` // task 1008: audio attachment entry gate
	VisionFallbackEnabled bool               `json:"visionFallbackEnabled,omitempty"`
	AutoApproveTools      bool               `json:"autoApproveTools"`
	Bypass                bool               `json:"bypass"` // legacy JSON key for YOLO/full-access tool auto-approval
	CollaborationMode     string             `json:"collaborationMode"`
	ToolApprovalMode      string             `json:"toolApprovalMode"`
	// Autopilot is the RAW first-axis flag (task 465 two-axis matrix). The
	// synthesized CollaborationMode label orders plan>goal>autopilot, so a
	// goal × autopilot tab reports "goal" and the composer could no longer see the unattended state it must show; the raw flag keeps both axes visible at once.
	Autopilot      bool   `json:"autopilot,omitempty"`
	SubagentPolicy string `json:"subagentPolicy,omitempty"`
	// TokenMode and AgentPreset are deprecated dual-write wire values pinned to
	// their safe defaults; one-version-old frontends still parse them.
	TokenMode   string           `json:"tokenMode"`
	AgentPreset string           `json:"agentPreset,omitempty"`
	Goal        string           `json:"goal,omitempty"`
	GoalStatus  string           `json:"goalStatus,omitempty"`
	GoalRuntime *GoalRuntimeView `json:"goalRuntime,omitempty"`
	// GoalObject (tl1472) is the objectified goal's visibility carrier: the
	// tidied summary plus phase AND activation together (only showing the
	// running half would replicate the 1459 mirror incident). Nil when the
	// session holds no goal.
	GoalObject *GoalObjectWire `json:"goalObject,omitempty"`
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

// GoalObjectWire is the desktop wire form of the tl1472 goal object view.
type GoalObjectWire struct {
	Goal       string `json:"goal,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Phase      string `json:"phase"`
	Activation string `json:"activation"`
	StopCause  string `json:"stopCause,omitempty"`
}

// goalObjectFromController probes the optional tl1472 object port so builds
// whose controller predates the objectification keep compiling (the same
// capability-probe shape as ImageInputSnapshot above). Present only when a
// goal object actually exists, so the sidebar card stays hidden otherwise.
func goalObjectFromController(ctrl control.SessionAPI) *GoalObjectWire {
	if ctrl == nil {
		return nil
	}
	probe, ok := ctrl.(interface{ GoalObject() control.GoalObjectView })
	if !ok {
		return nil
	}
	view := probe.GoalObject()
	if view.Goal == "" && view.Phase == "" {
		return nil
	}
	return &GoalObjectWire{
		Goal:       view.Goal,
		Summary:    view.Summary,
		Phase:      view.Phase,
		Activation: view.Activation,
		StopCause:  view.StopCause,
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
	// tl 1266：会话实体查无（恢复标记×实时 Stat / 清理待执行）时把断开投影进
	// runtime 视图——前端 runtimeReadyForSubmit 据此拒绝发送并原样抛出断开
	// 文案（方向③的前端面），下方 Ready 一并翻假。读取型投影，注册表不动，
	// 实体回来即自愈。
	disconnected := tabSessionDisconnected(tab)
	a.mu.RUnlock()
	if tab == nil {
		meta := Meta{EventChannel: eventChannel}
		if ref, ok := a.remoteTabRefFor(tabID); ok {
			meta.Remote = &ref
		}
		return meta
	}
	if disconnected {
		runtimeView = SessionRuntimeView{
			Phase: sessionRuntimeFailed,
			Epoch: runtimeView.Epoch,
			Issue: sessionMissingRuntimeIssue(),
		}
	}
	cwd := snap.workspaceRoot
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	// Git branch and image-input capability come from the per-tab cache
	// refreshed in the background (refreshTabMetaExtras); computing them here
	// put a config load + model resolution on every meta request. A miss or stale entry schedules a refresh and serves the last known values (empty on the very first call; the "tab:meta" event delivers the refresh).
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
		SessionGeneration:     tab.SessionGeneration,
		Cwd:                   cwd,
		WorkspaceRoot:         cwd,
		WorkspaceName:         tabWorkspaceNameForScope(snap.scope, cwd),
		WorkspacePath:         cwd,
		GitBranch:             extras.gitBranch,
		ImageInputEnabled:     extras.imageInputEnabled,
		AudioInputEnabled:     extras.audioInputEnabled,
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
		GoalObject:            goalObjectFromController(snap.ctrl),
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
// Failures must return error so the Wails Promise rejects: the first Goal turn can submit a structured Skill without a /goal prose fallback, and the frontend aborts that submit when activation fails.
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
// its delivery scope; a terminal Goal with the same text still starts a fresh scope when the user explicitly enters it again.
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
