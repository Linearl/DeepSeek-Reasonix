package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

// CompressRangesTool is the tl1419 model-driven ranges fold: the model
// initiates the tl1397 small-segment incremental compression at a natural
// work boundary. Registered only while experimental_model_ranges_compact is
// on (curate_context precedent: with the switch off the tool does not exist
// in the registry at all, so the model-visible surface stays byte-identical
// to the pre-tl1419 baseline). The host executes the fold and enforces the
// flood gate — per-fold savings floor + sliding frequency window replace the
// whole-range compress cooldown; every rejection comes back with the failing
// gate and its billing-ruler readings so the model can decide retry vs wait.
type CompressRangesTool struct{}

func (CompressRangesTool) Name() string { return "compress_ranges" }

func (CompressRangesTool) Description() string {
	return "Fold the recent un-summarized work segment into one appended running note, keeping everything before it byte-for-byte. This is the incremental lane of compress: it needs no anchor — the host folds exactly the work that accumulated since the newest summary (up to the active turn) — so use it right after a large phase finished or before switching topics, when that stretch is no longer needed verbatim. The host refuses a fold whose measured savings fall under its economics floor or whose frequency would churn the prompt cache too often, and reports the numbers either way; a rejection usually means more work should accumulate first, not that folding is unavailable. Put the facts, decisions, changed files and open questions that must survive into `focus` — the note only carries what the segment needs for the work to resume without re-deriving it."
}

func (CompressRangesTool) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"additionalProperties":false,
"properties":{
  "focus":{"type":"string","maxLength":2000,"description":"Optional guidance about facts, decisions, changed files and open questions the note must preserve."}
}
}`)
}

// ReadOnly is true in the permission/workspace sense: the fold changes only
// the owning Agent's context projection (same contract as compress). The
// agent batcher separately serializes projection-installing calls.
func (CompressRangesTool) ReadOnly() bool { return true }

func (CompressRangesTool) PlanModeSafe() bool { return true }

func (CompressRangesTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var request struct {
		Focus string `json:"focus"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return "", fmt.Errorf("invalid compress_ranges args: %w", err)
		}
	}
	request.Focus = strings.TrimSpace(request.Focus)
	if len(request.Focus) > maxCompressFocusBytes {
		return "", fmt.Errorf("compress_ranges: focus exceeds %d bytes", maxCompressFocusBytes)
	}
	folder, ok := tool.RangesFolderFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("compress_ranges is unavailable outside an active agent session")
	}
	result, err := folder.RangesFoldCompact(ctx, tool.RangesFoldRequest{Focus: request.Focus})
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode compress_ranges result: %w", err)
	}
	return string(out), nil
}
