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

import { create } from "zustand";

import { app } from "../lib/bridge";
import type { PlanUsageResult } from "../lib/planUsage";

export const PLAN_USAGE_POLL_MS = 60_000;

type PlanUsageState = {
  /** Last bridge answer; null = never queried. */
  view: PlanUsageResult | null;
  /** True while a refresh is in flight (drives the card's spinner state). */
  loading: boolean;
  /** The visible tab's id, as reported by the mounted surfaces. */
  tabId: string | undefined;
  /** Report the visible tab. Idempotent; a change re-queries immediately so a
   * tab switch never shows the previous tab's quota. */
  reportTab: (tabId: string | undefined) => void;
  /** Manual refresh (the card's refresh button). Overlapping calls are safe —
   * only the latest-started answer may land. */
  refresh: () => Promise<void>;
};

// Monotonic query sequence: a slower older answer must never overwrite a
// newer one (tab switched mid-flight).
let refreshSeq = 0;

export const usePlanUsageStore = create<PlanUsageState>((set, get) => ({
  view: null,
  loading: false,
  tabId: undefined,
  reportTab: (tabId) => {
    if (get().tabId === tabId) return;
    set({ tabId });
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
      if (seq === refreshSeq) set({ loading: false });
    }
  },
}));

let polling = false;
let pollTimer: number | null = null;

function tick() {
  if (typeof document !== "undefined" && document.hidden) return;
  void usePlanUsageStore.getState().refresh();
}

/** ensurePlanUsagePolling starts the 60s poll once for the app lifetime.
 * Idempotent; the desktop app never tears it down (the status bar remounts
 * across layouts and the bridge call is free when no plan provider exists).
 * A model-catalog change (settings-driven default or connection edits)
 * re-queries so the surfaces follow the new current model without waiting a
 * full interval. */
export function ensurePlanUsagePolling(): void {
  if (polling) return;
  polling = true;
  void usePlanUsageStore.getState().refresh();
  pollTimer = window.setInterval(tick, PLAN_USAGE_POLL_MS);
  window.addEventListener("reasonix:model-catalog-changed", tick);
}

// For tests: stop the timer and reset the singleton.
export function stopPlanUsagePollingForTest(): void {
  if (pollTimer !== null) {
    window.clearInterval(pollTimer);
    pollTimer = null;
  }
  window.removeEventListener("reasonix:model-catalog-changed", tick);
  polling = false;
  refreshSeq++;
  usePlanUsageStore.setState({ view: null, loading: false, tabId: undefined });
}
