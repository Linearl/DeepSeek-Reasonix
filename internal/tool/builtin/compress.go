package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

const (
	maxCompressAnchorBytes = 512
	maxCompressFocusBytes  = 2000
)

func init() { tool.RegisterBuiltin(compressContext{}) }

type compressContext struct{}

func (compressContext) Name() string { return "compress" }

func (compressContext) Description() string {
	return "Compress a selected part of the current model-visible conversation without deleting visible history. Use it when the work has reached a natural boundary — a large phase just finished, or the conversation is switching to a different topic — and the history is long enough that its detail no longer needs to stay live; also use it when the user explicitly asks. Do NOT fold a short context: re-reading is cheaper than the detail you would discard, and the host refuses a fold below the floor it reports back. When you do fold, put the facts, decisions and acceptance criteria that must survive into `focus`. Choose `before` to summarize everything before the uniquely matched user turn while keeping that turn and later context, or `after` to summarize from that turn through the last completed turn while keeping the active turn. The anchor must be an exact, unique excerpt from a real user message; use a longer excerpt if the tool reports multiple matches."
}

func (compressContext) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"additionalProperties":false,
"properties":{
  "direction":{"type":"string","enum":["before","after"],"description":"Which side of the anchor user turn to compress."},
  "anchor":{"type":"string","minLength":1,"maxLength":512,"description":"An exact, unique excerpt from one real user message in the current model-visible conversation."},
  "focus":{"type":"string","maxLength":2000,"description":"Optional guidance about facts or decisions the summary must preserve."}
},
"required":["direction","anchor"]
}`)
}

// ReadOnly is true in the permission/workspace sense: compress changes only
// the owning Agent's context projection. The Agent batcher separately forces it
// into a serial lane because projection installation is stateful.
func (compressContext) ReadOnly() bool { return true }

func (compressContext) PlanModeSafe() bool { return true }

func (compressContext) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var request struct {
		Direction string `json:"direction"`
		Anchor    string `json:"anchor"`
		Focus     string `json:"focus"`
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return "", fmt.Errorf("invalid compress args: %w", err)
	}
	request.Direction = strings.TrimSpace(request.Direction)
	request.Anchor = strings.TrimSpace(request.Anchor)
	request.Focus = strings.TrimSpace(request.Focus)
	if request.Direction != "before" && request.Direction != "after" {
		return "", fmt.Errorf("compress: direction must be before or after")
	}
	if request.Anchor == "" {
		return "", fmt.Errorf("compress: anchor must not be empty")
	}
	if len(request.Anchor) > maxCompressAnchorBytes {
		return "", fmt.Errorf("compress: anchor exceeds %d bytes", maxCompressAnchorBytes)
	}
	if len(request.Focus) > maxCompressFocusBytes {
		return "", fmt.Errorf("compress: focus exceeds %d bytes", maxCompressFocusBytes)
	}
	compressor, ok := tool.ContextCompressorFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("compress is unavailable outside an active agent session")
	}
	result, err := compressor.CompressContext(ctx, tool.CompressRequest{
		Direction: request.Direction,
		Anchor:    request.Anchor,
		Focus:     request.Focus,
	})
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode compress result: %w", err)
	}
	return string(out), nil
}
