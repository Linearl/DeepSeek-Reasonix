import { useCallback, useEffect, useRef, useState } from "react";
import { app } from "./bridge";
import type { ContextPanelInfo } from "./types";

// tl1500 — 会话指标快照（对话请求数 / 运行时间）供底部信息栏芯片读取。
//
// 这两个读数的权威源是 `app.ContextPanel(tabId)`（右侧栏概览「会话指标」卡
// 同一 RPC、同一字段：requestCount / elapsedMs），信息栏不另行推导口径，
// 保证「卡与信息栏同读数」（tl1500 任务②的前提就是两处重复）。
//
// 取数纪律（照 components/ContextPanel.tsx 既有形态，不新增机制）：
//   - 门控：仅当两枚芯片任一可见（或测试注入了快照）才发请求；不可见时零 IPC、
//     快照清空——不复制 559 之外任何「常驻后台轮询」形态；
//   - 事件驱动：context 引用随回合事件流更新时按 1s 节流刷新（与卡片的
//     usage 节流同口径），回合之间零请求——卡片在回合间同样静止；
//   - 顺序保护：seq 守卫丢弃过期响应（ContextPanel.tsx 同款 refreshSeq）；
//   - 失败静默：bridge 不可用/无活动 tab 时快照留空，芯片显示 "-"。

export interface SessionMetricsSnapshot {
  /** Session-wide request count (ContextPanelInfo.requestCount). */
  requestCount?: number;
  /** Session elapsed milliseconds (ContextPanelInfo.elapsedMs). */
  elapsedMs?: number;
}

export type SessionMetricsFetcher = (tabId: string) => Promise<ContextPanelInfo>;

const defaultFetcher: SessionMetricsFetcher = (tabId) => app.ContextPanel(tabId);

export function sessionMetricsFrom(info: ContextPanelInfo | null | undefined): SessionMetricsSnapshot | null {
  if (!info) return null;
  return { requestCount: info.requestCount, elapsedMs: info.elapsedMs };
}

/**
 * Gated ContextPanel snapshot reader for the status bar chips. `activity`
 * should be the StatusBar `context` prop: its identity changes as the tab
 * reducer absorbs wire events, and each change may trigger one throttled
 * refresh (same face as ContextPanel.tsx's usage-key refresh).
 */
export function useSessionMetricsSnapshot({
  tabId,
  enabled,
  activity,
  fetcher = defaultFetcher,
  throttleMs = 1_000,
}: {
  tabId?: string;
  enabled: boolean;
  activity: unknown;
  fetcher?: SessionMetricsFetcher;
  throttleMs?: number;
}): SessionMetricsSnapshot | null {
  const [snapshot, setSnapshot] = useState<SessionMetricsSnapshot | null>(null);
  const seqRef = useRef(0);
  const lastRefreshAtRef = useRef(0);

  const refresh = useCallback(async () => {
    if (!tabId) return;
    const seq = ++seqRef.current;
    try {
      const next = sessionMetricsFrom(await fetcher(tabId));
      if (seqRef.current === seq) setSnapshot(next);
    } catch {
      // Bridge unavailable (or the tab went away) — keep the last snapshot;
      // a stale number is still labeled by the tooltip, "-" stays for none.
    }
  }, [fetcher, tabId]);

  // Reset when the gate flips shut; clear + refetch when the gate opens or
  // the tab changes (ContextPanel.tsx does the same setInfo(null) on its
  // refresh identity — a tab switch never shows the previous tab's numbers).
  // The throttle stamp is taken here so the activity effect below never
  // duplicates the mount-time fetch.
  useEffect(() => {
    if (!enabled) {
      seqRef.current += 1;
      setSnapshot(null);
      return;
    }
    setSnapshot(null);
    lastRefreshAtRef.current = Date.now();
    void refresh();
  }, [enabled, refresh]);

  // Event-driven refresh: only while enabled, throttled to one request per
  // throttleMs. `activity` is intentionally loose — it is an identity trigger.
  useEffect(() => {
    if (!enabled) return;
    const now = Date.now();
    if (now - lastRefreshAtRef.current < throttleMs) return;
    lastRefreshAtRef.current = now;
    void refresh();
  }, [enabled, activity, refresh, throttleMs]);

  return enabled ? snapshot : null;
}
