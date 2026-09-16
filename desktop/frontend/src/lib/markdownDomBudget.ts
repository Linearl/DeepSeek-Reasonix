// markdownDomBudget — bounded publication of parsed Markdown so one oversized
// page cannot explode the DOM (task 137 / upstream #10295 semantics).
//
// The pipeline still parses the document whole (definitions, footnotes, and
// reference links must resolve globally). Only the publish step is capped:
// blocks whose element/cell walk exceeds the budget are replaced by a
// lightweight placeholder instead of mounting thousands of nodes. Session
// data is never dropped — the source markdown stays available for re-parse.

import type { Element as HastElement, RootContent as HastRootContent } from "hast";
import { MARKDOWN_DOM_MAX_ELEMENTS, MARKDOWN_DOM_MAX_TABLE_CELLS } from "./resourceBudgets";
import { noteMarkdownDomRejection, noteMarkdownTableCellClamp } from "./sessionDiagnostics";

export type DomBudgetDecision = {
  publish: true;
  elements: number;
} | {
  publish: false;
  reason: "elements" | "cells";
  elements: number;
  cells?: number;
};

type Counters = { elements: number; cells: number };

function countNode(node: HastRootContent | HastElement | undefined | null, out: Counters, maxElements: number, maxCells: number): boolean {
  if (!node) return true;
  if (out.elements >= maxElements) return false;
  out.elements += 1;
  if (node.type === "element") {
    const tag = node.tagName;
    if (tag === "td" || tag === "th") {
      out.cells += 1;
      if (out.cells > maxCells) return false;
    }
    for (const child of node.children ?? []) {
      if (!countNode(child as HastRootContent, out, maxElements, maxCells)) return false;
    }
  }
  return true;
}

/**
 * decideMarkdownDomPublish walks a block's HAST and reports whether it fits
 * the element and table-cell budgets. Stops early once either budget is blown.
 */
export function decideMarkdownDomPublish(
  children: readonly HastRootContent[],
  opts?: { maxElements?: number; maxCells?: number },
): DomBudgetDecision {
  const maxElements = Math.max(1, opts?.maxElements ?? MARKDOWN_DOM_MAX_ELEMENTS);
  const maxCells = Math.max(1, opts?.maxCells ?? MARKDOWN_DOM_MAX_TABLE_CELLS);
  const out: Counters = { elements: 0, cells: 0 };
  for (const child of children) {
    if (!countNode(child, out, maxElements, maxCells)) {
      noteMarkdownDomRejection();
      const reason: "elements" | "cells" = out.cells > maxCells ? "cells" : "elements";
      return reason === "cells"
        ? { publish: false, reason, elements: out.elements, cells: out.cells }
        : { publish: false, reason, elements: out.elements };
    }
  }
  return { publish: true, elements: out.elements };
}

/**
 * clampVirtualTableRows keeps a large plain-text virtual table inside the cell
 * budget by dropping trailing rows. Header + kept rows are returned; the
 * dropped count lets the caller render a "showing first N rows" note.
 */
export function clampVirtualTableRows(
  header: string[],
  rows: string[][],
  opts?: { maxCells?: number },
): { header: string[]; rows: string[][]; droppedRows: number } {
  const maxCells = Math.max(header.length || 1, opts?.maxCells ?? MARKDOWN_DOM_MAX_TABLE_CELLS);
  const columns = Math.max(1, header.length || rows[0]?.length || 1);
  const maxRows = Math.max(0, Math.floor(maxCells / columns) - 1); // reserve header
  if (rows.length <= maxRows) return { header, rows, droppedRows: 0 };
  noteMarkdownTableCellClamp();
  return { header, rows: rows.slice(0, maxRows), droppedRows: rows.length - maxRows };
}
