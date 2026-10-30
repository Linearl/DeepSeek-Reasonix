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

/** 任务934: 运行级状态（服务侧 RunState 的 wire 镜像）。 */
export type WorkflowRunLiveState = "running" | "completed" | "failed";

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
}
