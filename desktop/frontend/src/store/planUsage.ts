// Task 287 — plan usage polling store.
// Task 666 — the poll targets the VISIBLE tab: the Go side resolves the
// provider behind that tab's current model ref (two same-brand keys each
// query their own quota) and answers unsupported for a non-plan provider, so
// every surface hides until a plan-capable model is actually in use.
//
// Quota is provider-scoped, so unlike balance one global store serves both
// display surfaces — the status bar item and the right-dock overview card.
// Only one tab is visible at a time, so both surfaces simply report their tab
// here (last writer wins) and every query carries it; a surface without a tab
// id makes the Go side fall back to the app's active tab. The Go side decides
// support: with no plan-capable CURRENT model the bridge call returns
// unsupported in microseconds (config scan, zero I/O), so the 60s poll costs
// nothing; a configured plan gets the task-mandated 60s refresh plus a manual
// refresh entry. Hidden tabs do not poll.
//
// 任务 872 — a hot model switch (ACTIVE_MODEL_CHANGED_EVENT) takes a stricter
// path than polling: the outgoing provider's snapshot is dropped in the same
// frame the switch lands (switching flag drives a skeleton), so its card can
// never coexist with the incoming provider's card while the new answer is in
// flight.
//
// fix-provider-poll-bound 刀② 按需查询（用户 2026-11-08 裁定）——tick 走
// 门槛判定（全部内存态，零额外查询）：
//  ① tabId undefined（无可见活跃面）→ 零桥调用（Go 侧不再被空转调用）；
//  ② 最近答案 unsupported（当前 tab 当前模型非 plan 家族）→ tick 零调用，
//     家族只会经事件改变（换 tab / 切模型 / 目录变更），事件路径强制重查；
//  ③ 对照语义不变：plan 家族活跃会话照常 60s 轮询（tl1487「常开」= 功能
//     不撤，不是无会话空转）。
// 刀① 失败有界由同主题后续 commit 落地。

import { create } from "zustand";

import { app } from "../lib/bridge";
import { ACTIVE_MODEL_CHANGED_EVENT } from "../lib/activeModelChanged";
import type { PlanUsageResult } from "../lib/planUsage";

export const PLAN_USAGE_POLL_MS = 60_000;

type PlanUsageState = {
  /** Last bridge answer; null = never queried. */
  view: PlanUsageResult | null;
  /** True while a refresh is in flight (drives the card's spinner state). */
  loading: boolean;
  /** 任务 872: true only during a model-switch handover — the outgoing
   * provider's snapshot was synchronously dropped and the answer for the new
   * model has not landed. The card renders a skeleton in this window instead
   * of either provider's quota, so the two provider cards never coexist. */
  switching: boolean;
  /** The visible tab's id, as reported by the mounted surfaces. */
  tabId: string | undefined;
  /** fix-provider-poll-bound 刀②: whether the last answer was plan-capable.
   * false = the visible tab's current model resolved to a non-plan provider —
   * ticks stay silent until an event re-verifies. undefined = never queried. */
  lastAnswerSupported: boolean | undefined;
  /** Report the visible tab. Idempotent; a change re-queries immediately so a
   * tab switch never shows the previous tab's quota. */
  reportTab: (tabId: string | undefined) => void;
  /** Manual refresh (the card's refresh button). Overlapping calls are safe —
   * only the latest-started answer may land. */
  refresh: () => Promise<void>;
  /** 任务 872: the tab's active model changed — synchronously drop the
   * outgoing provider's snapshot (it must never render beside the incoming
   * provider's card) and re-query the new model. In-flight answers for the
   * old model are dropped by the refreshSeq guard. */
  invalidateForModelSwitch: () => void;
};

// Monotonic query sequence: a slower older answer must never overwrite a
// newer one (tab switched mid-flight).
let refreshSeq = 0;

export const usePlanUsageStore = create<PlanUsageState>((set, get) => ({
  view: null,
  loading: false,
  switching: false,
  tabId: undefined,
  lastAnswerSupported: undefined,
  reportTab: (tabId) => {
    if (get().tabId === tabId) return;
    // New query context: the unsupported gate must not leak into it.
    set({ tabId, lastAnswerSupported: undefined });
    void get().refresh();
  },
  invalidateForModelSwitch: () => {
    // Drop any in-flight answer for the outgoing model first, then blank the
    // snapshot — both synchronously, so nothing can slip in between. The
    // refresh below re-queries the tab's now-active (incoming) model.
    refreshSeq++;
    set({ view: null, switching: true, lastAnswerSupported: undefined });
    void get().refresh();
  },
  refresh: async () => {
    const seq = ++refreshSeq;
    const tabId = get().tabId;
    set({ loading: true });
    try {
      const view = await app.GetProviderPlanUsage(tabId ?? "");
      if (seq !== refreshSeq) return;
      // 刀②: record the supported gate with the answer (unsupported → ticks
      // stay silent until an event re-verifies).
      set({ view, lastAnswerSupported: view.supported });
    } catch {
      if (seq !== refreshSeq) return;
      // Network/transport failures keep the last snapshot and the last gate —
      // the polling cadence will retry and the note surfaces through the view
      // when the backend degraded gracefully. Never throws out of a status
      // read.
    } finally {
      if (seq === refreshSeq) set({ loading: false, switching: false });
    }
  },
}));

let polling = false;
let pollTimer: number | null = null;

function tick() {
  if (typeof document !== "undefined" && document.hidden) return;
  const state = usePlanUsageStore.getState();
  // 刀② on-demand gate — all in-memory state, zero extra queries per tick:
  //   no visible surface (tabId undefined) → zero bridge calls;
  //   known non-plan family → zero calls until an event re-verifies.
  if (state.tabId === undefined) return;
  if (state.lastAnswerSupported === false) return;
  void state.refresh();
}

/** 任务 872: a hot model switch invalidates the snapshot instead of merely
 * refreshing it — the outgoing provider's quota must disappear in the same
 * frame the switch lands, not when the new answer arrives. */
function onActiveModelChanged() {
  usePlanUsageStore.getState().invalidateForModelSwitch();
}

/** fix-provider-poll-bound: a model-catalog change (settings-driven default
 * or connection edits) can flip the plan family of the visible tab — clear
 * the gate and force one re-verification immediately (a tab with no visible
 * surface only resets the gate; the next reportTab re-queries). */
function onModelCatalogChanged() {
  usePlanUsageStore.setState({ lastAnswerSupported: undefined });
  if (usePlanUsageStore.getState().tabId === undefined) return;
  void usePlanUsageStore.getState().refresh();
}

/** ensurePlanUsagePolling starts the 60s poll once for the app lifetime.
 * Idempotent; the desktop app never tears it down (the status bar remounts
 * across layouts and the bridge call is free when no plan provider exists).
 * 刀②: the first beat goes through the gated tick — with no visible surface
 * it issues zero bridge calls; a surface's reportTab starts the querying.
 * A model-catalog change (settings-driven default or connection edits)
 * re-queries so the surfaces follow the new current model without waiting a
 * full interval; a hot model switch (ACTIVE_MODEL_CHANGED_EVENT) takes the
 * stricter invalidate path above. */
export function ensurePlanUsagePolling(): void {
  if (polling) return;
  polling = true;
  tick();
  pollTimer = window.setInterval(tick, PLAN_USAGE_POLL_MS);
  window.addEventListener("reasonix:model-catalog-changed", onModelCatalogChanged);
  window.addEventListener(ACTIVE_MODEL_CHANGED_EVENT, onActiveModelChanged);
}

// For tests: stop the timer and reset the singleton.
export function stopPlanUsagePollingForTest(): void {
  if (pollTimer !== null) {
    window.clearInterval(pollTimer);
    pollTimer = null;
  }
  window.removeEventListener("reasonix:model-catalog-changed", onModelCatalogChanged);
  window.removeEventListener(ACTIVE_MODEL_CHANGED_EVENT, onActiveModelChanged);
  polling = false;
  refreshSeq++;
  usePlanUsageStore.setState({ view: null, loading: false, switching: false, tabId: undefined, lastAnswerSupported: undefined });
}
