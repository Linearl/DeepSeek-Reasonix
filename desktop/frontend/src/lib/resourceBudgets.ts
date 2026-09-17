// resourceBudgets — single source for the expensive-resource ceilings the
// desktop transcript stack shares (task 137 / upstream #10295 semantics).
// Values match the post-raise defaults already used by transcriptStore; this
// module only consolidates them so store, preview, DOM, and diagnostics read
// one set. Changing a number here is a deliberate budget change, not a drive-by.

/** Max simultaneously resident transcript sessions (weighted LRU). */
export const MAX_RESIDENT_SESSIONS = 24;

/** Global history body ceiling shared by all resident sessions. */
export const HISTORY_BODY_BUDGET_BYTES = 192 << 20; // 192 MiB

/** Global markdown parse-result cache ceiling. */
export const MARKDOWN_BUDGET_BYTES = 256 << 20; // 256 MiB

/** Tool payload preview: max UTF-8 bytes kept in a live preview slice. */
export const TOOL_PREVIEW_MAX_BYTES = 16 * 1024;

/** Tool payload preview: max logical blocks (lines / list items) kept. */
export const TOOL_PREVIEW_MAX_BLOCKS = 64;

/** Markdown DOM: max HAST elements published per page/block batch. */
export const MARKDOWN_DOM_MAX_ELEMENTS = 4_000;

/** Markdown DOM: max table cells published before the table degrades. */
export const MARKDOWN_DOM_MAX_TABLE_CELLS = 8_000;

export type ResourceBudgetSnapshot = {
  maxResidentSessions: number;
  historyBodyBudgetBytes: number;
  markdownBudgetBytes: number;
  toolPreviewMaxBytes: number;
  toolPreviewMaxBlocks: number;
  markdownDomMaxElements: number;
  markdownDomMaxTableCells: number;
};

export function resourceBudgetSnapshot(): ResourceBudgetSnapshot {
  return {
    maxResidentSessions: MAX_RESIDENT_SESSIONS,
    historyBodyBudgetBytes: HISTORY_BODY_BUDGET_BYTES,
    markdownBudgetBytes: MARKDOWN_BUDGET_BYTES,
    toolPreviewMaxBytes: TOOL_PREVIEW_MAX_BYTES,
    toolPreviewMaxBlocks: TOOL_PREVIEW_MAX_BLOCKS,
    markdownDomMaxElements: MARKDOWN_DOM_MAX_ELEMENTS,
    markdownDomMaxTableCells: MARKDOWN_DOM_MAX_TABLE_CELLS,
  };
}

// ── Task 161: user-tunable overrides (Settings → 缓存大小调整) ────────────────
// The transcript store copies these constants at module init, so overrides must
// be applied BEFORE the first store import executes — the desktop preferences
// sync (useDesktopPreferences → applyDesktopCacheTuning) runs during App boot,
// which is early enough. Restart-after-change is the documented contract.

let overrideMaxResidentSessions = 0;
let overrideHistoryBodyBudgetBytes = 0;
let overrideMarkdownBudgetBytes = 0;

/** Live (override-aware) ceilings; fall back to the shipped defaults. */
export function effectiveMaxResidentSessions(): number {
  return overrideMaxResidentSessions > 0 ? overrideMaxResidentSessions : MAX_RESIDENT_SESSIONS;
}
export function effectiveHistoryBodyBudgetBytes(): number {
  return overrideHistoryBodyBudgetBytes > 0 ? overrideHistoryBodyBudgetBytes : HISTORY_BODY_BUDGET_BYTES;
}
export function effectiveMarkdownBudgetBytes(): number {
  return overrideMarkdownBudgetBytes > 0 ? overrideMarkdownBudgetBytes : MARKDOWN_BUDGET_BYTES;
}

/**
 * Applies user preferences (MB units from [desktop] in config.toml; 0 keeps
 * the default). Bounds clamp mis-tuning into safe ranges: tab states ≤64,
 * body 32–512 MiB, markdown 64–2048 MiB.
 */
export function applyDesktopCacheTuning(prefs: { maxCachedTabs?: number; historyBodyBudgetMb?: number; markdownBudgetMb?: number }): void {
  if (prefs.maxCachedTabs !== undefined) {
    overrideMaxResidentSessions = Math.max(0, Math.min(64, Math.floor(prefs.maxCachedTabs)));
  }
  if (prefs.historyBodyBudgetMb !== undefined && prefs.historyBodyBudgetMb > 0) {
    overrideHistoryBodyBudgetBytes = Math.max(32, Math.min(512, Math.floor(prefs.historyBodyBudgetMb))) << 20;
  }
  if (prefs.markdownBudgetMb !== undefined && prefs.markdownBudgetMb > 0) {
    overrideMarkdownBudgetBytes = Math.max(64, Math.min(2048, Math.floor(prefs.markdownBudgetMb))) << 20;
  }
}
