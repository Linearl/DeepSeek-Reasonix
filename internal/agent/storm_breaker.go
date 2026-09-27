package agent

import (
	"fmt"
	"sort"
	"strings"

	"reasonix/internal/i18n"
	"reasonix/internal/provider"
)

// The storm breaker and the zero-gain progress ladder were the host's two
// no-progress guards (upstream #9766); both were retired with upstream #10223
// (v1.38.8) because they counted conversation rounds and could stop a turn
// after a long run of read-only tool rounds. What remains here are the
// non-stop companions the todo checkpoint and notices still need.

// repeatSuccessBreakThreshold is how many identical write-like successes the
// agent allows before refusing another copy in the same user turn. Two gives the
// model room for a natural self-correction; the third repeat is usually a
// no-op/write loop and should be redirected to a different tool or final answer.
const repeatSuccessBreakThreshold = 2

// stormBreakThreshold is how many consecutive fully-blocked batches arm the
// final-readiness loop-guard pass so the model can report the blocker. The
// storm breaker that used to also inject guidance (and could stop a turn) was
// retired with upstream #10223; this count only grants the report pass and
// never ends a turn.
const stormBreakThreshold = 3

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

// contextIsShort reports whether the visible context is still under the
// explicit-fold floor. Below it, re-reading is cheaper than folding, which is
// what routes a stall to the re-read guidance instead of the fold guidance
// (task 60, point 3).
func (a *Agent) contextIsShort() bool {
	threshold := a.compactTrigger()
	if threshold <= 0 {
		return true
	}
	return a.visibleContextTokens(a.snapshotExplicitCompression()) < int(float64(threshold)*explicitCompressFloorRatio)
}

func todoProgressNudgeMessage(rounds int) string {
	return fmt.Sprintf("Host progress check: the current todo has produced no new completion, unique read, command, or mutation for %d tool-call rounds. Reassess before using more tools: sign off the current item if it is done, narrow the remaining work without replacing the active item, or explain/ask about a real blocker. Do not repeat reads, commands, or writes just to reset this guard.", rounds)
}

// loopGuardBlockErrMsg is the errMsg carried by a per-call repeat guard block
// (see parseToolCall). Kept so blocked results keep their stable text.
const loopGuardBlockErrMsg = "blocked by loop guard"

func loopGuardNoticeText() string {
	return i18n.M.LoopGuard
}

// batchStormSignature returns a per-turn fixation signature — each call's
// (name, error/blocker) in order — and ok=true only when every call errored or
// was blocked. ok=false (any success) means the turn made progress, so the
// caller resets the counter. Keying on the host response rather than the args is
// deliberate: a stuck model reworks the arguments while hitting the same
// response, so identical-args matching would miss the loop. The storm breaker
// that consumed it is retired (#10223); the signature stays for its tests and
// any future observer that wants the same shape.
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
