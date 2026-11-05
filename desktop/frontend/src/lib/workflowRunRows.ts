// tl1433 —— 任务面板「运行中」列表并入工作流 run 行的视图投影（纯函数）。
//
// 数据源完全复用既有工作流状态查询面（workflowRunFeed 单例：WorkflowRunsSnapshot
// 拉取 + "workflow:runs" 推送 + agent:event 的 workflow_node 就地点亮），本模块
// 只做展示归约：从一次 wire 运行折出 run 行的五个字段（工作流名 / 当前节点 /
// 已完成节点数 / 总节点数 / 起点），不造新机制、不新增后端绑定。
//
// 与 WorkflowPanel 同一数据语义：
//   - 节点表按 nodeId 后见覆盖（状态机序；与 workflowRunFeed.runNodeMap 的
//     折叠规则一致）——门重跑/重试等多次变迁只按最终态计数；
//   - completed 与 gate_passed 都算已完成（与 feed 六态→五态投影同口径）；
//   - 总数取 DAG 定义的节点数（未见态节点的诚实总数），无 DAG 时退回节点表。

import type { WireRunNode, WireWorkflowRun } from "./workflowTypes";

/** 一个 run 行的展示投影（任务面板「运行中」列表的字段面）。 */
export interface WorkflowRunRowView {
  runId: string;
  /** 工作流名（wire 的 workflow 字段，面板标题同源）。 */
  workflow: string;
  /** 运行级状态（本件只入列 running；字段保留以便行内徽章同口径扩展）。 */
  state: WireWorkflowRun["state"];
  /** 当前节点名（运行中节点的 DAG name；无运行中节点=间隙/排队 → ""）。 */
  currentNode: string;
  /** 已完成节点数（completed / gate_passed）。 */
  doneCount: number;
  /** 总节点数（DAG 定义；缺 DAG 时退回节点表长度）。 */
  totalCount: number;
  /** 运行起点（unix ms；缺省 = 快照未带，行内时长显示为 —）。 */
  startedAt?: number;
}

/** 运行中的运行（任务面板「运行中」列表的入列条件；本件只并入运行中）。 */
export function runningWorkflowRuns(runs: readonly WireWorkflowRun[]): WireWorkflowRun[] {
  return runs.filter((run) => run.state === "running");
}

/** 一次 wire 运行 → run 行视图投影（纯归约，同输入同输出）。 */
export function workflowRunRowView(run: WireWorkflowRun): WorkflowRunRowView {
  // 节点表折叠：后见覆盖（事件序 = 状态机序），与 runNodeMap 同规则。
  const latest = new Map<string, WireRunNode>();
  for (const node of run.nodes) latest.set(node.nodeId, node);
  // nodeId → 节点名（当前节点显示用 DAG 名，读起来是人话而非内部 id）。
  const names = new Map<string, string>();
  for (const node of run.dag?.nodes ?? []) names.set(node.id, node.name);
  let doneCount = 0;
  let currentNode = "";
  for (const node of latest.values()) {
    if (node.state === "completed" || node.state === "gate_passed") doneCount += 1;
    if (!currentNode && node.state === "running") currentNode = names.get(node.nodeId) ?? node.nodeId;
  }
  return {
    runId: run.runId,
    workflow: run.workflow,
    state: run.state,
    currentNode,
    doneCount,
    totalCount: run.dag ? run.dag.nodes.length : latest.size,
    startedAt: run.startedAt,
  };
}
