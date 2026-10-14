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
  reportTab: (tabId) => {
    if (get().tabId === tabId) return;
    set({ tabId });
    void get().refresh();
  },
  invalidateForModelSwitch: () => {
    // Drop any in-flight answer for the outgoing model first, then blank the
    // snapshot — both synchronously, so nothing can slip in between. The
    // refresh below re-queries the tab's now-active (incoming) model.
    refreshSeq++;
    set({ view: null, switching: true });
    void get().refresh();
  },
  refresh: async () => {
    const seq = ++refreshSeq;
    const tabId = get().tabId;
    set({ loading: true });
    try {
      const view = await app.GetProviderPlanUsage(tabId ?? "");
      if (seq !== refreshSeq) return;
      set({ view });
    } catch {
      if (seq !== refreshSeq) return;
      // Network/transport failures keep the last snapshot; the polling cadence
      // will retry and the note surfaces through the view when the backend
      // degraded gracefully. Never throws out of a status read.
    } finally {
      if (seq === refreshSeq) set({ loading: false, switching: false });
    }
  },
}));

let polling = false;
let pollTimer: number | null = null;

function tick() {
  if (typeof document !== "undefined" && document.hidden) return;
  void usePlanUsageStore.getState().refresh();
}

/** 任务 872: a hot model switch invalidates the snapshot instead of merely
 * refreshing it — the outgoing provider's quota must disappear in the same
 * frame the switch lands, not when the new answer arrives. */
function onActiveModelChanged() {
  usePlanUsageStore.getState().invalidateForModelSwitch();
}

/** ensurePlanUsagePolling starts the 60s poll once for the app lifetime.
 * Idempotent; the desktop app never tears it down (the status bar remounts
 * across layouts and the bridge call is free when no plan provider exists).
 * A model-catalog change (settings-driven default or connection edits)
 * re-queries so the surfaces follow the new current model without waiting a
 * full interval; a hot model switch (ACTIVE_MODEL_CHANGED_EVENT) takes the
 * stricter invalidate path above. */
export function ensurePlanUsagePolling(): void {
  if (polling) return;
  polling = true;
  void usePlanUsageStore.getState().refresh();
  pollTimer = window.setInterval(tick, PLAN_USAGE_POLL_MS);
  window.addEventListener("reasonix:model-catalog-changed", tick);
  window.addEventListener(ACTIVE_MODEL_CHANGED_EVENT, onActiveModelChanged);
}

// For tests: stop the timer and reset the singleton.
export function stopPlanUsagePollingForTest(): void {
  if (pollTimer !== null) {
    window.clearInterval(pollTimer);
    pollTimer = null;
  }
  window.removeEventListener("reasonix:model-catalog-changed", tick);
  window.removeEventListener(ACTIVE_MODEL_CHANGED_EVENT, onActiveModelChanged);
  polling = false;
  refreshSeq++;
  usePlanUsageStore.setState({ view: null, loading: false, switching: false, tabId: undefined });
}
