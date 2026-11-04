package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

const (
	maxCurateDropEntries = 32
	maxCurateNoteBytes   = 240
)

// CurateContextTool is the tl1378 fine-grained curation tier: replace
// individual already-processed tool results with one-line notes instead of
// folding the whole range into a summary. Registered only while
// experimental_curate_context is on (ui_interact precedent: with the switch
// off the tool does not exist in the registry at all).
type CurateContextTool struct{}

func (CurateContextTool) Name() string { return "curate_context" }

func (CurateContextTool) Description() string {
	return "Replace already-processed tool results with one-line notes, leaving every result you do not name byte-for-byte. This is the fine-grained alternative to compress: use it when individual large results are no longer needed but the conversation is mid-topic and folding is rate-limited away or would discard context you still need. Cite each result by the host receipt id it ends with (`[receipt r_…]`); write the note as one line naming what the result established, so a later re-read of the note is enough. Only results from completed turns qualify — the host refuses entries from the active turn. The host rate-limits curations the same way it rate-limits folds: replacing content still invalidates the prompt cache from the earliest replaced entry onward, so batch several drops into one call instead of curating repeatedly."
}

func (CurateContextTool) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"additionalProperties":false,
"properties":{
  "drop":{"type":"array","minItems":1,"maxItems":32,"items":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","minLength":1,"maxLength":128,"description":"The host receipt id the tool result ends with, e.g. r_1a2b3c4d."},"note":{"type":"string","minLength":1,"maxLength":240,"description":"One line naming what the result established; it replaces the result verbatim."}},"required":["id","note"]},"description":"Tool results to replace with their notes. Unnamed results stay verbatim."}
},
"required":["drop"]
}`)
}

// ReadOnly is true in the permission/workspace sense: curate changes only the
// owning Agent's context projection. The batcher forces it into a serial lane
// via the safety table (ConcurrentSafe=false), matching compress.
func (CurateContextTool) ReadOnly() bool { return true }

func (CurateContextTool) PlanModeSafe() bool { return true }

func (CurateContextTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var request struct {
		Drop []tool.CurateDrop `json:"drop"`
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return "", fmt.Errorf("invalid curate_context args: %w", err)
	}
	if len(request.Drop) == 0 {
		return "", fmt.Errorf("curate_context: drop must name at least one tool result")
	}
	if len(request.Drop) > maxCurateDropEntries {
		return "", fmt.Errorf("curate_context: drop exceeds %d entries", maxCurateDropEntries)
	}
	for i := range request.Drop {
		request.Drop[i].ID = strings.TrimSpace(request.Drop[i].ID)
		request.Drop[i].Note = strings.TrimSpace(request.Drop[i].Note)
		if request.Drop[i].ID == "" {
			return "", fmt.Errorf("curate_context: drop[%d].id must not be empty", i)
		}
		if request.Drop[i].Note == "" {
			return "", fmt.Errorf("curate_context: drop[%d].note must not be empty", i)
		}
		if len(request.Drop[i].Note) > maxCurateNoteBytes {
			return "", fmt.Errorf("curate_context: drop[%d].note exceeds %d bytes", i, maxCurateNoteBytes)
		}
	}
	curator, ok := tool.ContextCuratorFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("curate_context is unavailable outside an active agent session")
	}
	result, err := curator.CurateContext(ctx, tool.CurateRequest{Drop: request.Drop})
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode curate_context result: %w", err)
	}
	return string(out), nil
}
