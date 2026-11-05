// tl1410 会话运行状态双源失步对账：顶栏 TabBar（tab.running，经 tabMetas——
// turn 等 5 种 Wire 事件触发 ListTabs 刷新 + 15s 前台兜底）与侧栏 ProjectTree
// （node.running，经 runtimeStateStore——runtime-state:changed 推送 + 30s 轮询）
// 是同一语义（会话是否在干活）的两条独立前端数据链。Go 侧同源（ListTabs 的
// TabMeta.Running 与 GetRuntimeStateSnapshot 的 topics/sessions 都实时派生自
// controller RuntimeStatus，tabs.go:2490 / runtime_state.go:controllerRuntimeState），
// 失步发生在前端取数通道：CPU 风暴事件泵阻塞时 turn_done 丢失/延迟，tabMetas
// 残留 running=true 而侧栏已恢复——用户 20261105 报告「顶栏 Running、侧栏不在
// 工作、数秒后自行恢复」的现场。
//
// 对账设计：以 runtimeStateStore 投影为对照源（schemaVersion=1 且 freshness=
// synced 才可信，510 unknown/legacy 不当真相），把每条 open 本地会话的
// claimsWork（与 Go 侧 TabMeta.Running 同一口径：running||pendingPrompt||
// backgroundJobs>0，tabs.go:2490）与 tabMetas 同 tabId 的 tab.running 对比，
// 不一致即触发一次 ListTabs 全量拉取（请求-响应通道，不经事件泵，CPU 风暴下
// 比推送可靠）。事件通路正常时订阅回检毫秒级收敛；事件丢失时 tick 兜底，
// 失步窗口 ≤ tick 周期 + 一次 IPC。
//
// 冷却：refresh 后 COOLDOWN 内不再触发——防两类误报死循环（Go 侧 TabMeta 有
// tl1266 sessionMissing 断开投影而 runtime-state 侧没有，投影规则差异导致的
// 「拉回仍不一致」场景被冷却限频到最坏 1 次/15s，与既有 15s 前台兜底同量级）。

import { useEffect, useRef } from "react";
import { runtimeStateStore, type RuntimeProjection, type RuntimeState } from "./runtimeStateStore";
import type { TabMeta } from "./types";

/** 对账兜底 tick 周期：事件丢失时失步窗口的上界（验收 ≤5s 收敛）。 */
export const TAB_RUNTIME_RECONCILE_TICK_MS = 5_000;
/** 一次对账拉取后的冷却：误报场景 IPC 频率上界 = 1 次/COOLDOWN。 */
export const TAB_RUNTIME_RECONCILE_COOLDOWN_MS = 15_000;

/** 对账可读的最小 tab 形（TabMeta 超集裁剪，测试无需构造全量 meta）。 */
export type ReconcileTab = Pick<TabMeta, "id" | "running">;

/**
 * 单会话的 claimsWork 判定（与 Go 侧 tabs.go TabMeta.Running 同口径）。
 * 返回 undefined = 投影不可信（schemaVersion !== 1，legacy 语义残缺），
 * 对账跳过该会话——拿过期/残缺数据当真相会误触发拉取。
 */
export function reconcileClaimsWork(state: RuntimeState | undefined): boolean | undefined {
  if (!state || state.schemaVersion !== 1) return undefined;
  return Boolean(state.running) || Boolean(state.pendingPrompt) || (Number(state.backgroundJobs) || 0) > 0;
}

/**
 * 找出第一条 store 投影与 tabMetas 运行态不一致的会话（返回其 tabId）。
 * 全部一致（或没有可信投影可比）返回 null。
 * 跳过：非 open 会话（tab 已关）、remote 会话（远端另有 freshness/状态通道，
 * 语义不同源，不纳入本次对账）、不可信投影、tabMetas 里找不到的 tabId
 * （file 类 tab、尚未入列表的 tab——找不到不等于失步，不误报）。
 */
export function findTabRuntimeMismatch(
  store: RuntimeProjection | undefined,
  tabs: readonly ReconcileTab[],
): string | null {
  if (!store) return null;
  for (const session of store.sessions) {
    if (!session.open || session.remote) continue;
    const claims = reconcileClaimsWork(session.state);
    if (claims === undefined) continue;
    if (session.freshness !== "synced") continue;
    const tab = tabs.find(candidate => candidate.id === session.tabId);
    if (!tab) continue;
    if (Boolean(tab.running) !== claims) return session.tabId;
  }
  return null;
}

export interface TabRuntimeReconcilerPorts {
  /** runtimeStateStore（或测试注入的同形 store）。 */
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => RuntimeProjection | undefined;
  /** 周期兜底定时器装配（测试注入 fake timers / 手动驱动）。 */
  setInterval_: (callback: () => void, ms: number) => unknown;
  clearInterval_: (timer: unknown) => void;
  /** 对账触发：ListTabs 全量拉取（请求-响应通道）。 */
  refresh: () => void;
  now?: () => number;
  tickMs?: number;
  cooldownMs?: number;
}

export function createTabRuntimeReconciler(ports: TabRuntimeReconcilerPorts) {
  const tickMs = ports.tickMs ?? TAB_RUNTIME_RECONCILE_TICK_MS;
  const cooldownMs = ports.cooldownMs ?? TAB_RUNTIME_RECONCILE_COOLDOWN_MS;
  const now = ports.now ?? (() => Date.now());
  let lastRefreshAt = -Infinity;
  let disposed = false;
  let tabMetas: readonly ReconcileTab[] = [];
  const recheck = () => {
    if (disposed) return;
    if (now() - lastRefreshAt < cooldownMs) return;
    if (findTabRuntimeMismatch(ports.getSnapshot(), tabMetas) !== null) {
      lastRefreshAt = now();
      ports.refresh();
    }
  };
  const stop = ports.subscribe(recheck);
  const timer = ports.setInterval_(recheck, tickMs);
  return {
    /** 每次 tabMetas 变化时由 hook 回填最新列表（store 事件回检用最新值）。 */
    setTabMetas: (tabs: readonly ReconcileTab[]) => { tabMetas = tabs; },
    /** 事件到达（store commit）与 tick 兜底都走同一条 recheck。 */
    recheck,
    dispose: () => {
      disposed = true;
      stop();
      ports.clearInterval_(timer);
    },
  };
}

/**
 * App 挂载点：订阅 runtimeStateStore + tick 兜底，检测到顶栏 tabMetas 与
 * store 投影运行态失步即触发 refreshTabMetas 拉齐。零渲染参与——失步检测
 * 是副作用型，不挂 useSyncExternalStore（不因 store 更新重渲染 App）。
 */
export function useTabRuntimeReconcile(
  refreshTabMetas: () => Promise<TabMeta[]> | void,
  tabMetas: readonly TabMeta[],
): void {
  const tabMetasRef = useRef(tabMetas);
  tabMetasRef.current = tabMetas;
  const refreshRef = useRef(refreshTabMetas);
  refreshRef.current = refreshTabMetas;
  // 渲染期回填（先例：AppRuntime 的 workspaceScopeActiveTabRef）——effect 只建
  // 一次，tabMetas 每次刷新都要喂给 reconciler 的 store 事件回检。
  const reconcilerRef = useRef<ReturnType<typeof createTabRuntimeReconciler> | undefined>(undefined);
  reconcilerRef.current?.setTabMetas(tabMetas);
  useEffect(() => {
    const reconciler = createTabRuntimeReconciler({
      subscribe: runtimeStateStore.subscribe,
      getSnapshot: runtimeStateStore.getSnapshot,
      setInterval_: (callback, ms) => window.setInterval(callback, ms),
      clearInterval_: timer => window.clearInterval(timer as number),
      refresh: () => { void refreshRef.current(); },
    });
    reconciler.setTabMetas(tabMetasRef.current);
    reconcilerRef.current = reconciler;
    return () => {
      reconcilerRef.current = undefined;
      reconciler.dispose();
    };
  }, []);
}
