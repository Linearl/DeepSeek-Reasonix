package agent

import (
	"fmt"
	"sort"
	"strings"

	"reasonix/internal/event"
	"reasonix/internal/i18n"
	"reasonix/internal/provider"
)

// stormBreakThreshold is how many times in a row the same tool may fail the same
// way before the loop stops echoing the raw error back and instead returns a
// directive to change approach. Two natural self-corrections are healthy; the
// third identical failure is a death-spiral — the dominant case being a tool call
// whose arguments are truncated at the output-token ceiling, which the model then
// re-emits (re-worded but still over-long), truncating the same way again.
const stormBreakThreshold = 3

// repeatSuccessBreakThreshold is how many identical write-like successes the
// agent allows before refusing another copy in the same user turn. Two gives the
// model room for a natural self-correction; the third repeat is usually a
// no-op/write loop and should be redirected to a different tool or final answer.
const repeatSuccessBreakThreshold = 2

const (
	// todoProgressNudgeRounds is the first adaptive checkpoint. The host asks
	// the model to reassess, but keeps the turn alive so it can recover.
	todoProgressNudgeRounds = 8
	// maxTodoStallRounds is the second Goal-only adaptive checkpoint. It resets
	// the intervention epoch and asks for a new plan without ending the run.
	maxTodoStallRounds = 16
)

// reReadGuidanceMessage is the short-context arm of the same checkpoint as
// todoProgressNudgeMessage (task 60, point 3). Trace-as-State (arXiv 2609.02702) puts the
// earlier reasoning in front of the question instead of the question alone, and the
// material that reasoning was about is still on disk -- a fold discards conversation,
// not files. So the cheapest recovery from a stall is to re-open what was already read,
// with the previous thinking in hand, rather than to explore somewhere new.
func reReadGuidanceMessage(rounds int, files []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Host progress check: %d tool-call rounds brought no new completion, unique read, command, or mutation. Before trying another angle, re-read what you already gathered -- on a stall the answer is usually in the material, not in the direction.\n", rounds)
	if len(files) > 0 {
		b.WriteString("\nRecently read (re-open the relevant ones before exploring further):\n")
		for _, file := range files {
			fmt.Fprintf(&b, "- %s\n", file)
		}
	}
	b.WriteString("\nCarry your earlier reasoning into the re-read: what you concluded then is still available to you, including the dead ends -- that is what makes a second pass cheap.")
	return b.String()
}

// recentReadPaths returns the identities of the files this run read most recently,
// de-duplicated and capped. It is best-effort guidance for the message above; an empty
// result just means the stall happened before any read landed.
// contextIsShort reports whether the visible context is still under the explicit-fold
// floor. Below it, re-reading is cheaper than folding, which is what routes a stall to
// the re-read guidance instead of the fold guidance (task 60, point 3).
func (a *Agent) contextIsShort() bool {
	threshold := a.compactTrigger()
	if threshold <= 0 {
		return true
	}
	return a.visibleContextTokens(a.snapshotExplicitCompression()) < int(float64(threshold)*explicitCompressFloorRatio)
}

func (a *Agent) recentReadPaths(limit int) []string {
	if a == nil || len(a.reads.visible) == 0 || limit <= 0 {
		return nil
	}
	seen := make(map[string]bool, len(a.reads.visible))
	paths := make([]string, 0, limit)
	for _, delivery := range a.reads.visible {
		identity := strings.TrimSpace(delivery.source.Identity)
		if identity == "" || seen[identity] {
			continue
		}
		seen[identity] = true
		paths = append(paths, identity)
	}
	sort.Strings(paths)
	if len(paths) > limit {
		paths = paths[:limit]
	}
	return paths
}
func todoProgressNudgeMessage(rounds int) string {
	return fmt.Sprintf("Host progress check: the current todo has produced no new completion, unique read, command, or mutation for %d tool-call rounds. Reassess before using more tools: sign off the current item if it is done, narrow the remaining work without replacing the active item, or explain/ask about a real blocker. Do not repeat reads, commands, or writes just to reset this guard.", rounds)
}

// loopGuardBlockErrMsg is the errMsg carried by a repeat-success loop-guard
// block. applyStormBreaker matches it to arm the final-readiness loop-guard
// pass, since that guard also invites the model to report the blocker.
const loopGuardBlockErrMsg = "blocked by loop guard"

// applyStormBreaker detects a run of zero-progress turns and, past the
// threshold, rewrites the model-facing result (results[0]) into a directive to
// change approach. Two detectors, because a stuck model varies its retries two
// ways. The signature detector keys on each call's (tool, error/blocker) — not
// its args — since a stuck model reworks the arguments cosmetically while
// hitting the same host refusal or failure (see the stormSig field doc). The
// streak detector counts consecutive turns in which every call was blocked,
// regardless of shape: rotating tools, reordering a batch, or a blocker whose
// text varies per attempt escapes the signature but is still zero progress —
// only a host refusal (not a plain error) proves that, so the streak requires
// blocked outcomes. Any success resets both. When a guard fires — or when a
// call in the batch was already blocked by the per-call repeat-success guard —
// the final-readiness loop-guard pass is armed so the model may report the
// blocker (see loopGuardAllowsFinal). A positive maxSteps value remains a hard
// backstop; with no explicit step or budget limit this guidance alone does not
// guarantee a finite run.
func (a *Agent) applyStormBreaker(calls []provider.ToolCall, outcomes []toolOutcome, receiptMark int) intervention {
	allBlocked := len(outcomes) > 0
	for _, outcome := range outcomes {
		if !outcome.blocked {
			allBlocked = false
			break
		}
	}
	if allBlocked {
		a.turn.blockedTurnStreak++
		// Track whether one refusal reason keeps coming back. Rotating tools
		// while the same constraint denies every call is not the model retrying
		// a fixed call — it cannot change approach its way out (task 171). The
		// signature is only computed for fully-blocked rounds, so the mixed and
		// successful paths stay allocation-free.
		refusal := blockedRefusalSignature(outcomes)
		switch {
		case refusal == "":
			a.turn.blockedConstraintSig, a.turn.blockedConstraintStreak = "", 0
		case refusal == a.turn.blockedConstraintSig:
			a.turn.blockedConstraintStreak++
		default:
			a.turn.blockedConstraintSig, a.turn.blockedConstraintStreak = refusal, 1
		}
	} else {
		a.turn.blockedTurnStreak = 0
		a.turn.blockedConstraintSig, a.turn.blockedConstraintStreak = "", 0
	}
	for _, outcome := range outcomes {
		if outcome.blocked && outcome.errMsg == loopGuardBlockErrMsg {
			a.armLoopGuardPass(receiptMark)
			break
		}
	}

	sig, ok := batchStormSignature(calls, outcomes)
	switch {
	case !ok:
		a.turn.stormSig, a.turn.stormCount = "", 0
	case sig != a.turn.stormSig:
		a.turn.stormSig, a.turn.stormCount = sig, 1
	default:
		a.turn.stormCount++
	}
	stormHit := ok && a.turn.stormCount >= stormBreakThreshold
	if !stormHit && consecutiveNormalizedFailure(calls, outcomes, &a.turn.loop) {
		stormHit = true
		a.turn.stormCount = max(a.turn.stormCount, 2)
	}
	streakHit := allBlocked && a.turn.blockedTurnStreak >= stormBreakThreshold
	// A streak whose rounds were all refused for the SAME reason is a
	// constraint surface: the host denies every legitimate call, the state is
	// per-turn, and the next user message clears it. Telling the model to
	// "change approach" there burns rounds and ends in a deadlock (task 171).
	constraintSurface := streakHit && a.turn.blockedConstraintStreak >= stormBreakThreshold
	if !stormHit && !streakHit {
		return intervention{}
	}

	const blockedAdvice = "Change approach: do not keep retrying a blocked tool by changing the tool, command, or arguments. Respect the permission, plan-mode, hook, or loop-guard blocker; use an already-allowed tool, ask the user for the specific approval or choice if appropriate, or explain the blocker in your final answer."
	var guard, detail string
	guardVerdict := verdictRedirect
	if stormHit {
		subject := fmt.Sprintf("%q", calls[0].Name)
		short := calls[0].Name
		if len(calls) > 1 {
			subject = fmt.Sprintf("this batch of %d tool calls", len(calls))
			short = fmt.Sprintf("a batch of %d calls", len(calls))
		}
		anyBlocked := false
		for _, outcome := range outcomes {
			if outcome.blocked {
				anyBlocked = true
				break
			}
		}
		action := "failed"
		advice := "Change approach: if an argument is being truncated, write less in one call and split the work into several smaller calls; otherwise fix the arguments, use a different tool, or explain the blocker in your final answer."
		parameterErrors := true
		hasSchemaError := false
		for _, outcome := range outcomes {
			validation := strings.HasPrefix(outcome.errMsg, "argument_validation:")
			schema := validation && strings.HasSuffix(outcome.errMsg, ":schema")
			parameterErrors = parameterErrors && validation && !schema
			hasSchemaError = hasSchemaError || schema
		}
		if parameterErrors {
			advice = "Stop repeating the same invalid input. Correct the parameters using the reported contract and retry; valid calls still undergo normal permission checks. If you cannot construct a valid call, report that tool argument generation failed and state what remains unfinished."
		} else if hasSchemaError {
			advice = "A host tool schema is invalid. Rewriting arguments cannot repair that configuration error; report it and the unfinished work. Handle any other failures according to their individual diagnostics."
		}
		if anyBlocked {
			action = "been blocked or failed"
			advice = blockedAdvice
		}
		guard = fmt.Sprintf(
			"[loop guard] %s has now %s %d times in a row with the same host response. Re-sending it — even with the wording changed — will not help: the calls keep hitting the same outcome. %s",
			subject, action, a.turn.stormCount, advice)
		detail = fmt.Sprintf(
			"loop guard: %s hit the same host response %d× — nudging the model to change approach",
			short, a.turn.stormCount)
	} else if constraintSurface {
		guard = fmt.Sprintf(
			"[loop guard] every tool call in the last %d rounds was blocked by the same host constraint (%s). This is a constraint surface, not a repeated call: retrying, reordering, or switching tools cannot clear it, and waiting for the user's next message does — the constraint and budget state is per user turn. Report what is blocked, what is already established, and what remains unfinished, then stop.",
			a.turn.blockedTurnStreak, a.turn.blockedConstraintSig)
		detail = fmt.Sprintf(
			"loop guard: %d rounds refused by one constraint surface (%s) — asking the model to report and stop",
			a.turn.blockedTurnStreak, a.turn.blockedConstraintSig)
		guardVerdict = verdictLand
	} else {
		guard = fmt.Sprintf(
			"[loop guard] every tool call in the last %d turns has been blocked by the host (permission, plan mode, hook, or loop guard). Switching tools, reordering calls, or rewording arguments will not help while the blockers stand. %s",
			a.turn.blockedTurnStreak, blockedAdvice)
		detail = fmt.Sprintf(
			"loop guard: every tool call blocked %d turns in a row — nudging the model to change approach",
			a.turn.blockedTurnStreak)
	}
	// Before a guard stops the turn, persist the model's latest text: the
	// transcript may be mid-turn, and a crash would otherwise lose the handoff
	// the user is waiting for (task 171).
	a.writePendingHandoff(constraintSurface, a.turn.blockedConstraintSig)
	a.armLoopGuardPass(receiptMark)
	return intervention{
		verdict:  guardVerdict,
		guidance: guard,
		notice:   noticeFor(event.NoticeCodeLoopGuard, event.LevelInfo, loopGuardNoticeText(), detail),
	}
}

// blockedRefusalSignature reduces a fully-blocked batch to the reason the host
// refused it, normalised so the SAME constraint read through different tools
// compares equal. Empty means "not a uniform refusal" — mixed outcomes, or a
// refusal this host does not classify.
func blockedRefusalSignature(outcomes []toolOutcome) string {
	if len(outcomes) == 0 {
		return ""
	}
	reasons := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		if !outcome.blocked {
			return ""
		}
		reasons = append(reasons, refusalReason(outcome.errMsg))
	}
	sort.Strings(reasons)
	return strings.Join(reasons, "|")
}

// refusalReason names the constraint behind a host refusal. Tool names are
// dropped on purpose: the constraint, not the tool, is what stays in the way
// across a blocked run.
func refusalReason(errMsg string) string {
	msg := strings.ToLower(firstLine(strings.TrimSpace(errMsg)))
	switch {
	case strings.Contains(msg, "establish a concrete todo"):
		return "first_writer_contract"
	case strings.Contains(msg, "evidence required"), strings.Contains(msg, "has not seen its current content"):
		return "write_evidence"
	case strings.Contains(msg, "plan mode"), strings.Contains(msg, "plan-mode"):
		return "plan_mode"
	case strings.Contains(msg, "permission"):
		return "permission"
	case strings.Contains(msg, "hook"):
		return "hook"
	case strings.Contains(msg, "loop guard"):
		return "loop_guard"
	case strings.Contains(msg, "forbid"), strings.Contains(msg, "not allowed"), strings.Contains(msg, "forbidden"):
		return "forbidden"
	}
	return msg
}

func loopGuardNoticeText() string {
	return i18n.M.LoopGuard
}

// batchStormSignature returns a per-turn fixation signature — each call's
// (name, error/blocker) in order — and ok=true only when every call errored or
// was blocked. ok=false (any success) means the turn made progress, so the
// caller resets the counter. Keying on the host response rather than the args is
// deliberate: a stuck model reworks the arguments while hitting the same
// response, so identical-args matching would miss the loop.
func batchStormSignature(calls []provider.ToolCall, outcomes []toolOutcome) (string, bool) {
	if len(calls) == 0 {
		return "", false
	}
	var sb strings.Builder
	for i := range calls {
		if outcomes[i].errMsg == "" {
			return "", false
		}
		name := calls[i].Name
		if calls[i].ResolvedName != "" {
			name = calls[i].ResolvedName
		}
		sb.WriteString(name)
		sb.WriteByte(0)
		sb.WriteString(errorCategory(name, outcomes[i].errMsg))
		sb.WriteByte(0)
	}
	return sb.String(), true
}
