package agent

import (
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"strings"
)

func (a *Agent) boundProviderVisibleResult(raw, toolName, callID, args string) (body, notice, original string) {
	summarized := summarizeCIOutput(raw)
	body, notice = truncateToolOutputFor(summarized, toolName, callID)
	deduped := a.dedupeProviderVisibleResult(toolName, callID, args, raw, body)
	if deduped != body {
		original = raw
	}
	body = deduped
	if summarized != raw || notice != "" {
		original = raw
	}
	return body, notice, original
}

// dedupeExemptTools lists time-sensitive status tools whose provider-visible
// results never deduplicate (task 214). They poll mutable host state: two
// identical readings a few minutes apart mean "nothing changed since last
// poll", which is exactly the incremental signal the model is asking for —
// not a wasted repeat. Their outputs are small status summaries, so exempting
// them does not reopen the token-leak the deduper exists to prevent.
var dedupeExemptTools = map[string]bool{
	"read_collab_status": true,
}

// dedupeFingerprint is the duplicate key: tool name + canonicalized call
// arguments + the raw result (task 976). Keying on the result text alone let
// two different writes collide whenever their acks were identical — a
// todo_write ack only carries counts, so distinct ops/content with equal
// counts produced one fingerprint and every later write was falsely reported
// as a duplicate of the first (0528 install regression, task 972 fix 3
// exposure). Arguments split those collisions; the result half keeps
// same-arguments reads distinct when the underlying data changed. The map
// lives in turnLoopState, which is zeroed per user turn (beginRunTurn) and on
// compaction, so the key already carries a turn boundary.
//
// sha512 (truncated to 12 bytes, same strength as before): this is a
// dedupe fingerprint, not password hashing — sha256 here tripped GitHub
// code-scanning rule go/weak-sensitive-data-hashing (#97, task 985).
func dedupeFingerprint(toolName, args, raw string) string {
	sum := sha512.Sum512([]byte(toolName + "\x00" + canonicalToolArgs(args) + "\x00" + raw))
	return hex.EncodeToString(sum[:12])
}

func (a *Agent) dedupeProviderVisibleResult(toolName, callID, args, raw, visible string) string {
	if a == nil || strings.TrimSpace(raw) == "" {
		return visible
	}
	// Task 216: error results are never deduplicated. A retried call that
	// fails with the identical text must still surface that failure —
	// swallowing the repeated error removes the "still wrong" signal and
	// leaves the model unable to self-correct.
	if strings.HasPrefix(raw, "error:") {
		return visible
	}
	// Task 214: time-sensitive status tools skip deduplication entirely.
	if dedupeExemptTools[toolName] {
		return visible
	}
	fp := dedupeFingerprint(toolName, args, raw)
	prev, seen := a.turn.loop.rememberFingerprint(fp, callID)
	if seen && prev != callID {
		// Task 976, self-heal: a deduplicated todo_write while the host panel
		// is empty means "the previous result still stands" would be a lie —
		// the model would stop retrying a write that never landed, and the
		// write gate (which reads the same empty panel) would keep blocking
		// every follow-up tool: the 0528 self-lock. Panel empty = hand back
		// the real result of this call and let the model proceed.
		if toolName == "todo_write" && len(a.CurrentTaskTodoState()) == 0 {
			return visible
		}
		// Task 972, fix 3: a deduplicated repeat must still answer the
		// question the call asked. "Omitted" alone left the model unable to
		// see whether its write landed and forced a probe re-send — so the
		// reply now says the previous result still stands and echoes a
		// bounded summary of that identical original.
		return fmt.Sprintf("duplicate tool result omitted (identical to call_id=%s, fingerprint=%s). 已去重，上次结果仍有效；上次结果：%s Full original remains locally; page it with session:tool_result if needed.%s", prev, fp, duplicateResultSummary(raw), a.duplicateStateSnapshot())
	}
	return visible
}

// duplicateResultSummary echoes a bounded, single-line excerpt of a
// deduplicated result (task 972, fix 3) so the model sees what the identical
// earlier call returned without re-sending it.
func duplicateResultSummary(raw string) string {
	const limit = 300
	s := strings.Join(strings.Fields(raw), " ")
	runes := []rune(s)
	if len(runes) > limit {
		s = string(runes[:limit]) + "…"
	}
	return s
}

// duplicateStateSnapshot appends the host's current plan to a deduplicated
// result: repeating the call cannot help when the list has already moved on
// (#39 P1). It stays empty when the turn has no todos so unrelated duplicates
// remain short.
func (a *Agent) duplicateStateSnapshot() string {
	todos := a.CurrentTaskTodoState()
	if len(todos) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(" Current plan: ")
	for i, item := range todos {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%d. [%s] %s", i+1, item.Status, item.Content)
	}
	b.WriteString(".")
	return b.String()
}
