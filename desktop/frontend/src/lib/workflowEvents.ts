// 任务 686 —— 工作流运行事件契约（预拟）+ 运行态 store（纯模块）。
//
// 契约地位：685 编排引擎未落地。事件形状按其任务书语义预拟——节点状态变迁
// （排队/运行中/完成/unconfirmed/失败/门通过）走 internal/event 总线，前端经
// bridge onEvent 收流。本模块是订阅接口骨架：事件源可插拔（真实源 685 落地
// 后接 bridge；测试与演示用 mock 源），store 只吃 WorkflowEvent 判定态。
// （handoff-686.md 等待点②：schema 对齐后在 App 侧把 wire 事件映射进来。）
//
// 秒级可见的保证：事件到达即 setState（驱动 React 订阅），无轮询。

import type { WorkflowDag } from "./workflowDag";

export type WorkflowNodeState = "queued" | "running" | "done" | "unconfirmed" | "failed";
export type WorkflowRunState = "planned" | "running" | "completed" | "errored" | "stopped";

/** 节点的下钻详情（点击节点看的四类信息 + 耗时/tokens）。 */
export interface WorkflowNodeDetail {
  /** ① ask 指令全文（subagent）。 */
  instruction?: string;
  /** ② 类型化结果（子代理产出；门节点为 stdout 摘要）。 */
  result?: string;
  /** ③ 独立确认记录（发现类结论的第二子代理重现）。 */
  confirmation?: { confirmed: boolean; evidence: string };
  /** ④ 门命令 + exit code + stderr（gate 节点）。 */
  gate?: { command: string; exitCode?: number; stderr?: string };
  durationMs?: number;
  tokens?: number;
}

export type WorkflowEvent =
  | { type: "run-state"; state: WorkflowRunState; at: number; err?: string }
  | { type: "node-state"; nodeId: string; state: WorkflowNodeState; at: number }
  | { type: "node-detail"; nodeId: string; at: number; detail: WorkflowNodeDetail };

export interface WorkflowNodeRun {
  state: WorkflowNodeState;
  /** 最近一次状态变迁时刻（unix ms）。 */
  changedAt?: number;
  detail?: WorkflowNodeDetail;
}

export interface WorkflowRunSnapshot {
  runState: WorkflowRunState;
  runError?: string;
  startedAt?: number;
  endedAt?: number;
  nodes: Record<string, WorkflowNodeRun>;
}

export interface WorkflowRunProgress {
  done: number;
  total: number;
  running: number;
  failed: number;
  unconfirmed: number;
  elapsedMs?: number;
  tokens?: number;
}

/** 空运行态：计划视图（运行前全图预览）——所有节点排队灰。 */
export function plannedRunSnapshot(dag: WorkflowDag): WorkflowRunSnapshot {
  const nodes: Record<string, WorkflowNodeRun> = {};
  for (const node of dag.nodes) nodes[node.id] = { state: "queued" };
  return { runState: "planned", nodes };
}

/** 纯归约：同一 (snapshot, event) 组合永远得到同一结果（可测、可回放）。 */
export function applyWorkflowEvent(prev: WorkflowRunSnapshot, event: WorkflowEvent): WorkflowRunSnapshot {
  switch (event.type) {
    case "run-state": {
      const startedAt = prev.startedAt ?? (event.state === "running" ? event.at : undefined);
      const endedAt =
        event.state === "completed" || event.state === "errored" || event.state === "stopped" ? event.at : prev.endedAt;
      return { ...prev, runState: event.state, runError: event.err, startedAt, endedAt };
    }
    case "node-state": {
      const existing = prev.nodes[event.nodeId];
      if (!existing) return prev; // 未知节点：忽略（前向兼容 685 的动态节点）
      return {
        ...prev,
        nodes: { ...prev.nodes, [event.nodeId]: { ...existing, state: event.state, changedAt: event.at } },
      };
    }
    case "node-detail": {
      const existing = prev.nodes[event.nodeId];
      if (!existing) return prev;
      return {
        ...prev,
        nodes: { ...prev.nodes, [event.nodeId]: { ...existing, detail: { ...existing.detail, ...event.detail } } },
      };
    }
    default:
      return prev;
  }
}

/** 进度聚合（工具条显示 n/N + 耗时 + tokens）。 */
export function workflowRunProgress(snapshot: WorkflowRunSnapshot, now?: number): WorkflowRunProgress {
  const nodes = Object.values(snapshot.nodes);
  let done = 0;
  let running = 0;
  let failed = 0;
  let unconfirmed = 0;
  let tokens = 0;
  for (const node of nodes) {
    if (node.state === "done") done += 1;
    else if (node.state === "running") running += 1;
    else if (node.state === "failed") failed += 1;
    else if (node.state === "unconfirmed") unconfirmed += 1;
    tokens += node.detail?.tokens ?? 0;
  }
  const end = snapshot.endedAt ?? now;
  const elapsedMs =
    snapshot.startedAt !== undefined && end !== undefined ? Math.max(0, end - snapshot.startedAt) : undefined;
  return { done, total: nodes.length, running, failed, unconfirmed, elapsedMs, tokens };
}

export interface WorkflowRunStore {
  getSnapshot(): WorkflowRunSnapshot;
  subscribe(listener: () => void): () => void;
  dispatch(event: WorkflowEvent): void;
  /** 重新装载一份编排（回到计划视图）。 */
  reset(dag: WorkflowDag): void;
}

/** 极小外部 store（zustand 不必为一个面板引入；useSyncExternalStore 直接吃）。 */
export function createWorkflowRunStore(dag: WorkflowDag): WorkflowRunStore {
  let snapshot = plannedRunSnapshot(dag);
  const listeners = new Set<() => void>();
  return {
    getSnapshot: () => snapshot,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    dispatch(event) {
      const next = applyWorkflowEvent(snapshot, event);
      if (next === snapshot) return;
      snapshot = next;
      for (const listener of listeners) listener();
    },
    reset(nextDag) {
      snapshot = plannedRunSnapshot(nextDag);
      for (const listener of listeners) listener();
    },
  };
}

/** mock 事件源：fixture 演示脚本（685 落地前让「运行中秒级点亮」可被人工与
 * 测试验证）。返回取消函数；stepMs 缩短即为测试注入。 */
export function playFixtureRun(
  store: WorkflowRunStore,
  opts: { stepMs?: number; now?: () => number } = {},
): () => void {
  const stepMs = opts.stepMs ?? 900;
  const now = opts.now ?? (() => Date.now());
  const script: WorkflowEvent[] = [
    { type: "run-state", state: "running", at: 0 },
    { type: "node-state", nodeId: "plan", state: "running", at: 0 },
    { type: "node-detail", nodeId: "plan", at: 0, detail: { result: "方案：7 键搬迁，先渲染后 decode…", tokens: 1840, durationMs: 4200 } },
    { type: "node-state", nodeId: "plan", state: "done", at: 0 },
    { type: "node-state", nodeId: "read-facts", state: "running", at: 0 },
    { type: "node-state", nodeId: "scan-deps", state: "running", at: 0 },
    { type: "node-detail", nodeId: "read-facts", at: 0, detail: { result: "12 键读写面清单", tokens: 2210, durationMs: 6100 } },
    { type: "node-state", nodeId: "read-facts", state: "done", at: 0 },
    { type: "node-detail", nodeId: "scan-deps", at: 0, detail: { result: "9 处测试引用 + 3 处面板引用", tokens: 1560, durationMs: 5400 } },
    { type: "node-state", nodeId: "scan-deps", state: "done", at: 0 },
    { type: "node-state", nodeId: "gate-tests", state: "running", at: 0 },
    {
      type: "node-detail",
      nodeId: "gate-tests",
      at: 0,
      detail: { gate: { command: "go test ./internal/config/... -count=1", exitCode: 1, stderr: "--- FAIL: TestRenderTOMLRoundTripsLabSwitches (0.00s)\n    missing key: experimental_workflow_panel" }, durationMs: 3800 },
    },
    { type: "node-state", nodeId: "gate-tests", state: "failed", at: 0 },
    { type: "node-state", nodeId: "fix-failures", state: "running", at: 0 },
    { type: "node-detail", nodeId: "fix-failures", at: 0, detail: { result: "fixed: TestRenderTOMLRoundTripsLabSwitches", tokens: 980, durationMs: 5100 } },
    { type: "node-state", nodeId: "fix-failures", state: "done", at: 0 },
    { type: "node-state", nodeId: "gate-tests", state: "running", at: 0 },
    { type: "node-detail", nodeId: "gate-tests", at: 0, detail: { gate: { command: "go test ./internal/config/... -count=1", exitCode: 0, stderr: "" }, durationMs: 3600 } },
    { type: "node-state", nodeId: "gate-tests", state: "done", at: 0 },
    { type: "node-state", nodeId: "confirm", state: "running", at: 0 },
    { type: "node-detail", nodeId: "confirm", at: 0, detail: { confirmation: { confirmed: true, evidence: "重现门输出：exit=0，PASS 全绿" }, tokens: 1120, durationMs: 4400 } },
    { type: "node-state", nodeId: "confirm", state: "done", at: 0 },
    { type: "node-state", nodeId: "report", state: "running", at: 0 },
    { type: "node-detail", nodeId: "report", at: 0, detail: { result: "迁移完成：12 键搬迁、门全绿、确认通过", durationMs: 2100 } },
    { type: "node-state", nodeId: "report", state: "done", at: 0 },
    { type: "run-state", state: "completed", at: 0 },
  ];
  let index = 0;
  let cancelled = false;
  const tick = () => {
    if (cancelled || index >= script.length) return;
    const event = { ...script[index] };
    // 统一盖真实时间戳（脚本里的 at 占位 0）。
    const stamp = now();
    if (event.type === "run-state" || event.type === "node-state" || event.type === "node-detail") event.at = stamp;
    store.dispatch(event);
    index += 1;
    if (index < script.length) timer = window.setTimeout(tick, stepMs);
  };
  let timer = window.setTimeout(tick, stepMs);
  return () => {
    cancelled = true;
    window.clearTimeout(timer);
  };
}
