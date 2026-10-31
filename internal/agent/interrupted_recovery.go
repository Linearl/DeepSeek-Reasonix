package agent

import (
	"fmt"
	"html"
	"slices"
	"strings"

	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

const interruptedRecoveryTag = "interrupted-turn-recovery"

const (
	maxRecoveryTools = 24
	maxRecoveryFiles = 8
	maxRecoveryValue = 240
)

// Outcome-unknown interruption forms (tl1278④). Every pending record lands in
// exactly one of them, and the recovery summary copy distinguishes the two:
// returned_unverified means the call ended (its result was produced/streamed)
// but the result could not be confirmed persisted; no_response means the call
// was issued and never returned.
const (
	outcomeFormReturnedUnverified = "returned_unverified"
	outcomeFormNoResponse         = "no_response"
)

// pendingInterruptedRecovery returns the newest unconsumed recovery handoff.
// A later real user turn consumes older handoffs implicitly, so the persisted
// LocalOnly record never needs an in-place mutation that could churn history.
func (a *Agent) transcriptInterruptedRecovery() *provider.InterruptedTurnRecovery {
	if a == nil || a.sess.conversation == nil {
		return nil
	}
	msgs := a.sess.conversation.Snapshot()
	for _, v := range slices.Backward(msgs) {
		m := v
		if m.LocalOnly && m.InterruptedTurn != nil && m.InterruptedTurn.Pending {
			copy := *m.InterruptedTurn
			if copy.FailureDiagnostic != nil {
				diagnostic := *copy.FailureDiagnostic
				copy.FailureDiagnostic = &diagnostic
			}
			copy.WriteChecks = append([]provider.WriteRecoveryCheck(nil), copy.WriteChecks...)
			copy.SatisfiedWrites = append([]provider.InterruptedToolSummary(nil), copy.SatisfiedWrites...)
			copy.CompletedTools = append([]provider.InterruptedToolSummary(nil), copy.CompletedTools...)
			copy.InterruptedTools = append([]string(nil), copy.InterruptedTools...)
			copy.NotStartedTools = append([]provider.InterruptedToolSummary(nil), copy.NotStartedTools...)
			copy.UnknownTools = append([]provider.InterruptedToolSummary(nil), copy.UnknownTools...)
			a.reclassifySideEffectFreeUnknowns(&copy)
			return &copy
		}
		if IsUserAuthoredTurnMessage(m) {
			return nil
		}
	}
	return nil
}

// interruptedRecoveryBlock is appended only at the mutable user-message tail.
// It contains no raw tool arguments, results, assistant text, or reasoning.
func interruptedRecoveryBlock(r *provider.InterruptedTurnRecovery) string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<%s>\n", interruptedRecoveryTag)
	if len(r.UserConfirmedTools) > 0 {
		b.WriteString("The previous turn was interrupted. Preserve the stated provenance: user-confirmed effects are attestations, not tool results.\n")
	} else {
		b.WriteString("The previous turn was interrupted. Treat these as host-verified recovery facts, not as a new task.\n")
	}
	if len(r.CompletedTools) == 0 {
		b.WriteString("completed_tools: none\n")
	} else {
		b.WriteString("completed_tools:\n")
		for i, tool := range r.CompletedTools {
			if i >= maxRecoveryTools {
				fmt.Fprintf(&b, "- ... %d additional completed tool pair(s) omitted\n", len(r.CompletedTools)-i)
				break
			}
			fmt.Fprintf(&b, "- %s", html.EscapeString(strings.TrimSpace(tool.Name)))
			if len(tool.Files) > 0 {
				files := tool.Files
				if len(files) > maxRecoveryFiles {
					files = files[:maxRecoveryFiles]
				}
				clipped := make([]string, 0, len(files))
				for _, file := range files {
					clipped = append(clipped, html.EscapeString(clipRecoveryValue(file)))
				}
				fmt.Fprintf(&b, " files=%s", strings.Join(clipped, ","))
			}
			if tool.Added != 0 || tool.Removed != 0 {
				fmt.Fprintf(&b, " diff=+%d/-%d", tool.Added, tool.Removed)
			}
			b.WriteByte('\n')
		}
	}
	if len(r.InterruptedTools) == 0 {
		b.WriteString("interrupted_tools: none\n")
	} else {
		b.WriteString("interrupted_tools: ")
		for i, name := range r.InterruptedTools {
			if i >= maxRecoveryTools {
				fmt.Fprintf(&b, ", ... %d additional call(s) omitted", len(r.InterruptedTools)-i)
				break
			}
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(html.EscapeString(strings.TrimSpace(name)))
		}
		b.WriteByte('\n')
	}
	writeRecoveryChecks(&b, r.WriteChecks)
	writeRecoveryCalls(&b, "write_postconditions_satisfied_do_not_repeat", r.SatisfiedWrites)
	writeRecoveryCalls(&b, "not_started_tools", r.NotStartedTools)
	// tl1278③: outcome-unknown entries are graded by the call's read-only class
	// instead of one blanket「inspect everything」instruction. Read-only calls
	// cannot have landed an external effect, so re-running them is safe; write
	// class keeps the strict inspect-first copy. Each entry carries its tl1278④
	// interruption form inline (form=returned_unverified / form=no_response).
	readOnlyUnknown, writeUnknown := splitUnknownByReadOnly(r.UnknownTools)
	writeUnknownRecoveryCalls(&b, "outcome_unknown_tools", writeUnknown)
	writeUnknownRecoveryCalls(&b, "outcome_unknown_tools_read_only_retry_safe", readOnlyUnknown)
	writeRecoveryCalls(&b, "failed_tools", r.FailedTools)
	writeRecoveryCalls(&b, "user_confirmed_effects_do_not_repeat", r.UserConfirmedTools)
	if r.DroppedPartialText || r.DroppedPartialReasoning {
		b.WriteString("unsafe_partial_output: excluded from model context")
		if r.DroppedPartialText && r.DroppedPartialReasoning {
			b.WriteString(" (assistant text and reasoning)\n")
		} else if r.DroppedPartialReasoning {
			b.WriteString(" (reasoning)\n")
		} else {
			b.WriteString(" (assistant text)\n")
		}
	}
	// tl1278③④: the tail instruction names both gradings — read-only vs write
	// class, and returned_unverified vs no_response. Section labels are not
	// repeated here on purpose: an empty graded section must leave no
	// outcome-unknown token in the block at all (the task-433 reclassification
	// contract asserts exactly that).
	b.WriteString("Before continuing, inspect the current workspace and prior completed tool results. Do not blindly repeat completed writes. Outcome-unknown calls are graded: read-only calls cannot have caused external effects — re-running them is safe and no effect inspection is needed. Write-class calls (including legacy entries without grading): first inspect side effects and external state; never assume they did not run. Only retry after verifying it is safe. form=returned_unverified means the call ended and its result may not have been persisted — for read-only tools just re-run it, for write-class tools inspect external state before retrying. form=no_response means the call was issued but never returned; treat its outcome as unknown. Calls marked not_started may be planned again with complete arguments.\n")
	fmt.Fprintf(&b, "</%s>", interruptedRecoveryTag)
	return b.String()
}

func withInterruptedRecovery(input string, r *provider.InterruptedTurnRecovery) string {
	block := interruptedRecoveryBlock(r)
	if block == "" {
		return input
	}
	return block + "\n\n" + input
}

// reclassifySideEffectFreeUnknowns applies the task-433 whitelist to the
// prompt-tail handoff copy: an outcome-unknown entry whose call could not have
// produced an external effect is downgraded to not_started, so the model is
// told it may re-plan the call immediately instead of being told to inspect
// effects that cannot exist (自动判「未生效」→ 快速续轮). Entries that stay
// outcome-unknown are annotated for the graded summary copy (tl1278③④): the
// read-only flag comes from the durable record (same source the permission and
// concurrency consumers read) with the safety registry as the name-only
// fallback, and the outcome form distinguishes「已结束未证实」from「发出未返回」.
// The durable handoff message itself is never rewritten — only the returned
// copy is.
func (a *Agent) reclassifySideEffectFreeUnknowns(r *provider.InterruptedTurnRecovery) {
	if a == nil || a.sess.conversation == nil || r == nil || len(r.UnknownTools) == 0 {
		return
	}
	moved := false
	kept := r.UnknownTools[:0]
	for _, c := range r.UnknownTools {
		if a.interruptedSummarySideEffectFree(c) {
			r.NotStartedTools = append(r.NotStartedTools, provider.InterruptedToolSummary{ID: c.ID, Name: c.Name})
			moved = true
			continue
		}
		c.ReadOnly, c.OutcomeForm = a.annotateInterruptedUnknown(c)
		kept = append(kept, c)
	}
	if moved {
		r.UnknownTools = kept
	}
}

// annotateInterruptedUnknown resolves the tl1278 grading annotations for one
// outcome-unknown entry. The durable record wins (it carries the per-call
// read-only stamp taken from the resolved tool, and the finished state that
// separates the two interruption forms); a name-only legacy entry falls back
// to the static safety registry, failing closed to write-class (bash without
// arguments stays strict — 宁可多核实，不可误放).
func (a *Agent) annotateInterruptedUnknown(c provider.InterruptedToolSummary) (bool, string) {
	if record := a.sess.conversation.toolRecoveryRecord(c.ID); record != nil {
		return record.ReadOnly, interruptedUnknownOutcomeForm(*record)
	}
	return tool.SafetyOfName(c.Name).ReadOnly, ""
}

// interruptedUnknownOutcomeForm names which of the two interruption shapes a
// pending record is in (tl1278④). started/running with no finish stamp is the
// genuine「发出未返回」; anything else pending has run through the loop's
// finish path (state unknown / failed-effect-unknown), i.e. the call ended and
// its result was produced but could not be confirmed persisted.
func interruptedUnknownOutcomeForm(r provider.ToolCallRecord) string {
	if (r.State == provider.ToolRunStarted || r.State == provider.ToolRunRunning) && r.FinishedAt == 0 {
		return outcomeFormNoResponse
	}
	return outcomeFormReturnedUnverified
}

// interruptedSummarySideEffectFree judges one handoff entry. The durable
// record, when available, supplies the canonical tool and the real arguments
// (a bash probe needs its command text); without a record the name alone
// decides and bash fails closed (unknown args → keep the manual review).
func (a *Agent) interruptedSummarySideEffectFree(c provider.InterruptedToolSummary) bool {
	if record := a.sess.conversation.toolRecoveryRecord(c.ID); record != nil {
		if interruptedCallSideEffectFree(record.Identity.CanonicalTool, record.Arguments) {
			return true
		}
	}
	return interruptedCallSideEffectFree(c.Name, nil)
}

func clipRecoveryValue(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= maxRecoveryValue {
		return value
	}
	return string(runes[:maxRecoveryValue]) + "…"
}

func writeRecoveryCalls(b *strings.Builder, label string, calls []provider.InterruptedToolSummary) {
	if len(calls) == 0 {
		return
	}
	fmt.Fprintf(b, "%s:\n", label)
	for i, call := range calls {
		if i >= maxRecoveryTools {
			fmt.Fprintf(b, "- ... %d omitted\n", len(calls)-i)
			break
		}
		fmt.Fprintf(b, "- %s id=%s\n", html.EscapeString(clipRecoveryValue(call.Name)), html.EscapeString(clipRecoveryValue(call.ID)))
	}
}

// splitUnknownByReadOnly grades the outcome-unknown entries for the tl1278③
// summary sections. Unannotated (legacy) entries stay write-class: the strict
// inspect-first copy is the safe default.
func splitUnknownByReadOnly(calls []provider.InterruptedToolSummary) (readOnly, writeClass []provider.InterruptedToolSummary) {
	for _, c := range calls {
		if c.ReadOnly {
			readOnly = append(readOnly, c)
		} else {
			writeClass = append(writeClass, c)
		}
	}
	return readOnly, writeClass
}

// writeUnknownRecoveryCalls renders one graded unknown list with each entry's
// tl1278④ interruption form inline. An empty form (legacy handoff entry) is
// omitted — the tail instruction tells the model to treat ungraded entries as
// write-class unknown.
func writeUnknownRecoveryCalls(b *strings.Builder, label string, calls []provider.InterruptedToolSummary) {
	if len(calls) == 0 {
		return
	}
	fmt.Fprintf(b, "%s:\n", label)
	for i, call := range calls {
		if i >= maxRecoveryTools {
			fmt.Fprintf(b, "- ... %d omitted\n", len(calls)-i)
			break
		}
		fmt.Fprintf(b, "- %s id=%s", html.EscapeString(clipRecoveryValue(call.Name)), html.EscapeString(clipRecoveryValue(call.ID)))
		if call.OutcomeForm != "" {
			fmt.Fprintf(b, " form=%s", call.OutcomeForm)
		}
		b.WriteByte('\n')
	}
}

func writeRecoveryChecks(b *strings.Builder, checks []provider.WriteRecoveryCheck) {
	for i, check := range checks {
		if i >= maxRecoveryTools*maxRecoveryFiles {
			break
		}
		fmt.Fprintf(b, "write_postcondition: id=%s path=%s state=%s\n", html.EscapeString(clipRecoveryValue(check.CallID)), html.EscapeString(clipRecoveryValue(check.Path)), html.EscapeString(clipRecoveryValue(check.State)))
	}
}
