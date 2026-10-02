// Tab batch-close policy (task 223): which tabs a batch menu entry targets,
// and what close policy they get.
//
// Upstream comparison (recorded per task 223 acceptance):
//   upstream esengine/DeepSeek-Reasonix v1.38.11 (tag commit 11f9705f0,
//   2026-09-21) routes "Close other tabs" / "Close tabs to right" through its
//   per-tab onClose (desktop/frontend/src/components/TabContainer/TabBar.tsx
//   :60-80, foreach → onClose) and has no ActiveWork/keep_running machinery
//   anywhere in that tree — upstream's standard close path governs, i.e.
//   active-work closes go through its stop/ask behavior.
// The fork's "close never blocks; active work detaches to the background
// runtime" semantics (task 162, single-tab entries) must therefore be
// applied explicitly here — this module is where that decision lives.

export type BatchCloseTarget = {
  /** Ids the batch entry will close, in tab order. */
  ids: string[];
  /**
   * The tab to activate after the close, when the caller passes a menu tab.
   * (Callers may override — e.g. closing right of a tab whose right side
   * contains the currently active tab keeps the menu tab active.)
   */
  nextActiveTabId?: string;
};

type TabLike = { id: string };

/** 关闭其他: every tab except the menu tab. */
export function selectCloseOtherIds(tabs: TabLike[], menuTabId: string | null): BatchCloseTarget {
  if (!menuTabId) return { ids: [] };
  return { ids: tabs.filter((tab) => tab.id !== menuTabId).map((tab) => tab.id), nextActiveTabId: menuTabId };
}

/** 关闭非活跃标签页 (task 368): every tab except the currently ACTIVE one —
 * Chrome's "close other tabs" semantics keyed to the active tab instead of the
 * right-clicked tab. No side effects beyond the close: sessions are not
 * touched and batch close keeps running work (batchClosePolicy), so no
 * confirmation dialog and no experimental flag (正文口径：默认可用). */
export function selectCloseInactiveIds(tabs: TabLike[], activeTabId: string | null | undefined): BatchCloseTarget {
  if (!activeTabId) return { ids: [] };
  return { ids: tabs.filter((tab) => tab.id !== activeTabId).map((tab) => tab.id), nextActiveTabId: activeTabId };
}

/** 关闭右侧: every tab strictly right of the menu tab. */
export function selectCloseRightIds(
  tabs: TabLike[],
  menuIndex: number,
  menuTabId: string | null | undefined,
  activeTabId: string | null | undefined,
): BatchCloseTarget {
  if (menuIndex < 0 || menuIndex >= tabs.length - 1 || !menuTabId) return { ids: [] };
  const ids = tabs.slice(menuIndex + 1).map((tab) => tab.id);
  // Closing right takes the active tab with it when the active tab is on that
  // side; fall back to the menu tab so the strip never loses its selection.
  const nextActiveTabId = activeTabId && ids.includes(activeTabId) ? menuTabId : undefined;
  return { ids, nextActiveTabId };
}

export type ClosePolicy = "keep_running" | "stop_and_close";

/**
 * The policy batch close applies to every tab it touches (task 223):
 * always keep_running — closing detaches active work to the background
 * runtime, exactly like the single-tab close entry (task 162). The
 * explicit "停止任务并关闭" menu entry keeps stop_and_close; this function
 * is batch-only by design so a future regression cannot silently reintroduce
 * the residual upstream stop prompt on 关闭右侧/关闭其他.
 */
export function batchClosePolicy(): ClosePolicy {
  return "keep_running";
}
