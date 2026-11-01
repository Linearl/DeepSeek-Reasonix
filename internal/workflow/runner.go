package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/sandbox"
	"reasonix/internal/shellrun"
	"reasonix/internal/tool"
)

// DefaultGateTimeout is the default deterministic-gate timeout, mirroring the
// zcode world.run default of 300s (overridable per gate node, no hard cap).
const DefaultGateTimeout = 300 * time.Second

// SubagentRequest is one node's dispatch to the sub-agent substrate (task 685
// point 2: nodes execute on the internal/agent task machinery).
type SubagentRequest struct {
	NodeID      string
	Prompt      string // fully interpolated, typed-result hint already appended
	Description string
	Tools       []string
	Model       string
	Effort      string
	MaxSteps    int
	// ReadOnly forces the read-only task path — independent confirmers are
	// read-only by protocol (reproduce from evidence, never edit).
	ReadOnly bool
}

// SubagentRunner runs one sub-agent and returns its final answer. Injected so
// the engine stays testable; TaskSubagentRunner is the production adapter.
type SubagentRunner interface {
	Run(ctx context.Context, req SubagentRequest) (string, error)
}

// GateResult is one deterministic-gate execution. A non-zero ExitCode is a
// VALUE the branch reads, not an error — errors are reserved for the world
// failing to answer (timeout, launch failure, cancellation).
type GateResult struct {
	ExitCode int
	Output   string // failure output tail (stdout+stderr merged by shellrun)
}

// GateRunner runs one literal gate command. timeout <= 0 means
// DefaultGateTimeout. Returns an error only when the world did not answer.
type GateRunner func(ctx context.Context, command string, timeout time.Duration) (GateResult, error)

// ShellGateRunner is the default GateRunner: resolved system shell through
// the shared foreground runner (same hardening as the model bash tool —
// timeout classification, bounded output, cancellation, Windows codepage
// decoding).
func ShellGateRunner(ctx context.Context, command string, timeout time.Duration) (GateResult, error) {
	if timeout <= 0 {
		timeout = DefaultGateTimeout
	}
	sh := sandbox.ResolveShell("", "", nil)
	argv, _ := sandbox.Command(sandbox.Spec{}, sh, command)
	res := shellrun.RunForeground(ctx, shellrun.Request{
		Argv:      argv,
		Timeout:   timeout,
		ShellKind: sh.Kind.String(),
		ShellPath: sh.Path,
		Source:    "workflow_gate",
	})
	switch res.State {
	case tool.ShellStateCancelled:
		if ctx.Err() != nil {
			return GateResult{}, ctx.Err()
		}
		return GateResult{}, fmt.Errorf("gate %q cancelled: %w", command, res.Err)
	case tool.ShellStateTimedOut:
		return GateResult{}, fmt.Errorf("gate %q: %w", command, res.Err)
	}
	if res.ExitCode == nil {
		return GateResult{}, fmt.Errorf("gate %q produced no exit code: %w", command, res.Err)
	}
	return GateResult{ExitCode: *res.ExitCode, Output: res.OutputTail}, nil
}

// TaskSubagentRunner adapts the production agent.TaskTool (and its read-only
// twin) to the engine's SubagentRunner, so every node runs on the same
// sub-agent substrate as task/fleet/read_only_task.
type TaskSubagentRunner struct {
	task     *agent.TaskTool
	readOnly *agent.ReadOnlyTaskTool
}

// NewTaskSubagentRunner wraps one TaskTool for both writer and read-only node
// execution (confirm nodes take the read-only path).
func NewTaskSubagentRunner(task *agent.TaskTool) *TaskSubagentRunner {
	return &TaskSubagentRunner{task: task, readOnly: agent.NewReadOnlyTaskTool(task)}
}

// Run executes one node's sub-agent call via tool Execute with a JSON-args
// envelope matching the task tool schema. The answer returned is the child's
// UNMODIFIED final text: the host may wrap the tool's answer with a
// completion envelope ("status: …", "summary: …") and receipts for the
// parent model, and that envelope is prose, not the JSON a typed result
// needs (G1) — so the raw answer recorded on ctx wins whenever the host
// provided it.
func (r *TaskSubagentRunner) Run(ctx context.Context, req SubagentRequest) (string, error) {
	if r == nil || r.task == nil {
		return "", fmt.Errorf("workflow: subagent runner is not configured")
	}
	ctx = agent.WithSubagentRawAnswerSink(ctx)
	args := map[string]any{"prompt": req.Prompt}
	set := func(key, v string) {
		if v != "" {
			args[key] = v
		}
	}
	set("description", req.Description)
	set("model", req.Model)
	set("effort", req.Effort)
	if len(req.Tools) > 0 {
		args["tools"] = req.Tools
	}
	if req.MaxSteps > 0 {
		args["max_steps"] = req.MaxSteps
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	var answer string
	if req.ReadOnly && r.readOnly != nil {
		answer, err = r.readOnly.Execute(ctx, raw)
	} else {
		answer, err = r.task.Execute(ctx, raw)
	}
	if err != nil {
		return "", err
	}
	return resolvedSubagentAnswer(ctx, req.NodeID, answer), nil
}

// resolvedSubagentAnswer prefers the child's raw answer recorded by the host
// during the run and falls back to the answer text Execute returned. The
// fallback keeps the engine runnable against hosts that never record (version
// skew), but it silently re-opens G1 — the host envelope would reach typed
// parsing — so the fallback path leaves a trace.
func resolvedSubagentAnswer(ctx context.Context, nodeID, answer string) string {
	if orig := agent.SubagentRawAnswer(ctx); orig != "" {
		return orig
	}
	slog.Debug("workflow: host recorded no raw sub-agent answer; using the tool's answer text",
		"node", nodeID,
		"hint", "typed-result parsing may see the host completion envelope (G1)")
	return answer
}
