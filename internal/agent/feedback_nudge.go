package agent

import (
	"strings"
)

// Task 172 feedback touchpoints ("意见箱触达策略"). One mechanism, one switch,
// two triggers:
//
//   - T1 (completion): after a turn reaches a clean final answer, the host
//     appends ONE host-generated user message inviting the model to point the
//     user at the feedback inbox. Costs one extra model round — that is the
//     documented overhead the settings hint names.
//   - T2 (mid-turn steer): when a user steer is consumed mid-turn, the host
//     appends ONE short guidance note telling the model it may mention the
//     inbox at the end of the correction turn. The model judges on its own;
//     the current task is never interrupted.
//
// Anti-loop gates (at least two, all three implemented):
//
//  1. per-turn flag (turnruntime.feedbackNudges, hard cap 1): the turn a nudge
//     produced ends inside the same Run, so the very next final answer hits the
//     cap and cannot ask again — ten consecutive turns see one question at most;
//  2. per-session cooldown (feedbackNudgeTurn/feedbackNudgeLastTurn): T1 and
//     T2 share the counter, and two injections must be ≥10 user rounds apart;
//     this also blocks T1 from stacking onto a turn T2 already touched;
//  3. marker recognition (FeedbackNudgeMarker): an input that itself carries
//     the marker (replayed history, queued guidance quoting the nudge) is
//     skipped, so a nudge can never seed another nudge across turns.
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

// feedbackNudgeCompletionGuidance is the T1 host message. It asks for one
// short invitation and lets the model skip when there is nothing worth
// reporting, so a routine success does not manufacture fake feedback.
func feedbackNudgeCompletionGuidance() string {
	return FeedbackNudgeMarker + " task-172 T1]\n" +
		"The user's task has just been completed. If this session surfaced anything worth reporting — a bug, a rough edge, or a suggestion about Reasonix itself — end your reply with ONE short sentence inviting the user to record it in the feedback inbox (the submit_feedback tool can write it). " +
		"If there is nothing worth reporting, or the user already gave feedback this session, skip the invitation entirely. Do not repeat this request, and do not let it change the task's outcome."
}

// feedbackNudgeSteerGuidance is the T2 host note appended right after a
// consumed mid-turn steer. Guidance only: the model weaves it into the
// correction turn if and only if the correction exposed something reportable.
func feedbackNudgeSteerGuidance() string {
	return FeedbackNudgeMarker + " task-172 T2]\n" +
		"The user just sent a corrective mid-turn message. If working through that correction exposes a reportable problem with Reasonix itself, you may briefly mention at the end of your final answer that the feedback inbox (submit_feedback) can record it. " +
		"Judge on your own; do not interrupt or delay the current task for this, and stay silent when there is nothing to report."
}

// feedbackNudgeAllowed reports whether the shared gates permit one injection
// right now: dial on (boot pre-ANDed the parent switch), per-turn cap free,
// input not a nudge echo (gate 3), cooldown elapsed (gate 2).
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
// It reports whether the loop should continue for the invitation round.
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
