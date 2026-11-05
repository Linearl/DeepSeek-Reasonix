package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/jobs"
	"reasonix/internal/sandbox"
	"reasonix/internal/shellrun"
	"reasonix/internal/taskmonitor"
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
	// Occurrence is this nodeID's 1-based execution index within one run.
	// Plain subagent/confirm nodes execute once (0 or 1); gate-loop fixers
	// re-run per round (round number), and the task-panel monitor id must
	// stay unique per execution (tl1324 ⑤: same id never reused — the
	// recorder's start→done pairing is an in-memory map keyed by id).
	Occurrence int
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
// envelope matching the task tool schema. When a task-monitor recorder is
// assembled (controller initializeTaskRecorder → SetTaskMonitorRecorder),
// the node's lifecycle is registered node-level into the task panel: start
// on entry, done on EVERY exit path — error, cancellation, and panic
// included (tl1324 ⑥: the heartbeat only stops at RecordDone; a missed done
// on the failure path leaves the lease renewing forever = a zombie "alive"
// row on the panel).
//
// The answer returned is the child's UNMODIFIED final text: the host may
// wrap the tool's answer with a completion envelope ("status: …",
// "summary: …") and receipts for the parent model, and that envelope is
// prose, not the JSON a typed result needs (G1) — so the raw answer recorded
// on ctx wins whenever the host provided it.
func (r *TaskSubagentRunner) Run(ctx context.Context, req SubagentRequest) (string, error) {
	if r == nil || r.task == nil {
		return "", fmt.Errorf("workflow: subagent runner is not configured")
	}
	rec := currentTaskMonitorRecorder()
	if rec == nil {
		return r.runNode(ctx, req)
	}
	return r.runNodeMonitored(ctx, req, rec)
}

// runNodeMonitored wraps one node execution with task-panel lifecycle
// registration (tl1324 G6). The recorder captured here is held for BOTH the
// start and the done call: the recorder's start→done pairing is an
// in-memory map on that instance, so a mid-run re-assembly of a newer
// controller recorder must not split the pair across instances (the delayed
// done would miss the map and never stop the heartbeat).
func (r *TaskSubagentRunner) runNodeMonitored(ctx context.Context, req SubagentRequest, rec *taskmonitor.TaskRecorder) (string, error) {
	return runNodeWithMonitor(ctx, req, rec, r.runNode)
}

// runNodeWithMonitor is the testable core of the registration wrapper (exec
// injectable so the panic path is pinned deterministically).
func runNodeWithMonitor(ctx context.Context, req SubagentRequest, rec *taskmonitor.TaskRecorder, exec func(context.Context, SubagentRequest) (string, error)) (answer string, err error) {
	sid := jobs.SessionFromContext(ctx)
	id := wfMonitorTaskID(runIDFromContext(ctx), req)
	label := wfMonitorLabel(runIDFromContext(ctx), req)
	rec.RecordStartSession(sid, id, monitorKindWorkflowSubagent, label)
	defer func() {
		if p := recover(); p != nil {
			rec.RecordDone(id, jobs.Failed, fmt.Errorf("workflow subagent panic: %v", p))
			panic(p)
		}
		switch {
		case err == nil:
			rec.RecordDone(id, jobs.Done, nil)
		case ctx.Err() != nil:
			// The run was cancelled (panel stop / session teardown): report
			// the interrupt as its own terminal so the row leaves "alive".
			rec.RecordDone(id, jobs.Interrupted, err)
		default:
			rec.RecordDone(id, jobs.Failed, err)
		}
	}()
	answer, err = exec(ctx, req)
	if err != nil {
		return "", err
	}
	return answer, nil
}

// monitorKindWorkflowSubagent is the kind reported for workflow sub-agent
// node rows (jobs-side rows report "task").
const monitorKindWorkflowSubagent = "workflow_subagent"

// wfMonitorTaskID builds the task-panel registration id for one node
// execution: wf:<runId>:<nodeID>, suffixed #<occurrence> from the second
// execution on (gate-loop fixer rounds). The wf: prefix keeps the namespace
// disjoint from jobs.Manager job ids (pin 4: both coexist in one store).
func wfMonitorTaskID(runID string, req SubagentRequest) string {
	if runID == "" {
		runID = "norun"
	}
	id := "wf:" + runID + ":" + req.NodeID
	if req.Occurrence > 1 {
		id += fmt.Sprintf("#%d", req.Occurrence)
	}
	return id
}

// wfMonitorLabel builds the panel display label: wf:<runId 前 8>:<节点描述或
// nodeID>（tl1324 ④，节点级粒度）。
func wfMonitorLabel(runID string, req SubagentRequest) string {
	if runID == "" {
		runID = "norun"
	}
	if len(runID) > 8 {
		runID = runID[:8]
	}
	desc := req.Description
	if desc == "" {
		desc = req.NodeID
	}
	return "wf:" + runID + ":" + desc
}

// runNode is the un-monitored node execution (identical behavior to
// pre-tl1324 Run when no recorder is assembled).
func (r *TaskSubagentRunner) runNode(ctx context.Context, req SubagentRequest) (string, error) {
	ctx = agent.WithSubagentRawAnswerSink(ctx)
	// tl1434: 同一派发 ctx 再装 ref sink——TaskTool 在 transcript ref 生成处
	// 登记进来，Execute 返回后交给 run 级 recorder 落 nodeId→执行实例关联。
	ctx = agent.WithSubagentRefSink(ctx)
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
	recordWorkflowSubagentRef(ctx, req, agent.SubagentRef(ctx))
	return resolvedSubagentAnswer(ctx, req.NodeID, answer), nil
}

// subagentRefRecorder 是 run 级的 nodeId→ref 登记口（service.RunWorkflow 在
// runCtx 上安装；引擎并发段只经此闭包触达服务登记，不持引擎锁）。缺省无
// recorder（headless 测试/旧宿主）= no-op，派发行为与 tl1434 之前逐字节相同。
type subagentRefRecorder func(req SubagentRequest, ref, parentSession string)

// withSubagentRefRecorder / subagentRefRecorderFrom 是 recorder 的 ctx 载体。
type subagentRefRecorderKey struct{}

func withSubagentRefRecorder(ctx context.Context, rec subagentRefRecorder) context.Context {
	if rec == nil {
		return ctx
	}
	return context.WithValue(ctx, subagentRefRecorderKey{}, rec)
}

func subagentRefRecorderFrom(ctx context.Context) subagentRefRecorder {
	rec, _ := ctx.Value(subagentRefRecorderKey{}).(subagentRefRecorder)
	return rec
}

// recordWorkflowSubagentRef 把一次派发产出的 transcript ref 报给 run 级登记
// （tl1434）。空 ref（ephemeral 运行：resume-from-panel 无父会话时 transcript
// 不持久化）不上报——查看面按「无关联」降级，不伪造身份。
func recordWorkflowSubagentRef(ctx context.Context, req SubagentRequest, ref string) {
	if ref == "" {
		return
	}
	rec := subagentRefRecorderFrom(ctx)
	if rec == nil {
		return
	}
	rec(req, ref, jobs.SessionFromContext(ctx))
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
