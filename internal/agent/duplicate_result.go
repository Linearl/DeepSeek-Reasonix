package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

func (a *Agent) boundProviderVisibleResult(raw, toolName, callID string) (body, notice, original string) {
	summarized := summarizeCIOutput(raw)
	body, notice = truncateToolOutputFor(summarized, toolName, callID)
	deduped := a.dedupeProviderVisibleResult(toolName, callID, raw, body)
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

func (a *Agent) dedupeProviderVisibleResult(toolName, callID, raw, visible string) string {
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
	sum := sha256.Sum256([]byte(raw))
	fp := hex.EncodeToString(sum[:12])
	prev, seen := a.turn.loop.rememberFingerprint(fp, callID)
	if seen && prev != callID {
		return fmt.Sprintf("duplicate tool result omitted (identical to call_id=%s, fingerprint=%s). Full original remains locally; page it with session:tool_result if needed.%s", prev, fp, a.duplicateStateSnapshot())
	}
	return visible
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
