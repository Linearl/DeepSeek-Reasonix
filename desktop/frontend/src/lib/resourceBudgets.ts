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
