package main

import (
	"errors"
	"fmt"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
)

// bound command surface (frontend → controller)
// Each method guards on a nil controller so a pre-startup or failed-build call is
// a no-op, never a panic.

// Submit runs raw user input as a turn; slash commands and @-references are
// resolved by the controller. Output arrives asynchronously on eventChannel.
func (a *App) Submit(input string) error {
	return a.SubmitToTab("", input)
}

var errEmptyTurnInput = errors.New("message cannot be empty")

func validateTurnInput(input string) error {
	if strings.TrimSpace(input) == "" {
		return errEmptyTurnInput
	}
	return nil
}

func (a *App) SubmitToTab(tabID, input string) error {
	if err := validateTurnInput(input); err != nil {
		return err
	}
	return a.submitToTab(tabID, input, false)
}

// submitToTab is the shared submit body. fromBridge marks submissions driven
// by the IM takeover bridge; local (frontend) submissions on a taken-over tab
// reclaim remote control first — typing locally is the grab-back gesture.
func (a *App) submitToTab(tabID, input string, fromBridge bool, submissionID ...string) error {
	_, err := a.submitToTabResult(tabID, input, fromBridge, false, submissionID...)
	return err
}

func (a *App) submitToTabResult(tabID, input string, fromBridge, classifyManagement bool, submissionID ...string) (control.SubmitResult, error) {
	management := control.SubmitResult{Disposition: control.SubmitManagementHandled}
	trimmed := strings.TrimSpace(input)
	if trimmed == "/reload" {
		tab, _ := a.tabAndCtrlByID(tabID)
		if a.tabIsReadOnly(tab) {
			return control.SubmitResult{}, readOnlyChannelErr()
		}
		if tab == nil {
			return control.SubmitResult{}, a.workspaceNotReadyErr(tab)
		}
		if !fromBridge && a.botBridge != nil {
			a.botBridge.reclaimFromDesktop(tab.ID)
		}
		return management, a.ReloadRuntime(tab.ID)
	}
	if trimmed == "/effort" || strings.HasPrefix(trimmed, "/effort ") {
		tab, _ := a.tabAndCtrlByID(tabID)
		if a.tabIsReadOnly(tab) {
			return control.SubmitResult{}, readOnlyChannelErr()
		}
		if tab == nil {
			return control.SubmitResult{}, a.workspaceNotReadyErr(tab)
		}
		if !fromBridge && a.botBridge != nil {
			a.botBridge.reclaimFromDesktop(tab.ID)
		}
		a.runEffortCommandForTab(tabID, trimmed)
		return management, nil
	}
	if classifyManagement {
		tab, ctrl := a.tabAndCtrlByID(tabID)
		if a.tabIsReadOnly(tab) {
			return control.SubmitResult{}, readOnlyChannelErr()
		}
		if err := a.workspaceRuntimeAdmissionErr(tab, ctrl); err != nil {
			return control.SubmitResult{}, err
		}
		if err := a.ensureTabControllerWorkspace(tab); err != nil {
			return control.SubmitResult{}, err
		}
		ctrl = a.controllerForTab(tab)
		if ctrl == nil {
			return control.SubmitResult{}, a.workspaceNotReadyErr(tab)
		}
		managementRoute := false
		if classifier, ok := ctrl.(interface {
			ClassifySubmitRoute(input string) control.SubmitDisposition
		}); ok {
			managementRoute = classifier.ClassifySubmitRoute(input) == control.SubmitManagementHandled
		}
		if managementRoute {
			// Management commands still take the tab admission lock so they cannot
			// race an active turn or a controller replacement.
			admission, admittedCtrl, err := a.beginTabTurn(tabID, !fromBridge, submissionID...)
			if err != nil {
				return control.SubmitResult{}, err
			}
			defer admission.abort()
			tab = admission.tab
			a.ensureTabTopicIndexedForUserTurn(tab)
			if submitter, supported := admittedCtrl.(interface {
				SubmitDisplayWithResult(display, input string) control.SubmitResult
			}); supported {
				result := submitter.SubmitDisplayWithResult(input, input)
				admission.finish(admittedCtrl)
				return result, nil
			}
			admittedCtrl.SubmitDisplay(input, input)
			admission.finish(admittedCtrl)
			return management, nil
		}
	}
	admission, ctrl, err := a.beginTabTurn(tabID, !fromBridge, submissionID...)
	if err != nil {
		return control.SubmitResult{}, err
	}
	defer admission.abort()
	tab := admission.tab
	a.ensureTabTopicIndexedForUserTurn(tab)
	result := control.SubmitResult{Disposition: control.SubmitTurnStarted}
	if submitter, ok := ctrl.(interface {
		SubmitDisplayWithResult(display, input string) control.SubmitResult
	}); ok {
		result = submitter.SubmitDisplayWithResult(input, input)
	} else {
		ctrl.SubmitDisplay(input, input)
	}
	admission.finish(ctrl)
	return result, nil
}

func (a *App) submitUserTurnToTabWithSink(tabID, input string, forwarder event.Sink) bool {
	admission, ctrl, err := a.beginTabTurn(tabID, false)
	if err != nil {
		return false
	}
	defer admission.abort()
	tab := admission.tab
	var generation uint64
	if forwarder != nil {
		generation = tab.sink.SetBotSink(forwarder)
	}
	a.ensureTabTopicIndexedForUserTurn(tab)
	ctrl.SubmitUserTurn(input, input)
	started := admission.finish(ctrl)
	if !started && forwarder != nil {
		tab.sink.clearBotSink(generation)
	}
	return started
}

// RunShell executes a shell command directly (bypassing the model) and streams
// output as events on eventChannel.
func (a *App) RunShell(command string) error {
	return a.RunShellForTab("", command)
}

func (a *App) RunShellForTab(tabID, command string) error {
	admission, ctrl, err := a.beginTabTurn(tabID, true)
	if err != nil {
		return err
	}
	defer admission.abort()
	tab := admission.tab
	a.ensureTabTopicIndexedForUserTurn(tab)
	ctrl.RunShell(command)
	admission.finish(ctrl)
	return nil
}

// SubmitDisplay runs input as a turn while recording a shorter UI-only display
// string for the saved desktop transcript. The model still receives input.
func (a *App) SubmitDisplay(display, input string) error {
	return a.SubmitDisplayToTab("", display, input)
}

func (a *App) SubmitDisplayToTab(tabID, display, input string) error {
	return a.submitDisplayToTab(tabID, display, input, "")
}

func (a *App) SubmitDeliveryRecoveryToTab(tabID, display, input string) error {
	return a.submitDeliveryRecoveryToTab(tabID, display, input, "")
}

// InvocationRequest is the Wails-bound form of a composer invocation entity.
type InvocationRequest struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Offset int    `json:"offset"`
}

func controlInvocationRequests(invocations []InvocationRequest) []control.InvocationRequest {
	out := make([]control.InvocationRequest, 0, len(invocations))
	for _, invocation := range invocations {
		out = append(out, control.InvocationRequest{
			Name: invocation.Name, Kind: invocation.Kind, Offset: invocation.Offset,
		})
	}
	return out
}

func (a *App) SubmitInvocationsToTab(tabID, display, input string, invocations []InvocationRequest) error {
	return a.submitInvocationsToTab(tabID, display, input, invocations, "")
}

func validateInvocationTurnInput(input string, invocations []InvocationRequest) error {
	// A skill-only turn legitimately has no explicit task: the resolved
	// invocation content becomes the provider input. Without an invocation,
	// keep the same empty-input protection as every other submit path.
	if len(invocations) > 0 {
		return nil
	}
	return validateTurnInput(input)
}

func (a *App) submitInitialGoalToLocalTab(
	tabID, toolApprovalMode, goal, display, input string,
	invocations []InvocationRequest,
	submissionID ...string,
) ([]string, error) {
	admission, ctrl, err := a.beginTabTurn(tabID, true, submissionID...)
	if err != nil {
		return []string{}, err
	}
	defer admission.abort()

	tab := admission.tab
	toolApprovalMode = normalizeToolApprovalMode(toolApprovalMode)
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return []string{}, fmt.Errorf("goal is required")
	}
	a.mu.Lock()
	if a.tabs[tab.ID] != tab {
		a.mu.Unlock()
		return []string{}, a.workspaceNotReadyErr(nil)
	}
	tab.toolApprovalMode = toolApprovalMode
	tab.goal = goal
	tab.mode = tabModeFromAxes(false, toolApprovalMode == control.ToolApprovalYolo)
	a.saveTabsLocked()
	a.mu.Unlock()

	ctrl.SetPlanMode(false)
	drained := applyTabToolApprovalModeToController(ctrl, toolApprovalMode)
	syncTabGoalToController(ctrl, goal)
	a.ensureTabTopicIndexedForUserTurn(tab)
	if len(invocations) > 0 {
		ctrl.SubmitInvocationDisplay(display, input, controlInvocationRequests(invocations))
	} else {
		ctrl.SubmitDisplay(display, input)
	}
	admission.finish(ctrl)
	return drained, nil
}

// SubmitInitialGoalToTab activates a Goal and submits its first turn on the
// requested tab.
func (a *App) SubmitInitialGoalToTab(
	tabID, goal, display, input string,
	invocations []InvocationRequest,
	collaborationMode, toolApprovalMode string,
) ([]string, error) {
	if err := validateInvocationTurnInput(input, invocations); err != nil {
		return []string{}, err
	}
	return a.submitInitialGoalToLocalTab(
		tabID, toolApprovalMode, goal, display, input, invocations,
	)
}

func (a *App) SubmitEditedDisplayToTab(tabID, display, input, original string) error {
	return a.submitEditedDisplayToTab(tabID, display, input, original, "")
}

func (a *App) bindControllerDisplayRecorder(ctrl control.SessionAPI) {
	if ctrl == nil {
		return
	}
	ctrl.SetDisplayRecorder(func(content, display string) {
		dir := ctrl.SessionDir()
		if dir == "" {
			dir = config.SessionDir()
		}
		_ = recordSessionDisplay(dir, ctrl.SessionPath(), content, display)
	})
}

// Cancel aborts the in-flight turn.
func (a *App) Cancel() {
	a.CancelTab("")
}

func (a *App) CancelTab(tabID string) {
	if ctrl := a.ctrlByTabID(tabID); ctrl != nil {
		ctrl.Cancel()
	}
}

// CancelStopForTab drives the 任务461-P7 three-level stop escalation for one
// tab: each press advances one level (graceful cancel → force grace → force).
// Controllers without the escalation half (older builds, fakes) fall back to
// the ordinary cancel.
func (a *App) CancelStopForTab(tabID string) {
	if ctrl := a.ctrlByTabID(tabID); ctrl != nil {
		if esc, ok := ctrl.(interface{ CancelStop() }); ok {
			esc.CancelStop()
			return
		}
		ctrl.Cancel()
	}
}

// CancelTabResult reports what a session-level stop actually did, so the
// frontend can distinguish "cancelled here", "another process owns the turn"
// and "nothing was running" instead of showing a blanket failure.
type CancelTabResult struct {
	TabID          string `json:"tabId"`
	Cancelled      bool   `json:"cancelled"`
	OwnerElsewhere bool   `json:"ownerElsewhere"`
	NoRunningTurn  bool   `json:"noRunningTurn"`
}

// CancelTabWithResult stops whatever runs in this tab and reports the outcome.
// A tab whose controller is absent but whose active work is visible still has
// a running turn — it belongs to another process (heartbeat, serve pool, a
// second writer), so the frontend says so rather than reporting a failure.
func (a *App) CancelTabWithResult(tabID string) CancelTabResult {
	if ctrl := a.ctrlByTabID(tabID); ctrl != nil {
		if st := ctrl.RuntimeStatus(); st.Running || st.Cancellable || st.PendingPrompt || st.BackgroundJobs > 0 {
			ctrl.Cancel()
			return CancelTabResult{TabID: tabID, Cancelled: true}
		}
	}
	if work := a.ActiveWorkForTab(tabID); work.Running || work.Cancellable || len(work.Jobs) > 0 {
		return CancelTabResult{TabID: tabID, OwnerElsewhere: true}
	}
	return CancelTabResult{TabID: tabID, NoRunningTurn: true}
}

// Steer sends mid-turn guidance to the agent without interrupting the in-flight request.
func (a *App) Steer(text string) error {
	return a.SteerForTab("", text)
}

// SteerForTab sends mid-turn guidance to a specific tab's active agent turn.
// A rejected steer is returned to the frontend so its guidance shelf retains
// the text and submits it as a regular follow-up after the turn completes.
func (a *App) SteerForTab(tabID, text string) error {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return readOnlyChannelErr()
	}
	if ctrl == nil {
		return a.workspaceNotReadyErr(tab)
	}
	if err := a.ensureTabControllerWorkspace(tab); err != nil {
		return err
	}
	ctrl = a.controllerForTab(tab)
	if ctrl == nil {
		return a.workspaceNotReadyErr(tab)
	}
	steerer, ok := ctrl.(interface{ TrySteer(string) bool })
	if !ok {
		return fmt.Errorf("this runtime cannot accept mid-turn guidance")
	}
	if !steerer.TrySteer(text) {
		return fmt.Errorf("the turn ended before guidance could be applied; it will remain queued for the next turn")
	}
	return nil
}

func (a *App) tabAndCtrlByID(tabID string) (*WorkspaceTab, control.SessionAPI) {
	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	if tab == nil {
		a.mu.RUnlock()
		return nil, nil
	}
	ctrl := tab.Ctrl
	retryStartup := ctrl == nil && (tab.StartupErrLeaseHeld || tab.modelApplication.startupRetry)
	a.mu.RUnlock()
	if retryStartup && a.tryRecoverStartupLeaseHeldTab(tab) {
		a.mu.RLock()
		defer a.mu.RUnlock()
		if a.tabs[tab.ID] != tab {
			return nil, nil
		}
		return tab, tab.Ctrl
	}
	return tab, ctrl
}

// activeTabAndCtrl snapshots the active tab and its controller in one locked
// read, so callers never do a check-then-use on tab.Ctrl after the lock is
// released (a rebuild can swap the controller in between).
func (a *App) activeTabAndCtrl() (*WorkspaceTab, control.SessionAPI) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	tab := a.activeTabLocked()
	if tab == nil {
		return nil, nil
	}
	return tab, tab.Ctrl
}

// activeMCPRuntime snapshots the complete target of a Wails MCP action in one
// critical section. MCP operations may outlive a frontend tab switch; carrying
// the invoking workspace root prevents config/authorization reads from drifting to the
// newly active tab while controller calls still target the original runtime.
// mcpAppsSandboxAvailable reports whether Desktop may declare the Apps
// capability profile for newly acquired shared hosts.
