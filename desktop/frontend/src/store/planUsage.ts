// Task 287 — plan usage polling store.
//
// Plan quota is provider-scoped (identical across tabs), so unlike balance it
// lives in one global store that both display surfaces — the status bar item
// and the right-dock overview card — subscribe to. The Go side decides
// support: with no plan-capable provider configured the bridge call returns
// unsupported in microseconds (config scan, zero I/O), so the 60s poll costs
// nothing for users without a plan; a configured plan gets the task-mandated
// 60s refresh plus a manual refresh entry. Hidden tabs do not poll.

import { create } from "zustand";

import { app } from "../lib/bridge";
import type { PlanUsageResult } from "../lib/planUsage";

export const PLAN_USAGE_POLL_MS = 60_000;

type PlanUsageState = {
  /** Last bridge answer; null = never queried. */
  view: PlanUsageResult | null;
  /** True while a refresh is in flight (drives the card's spinner state). */
  loading: boolean;
  /** Manual refresh (the card's refresh button). Safe to call concurrently —
   * the latest answer wins. */
  refresh: () => Promise<void>;
};

export const usePlanUsageStore = create<PlanUsageState>((set, get) => ({
  view: null,
  loading: false,
  refresh: async () => {
    if (get().loading) return;
    set({ loading: true });
    try {
      const view = await app.GetProviderPlanUsage();
      set({ view });
    } catch {
      // Network/transport failures keep the last snapshot; the polling cadence
      // will retry and the note surfaces through the view when the backend
      // degraded gracefully. Never throws out of a status read.
    } finally {
      set({ loading: false });
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
 * across layouts and the bridge call is free when no plan provider exists). */
export function ensurePlanUsagePolling(): void {
  if (polling) return;
  polling = true;
  void usePlanUsageStore.getState().refresh();
  pollTimer = window.setInterval(tick, PLAN_USAGE_POLL_MS);
}

// For tests: stop the timer and reset the singleton.
export function stopPlanUsagePollingForTest(): void {
  if (pollTimer !== null) {
    window.clearInterval(pollTimer);
    pollTimer = null;
  }
  polling = false;
}
