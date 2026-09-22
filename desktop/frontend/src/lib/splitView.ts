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

// ── divider ratio memory (task 70, 二期: 比例记忆) ────────────────────────────
// The pane split is stored beside the split state itself: an additive number
// key, same old-build-ignores-the-key contract. Dragging clamps through
// clampedSplitRatio so a stored value can never park either pane too narrow —
// and a container too narrow for two legal panes collapses to 50/50 rather
// than an unusable sliver.

/** Fraction bounds for the primary pane, plus the per-pane floor in px. */
export const SPLIT_RATIO_MIN = 0.2;
export const SPLIT_RATIO_MAX = 0.8;
const SPLIT_MIN_PANE_PX = 360;
const RATIO_KEY = "desktop:splitRatio";

/**
 * clampedSplitRatio bounds a candidate ratio two ways: the static 0.2/0.8
 * guard and a per-pane pixel floor (task 70's hard constraint: each pane stays
 * at least ~360px wide). When the container itself is too narrow for two legal
 * panes the two rules disagree, and the answer is 0.5 — an equal split is the
 * only defensible fallback. NaN/zero width reads as "no opinion" (0.5).
 */
export function clampedSplitRatio(raw: number, containerWidth: number): number {
  if (!Number.isFinite(raw)) return 0.5;
  let lo = SPLIT_RATIO_MIN;
  let hi = SPLIT_RATIO_MAX;
  if (containerWidth > 0) {
    const byPx = SPLIT_MIN_PANE_PX / containerWidth;
    if (byPx > hi - byPx) return 0.5;
    lo = Math.max(lo, byPx);
    hi = Math.min(hi, 1 - byPx);
  }
  if (lo > hi) return 0.5;
  return Math.min(hi, Math.max(lo, raw));
}

export function normalizeSplitRatio(raw: unknown): number {
  // null/undefined/"" are "no value", not zero: Number(null) === 0 would park
  // the ratio at the floor instead of the 50/50 default.
  if (raw === null || raw === undefined || raw === "") return 0.5;
  const value = typeof raw === "number" ? raw : Number(raw);
  if (!Number.isFinite(value)) return 0.5;
  return Math.min(SPLIT_RATIO_MAX, Math.max(SPLIT_RATIO_MIN, value));
}

export function loadSplitRatio(): number {
  try {
    const stored = localStorage.getItem(RATIO_KEY);
    if (stored === null) return 0.5;
    return normalizeSplitRatio(JSON.parse(stored));
  } catch {
    return 0.5;
  }
}

export function persistSplitRatio(ratio: number): void {
  try {
    localStorage.setItem(RATIO_KEY, JSON.stringify(normalizeSplitRatio(ratio)));
  } catch {
    // A full or unavailable storage must not break the layout.
  }
}

// ── cross-group drag (task 70, 二期: 拖拽跨组) ───────────────────────────────
// With a split open the strip is two groups and the secondary group holds
// exactly ONE tab (the phase-one model: SplitState.secondaryTabId). Moving a
// tab across is therefore a single-slot exchange in either direction:
//
//   "replace-secondary" — a primary tab dropped ON the secondary tab becomes
//     the new secondary; the old secondary returns to the primary group.
//   "return-primary"    — the secondary tab dropped ON a primary tab goes back
//     to the primary group; with no secondary left the split closes (the same
//     end state as its context-menu toggle).
//   null                — same group: the drop is an ordinary reorder.
export type CrossGroupDrop = "replace-secondary" | "return-primary";

export function crossGroupDropIntent(
  draggedId: string,
  targetId: string,
  splitTabId: string | null | undefined,
): CrossGroupDrop | null {
  if (!splitTabId || !draggedId || !targetId || draggedId === targetId) return null;
  if (draggedId !== splitTabId && targetId === splitTabId) return "replace-secondary";
  if (draggedId === splitTabId && targetId !== splitTabId) return "return-primary";
  return null;
}

// ── experiment gate (task 70-1) ───────────────────────────────────────────────
// The split is an experiment: with the switch off the tab context menu keeps
// exactly the pre-split item list, so nothing about the classic tab bar changes.
let splitViewEnabled = false;
const splitViewEnabledListeners = new Set<(enabled: boolean) => void>();

export function isSplitViewEnabled(): boolean {
  return splitViewEnabled;
}

export function setSplitViewEnabled(next: boolean): void {
  if (splitViewEnabled === next) return;
  splitViewEnabled = next;
  for (const listener of splitViewEnabledListeners) listener(next);
}

export function onSplitViewEnabledChange(cb: (enabled: boolean) => void): () => void {
  splitViewEnabledListeners.add(cb);
  return () => splitViewEnabledListeners.delete(cb);
}

// ── secondary pane title (task 70-5) ─────────────────────────────────────────
// The topicbar lives deep under the shell and does not receive the split state, so
// the pane title travels through this store instead of through props: App publishes
// the secondary tab's title, the topicbar renders it next to the primary one.
export type SplitPaneTitle = { text: string; hover: string };

let splitPaneTitle: SplitPaneTitle | null = null;
const splitPaneTitleListeners = new Set<(title: SplitPaneTitle | null) => void>();

export function getSplitPaneTitle(): SplitPaneTitle | null {
  return splitPaneTitle;
}

export function setSplitPaneTitle(next: SplitPaneTitle | null): void {
  const same = splitPaneTitle === next
    || (splitPaneTitle != null && next != null && splitPaneTitle.text === next.text && splitPaneTitle.hover === next.hover);
  if (same) return;
  splitPaneTitle = next;
  for (const listener of splitPaneTitleListeners) listener(next);
}

export function onSplitPaneTitleChange(cb: (title: SplitPaneTitle | null) => void): () => void {
  splitPaneTitleListeners.add(cb);
  return () => splitPaneTitleListeners.delete(cb);
}
