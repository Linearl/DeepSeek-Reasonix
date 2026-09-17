// Task 151 (round 3) — per-tab DOM residency.
//
// Measured cause (bench/tab-render.mjs, real Chromium, two 100-turn tabs):
// switching between two resident tabs costs 174 ms from click to DOM-complete
// because App.tsx holds a single Transcript instance whose item set is replaced,
// so React rebuilds the incoming tab's window node by node. Giving each tab its
// own pane and only toggling visibility drops the same switch to 7 ms.
//
// The list is most-recently-used, index 0 being the tab the user is looking at.
// The limit is deliberately small: every extra resident tab keeps a full window of
// transcript DOM (markdown, rows, measurements) alive.
import { useLayoutEffect, useMemo, useState } from "react";

export const TRANSCRIPT_RESIDENCY_STORAGE_KEY = "reasonix.transcriptResidency";
/** Active tab + one tab to switch back to. The reported pain is back-and-forth. */
export const TRANSCRIPT_RESIDENCY_DEFAULT = 2;
export const TRANSCRIPT_RESIDENCY_MAX = 4;

/**
 * How many transcript panes may keep their DOM. 1 means "only the visible tab",
 * i.e. the behaviour before residency — the escape hatch if a session ever grows
 * large enough that keeping a second window alive costs more than it saves.
 */
export function transcriptResidencyLimit(): number {
  try {
    const raw = window.localStorage?.getItem(TRANSCRIPT_RESIDENCY_STORAGE_KEY);
    if (raw === null || raw === undefined) return TRANSCRIPT_RESIDENCY_DEFAULT;
    const parsed = Number.parseInt(raw, 10);
    if (!Number.isFinite(parsed)) return TRANSCRIPT_RESIDENCY_DEFAULT;
    return Math.max(1, Math.min(TRANSCRIPT_RESIDENCY_MAX, parsed));
  } catch {
    return TRANSCRIPT_RESIDENCY_DEFAULT;
  }
}

/** Pure MRU update, kept separate so the ordering rules are testable without React. */
export function nextResidentTabs(current: readonly string[], activeTabId: string | undefined, limit: number): string[] {
  const capped = Math.max(1, limit);
  const list = [...current];
  if (!activeTabId) return list.slice(0, capped);
  if (list[0] === activeTabId && list.length <= capped) return list;
  return [activeTabId, ...list.filter((tabId) => tabId !== activeTabId)].slice(0, capped);
}

/**
 * Resident tab ids, most recent first. `eligibleTabIds` are the tabs allowed to
 * keep a pane (open, local, with a transcript); anything else is dropped as soon
 * as it disappears from that set, which is what frees the DOM when a tab closes.
 */
export function useResidentTranscriptTabs(
  activeTabId: string | undefined,
  eligibleTabIds: readonly string[],
  limit: number,
): string[] {
  const [order, setOrder] = useState<string[]>([]);
  useLayoutEffect(() => {
    setOrder((current) => nextResidentTabs(current, activeTabId, limit));
  }, [activeTabId, limit]);
  return useMemo(() => {
    const kept = order.filter((tabId) => eligibleTabIds.includes(tabId));
    // The visible tab always keeps a pane: it is the one rendering right now.
    if (activeTabId && !kept.includes(activeTabId) && eligibleTabIds.includes(activeTabId)) kept.unshift(activeTabId);
    return kept.slice(0, Math.max(1, limit));
  }, [activeTabId, eligibleTabIds, limit, order]);
}
