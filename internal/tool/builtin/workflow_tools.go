package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"reasonix/internal/tool"
)

// 任务 934 —— 工作流管理工具：把 685 声明式 DAG 引擎（internal/workflow，
// 定义/执行/门/确认/报告全绿但此前零调用方）接进 agent 工具面，完成
// 「对话中创建-管理-运行」闭环的 agent 侧。
//
// 架构与 heartbeat 管理三件套同款：引擎住在 internal/workflow，由宿主
// （boot）在装配时注入 WorkflowService seam；本包只持有 JSON 视图契约，
// 不 import 引擎包（workflow → agent → builtin 的依赖方向不允许反向）。
// 未注入时（CLI/无装配的测试）工具以专门的错误文案失败，不猜测文件路径。
//
// schema 契约单源：文档校验全部走引擎自己的 Parse/Validate（本包不发第二
// 份 schema 真相），工具描述只是给模型的使用说明。
//
// 审批面：workflow_define / workflow_run 为写类（ReadOnly=false），默认走
// 既有审批；list/get/status 只读。

// WorkflowSummary 是一个已定义工作流的清单行。
type WorkflowSummary struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	SchemaVersion int    `json:"schemaVersion"`
	Nodes         int    `json:"nodes"`
	Edges         int    `json:"edges"`
	Subagents     int    `json:"subagents"`
	Gates         int    `json:"gates"`
	// LoopGates 是带修复环（loop）的门数——环内含 fixer 子代理，运行时长
	// 与预算消耗都按轮数放大。
	LoopGates int `json:"loopGates,omitempty"`
	Joins     int `json:"joins"`
	Phases    int `json:"phases"`
	Confirms  int `json:"confirms"`
}

// WorkflowDefineResult 回执创建/覆盖结果与形状摘要。
type WorkflowDefineResult struct {
	Name      string `json:"name"`
	Created   bool   `json:"created"`
	Nodes     int    `json:"nodes"`
	Edges     int    `json:"edges"`
	Gates     int    `json:"gates"`
	LoopGates int    `json:"loopGates,omitempty"`
}

// WorkflowGetResult 回执 canonical JSON 文档（Parse 后回写的规范形态，
// 字段面即引擎 frozen 契约）。
type WorkflowGetResult struct {
	Name     string `json:"name"`
	Document string `json:"document"`
}

// WorkflowListResult 是清单结果。
type WorkflowListResult struct {
	Workflows []WorkflowSummary `json:"workflows"`
}

// WorkflowRunResult 回执运行句柄；runId 是后续 workflow_status 的键。
type WorkflowRunResult struct {
	RunID    string `json:"runId"`
	Workflow string `json:"workflow"`
	State    string `json:"state"`
	Hint     string `json:"hint"`
}

// WorkflowRunNodeState 是一次运行中一个节点的当前态（六态契约，见
// internal/event/workflow.go：queued/running/completed/unconfirmed/failed/
// gate_passed）。
type WorkflowRunNodeState struct {
	NodeID    string `json:"nodeId"`
	NodeType  string `json:"nodeType"`
	State     string `json:"state"`
	Round     int    `json:"round,omitempty"`
	Detail    string `json:"detail,omitempty"`
	ChangedAt int64  `json:"changedAt,omitempty"` // unix ms
}

// WorkflowReportView 是引擎终局报告（结论/发现/已验证/未覆盖）。
type WorkflowReportView struct {
	Conclusion string                `json:"conclusion"`
	Findings   []WorkflowFindingView `json:"findings"`
	Verified   []string              `json:"verified"`
	NotCovered []string              `json:"notCovered"`
}

// WorkflowFindingView 是一条发现（状态由引擎指派：verified/unconfirmed）。
type WorkflowFindingView struct {
	Where    string `json:"where"`
	What     string `json:"what"`
	Evidence string `json:"evidence"`
	Status   string `json:"status"`
	Severity string `json:"severity"`
}

// WorkflowRunView 是一次运行的完整状态视图。
type WorkflowRunView struct {
	RunID     string                 `json:"runId"`
	Workflow  string                 `json:"workflow"`
	State     string                 `json:"state"` // running | completed | failed
	StartedAt int64                  `json:"startedAt,omitempty"`
	EndedAt   int64                  `json:"endedAt,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Nodes     []WorkflowRunNodeState `json:"nodes,omitempty"`
	Report    *WorkflowReportView    `json:"report,omitempty"`
}

// WorkflowStatusResult 是状态查询结果（新→旧）。
type WorkflowStatusResult struct {
	Runs []WorkflowRunView `json:"runs"`
}

// WorkflowService 是宿主注入的 seam（internal/workflow 的 Service 实现）。
// 全部方法必须并发安全；实现方拥有校验、持久化与运行登记。
type WorkflowService interface {
	DefineWorkflow(ctx context.Context, doc string, overwrite bool) (WorkflowDefineResult, error)
	ListWorkflows(ctx context.Context) (WorkflowListResult, error)
	GetWorkflow(ctx context.Context, name string) (WorkflowGetResult, error)
	RunWorkflow(ctx context.Context, name string) (WorkflowRunResult, error)
	WorkflowStatus(ctx context.Context, runID string) (WorkflowStatusResult, error)
}

var workflowService atomic.Pointer[WorkflowService]

// SetWorkflowService wires the host implementation in at boot. Passing nil
// clears a previous registration (used by tests).
func SetWorkflowService(s WorkflowService) {
	if s == nil {
		workflowService.Store(nil)
		return
	}
	workflowService.Store(&s)
}

// workflowServiceFromCtx returns the injected service, or a descriptive error
// for sessions running without the workflow engine attached (CLI, headless).
func workflowServiceFromCtx() (WorkflowService, error) {
	if p := workflowService.Load(); p != nil {
		return *p, nil
	}
	return nil, fmt.Errorf("workflow tools require a Reasonix session booted with the workflow engine attached; this session has no workflow service (CLI/serve sessions without boot wiring cannot manage workflows)")
}

// workflowContractDoc 是五个工具共享的文档契约说明（单一事实，描述不与
// schema.go 漂移——结构性校验以引擎为准，这里只教模型怎么写）。
const workflowContractDoc = `Document contract (authoritative; the engine validates every field):
- version: must be 1. name (required, unique per catalog): the workflow's key for workflow_get / workflow_run. description: optional.
- nodes: list of typed vertices. Node id: lowercase [a-z0-9_-], unique. Types:
  * subagent: prompt (required; {{upstream_node}} / {{node.field}} interpolations resolve from ancestors), description, result (typed fields: name/type/description — the answer must be a JSON object with exactly these fields), findings_field (array-of-findings field promoted into the report), tools (allowlist), model, effort, max_steps.
  * gate: command (required, literal shell), timeout (e.g. "90s", default 300s), tier ("fast"|"strong"; strong gates must dominate every delivery sink), loop (optional fix-until-pass: {fixer: <subagent node id>, max_rounds: N}; the fixer node gets no edges of its own).
  * phase: label only — a stage marker; downstream nodes report under it.
  * join: no fields — merges all active upstream results into {node: result}.
  * confirm: target (required, a subagent node) — a second, READ-ONLY sub-agent independently reproduces the target's findings; failed confirmation keeps findings labelled unconfirmed (never drops them).
- edges: {from, to, when}. when is "always" (default), or "pass"/"fail" — branch conditions legal only on edges leaving a gate node (branch on its exit code). The graph must stay acyclic; the gate repair loop lives INSIDE the gate node.
- report: {conclusion: template with {{refs}}, not_covered: [..]} — not_covered passes through verbatim; the engine never auto-fills it (a check that exists but did not run is unverified work).
A workflow document is YAML or JSON (JSON when the first non-space byte is "{"). Carrying another node type's field is rejected, not ignored.`

func init() {
	tool.RegisterBuiltin(workflowDefine{})
	tool.RegisterBuiltin(workflowList{})
	tool.RegisterBuiltin(workflowGet{})
	tool.RegisterBuiltin(workflowRun{})
	tool.RegisterBuiltin(workflowStatus{})
}

// workflow_define ----------------------------------------------------------------

type workflowDefine struct{}

func (workflowDefine) Name() string { return "workflow_define" }

func (workflowDefine) Description() string {
	return "Create or replace one workflow definition (a declarative DAG the Reasonix engine executes: subagent research nodes, deterministic shell gates with optional fix-until-pass loops, joins, phases, independent confirmations, and a salvage report). The document is validated by the engine before anything is stored — a rejected document names the offending field and nothing is written. Read the result's counts back to sanity-check the shape. " + workflowContractDoc
}

func (workflowDefine) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
  "document":{"type":"string","description":"The full workflow document, YAML or JSON."},
  "overwrite":{"type":"boolean","description":"Replace an existing workflow of the same name. Required true to overwrite; absent/false refuses with the existing name named."}
},"required":["document"]}`)
}

func (workflowDefine) ReadOnly() bool { return false }

func (workflowDefine) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	svc, err := workflowServiceFromCtx()
	if err != nil {
		return "", err
	}
	var p struct {
		Document  string `json:"document"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Document) == "" {
		return "", fmt.Errorf("document is required: the full workflow document (YAML or JSON)")
	}
	result, err := svc.DefineWorkflow(ctx, p.Document, p.Overwrite)
	if err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// workflow_list ----------------------------------------------------------------

type workflowList struct{}

func (workflowList) Name() string { return "workflow_list" }

func (workflowList) Description() string {
	return "List every stored workflow definition with its shape summary (node/gate/join/phase/confirm counts). Read-only; call it before workflow_get or workflow_run to see what exists."
}

func (workflowList) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
}

func (workflowList) ReadOnly() bool { return true }

func (workflowList) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	svc, err := workflowServiceFromCtx()
	if err != nil {
		return "", err
	}
	result, err := svc.ListWorkflows(ctx)
	if err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// workflow_get ----------------------------------------------------------------

type workflowGet struct{}

func (workflowGet) Name() string { return "workflow_get" }

func (workflowGet) Description() string {
	return "Fetch one stored workflow as its canonical JSON document (the exact contract the engine executes and the visualization panel renders). Use it to read a definition back before editing it with workflow_define."
}

func (workflowGet) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
  "name":{"type":"string","description":"Workflow name from workflow_list."}
},"required":["name"]}`)
}

func (workflowGet) ReadOnly() bool { return true }

func (workflowGet) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	svc, err := workflowServiceFromCtx()
	if err != nil {
		return "", err
	}
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Name) == "" {
		return "", fmt.Errorf("name is required: the workflow name from workflow_list")
	}
	result, err := svc.GetWorkflow(ctx, p.Name)
	if err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// workflow_run ----------------------------------------------------------------

type workflowRun struct{}

func (workflowRun) Name() string { return "workflow_run" }

func (workflowRun) Description() string {
	return "Start one stored workflow. The run executes in the background on this session's sub-agent substrate: nodes run in topological order, gate failures drive their fix loops, and node state events stream to THIS conversation (the workflow visualization panel lights up live). The call returns immediately with a runId — poll workflow_status for progress and the final report. A run outlives the turn that started it. " + workflowContractDoc
}

func (workflowRun) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
  "name":{"type":"string","description":"Workflow name from workflow_list."}
},"required":["name"]}`)
}

func (workflowRun) ReadOnly() bool { return false }

func (workflowRun) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	svc, err := workflowServiceFromCtx()
	if err != nil {
		return "", err
	}
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Name) == "" {
		return "", fmt.Errorf("name is required: the workflow name from workflow_list")
	}
	result, err := svc.RunWorkflow(ctx, p.Name)
	if err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// workflow_status ----------------------------------------------------------------

type workflowStatus struct{}

func (workflowStatus) Name() string { return "workflow_status" }

func (workflowStatus) Description() string {
	return "Report workflow runs: per-node six-state progress (queued/running/completed/unconfirmed/failed/gate_passed with gate round numbers), and, for finished runs, the engine report (conclusion, findings with verified/unconfirmed status, verified checks, not-covered list). With run_id: that run. Without: the most recent runs, newest first."
}

func (workflowStatus) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
  "run_id":{"type":"string","description":"Omit to list recent runs; pass the runId from workflow_run to inspect one run."}
},"required":[]}`)
}

func (workflowStatus) ReadOnly() bool { return true }

func (workflowStatus) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	svc, err := workflowServiceFromCtx()
	if err != nil {
		return "", err
	}
	var p struct {
		RunID string `json:"run_id"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	result, err := svc.WorkflowStatus(ctx, p.RunID)
	if err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
