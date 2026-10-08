import type { ProjectRuntimeTopic } from "./types";

export interface RuntimeState {
  schemaVersion: number;
  runtimeEpoch: string;
  revision: number;
  phase: "idle" | "executing" | "finishing" | "closed";
  running: boolean;
  turnId: string;
  turnStatus: string;
  turnEventSeq: number;
  pendingPrompt: boolean;
  cancelRequested: boolean;
  cancellable: boolean;
  backgroundJobs: number;
  activity: string;
  // 任务408 异步决策点回访：持久待批卡片数（"N 个待你决定"）。开关关时恒 0/缺省。
  pendingCards?: number;
}
export interface RuntimeSession {
  tabId: string;
  scope: string;
  workspaceRoot: string;
  topicId: string;
  sessionPath: string;
  sessionGeneration: number;
  open: boolean;
  remote: boolean;
  hostId?: string;
  freshness: "synced" | "unknown" | "syncing";
  state: RuntimeState;
}
export interface RuntimeProjection {
  epoch: string;
  revision: number;
  sessions: RuntimeSession[];
  topics: ProjectRuntimeTopic[];
}

// 任务510（b 收敛故障面）：`failed` 参数已移除。全局同步失败（store.fail）曾把
// 所有会话一起拖成 unknown——任一 tab 的同步异常就能让全部 composer 一起丢掉停
// 止按钮（用户反馈「运行中无终止按钮」的成因之一）。unknown 现在只由本会话
// freshness 决定；全局失败仍可经 store.getFailed 供项目树等消费方整体降级。
export function selectRuntime(session?: RuntimeSession) {
  const state = session?.state;
  const known = state?.schemaVersion === 1;
  const unknown = Boolean(session && session.freshness !== "synced");
  const finishing = known && state.phase === "finishing";
  const kind = unknown ? "unknown" : !known ? "legacy" : finishing ? "finishing"
    : state.cancelRequested ? "cancelling" : state.pendingPrompt ? "waiting_confirmation"
    : state.phase === "executing" ? state.activity === "streaming" ? "streaming" : "thinking"
    : state.backgroundJobs > 0 ? "background_job" : "idle";
  return { kind, known, unknown, finishing, state,
    running: known ? state.running : undefined,
    // 任务510（a 保出口）：cancellable 不再因 unknown 强制 false。unknown 只说明
    // 投影可能过期；停止请求走控制通道，与投影通道分离，过期数据不该锁死安全出口。
    cancellable: known ? !finishing && state.cancellable && !state.cancelRequested : undefined,
    spinning: !unknown && (kind === "thinking" || kind === "streaming" || kind === "cancelling" || kind === "background_job"),
  };
}

export function createRuntimeStateStore() {
  let snapshot: RuntimeProjection | undefined;
  let failed = false;
  const listeners = new Set<() => void>();
  const notify = () => listeners.forEach(listener => listener());
  return {
    getSnapshot: () => snapshot,
    getFailed: () => failed,
    subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; },
    fail() { if (!failed) { failed = true; notify(); } },
    commit(next: RuntimeProjection) {
      if (snapshot === next && !failed) return;
      snapshot = next;
      failed = false;
      notify();
    },
  };
}
export const runtimeStateStore = createRuntimeStateStore();
