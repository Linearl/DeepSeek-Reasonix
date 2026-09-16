// Task 137: resource budgets, tool payload preview, markdown DOM budget.
import assert from "node:assert/strict";
import {
  HISTORY_BODY_BUDGET_BYTES,
  MARKDOWN_BUDGET_BYTES,
  MARKDOWN_DOM_MAX_ELEMENTS,
  MARKDOWN_DOM_MAX_TABLE_CELLS,
  MAX_RESIDENT_SESSIONS,
  TOOL_PREVIEW_MAX_BLOCKS,
  TOOL_PREVIEW_MAX_BYTES,
  resourceBudgetSnapshot,
} from "../lib/resourceBudgets";
import { toolPayloadPreview, utf8ByteLength, utf8Prefix } from "../lib/toolPayloadPreview";
import { clampVirtualTableRows, decideMarkdownDomPublish } from "../lib/markdownDomBudget";
import { resetSessionDiagnostics, sessionPipelineDiagnostics } from "../lib/sessionDiagnostics";

let passed = 0;
function check(cond: boolean, label: string): void {
  if (!cond) {
    console.error(`  FAIL  ${label}`);
    process.exitCode = 1;
    return;
  }
  passed += 1;
  console.log(`  PASS  ${label}`);
}

// ── constants match the pre-consolidation defaults ──────────────────────────
check(MAX_RESIDENT_SESSIONS === 24, "resident sessions default stays 24");
check(HISTORY_BODY_BUDGET_BYTES === 192 * 1024 * 1024, "history body default stays 192MiB");
check(MARKDOWN_BUDGET_BYTES === 256 * 1024 * 1024, "markdown default stays 256MiB");
const snap = resourceBudgetSnapshot();
check(snap.maxResidentSessions === 24 && snap.historyBodyBudgetBytes === 192 << 20 && snap.markdownBudgetBytes === 256 << 20, "snapshot mirrors the three store ceilings");
check(snap.toolPreviewMaxBytes === TOOL_PREVIEW_MAX_BYTES && snap.markdownDomMaxElements === MARKDOWN_DOM_MAX_ELEMENTS, "snapshot includes preview/DOM budgets");

// ── utf8 helpers ────────────────────────────────────────────────────────────
check(utf8ByteLength("abc") === 3, "ascii byte length");
check(utf8ByteLength("中文") === 6, "CJK is 3 bytes per char");
check(utf8ByteLength("😀") === 4, "emoji is 4 bytes");
check(utf8Prefix("中文abc", 4) === "中", "utf8Prefix never splits a 3-byte char");
check(utf8Prefix("😀x", 3) === "", "utf8Prefix never splits a 4-byte char");

// ── toolPayloadPreview ──────────────────────────────────────────────────────
resetSessionDiagnostics();
const short = toolPayloadPreview("hello");
check(!short.truncated && short.text === "hello", "short payload is untouched");

const longAscii = "a".repeat(TOOL_PREVIEW_MAX_BYTES + 100);
const cut = toolPayloadPreview(longAscii);
check(cut.truncated && cut.bytes === TOOL_PREVIEW_MAX_BYTES, "byte budget truncates");
check(cut.text.length === TOOL_PREVIEW_MAX_BYTES, "ascii prefix length equals bytes");

const manyLines = Array.from({ length: TOOL_PREVIEW_MAX_BLOCKS + 10 }, (_, i) => `line-${i}`).join("\n");
const blockCut = toolPayloadPreview(manyLines);
check(blockCut.truncated && blockCut.blocks === TOOL_PREVIEW_MAX_BLOCKS, "block budget truncates");

const cjk = "中".repeat(TOOL_PREVIEW_MAX_BYTES); // 3 bytes each
const cjkCut = toolPayloadPreview(cjk);
check(cjkCut.truncated, "CJK over budget truncates");
check(utf8ByteLength(cjkCut.text) <= TOOL_PREVIEW_MAX_BYTES, "CJK prefix stays inside byte budget");
check(cjkCut.text.length % 1 === 0 && !/[\uD800-\uDFFF]/.test(cjkCut.text), "no orphan surrogate in CJK prefix");

const diag = sessionPipelineDiagnostics().resourceBudget;
check(Boolean(diag && diag.toolPreviewTruncations >= 3), "preview truncations counted in diagnostics");

// ── markdownDomBudget ───────────────────────────────────────────────────────
resetSessionDiagnostics();
const small = decideMarkdownDomPublish([
  { type: "element", tagName: "p", properties: {}, children: [{ type: "text", value: "hi" }] },
]);
check(small.publish === true, "small block publishes");

const hugeChildren = Array.from({ length: 50 }, () => ({
  type: "element" as const,
  tagName: "p",
  properties: {},
  children: Array.from({ length: 100 }, () => ({ type: "text" as const, value: "x" })),
}));
const rejected = decideMarkdownDomPublish(hugeChildren, { maxElements: 100 });
check(rejected.publish === false, "element budget rejects oversized block");
check(!rejected.publish && rejected.reason === "elements", "rejection reason is elements");

const tableCells = Array.from({ length: 20 }, () => ({
  type: "element" as const,
  tagName: "tr",
  properties: {},
  children: Array.from({ length: 10 }, () => ({
    type: "element" as const,
    tagName: "td",
    properties: {},
    children: [{ type: "text" as const, value: "c" }],
  })),
}));
const cellReject = decideMarkdownDomPublish([{ type: "element", tagName: "table", properties: {}, children: tableCells }], {
  maxElements: 10_000,
  maxCells: 50,
});
check(cellReject.publish === false && !cellReject.publish && cellReject.reason === "cells", "cell budget rejects oversized table");

const clamped = clampVirtualTableRows(["a", "b"], Array.from({ length: 100 }, (_, i) => [`r${i}`, "x"]), { maxCells: 20 });
check(clamped.droppedRows > 0 && clamped.rows.length * 2 + 2 <= 20, "virtual table clamps to cell budget");
const noClamp = clampVirtualTableRows(["a"], [["x"]], { maxCells: 100 });
check(noClamp.droppedRows === 0, "small table is not clamped");

const domDiag = sessionPipelineDiagnostics().resourceBudget;
check(Boolean(domDiag && domDiag.markdownDomRejections >= 2 && domDiag.markdownTableCellClamps >= 1), "DOM/cell clamps counted in diagnostics");

resetSessionDiagnostics();
check(sessionPipelineDiagnostics().resourceBudget === undefined, "reset clears resource budget diagnostics");

console.log(`\n${passed} passed${process.exitCode ? ", with failures" : ""}`);
if (process.exitCode) process.exit(1);
