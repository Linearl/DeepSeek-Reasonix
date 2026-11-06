/** 任务685: workflow DAG node state transition (wire kind "workflow_node").
 * Six states drive the 686 visualization: queued (gray) / running (pulse) /
 * completed (green) / unconfirmed (yellow) / failed (red) / gate_passed. */
export type WorkflowNodeState = "queued" | "running" | "completed" | "unconfirmed" | "failed" | "gate_passed";
export interface WireWorkflowNode {
  runId: string;
  workflow: string;
  nodeId: string;
  nodeType: "subagent" | "gate" | "phase" | "join" | "confirm" | string;
  state: WorkflowNodeState;
  phase?: string;
  round?: number;
  detail?: string;
}

/** 任务934: 运行级状态（服务侧 RunState 的 wire 镜像）。
 * 任务1316 增 interrupted：进程重启后从磁盘恢复的未完 run（可续跑）。 */
export type WorkflowRunLiveState = "running" | "completed" | "failed" | "interrupted";

/** DAG 节点 wire（WorkflowDagNode 镜像；kind 五类全开）。 */
export interface WireRunDagNode {
  id: string;
  name: string;
  kind: "subagent" | "gate" | "join" | "phase" | "confirm";
  phase?: string;
  instruction?: string;
  command?: string;
}

export interface WireRunDagEdge {
  from: string;
  to: string;
  label?: string;
}

/** 运行所装载定义（面板用它渲染与引擎执行同一张图）。 */
export interface WireRunDag {
  id: string;
  name: string;
  phases?: string[];
  nodes: WireRunDagNode[];
  edges: WireRunDagEdge[];
}

/** 一个节点的当前态投影（六态契约，见 internal/event/workflow.go）。 */
export interface WireRunNode {
  nodeId: string;
  nodeType: string;
  state: WorkflowNodeState;
  round?: number;
  detail?: string;
  changedAt?: number;
  /** 本轮 running 起点（unix ms；缺省 = 未见 running，如排队即跳过）。 */
  nodeStartedAt?: number;
  /** tl1434: 节点执行实例关联（子代理 transcript ref，执行序，下标+1=
   *  occurrence）。缺省 = 无关联（老 run/ephemeral 派发/非子代理节点），
   *  详情面「工作过程」入口按此降级。 */
  subagentRefs?: string[];
  /** tl1454: 节点用量 / 失败原因码 / 非致命契约提示（模板视图开启时渲染）。 */
  tokens?: number;
  requests?: number;
  reason?: WorkflowFailureReason;
  contract?: string;
}

/** 一次运行的完整 wire 快照（desktop workflowRunWire 镜像）。 */
export interface WireWorkflowRun {
  runId: string;
  workflow: string;
  state: WorkflowRunLiveState;
  startedAt?: number;
  endedAt?: number;
  error?: string;
  dag?: WireRunDag;
  nodes: WireRunNode[];
  /** 任务1316：服务侧可续判定（interrupted+定义在+指纹匹配）。续跑钮可见性权威。 */
  resumable?: boolean;
  /** tl1454：模板版本 / 发起来源 / 预算与用量 / 失败定位（模板视图消费）。 */
  version?: string;
  origin?: string;
  budget?: { tokens?: number; rounds?: number; wallMs?: number };
  usedTokens?: number;
  usedRequests?: number;
  failure?: { nodeId?: string; reason: WorkflowFailureReason; detail: string };
}

/** tl1454: 引擎失败原因码（internal/workflow/budget.go Reason* 镜像）。 */
export type WorkflowFailureReason =
  | "timeout"
  | "cancelled"
  | "budget"
  | "subagent_error"
  | "result_contract"
  | "gate_failed"
  | "gate_error"
  | "gate_denied"
  | "internal";

/** tl1454: 校验诊断类别（internal/workflow/diagnose.go Diag* 镜像）。 */
export type WorkflowDiagnosticKind = "syntax" | "field" | "structure" | "reference" | "version";

export interface WorkflowTemplateDiagnostic {
  kind: WorkflowDiagnosticKind;
  message: string;
  path?: string;
  nodeId?: string;
  line?: number;
  column?: number;
}

export interface WorkflowTemplateSummary {
  name: string;
  description?: string;
  version: string;
  fingerprint: string;
  nodes: number;
  edges: number;
  gates: number;
  subagents: number;
  confirms: number;
  hasBudget?: boolean;
}

export interface WorkflowTemplateInvalidFile {
  file: string;
  kind: WorkflowDiagnosticKind;
  message: string;
  path?: string;
  line?: number;
  column?: number;
}

export interface WorkflowTemplateList {
  templates: WorkflowTemplateSummary[];
  invalid?: WorkflowTemplateInvalidFile[];
}

export interface WorkflowNodeContract {
  id: string;
  type: string;
  inputs?: string;
  outputs?: string;
  failure: string;
  limits?: string;
}

export interface WorkflowTemplateValidation {
  ok: boolean;
  diagnostic?: WorkflowTemplateDiagnostic;
  summary?: WorkflowTemplateSummary;
  contracts?: WorkflowNodeContract[];
}

export interface WorkflowTemplateSource {
  document: string;
  version: string;
}

export interface WorkflowTemplateSaveResult {
  name: string;
  created: boolean;
  version: string;
  fingerprint: string;
}

export interface WorkflowTemplateHistoryEntry {
  version: string;
  fingerprint: string;
  savedAt: string;
}

export interface WorkflowTemplateRunPreview {
  name: string;
  version: string;
  workspace: string;
  gates: Array<{ nodeId: string; command: string; denied: boolean; reason?: string }>;
  subagents: Array<{ nodeId: string; type: string; tools?: string[]; allTools: boolean; readOnly: boolean; timeout?: string; maxSteps?: number }>;
  budget?: { tokens?: number; rounds?: number; wall?: string };
  blocked: boolean;
}

export interface WorkflowTemplateRunStart {
  runId: string;
  state: string;
}
