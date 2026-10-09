package agent

import "strings"

// FormatSubagentRunResult renders the sub-agent result the parent model reads:
// status, transcript reference, optional usage footer, then the final answer.
// notes (task 632 usage line) ride between the reference guidance and the
// answer, and only for persisted runs — an ephemeral run (empty Ref) keeps its
// exact historical bare-answer shape.
func FormatSubagentRunResult(answer string, run *SubagentRun, failed bool, notes ...string) string {
	answer = GuardSubagentHostDecisionText(answer)
	if run == nil || run.Ref == "" {
		return answer
	}
	if failed {
		out := FormatSubagentOutcome(SubagentOutcome{Ref: run.Ref, Status: SubagentOutcomeFailed, FinalAnswer: answer})
		return strings.Replace(out, "Subagent reference: "+run.Ref, "Subagent reference (failed): "+run.Ref, 1)
	}
	guidance := FormatSubagentReference(run)
	if _, rest, ok := strings.Cut(guidance, "\n"); ok {
		guidance = rest
	} else {
		guidance = ""
	}
	out := FormatSubagentOutcome(SubagentOutcome{Ref: run.Ref, Status: SubagentOutcomeCompleted})
	if guidance != "" {
		out += "\n\n" + guidance
	}
	for _, note := range notes {
		if note = strings.TrimSpace(note); note != "" {
			out += "\n\n" + note
		}
	}
	if answer != "" {
		out += "\n\nFinal answer:\n" + answer
	}
	return out
}
