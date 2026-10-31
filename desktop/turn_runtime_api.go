package main

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/turnevent"
)

// TurnStartView is the synchronous admission receipt for the new Wails turn
// API. Events remain the streaming authority after admission.
type TurnStartView struct {
	TurnID       string                    `json:"turnId"`
	Status       event.TurnStatus          `json:"status"`
	Disposition  control.SubmitDisposition `json:"disposition"`
	RuntimeEpoch string                    `json:"runtimeEpoch,omitempty"`
	SubmissionID string                    `json:"submissionId,omitempty"`
}

// validatePromptIdentity fences a decision to the runtime and turn that
// rendered its card. It is intentionally shared by every decision surface;
// callers must still resolve the prompt on the same controller instance.
func (a *App) validatePromptIdentity(tabID, turnID, runtimeEpoch string) (control.SessionAPI, error) {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if ctrl == nil {
		return nil, a.workspaceNotReadyErr(tab)
	}
	status := ctrl.RuntimeStatus()
	if strings.TrimSpace(turnID) == "" || status.TurnID != strings.TrimSpace(turnID) {
		return nil, fmt.Errorf("turn %q is not the active turn for tab %q", turnID, tabID)
	}
	if epoch := strings.TrimSpace(runtimeEpoch); epoch != "" && tab != nil && tab.sink != nil && tab.sink.runtimeEpochSnapshot() != epoch {
		return nil, fmt.Errorf("runtime changed while resolving prompt for tab %q", tabID)
	}
	return ctrl, nil
}

// stoppableCtrl resolves the controller a Stop request targets. Only an idle
// tab is rejected; a stale turn id is logged and the active work still stops.
func (a *App) stoppableCtrl(tabID, turnID string) (control.SessionAPI, error) {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if ctrl == nil {
		return nil, a.workspaceNotReadyErr(tab)
	}
	status := ctrl.RuntimeStatus()
	if !status.Running && !status.Cancellable {
		return nil, errTurnNotRunning
	}
	if turnID = strings.TrimSpace(turnID); turnID != status.TurnID {
		slog.Info("desktop: stop targeted a stale turn id; interrupting the active turn", "tab", tabID, "requested", turnID, "active", status.TurnID)
	}
	return ctrl, nil
}

// StartTurnForTab is the turn-id-aware replacement for SubmitToTab. Existing
// Submit entry points remain compatibility wrappers during the protocol cutover.
func (a *App) StartTurnForTab(tabID, input, submissionID string) (TurnStartView, error) {
	if strings.TrimSpace(submissionID) == "" {
		return TurnStartView{}, fmt.Errorf("submissionId is required")
	}
	result, err := a.submitToTabResult(tabID, input, false, true, submissionID)
	if err != nil {
		return TurnStartView{}, err
	}
	if result.Disposition == control.SubmitManagementHandled {
		return TurnStartView{Disposition: result.Disposition, SubmissionID: submissionID}, nil
	}
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if ctrl == nil {
		return TurnStartView{}, a.workspaceNotReadyErr(tab)
	}
	// 任务900（上游 #9806/PR10781）：受理锁已释放，tab 可能已持有重建后的控制
	// 器（/compact 是可复现现场），它不保证持有本次提交的 receipt。受理时
	// submitToTabResult 已带回 admit 的那个控制器的 turn id，此处为权威；向
	// 重解析出的控制器查询只在回执缺失时回退（老调用方/非 Controller 实现）。
	turnID := result.TurnID
	if admitted, ok := ctrl.(interface{ TurnIDForSubmission(string) string }); ok && turnID == "" {
		turnID = admitted.TurnIDForSubmission(submissionID)
	}
	if strings.TrimSpace(turnID) == "" {
		// The submission was accepted, but this input never becomes a turn:
		// slash session ops (/compact /new /clear /model …), notices, and
		// other local runtime verbs are handled by the controller without
		// turn admission. Returning an error here made a *successful* local
		// command show up as "Send failed: turn admission did not produce a
		// durable turn id" (desktop report, 2026-08-29). Report an accepted
		// receipt with an empty turnId instead — callers treat a missing
		// turnId as "confirmed without turn association" (the turn_admitted
		// reducer ignores falsy ids) and the command's own Notice events
		// carry the outcome.
		epoch := ""
		if tab != nil && tab.sink != nil {
			epoch = tab.sink.runtimeEpochSnapshot()
		}
		return TurnStartView{TurnID: "", Status: "", RuntimeEpoch: epoch, SubmissionID: submissionID}, nil
	}
	epoch := ""
	if tab != nil && tab.sink != nil {
		epoch = tab.sink.runtimeEpochSnapshot()
	}
	// This is an admission receipt, not a potentially raced runtime snapshot.
	// Ordered events carry every later transition, including a provider that
	// completed before the Wails Promise was delivered.
	return TurnStartView{TurnID: turnID, Status: event.TurnQueued, Disposition: control.SubmitTurnStarted, RuntimeEpoch: epoch, SubmissionID: submissionID}, nil
}

// errTurnNotRunning tells the frontend the tab is already idle so it can
// reconcile its runtime view instead of reporting a failed Stop.
var errTurnNotRunning = &inboxCodedError{code: "turn_not_running", cause: errors.New("no turn is running")}

// InterruptTurnForTab stops the tab's active work. Stop is a session-level
// request: a turn id from a stale button still interrupts whatever is running
// now, because an unstoppable turn is worse than stopping its replacement.
//
// tl1304 ②: this is the desktop stop button's real first-press path. It used
// to call the bare Cancel(), which never armed the 任务461-P7 ④ 保底 timer — a
// turn that ignored the graceful cancel then sat at L1 forever and the
// automatic grace→force escalation never came (and the UI-side L2/L3 presses
// were locked out on top of it by the cancellable veto, tl1304 ①). CancelStop
// is the single escalation entry — level None → L1 graceful cancel + the 30s
// no-response fallback timer; Normal with a live turn → L2 force grace;
// Grace → L3 force — so every press, from any surface, advances the same
// state machine. Controllers without the escalation half (older builds,
// fakes) keep the ordinary cancel, the same fallback CancelStopForTab uses.
func (a *App) InterruptTurnForTab(tabID, turnID string) error {
	ctrl, err := a.stoppableCtrl(tabID, turnID)
	if err != nil {
		return err
	}
	if esc, ok := ctrl.(interface{ CancelStop() }); ok {
		esc.CancelStop()
		return nil
	}
	ctrl.Cancel()
	return nil
}

// InterruptTurnWithInboxItemsForTab is the receipt-capable Stop used by the
// Composer when it also discards queued follow-ups.
func (a *App) InterruptTurnWithInboxItemsForTab(tabID, turnID string, itemIDs []string) (InboxCancelResultView, error) {
	view := InboxCancelResultView{DiscardedItemIDs: []string{}}
	ctrl, err := a.stoppableCtrl(tabID, turnID)
	if err != nil {
		return view, err
	}
	result, err := ctrl.CancelWithInboxItemsResult(itemIDs, "desktop")
	if err != nil {
		return view, inboxWailsError(err)
	}
	view.DiscardedItemIDs = append(view.DiscardedItemIDs, result.DiscardedItemIDs...)
	view.Warning = result.Warning
	a.emitInboxChanged(tabID)
	return view, nil
}

// AnswerPromptForTab resolves an Ask only when it belongs to the exact active
// turn. Controller-side prompt ids remain independently idempotent.
func (a *App) AnswerPromptForTab(tabID, turnID, promptID string, answers []QuestionAnswer) error {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if ctrl == nil {
		return a.workspaceNotReadyErr(tab)
	}
	status := ctrl.RuntimeStatus()
	if strings.TrimSpace(turnID) == "" || status.TurnID != strings.TrimSpace(turnID) {
		return fmt.Errorf("turn %q is not the active turn for tab %q", turnID, tabID)
	}
	// Resolve on the controller instance that passed the turn-id fence. Calling
	// the legacy app wrapper here would re-resolve the tab and could deliver a
	// late answer to a replacement controller after a runtime rebuild.
	out := make([]event.AskAnswer, len(answers))
	for i, answer := range answers {
		out[i] = event.AskAnswer{QuestionID: answer.QuestionID, Selected: answer.Selected}
	}
	if checked, ok := ctrl.(interface {
		AnswerQuestionChecked(string, []event.AskAnswer) error
	}); ok {
		return checked.AnswerQuestionChecked(promptID, out)
	}
	ctrl.AnswerQuestion(promptID, out)
	return nil
}

type turnEventReader interface {
	TurnEventReplay(after uint64) (turnevent.ReplayView, error)
}

type TurnEventReplayView struct {
	Events             []turnevent.Envelope `json:"events"`
	FloorSequence      uint64               `json:"floorSeq"`
	LatestSequence     uint64               `json:"latestSeq"`
	NextAfterSequence  uint64               `json:"nextAfterSeq"`
	HasMore            bool                 `json:"hasMore"`
	ResetRequired      bool                 `json:"resetRequired"`
	TranscriptRevision int64                `json:"transcriptRevision,omitempty"`
	TranscriptDigest   string               `json:"transcriptDigest,omitempty"`
	HeadID             string               `json:"headId,omitempty"`
	LeafMessageID      string               `json:"leafMessageId,omitempty"`
	RuntimeEpoch       string               `json:"runtimeEpoch,omitempty"`
}

// TurnEventsForTab supplies the durable suffix used to repair sequence gaps or
// rebuild after a runtime epoch change.
func (a *App) TurnEventsForTab(tabID string, afterSeq uint64) (TurnEventReplayView, error) {
	empty := TurnEventReplayView{Events: []turnevent.Envelope{}}
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if ctrl == nil {
		return empty, a.workspaceNotReadyErr(tab)
	}
	reader, ok := ctrl.(turnEventReader)
	if !ok {
		return empty, fmt.Errorf("turn event replay is unavailable")
	}
	// Re-check the controller under the app lock before sampling the epoch.
	// This prevents pairing an old controller with a replacement runtime after
	// a session rebind races tabAndCtrlByID.
	epoch := ""
	a.mu.RLock()
	bound := tab != nil && a.tabs[tabID] == tab && tab.Ctrl == ctrl
	if bound && tab.sink != nil {
		epoch = tab.sink.runtimeEpochSnapshot()
	}
	a.mu.RUnlock()
	if !bound {
		return empty, fmt.Errorf("runtime changed while binding turn event replay")
	}
	replay, err := reader.TurnEventReplay(afterSeq)
	if replay.Events == nil {
		replay.Events = []turnevent.Envelope{}
	}
	return TurnEventReplayView{
		Events: replay.Events, FloorSequence: replay.FloorSequence,
		LatestSequence: replay.LatestSequence, NextAfterSequence: replay.NextAfterSequence,
		HasMore: replay.HasMore, ResetRequired: replay.ResetRequired,
		TranscriptRevision: replay.TranscriptRevision, TranscriptDigest: replay.TranscriptDigest,
		HeadID: replay.HeadID, LeafMessageID: replay.LeafMessageID,
		RuntimeEpoch: epoch,
	}, err
}

var _ control.SessionAPI = (*control.Controller)(nil)
