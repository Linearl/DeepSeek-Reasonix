// 任务 934 —— 686 面板真数据源（前端合流层）。
//
// 数据面两条腿，在面板侧合一：
//   ① "workflow:runs" 运行时通道：运行级快照（状态 + DAG 定义 + 节点态
//      投影），desktop 端 workflow_runs_app.go 合并推送（250ms 窗口）；
//   ② 既有 agent:event 通道上的 workflow_node 节点事件（internal/eventwire
//      wire 形态）：秒级节点点亮，feed 按 runId 就地更新对应运行。
//
// 拉推合流：首个订阅者出现时先全量拉（WorkflowRunsSnapshot 绑定）再订阅
// 推送，开面板不丢史。模块只做归约与订阅桥接（纯函数可测），不碰 React。

import type { WorkflowDag, WorkflowDagNode } from "./workflowDag";
import type { WorkflowNodeState, WireWorkflowRun, WireRunNode, WireRunDag, WorkflowRunLiveState } from "./workflowTypes";
import type { WorkflowRunSnapshot, WorkflowRunState, WorkflowNodeState as PanelNodeState } from "./workflowEvents";
import { app } from "./bridge";

export type {
  WorkflowRunLiveState,
  WireRunDagNode,
  WireRunDagEdge,
  WireRunDag,
  WireRunNode,
  WireWorkflowRun,
} from "./workflowTypes";

/** 把 wire 节点表折成 nodeId → 节点态（面板消费形态，保持首见序）。 */
export function runNodeMap(run: WireWorkflowRun): Map<string, WireRunNode> {
  const map = new Map<string, WireRunNode>();
  for (const node of run.nodes) {
    const existing = map.get(node.nodeId);
    // 就地更新：后到的同 id 覆盖（事件序 = 状态机序）。
    map.set(node.nodeId, existing ? { ...existing, ...node } : node);
  }
  return map;
}

/** 纯归约：快照合流——按 runId 整体替换（推送是全量快照，非增量）。 */
export function mergeRunsSnapshot(prev: WireWorkflowRun[], incoming: WireWorkflowRun[]): WireWorkflowRun[] {
  if (incoming.length === 0) return prev;
  const byId = new Map(prev.map((r) => [r.runId, r]));
  for (const run of incoming) byId.set(run.runId, run);
  // 保持已有相对序，新运行追加在尾（启动序，最老在前）。
  const order: string[] = [];
  for (const r of prev) order.push(r.runId);
  for (const r of incoming) if (!order.includes(r.runId)) order.push(r.runId);
  return order.map((id) => byId.get(id)!).filter(Boolean);
}

/** 最近优先的展示序（选择器默认选第一条）。 */
export function runsNewestFirst(runs: WireWorkflowRun[]): WireWorkflowRun[] {
  return [...runs].reverse();
}

interface FeedState {
  runs: WireWorkflowRun[];
  loaded: boolean;
}

type Listener = () => void;

/** 全局 feed 单例：面板（唯一消费方）经 useSyncExternalStore 订阅。 */
export interface WorkflowRunFeed {
  subscribe(listener: Listener): () => void;
  getRuns(): WireWorkflowRun[];
  /** 测试与降级入口：直接喂快照（绕开运行时通道）。 */
  ingest(runs: WireWorkflowRun[]): void;
  /** 测试入口：注入 workflow_node 节点事件（真实路径来自 agent:event）。 */
  ingestNodeEvent(event: { runId: string; nodeId: string; state: WorkflowNodeState; round?: number; detail?: string; at?: number }): void;
}

let state: FeedState = { runs: [], loaded: false };
const listeners = new Set<Listener>();
let wired = false;

function emit() {
  for (const l of [...listeners]) l();
}

function wireOnce() {
  if (wired) return;
  wired = true;
  if (typeof window !== "undefined" && window.runtime) {
    // ① 推送通道：运行级全量快照（合并窗口后的低频推送）。
    window.runtime.EventsOn("workflow:runs", (...args: unknown[]) => {
      const payload = args[0];
      if (!Array.isArray(payload)) return;
      state = { runs: mergeRunsSnapshot(state.runs, payload as WireWorkflowRun[]), loaded: true };
      emit();
    });
    // ② 节点事件通道：既有 agent:event 总线上按 kind 过滤，秒级点亮。
    //    与 bridge.onEvent 共用一条 Wails 通道，各自注册互不影响。
    window.runtime.EventsOn("agent:event", (...args: unknown[]) => {
      const payload = args[0] as { kind?: string; workflow?: { runId?: string; nodeId?: string; state?: string; round?: number; detail?: string } } | undefined;
      if (!payload || payload.kind !== "workflow_node" || !payload.workflow) return;
      const wf = payload.workflow;
      if (!wf.runId || !wf.nodeId || !wf.state) return;
      ingestNodeEventInternal({
        runId: wf.runId,
        nodeId: wf.nodeId,
        state: wf.state as WorkflowNodeState,
        round: wf.round,
        detail: wf.detail,
      });
    });
  }
  // ③ 全量拉取兜底（推送错过后开面板仍能见到全部运行；mock 环境返回空表）。
  void pullSnapshot();
}

async function pullSnapshot() {
  try {
    const runs = await app.WorkflowRunsSnapshot();
    state = { runs: mergeRunsSnapshot(state.runs, runs ?? []), loaded: true };
    emit();
  } catch {
    state = { ...state, loaded: true };
    emit();
  }
}

function ingestNodeEventInternal(event: { runId: string; nodeId: string; state: WorkflowNodeState; round?: number; detail?: string; at?: number }) {
  const idx = state.runs.findIndex((r) => r.runId === event.runId);
  if (idx < 0) return; // 未知运行：等下一次全量快照补齐（前向兼容）
  const run = state.runs[idx];
  const prevNode = run.nodes.find((n) => n.nodeId === event.nodeId);
  const node: WireRunNode = {
    nodeId: event.nodeId,
    nodeType: prevNode?.nodeType ?? "",
    state: event.state,
    round: event.round ?? prevNode?.round,
    detail: event.detail ?? prevNode?.detail,
    changedAt: event.at ?? Date.now(),
  };
  const others = run.nodes.filter((n) => n.nodeId !== event.nodeId);
  const next: WireWorkflowRun = { ...run, nodes: [...others, node] };
  const runs = [...state.runs];
  runs[idx] = next;
  state = { runs, loaded: state.loaded };
  emit();
}

export const workflowRunFeed: WorkflowRunFeed = {
  subscribe(listener) {
    wireOnce();
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  },
  getRuns() {
    return state.runs;
  },
  ingest(runs) {
    state = { runs: mergeRunsSnapshot(state.runs, runs), loaded: true };
    emit();
  },
  ingestNodeEvent(event) {
    ingestNodeEventInternal(event);
  },
};

// ── wire → 面板视图映射（纯函数）────────────────────────────────────

type PanelNodeRun = WorkflowRunSnapshot["nodes"][string];

/** 引擎六态 → 面板五态：completed/gate_passed 都点亮为绿（gate_passed 是
 * 门通过终态；轮次与诊断经 detail 侧携带）。 */
const SIX_TO_PANEL_STATE: Record<WorkflowNodeState, PanelNodeState> = {
  queued: "queued",
  running: "running",
  completed: "done",
  gate_passed: "done",
  unconfirmed: "unconfirmed",
  failed: "failed",
};

/** 运行级状态 → 面板运行徽章（failed 映射 errored：引擎报错中止）。 */
export function liveRunStateToPanelState(state: WorkflowRunLiveState): WorkflowRunState {
  switch (state) {
    case "running":
      return "running";
    case "completed":
      return "completed";
    default:
      return "errored";
  }
}

/** 一次真运行 → 面板运行快照（节点六态投影 + 诊断文本下钻）。 */
export function liveRunSnapshot(run: WireWorkflowRun): WorkflowRunSnapshot {
  const nodes: Record<string, PanelNodeRun> = {};
  for (const n of run.nodes) {
    const state = SIX_TO_PANEL_STATE[n.state] ?? "queued";
    nodes[n.nodeId] = {
      state,
      changedAt: n.changedAt,
      detail: n.detail ? { diagnostic: n.detail } : undefined,
    };
  }
  return {
    runState: liveRunStateToPanelState(run.state),
    runError: run.error,
    startedAt: run.startedAt,
    endedAt: run.endedAt,
    nodes,
  };
}

/** wire DAG → 面板布局 DAG（字段同构直传；kind 五类全开）。 */
export function wireDagToPanelDag(dag: WireRunDag): WorkflowDag {
  const nodes: WorkflowDagNode[] = dag.nodes.map((n) => ({
    id: n.id,
    name: n.name,
    kind: n.kind,
    phase: n.phase,
    instruction: n.instruction,
    command: n.command,
  }));
  return {
    id: dag.id,
    name: dag.name,
    phases: dag.phases,
    nodes,
    edges: dag.edges.map((e) => ({ from: e.from, to: e.to, label: e.label })),
  };
}
