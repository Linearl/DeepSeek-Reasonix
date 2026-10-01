package agent

import (
	"context"
	"errors"

	"reasonix/internal/baseproc"
	"reasonix/internal/tool"
)

// baseToolOutcome is what a routed base-tool call returned. images and
// execution stay nil by construction: only plain Execute tools route here
// (below), so no structured side channel exists to carry.
type baseToolOutcome struct {
	result    string
	images    []string
	execution *tool.ShellExecution
	err       error
}

// baseToolCall routes ONE resolved tool execution over the resident base
// channel (design §10 S1b / D3), or reports ok=false so the caller walks the
// pre-S1 in-process path unchanged. Every condition fails closed toward the
// local path:
//
//   - no base client wired (switch off — today's world) → local;
//   - inline mode (spawn failed / R1 fallback) → local, byte for byte;
//   - the server does not advertise the tools capability → the client answers
//     ErrNotWired without spending a round trip → local;
//   - runTool is not the registry's own instance (wrapped by path-binding,
//     plan gating, read shadowing, ...) → local: those wrappers are
//     client-side behaviour a subprocess cannot replay;
//   - a rich executor (Read/Detailed/Image) → local: its structured results
//     (read envelopes, shell execution records, image attachments) do not fit
//     v1 ToolCallResult{content, error};
//   - no call id → local (nothing to correlate progress against).
//
// Once the request HAS been sent, every outcome is handled: re-running
// locally could double-execute a side effect, so a channel death fails the
// call instead of retrying it (R1's inline fallback is a spawn/health
// decision, not a mid-call one).
//
// Activation note: the gate is dormant on current installations because no
// serve process advertises CapTools yet (the subprocess has no workspace
// root to build a registry for — base.attach root bookkeeping is a later
// slice). Advertising the capability for a registry is the point at which the
// base-side ownership review (ctx-bound tools, error typing) must pass.
func (a *Agent) baseToolCall(ctx context.Context, plan *toolCallPlan) (baseToolOutcome, bool) {
	bc := a.svc.base
	if bc == nil || bc.Mode() != baseproc.ModeRemote {
		return baseToolOutcome{}, false
	}
	runTool := plan.runTool
	if runTool == nil {
		return baseToolOutcome{}, false
	}
	switch runTool.(type) {
	case tool.ReadExecutor, tool.DetailedExecutor, tool.ImageTool:
		return baseToolOutcome{}, false
	}
	if a.svc.tools == nil || plan.call.ID == "" {
		return baseToolOutcome{}, false
	}
	if owned, ok := a.svc.tools.Get(runTool.Name()); !ok || owned != runTool {
		return baseToolOutcome{}, false // wrapped or foreign instance: client-side semantics
	}
	res, err := bc.ToolCall(ctx, baseproc.ToolCallParams{
		CallID:    plan.call.ID,
		Tool:      runTool.Name(),
		Args:      plan.runArgs,
		SessionID: a.SessionPath(),
	})
	switch {
	case errors.Is(err, baseproc.ErrNotWired):
		// Server lacks the tool face (or inline just answered): take the
		// local path — the R1 shape, with no request spent.
		return baseToolOutcome{}, false
	case err != nil:
		// Sent or transport-failed: never re-run locally (double execution).
		return baseToolOutcome{err: err}, true
	case res.Err != "":
		// v1 contract: a tool-level failure is a completed call whose text
		// the model sees. Typed errors (BlockedError/OperationError) do not
		// survive the wire — a documented S1b v1 limitation.
		return baseToolOutcome{err: errors.New(res.Err)}, true
	default:
		return baseToolOutcome{result: res.Content}, true
	}
}
