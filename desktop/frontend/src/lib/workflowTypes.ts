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
