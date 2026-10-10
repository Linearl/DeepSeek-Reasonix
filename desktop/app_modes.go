package main

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// sessionTempFromController returns the logical-session private temporary
// directory manager for a same-session controller rebuild. Nil when the
// controller is missing or is not a *control.Controller.
// App is the Wails-bound application object: the desktop frontend's command

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
	// Task 465 two-axis matrix: plan/goal/normal are the SECOND axis and no
	// longer touch the first (approval posture + autopilot flag) — the axes
	// are independent, so a dim-2 switch seeds the autopilot fields from the
	// tab's current values; only the "autopilot" tier and the 325 linkage
	// rewrite them.
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
			// legal product state (the goal guard works unattended too); approval
			// is pinned to yolo (autopilot implies yolo).
			tab.mode = tabModeFromAxes(tabModeHasPlan(tab.mode), true)
			tab.toolApprovalMode = control.ToolApprovalYolo
		} else {
			// Preference off: leave the posture untouched and drop back out of
			// the tier (bounds stay zeroed — an unbounded run is never implicit).
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
	a.applyCollabModeEffects(tab, tabIDForSave, ctrl, plan, goal, autopilotOn, wasAutopilot, autopilotRefused, assumedYolo, guardOwner)
}

// applyCollabModeEffects fires the post-lock autopilot guard edges, the
// user-visible notices, and the controller posture apply for a
// collaboration-mode switch. Callers have released a.mu.
func (a *App) applyCollabModeEffects(tab *WorkspaceTab, tabIDForSave string, ctrl control.SessionAPI, plan bool, goal string, autopilotOn, wasAutopilot, autopilotRefused, assumedYolo bool, guardOwner autopilotGuardOwner) {
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
		// slog line above is the desktop.log half). The live controller must
		// carry the posture the tab now promises, or the current turn keeps
		// stacking ask prompts under an unattended flag; drained ids have no
		// return channel on this void wire call and plan/sandbox-escape cards
		// never drain under yolo (#6432).
		a.noticeCodeForTab(tabIDForSave, event.LevelInfo, NoticeCodeAutopilotAssumedYolo, autopilotAssumedYoloText)
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
	// session metadata repair the index later instead of surfacing a false
	// "new session failed" error (task 550 ①: logged with the topic identity
	// and counted, not silent).
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
