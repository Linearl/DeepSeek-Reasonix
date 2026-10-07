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

// ── preview tier memory (task 247: 分栏比例语义纠偏) ──────────────────────────
// The stored fraction is NOT a two-pane width ratio (the task-70 二期 model: the
// primary pane's fraction, clamped to 0.2–0.8). It is the file-preview pane's
// INTRUSION into the session area, measured against the full split container —
// the width the session area alone occupied before the split opened:
//
//   preview pane width = container width × tier
//   session pane width = container width − preview pane width
//
// e.g. a 1000px session area at tier 60% → the preview takes 600px and the
// session keeps 400px. Continuous dragging is replaced by three fixed tiers;
// the divider drag survives but snaps to the same tiers instead of moving
// freely, so the pane can only ever land where the selector would put it.

export const SPLIT_PREVIEW_TIERS = [0.4, 0.5, 0.6] as const;
export type SplitPreviewTier = (typeof SPLIT_PREVIEW_TIERS)[number];
export const SPLIT_PREVIEW_TIER_DEFAULT: SplitPreviewTier = 0.5;

/** Per-pane floor in px, carried over from task-70 二期's hard constraint. */
const SPLIT_MIN_PANE_PX = 360;
const TIER_KEY = "desktop:splitPreviewTier";
/** task-70 二期's primary-fraction key: read once for migration, then removed. */
const LEGACY_RATIO_KEY = "desktop:splitRatio";

/**
 * normalizeSplitTier snaps a candidate onto the nearest fixed tier; null and
 * other non-numeric shapes are "no value", not zero, and read as the 50/50
 * default. Out-of-range survivors of old storage (or a manual edit) still land
 * on the closest legal tier instead of being rejected.
 */
export function normalizeSplitTier(raw: unknown): SplitPreviewTier {
  if (raw === null || raw === undefined || raw === "") return SPLIT_PREVIEW_TIER_DEFAULT;
  const value = typeof raw === "number" ? raw : Number(raw);
  if (!Number.isFinite(value)) return SPLIT_PREVIEW_TIER_DEFAULT;
  // Distances compare as integer percent: binary floats make 0.55 measure
  // infinitesimally closer to 0.6 than to 0.5, and a snap must not depend on
  // that noise. Equal distance keeps the earlier (narrower) tier.
  const percent = Math.round(value * 100);
  let best: SplitPreviewTier = SPLIT_PREVIEW_TIERS[0];
  for (const tier of SPLIT_PREVIEW_TIERS) {
    if (Math.abs(Math.round(tier * 100) - percent) < Math.abs(Math.round(best * 100) - percent)) best = tier;
  }
  return best;
}

/**
 * effectiveSplitTier applies task-70's per-pane pixel floor to a tier choice:
 * a tier that would park either pane under ~360px resolves to the 50/50
 * default, and a container too narrow for two legal panes collapses to it
 * outright. NaN/zero width reads as "no opinion" (the default).
 */
export function effectiveSplitTier(tier: number, containerWidth: number): SplitPreviewTier {
  const requested = normalizeSplitTier(tier);
  if (!(containerWidth > 0)) return SPLIT_PREVIEW_TIER_DEFAULT;
  const previewPx = requested * containerWidth;
  if (previewPx < SPLIT_MIN_PANE_PX || containerWidth - previewPx < SPLIT_MIN_PANE_PX) {
    return SPLIT_PREVIEW_TIER_DEFAULT;
  }
  return requested;
}

/**
 * splitPreviewTierFromPointer maps a divider-drag position onto a tier. The
 * preview pane sits on the RIGHT, so the pointer's distance to the container's
 * right edge — not to the left — is the preview width the user is asking for.
 */
export function splitPreviewTierFromPointer(clientX: number, rectLeft: number, rectWidth: number): SplitPreviewTier {
  if (!(rectWidth > 0)) return SPLIT_PREVIEW_TIER_DEFAULT;
  const previewFraction = (rectLeft + rectWidth - clientX) / rectWidth;
  return effectiveSplitTier(previewFraction, rectWidth);
}

/**
 * migrateLegacySplitRatio converts task-70 二期 storage (the PRIMARY pane's
 * fraction) into the intrusion semantics: the preview's fraction is the
 * complement, 1 − primary, snapped to the nearest fixed tier. A user parked at
 * the old 0.2 floor (an 80% preview) lands on the widest tier, the old 0.8
 * ceiling (a 20% preview) on the narrowest — direction preserved.
 */
export function migrateLegacySplitRatio(raw: unknown): SplitPreviewTier {
  if (raw === null || raw === undefined || raw === "") return SPLIT_PREVIEW_TIER_DEFAULT;
  const value = typeof raw === "number" ? raw : Number(raw);
  if (!Number.isFinite(value)) return SPLIT_PREVIEW_TIER_DEFAULT;
  return normalizeSplitTier(1 - value);
}

export function loadSplitPreviewTier(): SplitPreviewTier {
  try {
    const stored = localStorage.getItem(TIER_KEY);
    if (stored !== null) return normalizeSplitTier(JSON.parse(stored));
    // One-time migration from the task-70 二期 key. The legacy value is
    // converted, persisted under the new key and then removed, so a stale
    // primary fraction can never resurface after a downgrade/upgrade cycle.
    const legacy = localStorage.getItem(LEGACY_RATIO_KEY);
    if (legacy !== null) {
      const migrated = migrateLegacySplitRatio(JSON.parse(legacy));
      localStorage.removeItem(LEGACY_RATIO_KEY);
      persistSplitPreviewTier(migrated);
      return migrated;
    }
    return SPLIT_PREVIEW_TIER_DEFAULT;
  } catch {
    return SPLIT_PREVIEW_TIER_DEFAULT;
  }
}

export function persistSplitPreviewTier(tier: number): void {
  try {
    localStorage.setItem(TIER_KEY, JSON.stringify(normalizeSplitTier(tier)));
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
