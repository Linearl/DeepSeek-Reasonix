import { useSyncExternalStore } from "react";

// tl1500 — 右侧栏概览「会话指标」卡折叠偏好（纯前端展示偏好）。
//
// 该卡与底部信息栏重复（tl1500 起「对话请求数/运行时间」也已接入信息栏显示项），
// 折叠收起只是省版面，数据源（ContextPanel RPC）不受影响。默认展开——
// 不改变现有用户的默认体验；localStorage 单键持久化，形态照
// lib/toolGroupingPreference.ts 先例（任务 668）：缺失/损坏值回退默认、
// 存储不可用时仅内存生效、getSnapshot 惰性水合保证首帧即取到上次选择。

const STORAGE_KEY = "reasonix-session-metrics-fold";

let current = false; // false = expanded（默认）
let hydrated = false;
const listeners = new Set<() => void>();

function emit(): void {
  for (const listener of listeners) listener();
}

function readStored(): boolean {
  try {
    return localStorage.getItem(STORAGE_KEY) === "collapsed";
  } catch {
    return false; // Storage unavailable (hardened webviews): keep the default.
  }
}

export function getSessionMetricsCollapsed(): boolean {
  if (!hydrated) {
    hydrated = true;
    current = readStored();
  }
  return current;
}

export function setSessionMetricsCollapsed(collapsed: boolean): void {
  const next = collapsed === true;
  if (getSessionMetricsCollapsed() === next) return;
  current = next;
  try {
    localStorage.setItem(STORAGE_KEY, next ? "collapsed" : "expanded");
  } catch {
    // Storage may be unavailable; the in-memory value still applies this run.
  }
  emit();
}

export function useSessionMetricsCollapsed(): boolean {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    getSessionMetricsCollapsed,
    () => false, // server snapshot: default expanded
  );
}

/** 测试隔离口：清掉水合态与存储键，让每个用例从默认态起跑。 */
export function resetSessionMetricsFoldForTests(): void {
  try {
    localStorage.removeItem(STORAGE_KEY);
  } catch {
    /* ignore */
  }
  hydrated = false;
  current = false;
  emit();
}
