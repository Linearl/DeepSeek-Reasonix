// Split view: two conversations side by side in the classic layout (task 70).
//
// Only classic can host this. The window IS the tab strip there —
// AppChrome renders `app-chrome__tab-strip` for classic and a bare spacer for
// workbench — so workbench/creation have no tabs to group and nowhere to put a
// second pane. That is a property of the layout, not a scope cut.
//
// Storage is local-only, following the project-tree localStorage precedent: the
// shape is additive, an older build simply ignores the key, and nothing here is
// read until a split is actually opened.
//
// The states are deliberately explicit rather than inferred from a missing tab:
// dropping a split because its tab closed must not also drop the user's choice of
// which pane the status bar follows.

export type SplitPane = "primary" | "secondary";

export interface SplitState {
  /** Tab shown in the right pane. null means the split is closed. */
  secondaryTabId: string | null;
  /** Which pane the status bar, the overview panel and the shortcuts follow. */
  focusedPane: SplitPane;
}

const SPLIT_KEY = "desktop:splitView";

export const CLOSED_SPLIT: SplitState = { secondaryTabId: null, focusedPane: "primary" };

/** splitIsActive reports whether two transcripts should be mounted. */
export function splitIsActive(state: SplitState): boolean {
  return state.secondaryTabId !== null;
}

/**
 * normalizeSplitState keeps a stored value usable: an unknown pane falls back to
 * primary, and a missing/blank secondary tab reads as closed. Anything else would
 * let a stale key mount an empty pane on startup.
 */
export function normalizeSplitState(raw: unknown): SplitState {
  if (!raw || typeof raw !== "object") return CLOSED_SPLIT;
  const candidate = raw as Partial<SplitState>;
  const secondary = typeof candidate.secondaryTabId === "string" && candidate.secondaryTabId.trim() !== ""
    ? candidate.secondaryTabId
    : null;
  return {
    secondaryTabId: secondary,
    focusedPane: candidate.focusedPane === "secondary" ? "secondary" : "primary",
  };
}

export function loadSplitState(): SplitState {
  try {
    const stored = localStorage.getItem(SPLIT_KEY);
    if (!stored) return CLOSED_SPLIT;
    return normalizeSplitState(JSON.parse(stored));
  } catch {
    return CLOSED_SPLIT;
  }
}

export function persistSplitState(state: SplitState): void {
  try {
    if (!splitIsActive(state) && state.focusedPane === "primary") {
      localStorage.removeItem(SPLIT_KEY);
      return;
    }
    localStorage.setItem(SPLIT_KEY, JSON.stringify(state));
  } catch {
    // A full or unavailable storage must not break the layout.
  }
}

/**
 * reconcileSplitState drops a split whose tab is gone while preserving the pane the
 * user had focused — closing a tab is not a statement about focus.
 */
export function reconcileSplitState(state: SplitState, liveTabIds: readonly string[]): SplitState {
  if (state.secondaryTabId === null) return state;
  if (liveTabIds.includes(state.secondaryTabId)) return state;
  return { secondaryTabId: null, focusedPane: state.focusedPane };
}
