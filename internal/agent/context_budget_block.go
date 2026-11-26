package agent

import (
	"fmt"
	"log/slog"
	"strings"
)

// contextBudgetTag is the per-turn transient block carrying the context
// budget line (#9520). It rides the user turn head like the other transient
// blocks and is stripped from previews/titles via TransientUserBlockTags.
// contextBudgetTag names the per-turn block. It says "state", not "budget": the
// number is how full the window is, not how much allowance is left, and that
// difference is what made a filling window read like a running-out quota.
const contextBudgetTag = "context-state"

// contextBudgetWarnRatio: at or above this fraction of the compaction trigger
// the budget line upgrades to the "approaching" wording. Deliberately plain
// arithmetic — no crossing/re-arm state machine, the copy carries the phase.
const contextBudgetWarnRatio = 0.85

// ContextBudgetBlock renders the one-line budget the model can rely on every
// round (#9520): estimated prompt tokens, the window, and where auto
// compaction fires. Near the trigger it appends the planning guidance so the
// model knows older turns are summarized, not lost — the anxiety that drives
// rushed or abandoned large tasks is the failure mode this prevents.
func ContextBudgetBlock(used, trigger, window int) string {
	if window <= 0 || used <= 0 || window <= used {
		return ""
	}
	line := fmt.Sprintf("<%s>上下文 %dk/%dk (%d%%)；压缩进度 %d%%</%s>",
		contextBudgetTag, used/1000, window/1000, used*100/window, trigger*100/window, contextBudgetTag)
	if trigger > 0 && float64(used) >= float64(trigger)*contextBudgetWarnRatio {
		line += "\n上下文接近上限，将自动压缩并继续，无需处理"
	}
	return line
}

// WithContextBudget prefixes the user turn with the context-budget block.
// The numbers come from the same estimators the ContextManager compares
// against its thresholds, so the line can never disagree with what the host
// is about to do. Returns content unchanged when the window is unknown.
func (a *Agent) WithContextBudget(content string) string {
	if a == nil {
		return content
	}
	// Task 265: the lab switch gates the line itself; off leaves the model to
	// its own window model (the upstream behaviour).
	if !a.contextBudget {
		return content
	}
	window := a.effectiveContextWindow()
	if window <= 0 {
		return content
	}
	used := a.ContextUsedTokens()
	trigger := a.compactTrigger()
	block := ContextBudgetBlock(used, trigger, window)
	if block == "" || hasLeadingInjectedBlock(content, contextBudgetTag) {
		return content
	}
	// Debug, not Info: this runs once per user turn and would otherwise dominate the log. It is
	// what answers "why was the model told to write things down" after the fact - the advisory
	// rides on the same block, and only this line says whether it was included.
	slog.Debug("context budget block injected",
		"feature", "context-budget", "used", used, "window", window, "trigger", trigger,
		"advisory", strings.Contains(block, "上下文接近上限"))
	return block + "\n\n" + content
}
