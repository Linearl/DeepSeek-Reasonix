package main

// Task 325 (user ruling 2026-09-25): autopilot runs unattended — approval
// prompts are answered by the proxy reviewer — so it is only meaningful in the
// YOLO approval posture. Under ask/auto an unattended run stacks approval
// prompts nobody is there to answer (or leaks them through the proxy chain):
// a semantic conflict, not a style preference. The gate below is therefore a
// hard precondition at every autopilot enable path, and the reverse linkage
// removes the privileged combination the moment it would arise.
//
// Entry coverage (each callsite is marked "task 325"):
//   - composer collaboration-mode selector → SetCollaborationModeForTab:
//     since task 465's two-axis matrix this entry AUTO-SATISFIES the
//     precondition (approval moves to yolo, decision recorded) instead of
//     refusing — the gate still bounds what a refused enable would produce
//   - settings default for new sessions → SetDesktopAutopilot
//   - new-tab defaults → createTabEntryWithID
//   - restart restore / recovery-created autopilot sessions → restoreTabs
//   - any approval-mode change while autopilot is on → SetToolApprovalModeForTab
//     and SetComposerProfileForTab (reverse linkage, fail-closed by turning
//     autopilot off rather than trapping the user's approval switch)

import (
	"time"

	"reasonix/internal/control"
	"reasonix/internal/event"
)

const (
	// NoticeCodeAutopilotRequiresYolo: an autopilot enable request was refused
	// because the approval mode is not yolo (task 325). The composer tier
	// switch (SetCollaborationModeForTab) no longer refuses — task 465's
	// two-axis matrix auto-satisfies the precondition there — but the code
	// stays alive for the settings-default refusal and any future entry.
	NoticeCodeAutopilotRequiresYolo = "autopilot_requires_yolo"
	// NoticeCodeAutopilotClosedOffYolo: autopilot was on and the approval mode
	// moved away from yolo — autopilot turned itself off (task 325 reverse
	// linkage, fail-closed).
	NoticeCodeAutopilotClosedOffYolo = "autopilot_closed_off_yolo"
	// NoticeCodeAutopilotAssumedYolo: the autopilot tier switch auto-satisfied
	// the yolo precondition by moving approval to yolo itself (task 465
	// two-axis matrix). Unattended means "no human is available — decide for
	// yourself, record the decision, and continue": this notice plus the
	// autopilot-toggle log line are that record.
	NoticeCodeAutopilotAssumedYolo = "autopilot_assumed_yolo"
)

const (
	// autopilotRequiresYoloText is the English fallback; frontends localize by
	// the notice code above and fall back to this text.
	autopilotRequiresYoloText  = "Autopilot requires the YOLO approval mode (需要 yolo 审批模式); switch approval to YOLO first."
	autopilotClosedOffYoloText = "Approval mode left YOLO, so autopilot was switched off (审批离开 yolo，autopilot 已自动关闭)."
	autopilotAssumedYoloText   = "Autopilot on: approval switched to YOLO automatically (decision recorded) (无人值守开启，审批已自动切到 yolo，决策已记录)."
)

// autopilotGateAllowed reports whether autopilot may turn on under the given
// approval mode. Only yolo passes; ask/auto (and anything that normalizes to
// ask) is refused. Normalization lives in one place so legacy mode aliases
// ("full", "bypass") count as yolo exactly where the rest of the tab code
// counts them as yolo.
func autopilotGateAllowed(mode string) bool {
	return normalizeToolApprovalMode(mode) == control.ToolApprovalYolo
}

// gateRestoredAutopilotDefaults applies the task-325 yolo precondition to the
// desktopAutopilotDefaults result for one concrete approval mode. It is the
// single gate shared by the new-tab and restore paths so those two entries
// cannot drift apart. The task-477 ask-timeout pair and the task-544 ask
// auto-continue switch travel with the triple: an attended session never
// carries either ask sub-option, so a refused autopilot clears them too.
func gateRestoredAutopilotDefaults(on bool, maxRuntime, approvalGrace time.Duration, askEnabled bool, askWait time.Duration, askAutoContinue bool, approvalMode string) (bool, time.Duration, time.Duration, bool, time.Duration, bool) {
	if !on || !autopilotGateAllowed(approvalMode) {
		return false, 0, 0, false, 0, false
	}
	return on, maxRuntime, approvalGrace, askEnabled, askWait, askAutoContinue
}

// closeAutopilotForOffYolo clears the autopilot fields on tab when the tab is
// about to hold the forbidden (autopilot, non-yolo) combination. It returns
// true when the combination was present and got cleared — the reverse linkage
// fired. Caller holds the App lock and decides on notice emission.
func closeAutopilotForOffYolo(tab *WorkspaceTab, approvalMode string) bool {
	if tab == nil || !tab.autopilot || autopilotGateAllowed(approvalMode) {
		return false
	}
	return closeAutopilotForTier(tab)
}

// closeAutopilotForTier clears the autopilot fields on tab when a tier pick
// lands the approval axis off the autopilot tier (task 595 four-tier
// single-select). Yolo picks included: on the mode bar the yolo tier is plain
// yolo, not an autopilot alias — the only way back onto the tier is the
// autopilot tier itself. Callers that must keep the pre-595 semantics
// (heartbeat tasks re-arming yolo, the legacy bypass toggle) stay on
// closeAutopilotForOffYolo, which leaves (autopilot, yolo) untouched. Caller
// holds the App lock; returns true when the flag was cleared.
func closeAutopilotForTier(tab *WorkspaceTab) bool {
	if tab == nil || !tab.autopilot {
		return false
	}
	tab.autopilot = false
	tab.autopilotMaxRuntime = 0
	tab.autopilotApprovalGrace = 0
	tab.autopilotAskTimeoutEnabled = false
	tab.autopilotAskWait = 0
	tab.autopilotAskAutoContinue = false
	return true
}

// noticeCodeForTab emits one notice frame on the tab's event channel with a
// stable machine-readable code (best effort: a missing tab or sink drops it).
func (a *App) noticeCodeForTab(tabID string, level event.Level, code, text string) {
	tab := a.tabByID(tabID)
	if tab != nil && tab.sink != nil {
		tab.sink.Emit(event.Event{Kind: event.Notice, Level: level, Code: code, Text: text})
	}
}
