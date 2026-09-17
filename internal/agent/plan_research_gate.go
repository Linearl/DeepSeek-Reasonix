package agent

import (
	"slices"

	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// maxPlanResearchNudges bounds the plan-mode research gate (task 118): one
// nudge per run. The gate asks the model to delegate a read-only investigation
// before presenting a plan that can only restate what is already in context;
// one ask is enough to either produce that investigation or say why it is
// unnecessary.
const maxPlanResearchNudges = 1

// maxPlanResearchNudgeHardCap is the absolute ceiling for the configurable gate.
const maxPlanResearchNudgeHardCap = 2

// planResearchDelegationTools are the hosts a read-only investigation can be
// handed to. explore/research are the read-only profile entry points and
// read_only_task is the explicit one; writer-capable `task` is deliberately
// absent, because a plan turn is read-only and a writer dispatch is not a
// research step.
var planResearchDelegationTools = map[string]bool{
	tool.HostReadOnlyTask: true,
	"explore":             true,
	"research":            true,
}

// planResearchGateActive reports whether the host should require a read-only
// investigation before a plan is presented (task 118, opt-in).
func (a *Agent) planResearchGateActive() bool {
	return a != nil && a.planResearchGate && a.planMode.Load()
}

// dispatchedReadOnlyResearch reports whether this turn already handed research
// to a read-only sub-agent. It reads the transcript backwards to the user's own
// last message, so the check needs no extra turn state threaded through the
// tool path: the transcript is already the record of what was dispatched.
func (a *Agent) dispatchedReadOnlyResearch() bool {
	if a == nil || a.sess.conversation == nil {
		return false
	}
	for _, m := range slices.Backward(a.sess.conversation.Snapshot()) {
		if IsUserAuthoredTurnMessage(m) {
			return false
		}
		if m.Role == provider.RoleTool && planResearchDelegationTools[m.Name] {
			return true
		}
	}
	return false
}

// planResearchNudgeMessage is the visible host round for the gate. It offers
// both exits - delegate, or say what was left unread - so the gate adds a step
// instead of a wall.
func planResearchNudgeMessage() string {
	return "Host note before the plan: no read-only investigation has been delegated this turn, so the plan can only rest on what is already in this context. Either dispatch a read_only_task (or explore) sub-agent to check the parts of the code you have not read, or state plainly in the plan which areas you did not investigate and why the plan is safe without them. Then present the plan."
}

// planResearchNudgeNotice is the transcript line for the gate firing.
func planResearchNudgeNotice() string {
	return "The plan rested on the visible prefix alone, so the host asked for a read-only investigation (or an explicit list of what was not read) before the plan lands."
}

// normalizePlanResearchNudgeLimit returns the effective per-run gate cap. Zero
// or negative keeps the built-in default; values above the hard cap are clamped
// so the gate can never become a loop.
func normalizePlanResearchNudgeLimit(limit int) int {
	if limit <= 0 {
		return maxPlanResearchNudges
	}
	if limit > maxPlanResearchNudgeHardCap {
		return maxPlanResearchNudgeHardCap
	}
	return limit
}
