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

// 981 二轮（候选2，残余放大链 B）：runtime 通道此前零节流——活跃轮次每次
// thinking↔streaming 翻转都 commit→notify（无上限），每个订阅方（侧栏
// FolderActivity 逐文件夹、useRuntimeState 的停止按钮、远端树合并）都整轮
// 重渲染（调查报告 20261019 §3.1-B）。生产单例的 commit 按 400ms 合并、
// 窗口内只保最新一帧：60s 内 commit→notify ≤ 60000/400 = 150（验收硬条目）。
// 状态点更新随之延迟 ≤400ms——视觉无感（本就有翻转动画）。
export const RUNTIME_COMMIT_BATCH_MS = 400;

// 内容等价判定（候选2 下半）：sessions/topics 由 acceptRuntimeState 保住未变
// 成员的对象引用（逐元素 === 即可判等价）；revision 只是记账字段，内容等价
// 的新投影没有可渲染的差异，跳过 notify。
function runtimeProjectionContentEquals(current: RuntimeProjection, next: RuntimeProjection): boolean {
  if (current.epoch !== next.epoch) return false;
  if (current.sessions.length !== next.sessions.length || current.topics.length !== next.topics.length) return false;
  for (let index = 0; index < current.sessions.length; index++) {
    if (current.sessions[index] !== next.sessions[index]) return false;
  }
  for (let index = 0; index < current.topics.length; index++) {
    if (current.topics[index] !== next.topics[index]) return false;
  }
  return true;
}

export interface RuntimeStateStoreOptions {
  // 0（缺省）= 同步 commit，直接构造的 store（测试）保持原语义；生产单例传
  // RUNTIME_COMMIT_BATCH_MS。
  batchMs?: number;
}

export function createRuntimeStateStore(options?: RuntimeStateStoreOptions) {
  const batchMs = options?.batchMs ?? 0;
  let snapshot: RuntimeProjection | undefined;
  let failed = false;
  let pending: RuntimeProjection | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const listeners = new Set<() => void>();
  const notify = () => listeners.forEach(listener => listener());
  const apply = (next: RuntimeProjection) => { snapshot = next; failed = false; notify(); };
  const clearTimer = () => { if (timer !== undefined) { clearTimeout(timer); timer = undefined; } };
  return {
    getSnapshot: () => snapshot,
    getFailed: () => failed,
    subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; },
    fail() {
      // 失败比在途的合批更新更新：丢弃未落的 pending（其内容早于失败），否则
      // 窗口结束时它会把 failed 清回 false——同步语义下「commit 后 fail」的
      // 终态就是 failed=true，这里保持同一终态；恢复由下一次 commit/周期读完成。
      clearTimer();
      pending = undefined;
      if (!failed) { failed = true; notify(); }
    },
    commit(next: RuntimeProjection) {
      if (snapshot === next && !failed) return;
      if (!failed && snapshot && runtimeProjectionContentEquals(snapshot, next)) {
        // 等价内容跳过 notify；新引用仍要换上（revision 记账前移，后续 stale
        // 判定不失真）。若在途 pending 比本次更旧且内容不同，被本次更新取代，
        // 一并丢弃（最新帧为准）。
        clearTimer();
        pending = undefined;
        snapshot = next;
        return;
      }
      if (batchMs <= 0) { apply(next); return; }
      pending = next;
      if (timer !== undefined) return;
      timer = setTimeout(() => {
        timer = undefined;
        const flushed = pending;
        pending = undefined;
        if (flushed !== undefined) apply(flushed);
      }, batchMs);
    },
    // 立刻落地在途合批：测试的同步断言 + 生产卸载收尾用。
    flush() {
      clearTimer();
      const flushed = pending;
      pending = undefined;
      if (flushed !== undefined) apply(flushed);
    },
  };
}
export const runtimeStateStore = createRuntimeStateStore({ batchMs: RUNTIME_COMMIT_BATCH_MS });
