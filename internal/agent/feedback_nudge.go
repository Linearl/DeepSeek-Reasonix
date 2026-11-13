package agent

import (
	"strings"

	"reasonix/internal/tool"
)

// Task 172 feedback touchpoints ("意见箱触达策略"). One mechanism, one switch,
// two triggers:
//
//   - T1 (completion): after a turn reaches a clean final answer, the host
//     appends ONE host-generated user message telling the model how to route
//     what the session surfaced. Costs one extra model round — that is the
//     documented overhead the settings hint names.
//   - T2 (mid-turn steer): when a user steer is consumed mid-turn, the host
//     appends ONE short guidance note for the correction turn. The model
//     judges on its own; the current task is never interrupted.
//
// Classification contract (tl1350, user rules ①②): the inbox is no longer
// offered as an invitation for anything reportable. The host copy sorts the
// session's takeaways into three buckets —
//
//  1. reusable know-how (including anything rooted OUTSIDE Reasonix: upstream
//     model/API behavior, prompt wording, the user's own workflow) → the model
//     records it itself via memory:remember and must NOT mention the inbox;
//  2. a defect Reasonix itself can fix (built-in tool behavior/output,
//     host-appended text, rendering/UI, config defaults, apparent fork bugs)
//     → the only bucket that earns the inbox, and then by direct submission:
//     the model calls submit_feedback itself and closes with a one-line
//     receipt (tl1574, no permission ask);
//  3. nothing worth recording → both skip.
//
// Anti-loop gates (at least two, all four implemented):
//
//  1. per-turn flag (turnruntime.feedbackNudges, hard cap 1): the turn a nudge
//     produced ends inside the same Run, so the very next final answer hits the
//     cap and cannot ask again — ten consecutive turns see one question at most;
//  2. per-session cooldown (feedbackNudgeTurn/feedbackNudgeLastTurn): T1 and
//     T2 share the counter, and two injections must be ≥10 user rounds apart;
//     this also blocks T1 from stacking onto a turn T2 already touched;
//  3. marker recognition (FeedbackNudgeMarker): an input that itself carries
//     the marker (replayed history, queued guidance quoting the nudge) is
//     skipped, so a nudge can never seed another nudge across turns;
//  4. remember-receipt skip (tl1350 optional filter): a successful remember
//     receipt inside the current evidence window means the process knowledge
//     is already recorded — the whole touchpoint stays silent instead of
//     manufacturing noise on top of it. Receipts reset between user turns and
//     carry across one delivery scope, so the window reads "recorded this
//     turn / current scope".
//
// The dial arrives pre-ANDed via Options.FeedbackNudge (boot passes
// config.FeedbackNudgeEnabled, parent switch wins). Off = zero messages, zero
// extra rounds: every injection point returns before touching anything.

// FeedbackNudgeMarker marks host-injected feedback-touchpoint guidance
// (task 172). It is recognizable in message text so a nudge that comes back
// as input (replay, quoted guidance) is detected and skipped (gate 3).
const FeedbackNudgeMarker = "[Feedback inbox nudge"

// feedbackNudgeCooldownTurns is the shared T1/T2 throttle: two injections must
// be at least this many user message rounds apart. Hard-coded for phase 1
// (configurable later, per the 0928 ruling).
const feedbackNudgeCooldownTurns = 10

// feedbackNudgeMaxPerTurn is the per-run hard cap (gate 1). One T1 or one T2
// per turn — a nudge-produced answer can never trigger a second question.
const feedbackNudgeMaxPerTurn = 1

// feedbackNudgeCompletionGuidance is the T1 host message. Per the tl1350
// classification contract it routes the session's takeaways: reusable
// know-how (even when rooted outside Reasonix) goes to memory:remember with
// no inbox mention; only a defect Reasonix itself can fix earns the inbox,
// and then by direct submission — no permission ask, the model calls
// submit_feedback itself and closes with a one-line receipt (tl1574);
// nothing to record skips both, so a routine success does not manufacture
// fake feedback.
func feedbackNudgeCompletionGuidance() string {
	return FeedbackNudgeMarker + " task-172 T1]\n" +
		"The user's task has just been completed. Sort what this session surfaced into exactly one of three buckets before ending.\n" +
		"1. Reusable know-how — a practice, workaround, or lesson future sessions would benefit from, INCLUDING anything whose root cause lies outside Reasonix (upstream model or API behavior, prompt wording, the user's own workflow): record it yourself by calling the memory:remember tool (description + body). Do NOT mention the feedback inbox for this.\n" +
		"2. A defect Reasonix itself can fix — built-in tool behavior or output, host-appended text, rendering or UI, configuration defaults, or an apparent bug in this fork: do NOT ask permission — check the inbox for duplicates, submit it yourself by calling the submit_feedback tool, and end your reply with ONE short receipt line in the form ℹ️ 已投意见箱：<one-line summary>，要立项说一声. If the user says on the spot that it should become a task, start it immediately; otherwise the receipt must not delay or reshape the task's outcome.\n" +
		"3. Nothing worth recording: skip both — no remember call, no submission.\n" +
		"Do not repeat this request, and do not let it change the task's outcome."
}

// feedbackNudgeSteerGuidance is the T2 host note appended right after a
// consumed mid-turn steer. Guidance only: the model routes the correction's
// takeaways under the same tl1350 contract — know-how to memory:remember
// without an inbox mention, and only a fork-fixable defect may earn the
// inbox, submitted directly with a closing one-line receipt (tl1574).
func feedbackNudgeSteerGuidance() string {
	return FeedbackNudgeMarker + " task-172 T2]\n" +
		"The user just sent a corrective mid-turn message. If working through that correction surfaces reusable know-how — a practice or lesson future sessions would benefit from, including anything whose root cause lies outside Reasonix (upstream model or API behavior, prompt wording, the user's own workflow) — record it yourself via the memory:remember tool, and do NOT mention the feedback inbox for it.\n" +
		"Only if the correction exposed a defect Reasonix itself can fix (built-in tool behavior or output, host-appended text, rendering or UI, configuration defaults, or an apparent bug in this fork) may you touch the inbox: do NOT ask permission — check it for duplicates, submit it yourself via the submit_feedback tool, and end your final answer with ONE short receipt line in the form ℹ️ 已投意见箱：<one-line summary>，要立项说一声 (a same-turn ask to make it a task starts immediately).\n" +
		"Judge on your own; do not interrupt or delay the current task for this, and stay silent when neither applies."
}

// feedbackNudgeAllowed reports whether the shared gates permit one injection
// right now: dial on (boot pre-ANDed the parent switch), per-turn cap free,
// input not a nudge echo (gate 3), remember not already successful in the
// current evidence window (gate 4), cooldown elapsed (gate 2).
func (a *Agent) feedbackNudgeAllowed(state *turnRuntime, input string) bool {
	if !a.svc.feedbackNudge {
		return false
	}
	if state.terminal.feedbackNudges >= feedbackNudgeMaxPerTurn {
		return false
	}
	if strings.Contains(input, FeedbackNudgeMarker) {
		return false
	}
	// Gate 4 (tl1350): a successful remember receipt in the current evidence
	// window means the process knowledge is already recorded — an inbox nudge
	// would only duplicate it. Receipts reset between user turns and carry
	// across one delivery scope, so the window reads "recorded this turn /
	// current scope". Nil-safe when no ledger exists.
	if a.task.ledger.HasSuccessfulToolReceipt(tool.HostRemember) {
		return false
	}
	last := a.feedbackNudgeLastTurn.Load()
	if last != 0 && a.feedbackNudgeTurn.Load()-last < feedbackNudgeCooldownTurns {
		return false
	}
	return true
}

// injectFeedbackNudge records both gate bookkeeping entries and appends one
// host-generated guidance message. Callers must have checked
// feedbackNudgeAllowed first; the counter writes here are what close the gates.
func (a *Agent) injectFeedbackNudge(state *turnRuntime, guidance string) {
	state.terminal.feedbackNudges++
	a.feedbackNudgeLastTurn.Store(a.feedbackNudgeTurn.Load())
	a.sess.conversation.Add(HostGeneratedUserMessage(a.withTurnPreferences(guidance)))
}

// maybeNudgeFeedbackCompletion is the T1 hook (task 172). Called from
// handleFinalResponse right before a clean final answer ends the turn, after
// steer intake has closed so a real user steer always wins over the nudge.
// It reports whether the loop should continue for the receipt round (tl1574:
// the model submits and reports instead of inviting).
func (a *Agent) maybeNudgeFeedbackCompletion(state *turnRuntime, input string) bool {
	if !a.feedbackNudgeAllowed(state, input) {
		return false
	}
	a.injectFeedbackNudge(state, feedbackNudgeCompletionGuidance())
	return true
}

// maybeNudgeFeedbackSteer is the T2 hook (task 172). Called from runToolLoop
// right after a consumed steer is persisted, so the guidance lands in the
// same round the model sees the correction. Consuming the shared cooldown
// here is what keeps T1 from stacking a second touchpoint onto this turn.
func (a *Agent) maybeNudgeFeedbackSteer(state *turnRuntime, steerText string) {
	if !a.feedbackNudgeAllowed(state, steerText) {
		return
	}
	a.injectFeedbackNudge(state, feedbackNudgeSteerGuidance())
}
