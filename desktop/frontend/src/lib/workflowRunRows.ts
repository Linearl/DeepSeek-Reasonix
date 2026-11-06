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

import type { DictKey } from "./i18n";
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
  /** tl1433 四缺口④历史入口：运行终点（unix ms；缺省 = 未结束/快照未带）。 */
  endedAt?: number;
  /** tl1433 四缺口②胶囊计数面：正在跑的子代理型节点数（subagent / confirm
   *  节点处于 running = 该节点的子代理正在执行）。工作流派发的子代理不进
   *  jobs、不进前台登记表（tl1435），胶囊三源都看不见——这里从同一喂单的
   *  节点态直接数（推送常驻，不需要另开轮询）。 */
  runningSubagents: number;
}

/** 运行中的运行（任务面板「运行中」列表的入列条件；本件只并入运行中）。 */
export function runningWorkflowRuns(runs: readonly WireWorkflowRun[]): WireWorkflowRun[] {
  return runs.filter((run) => run.state === "running");
}

// tl1433 四缺口④：「最近」段窗口——run 结束后不再永远消失，终态且终点在
// 窗口内的进最近段（历史入口：点击仍跳工作流面板选中该 run，完整史与续跑
// 在面板）。窗口取宽（30 分钟）+ 条数取窄（3 条）：宁少勿吵，更早的 run 走
// 工作流面板自身的运行选择器（既有面，非本件新机制）。
export const WORKFLOW_RECENT_WINDOW_MS = 30 * 60_000;
export const WORKFLOW_RECENT_LIMIT = 3;

const WORKFLOW_TERMINAL_STATES = new Set<string>(["completed", "failed", "interrupted"]);

/** 终态且刚结束的运行（「最近」段的入列条件；endedAt 新→旧，条数封顶）。
 *  endedAt 缺失的终态 run 不入（不知何时结束的运行不冒充「最近」）；running
 *  行永远走运行段，与本函数无关。 */
export function recentEndedWorkflowRuns(runs: readonly WireWorkflowRun[], now: number = Date.now()): WireWorkflowRun[] {
  return runs
    .filter((run) => WORKFLOW_TERMINAL_STATES.has(run.state) && typeof run.endedAt === "number" && run.endedAt > 0 && now - run.endedAt >= 0 && now - run.endedAt <= WORKFLOW_RECENT_WINDOW_MS)
    .sort((a, b) => (b.endedAt ?? 0) - (a.endedAt ?? 0))
    .slice(0, WORKFLOW_RECENT_LIMIT);
}

/** 终态 run 的状态徽章键（任务面板与胶囊共用一处）。与 WorkflowPanel 运行徽章
 *  同一组键、同一语义（liveRunStateToPanelState：failed 落 errored，
 *  interrupted 落「已中断·可续跑」）。 */
export function endedWorkflowRunStateKey(state: WireWorkflowRun["state"]): DictKey {
  switch (state) {
    case "completed": return "workflowPanel.run.completed";
    case "failed": return "workflowPanel.run.errored";
    default: return "workflowPanel.run.interrupted";
  }
}

/** 节点表折叠：后见覆盖（事件序 = 状态机序），与 runNodeMap 同规则。 */
function latestNodes(run: WireWorkflowRun): Map<string, WireRunNode> {
  const latest = new Map<string, WireRunNode>();
  for (const node of run.nodes) latest.set(node.nodeId, node);
  return latest;
}

// 子代理型节点：subagent 直接派子代理；confirm 走只读子代理（workflow_runs_app.go
// 「confirm 也是一类子代理顶点」）。gate 跑命令、phase/join 是结构点，都不算。
const SUBAGENT_NODE_KINDS = new Set<string>(["subagent", "confirm"]);

/** 正在跑的子代理型节点数（种类以 DAG 定义为准，缺 DAG 退节点表 nodeType）。 */
export function runningSubagentNodeCount(run: WireWorkflowRun): number {
  const kinds = new Map<string, string>();
  for (const node of run.dag?.nodes ?? []) kinds.set(node.id, node.kind);
  let count = 0;
  for (const node of latestNodes(run).values()) {
    if (node.state !== "running") continue;
    if (SUBAGENT_NODE_KINDS.has(kinds.get(node.nodeId) ?? node.nodeType)) count += 1;
  }
  return count;
}

/** 一次 wire 运行 → run 行视图投影（纯归约，同输入同输出）。 */
export function workflowRunRowView(run: WireWorkflowRun): WorkflowRunRowView {
  const latest = latestNodes(run);
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
    endedAt: run.endedAt,
    runningSubagents: runningSubagentNodeCount(run),
  };
}

// ── tl1433 四缺口③：工作流子代理归属（层级展示）──────────────────────
//
// 工作流派发的子代理只在 sidecar（ListSubagentsByParent，tl1435）里可见，
// 运行中时 sidecar 不带 runId；而 run 的 subagentRefs（tl1434）只在节点执行
// **成功返回后**才登记（runner.go runNode：Execute 返回后 ReportSubagentRef），
// 运行中的子代理 ref 不在里面。所以归属按可信度递减取两条，归不上就留顶层
// ——宁可平铺，不错挂：
//   ① ref 命中某运行中 run 的 subagentRefs（节点已完成登记、sidecar 5s 轮询
//      尚未翻终态的窗口——精确）；
//   ② 恰好一个运行中 run 有正在跑的子代理型节点 → 归它（sidecar-only 运行
//      子代理只来自工作流派发；若派发时刻早于该 run 起点则不是它的子代理）。
// 多个 run 同时在派子代理时 ② 不成立：按派发时刻贴近节点起点去配会在并发
// 起跑时配错（两 run 节点相差几百毫秒即可颠倒），故不做——留顶层。

/** 归属探针：子代理的 ref 与派发时刻（sidecar createdAt，unix ms）。 */
export interface WorkflowChildProbe {
  ref?: string;
  startedAt?: number;
}

/** 派发时刻与 run 起点的比较容差（同为后端时钟，只吸收毫秒取整）。 */
const WORKFLOW_CHILD_START_SLACK_MS = 1000;

/** 把候选子代理（调用方已筛成「仅 sidecar 可见的运行中子代理」）归到运行
 *  中的工作流 run 下。返回 runId → 子代理（保持输入序）与归不上的顶层余项。 */
export function attributeWorkflowChildren<T>(
  children: readonly T[],
  runs: readonly WireWorkflowRun[],
  probe: (child: T) => WorkflowChildProbe,
): { byRun: Map<string, T[]>; flat: T[] } {
  const byRun = new Map<string, T[]>();
  const live = runningWorkflowRuns(runs);
  if (live.length === 0 || children.length === 0) return { byRun, flat: [...children] };
  const refOwner = new Map<string, string>();
  for (const run of live) {
    for (const node of run.nodes) {
      for (const ref of node.subagentRefs ?? []) refOwner.set(ref, run.runId);
    }
  }
  const dispatching = live.filter((run) => runningSubagentNodeCount(run) > 0);
  const sole = dispatching.length === 1 ? dispatching[0] : undefined;
  const flat: T[] = [];
  for (const child of children) {
    const { ref, startedAt } = probe(child);
    let owner = ref ? refOwner.get(ref) : undefined;
    if (!owner && sole) {
      const predates = startedAt !== undefined && sole.startedAt !== undefined && startedAt < sole.startedAt - WORKFLOW_CHILD_START_SLACK_MS;
      if (!predates) owner = sole.runId;
    }
    if (!owner) {
      flat.push(child);
      continue;
    }
    const list = byRun.get(owner);
    if (list) list.push(child);
    else byRun.set(owner, [child]);
  }
  return { byRun, flat };
}
