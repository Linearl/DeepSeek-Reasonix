package main

import (
	"log/slog"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/event"
)

// 任务581: 事发（2026-10-07 11:42-11:43，580 诊断）用户提交在 desktop 受理链
// 某段卡死且全程零日志——turns ledger、会话 WAL、desktop.log 均无痕迹，事后只
// 能圈定区间无法定位段。这里给每条提交链的各段计时：超过 submitAdmissionSlowWarn
// 的段留一行 Warn（带 stage 名），把「卡在哪一段」变成下一次事故可以直接读出的
// 答案。纯观测：不改任何受理语义、不加锁、不设超时（有界失败模式是另一个决策）。

// submitAdmissionSlowWarn 是单段受理耗时留痕阈值。var 以便测试收窄。
var submitAdmissionSlowWarn = 2 * time.Second

func timedSubmitStage(stage string, fn func()) {
	start := time.Now()
	fn()
	if elapsed := time.Since(start); elapsed >= submitAdmissionSlowWarn {
		slog.Warn("desktop: submit admission stage slow", "stage", stage, "ms", elapsed.Milliseconds())
	}
}

type turnSubmissionState struct {
	inFlight     bool
	submissionID string
}

func (t *WorkspaceTab) recordTurnStarted(now int64) int64 {
	t.telemMu.Lock()
	defer t.telemMu.Unlock()
	if t.usageTelemetry.activeTurnStartedAt == 0 {
		t.usageTelemetry.activeTurnStartedAt = now
	}
	return t.usageTelemetry.activeTurnStartedAt
}

func (t *WorkspaceTab) turnStartedAt() int64 {
	if t == nil {
		return 0
	}
	t.telemMu.Lock()
	defer t.telemMu.Unlock()
	return t.usageTelemetry.activeTurnStartedAt
}

// setBinding reroutes the sink while invalidating correlations that were
// created for a different frontend tab.
func (s *tabEventSink) setBinding(tabID string, app *App, generation ...uint64) {
	s.mu.Lock()
	previous := s.tabID
	if s.tabID != tabID {
		s.turn.submissionID = ""
	}
	s.tabID = tabID
	if len(generation) > 0 {
		if s.sessionGeneration != generation[0] {
			s.turn.submissionID = ""
		}
		s.sessionGeneration = generation[0]
	}
	if app != nil {
		s.app = app
	}
	boundApp := s.app
	s.mu.Unlock()
	// Task 557: the same controller keeps emitting across a detach (tab closed,
	// session kept running) or a reattach onto another tab, so its running
	// foreground sub-agents move with the binding — otherwise the old tab's
	// entries would linger with no turn left to clear them. Runs outside the
	// sink lock; the registry mutex is never taken under s.mu.
	if previous != tabID {
		boundApp.moveForegroundSubagents(previous, tabID)
	}
}

func (s *tabEventSink) setSessionGeneration(generation uint64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.sessionGeneration != generation {
		s.turn.submissionID = ""
	}
	s.sessionGeneration = generation
	s.mu.Unlock()
}

func (s *tabEventSink) sessionGenerationSnapshot() uint64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionGeneration
}

func (s *tabEventSink) setRuntimeEpoch(epoch string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.runtimeEpoch != epoch {
		s.turn.submissionID = ""
	}
	s.runtimeEpoch = epoch
	s.mu.Unlock()
}

func (s *tabEventSink) clearContext() {
	s.mu.Lock()
	s.ctx = nil
	// The #9601 context-less buffer is the second resting place for the same
	// stale events the runtimeEvents.Clear() below drains: events that arrived
	// in the ctx=nil window sit in pendingRuntimeEvents, and a reused sink's
	// setContext flushes them onto the new context — the small window of the
	// same #5352 "stale AI output bleeds into the visible session" shape this
	// method exists to prevent. Drop the buffer with the queue.
	s.pendingRuntimeEvents = nil
	s.turn.submissionID = ""
	s.mu.Unlock()
	s.runtimeEvents.Clear()
}

func firstSubmissionID(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func (s *tabEventSink) submissionIDSnapshot() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.turn.submissionID
}

type correlatedWireEventTab struct {
	wireEventTab
	SubmissionID string `json:"submissionId,omitempty"`
}

func toWireTabWithSubmission(e event.Event, tabID, runtimeEpoch, submissionID string, turnStartedAt int64, sessionGeneration ...uint64) any {
	wire := toWireTab(e, tabID, runtimeEpoch)
	if len(sessionGeneration) > 0 {
		wire.SessionGeneration = sessionGeneration[0]
	}
	if e.Kind == event.TurnStarted {
		wire.TurnStartedAt = turnStartedAt
	}
	if submissionID == "" {
		return wire
	}
	return correlatedWireEventTab{wireEventTab: wire, SubmissionID: submissionID}
}

// The WithID entry points correlate one optimistic desktop user item with the
// raw TurnDone produced by the turn that this call actually admits.
func (a *App) SubmitToTabWithID(tabID, input, submissionID string) error {
	if err := validateTurnInput(input); err != nil {
		return err
	}
	return a.submitToTab(tabID, input, false, submissionID)
}

func (a *App) SubmitDisplayToTabWithID(tabID, display, input, submissionID string) error {
	return a.submitDisplayToTab(tabID, display, input, submissionID)
}

func (a *App) submitDisplayToTab(tabID, display, input, submissionID string) error {
	if err := validateTurnInput(input); err != nil {
		return err
	}
	var admission *tabTurnAdmission
	var ctrl control.SessionAPI
	var beginErr error
	timedSubmitStage("begin_tab_turn", func() {
		admission, ctrl, beginErr = a.beginTabTurn(tabID, true, submissionID)
	})
	if beginErr != nil {
		return beginErr
	}
	defer admission.abort()
	tab := admission.tab
	timedSubmitStage("topic_index", func() { a.ensureTabTopicIndexedForUserTurn(tab) })
	timedSubmitStage("controller_submit", func() { ctrl.SubmitDisplay(display, input) })
	timedSubmitStage("admission_finish", func() { admission.finish(ctrl) })
	return nil
}

func (a *App) SubmitDeliveryRecoveryToTabWithID(tabID, display, input, submissionID string) error {
	return a.submitDeliveryRecoveryToTab(tabID, display, input, submissionID)
}

func (a *App) submitDeliveryRecoveryToTab(tabID, display, input, submissionID string) error {
	if err := validateTurnInput(input); err != nil {
		return err
	}
	var admission *tabTurnAdmission
	var ctrl control.SessionAPI
	var beginErr error
	timedSubmitStage("begin_tab_turn", func() {
		admission, ctrl, beginErr = a.beginTabTurn(tabID, true, submissionID)
	})
	if beginErr != nil {
		return beginErr
	}
	defer admission.abort()
	tab := admission.tab
	timedSubmitStage("topic_index", func() { a.ensureTabTopicIndexedForUserTurn(tab) })
	timedSubmitStage("controller_submit", func() { ctrl.SubmitDeliveryRecovery(display, input) })
	timedSubmitStage("admission_finish", func() { admission.finish(ctrl) })
	return nil
}

func (a *App) SubmitInvocationsToTabWithID(tabID, display, input string, invocations []InvocationRequest, submissionID string) error {
	return a.submitInvocationsToTab(tabID, display, input, invocations, submissionID)
}

func (a *App) submitInvocationsToTab(tabID, display, input string, invocations []InvocationRequest, submissionID string) error {
	if err := validateInvocationTurnInput(input, invocations); err != nil {
		return err
	}
	var admission *tabTurnAdmission
	var ctrl control.SessionAPI
	var beginErr error
	timedSubmitStage("begin_tab_turn", func() {
		admission, ctrl, beginErr = a.beginTabTurn(tabID, true, submissionID)
	})
	if beginErr != nil {
		return beginErr
	}
	defer admission.abort()
	tab := admission.tab
	timedSubmitStage("topic_index", func() { a.ensureTabTopicIndexedForUserTurn(tab) })
	timedSubmitStage("controller_submit", func() {
		ctrl.SubmitInvocationDisplay(display, input, controlInvocationRequests(invocations))
	})
	timedSubmitStage("admission_finish", func() { admission.finish(ctrl) })
	return nil
}

func (a *App) SubmitInitialGoalToTabWithID(
	tabID, goal, display, input string,
	invocations []InvocationRequest,
	collaborationMode, toolApprovalMode, submissionID string,
) ([]string, error) {
	if err := validateInvocationTurnInput(input, invocations); err != nil {
		return []string{}, err
	}
	return a.submitInitialGoalToLocalTab(
		tabID, toolApprovalMode, goal, display, input, invocations, submissionID,
	)
}

func (a *App) SubmitEditedDisplayToTabWithID(tabID, display, input, original, submissionID string) error {
	return a.submitEditedDisplayToTab(tabID, display, input, original, submissionID)
}

func (a *App) submitEditedDisplayToTab(tabID, display, input, original, submissionID string) error {
	if err := validateTurnInput(input); err != nil {
		return err
	}
	var admission *tabTurnAdmission
	var ctrl control.SessionAPI
	var beginErr error
	timedSubmitStage("begin_tab_turn", func() {
		admission, ctrl, beginErr = a.beginTabTurn(tabID, true, submissionID)
	})
	if beginErr != nil {
		return beginErr
	}
	defer admission.abort()
	tab := admission.tab
	timedSubmitStage("topic_index", func() { a.ensureTabTopicIndexedForUserTurn(tab) })
	timedSubmitStage("controller_submit", func() { ctrl.SubmitEditedDisplay(display, input, original) })
	timedSubmitStage("admission_finish", func() { admission.finish(ctrl) })
	return nil
}
