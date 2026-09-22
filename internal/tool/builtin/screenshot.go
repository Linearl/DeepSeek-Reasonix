package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"reasonix/internal/sandbox"
	"reasonix/internal/tool"
)

func init() { tool.RegisterBuiltin(screenshotTool{}) }

// screenshotTool captures a desktop window to a PNG file — the evidence
// primitive for agent-run UI verification (task 233, batch 1). Verification
// items like "does the dialog render" or "does the switch-back look right"
// need a picture the agent can take itself; the capture is Windows-only for
// now (PrintWindow with full-render content), and other platforms fail with
// an explicit error instead of silently degrading.
type screenshotTool struct {
	roots   []string
	rootSet *sandbox.WritableRootSet
	guard   SessionDataGuard
	managed ManagedConfigPaths
	workDir string
}

func (screenshotTool) Name() string { return "screenshot" }

func (screenshotTool) Description() string {
	return "Capture a desktop window to a PNG file — the evidence primitive for agent-run UI verification (task 233). Captures the Reasonix window by default; pass window_title (case-insensitive substring of a visible top-level window) to capture a dialog or any other window. The PNG is written to output (relative paths resolve against the workspace). Read-only on the captured window; writing the file still passes the normal write-permission checks."
}

func (screenshotTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"output":{"type":"string","description":"PNG file path to write (e.g. evidence/210-dialog.png)."},"window_title":{"type":"string","description":"Case-insensitive substring of a visible top-level window title. Omit to capture the Reasonix window itself."}},"required":["output"]}`)
}

func (screenshotTool) ReadOnly() bool { return false }

func (e screenshotTool) DeclareWriteAccess(args json.RawMessage) (tool.WriteAccessDeclaration, error) {
	return declareFilePathWriteAccess(e.workDir, args)
}

func (e screenshotTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Output      string `json:"output"`
		WindowTitle string `json:"window_title"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Output == "" {
		return "", fmt.Errorf("output is required (PNG file path to write)")
	}
	p.Output = resolveIn(e.workDir, p.Output)
	if err := confineWrite(ctx, effectiveWriteRoots(ctx, e.rootSet, e.roots), e.guard, e.managed, p.Output); err != nil {
		return "", err
	}
	w, h, title, err := captureWindowScreenshot(p.Output, p.WindowTitle)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("captured %q (%dx%d px) to %s", title, w, h, p.Output), nil
}
