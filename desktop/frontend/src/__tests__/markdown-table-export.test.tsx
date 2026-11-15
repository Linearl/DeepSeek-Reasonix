// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/markdown-table-export.test.tsx
//
// tl1596 — per-table copy/download button group (learned from zcode
// markdown-table.tsx:83/111). Acceptance matrix:
//   1. All three table states (small / large collapsed / large virtualized)
//      expose both buttons and export the FULL original table — the virtualized
//      state must read the React element tree (original data), not the DOM,
//      because a virtualized body only mounts the visible row window.
//   2. Formula-shaped cells (=cmd) must reach the CSV demoted to plain text
//      (leading apostrophe) so Excel does not execute them.
//   3. Chinese CSV payloads carry a UTF-8 BOM so Excel detects the encoding.
//   4. Rebuilt Markdown escapes pipes as "\|" (and newlines as <br>).
//
// jsdom note: Blob.text() UTF-8-decodes per spec and strips a leading BOM, so
// BOM assertions inspect the raw first bytes (EF BB BF) instead of the decoded
// text. jsdom mounting of virtualizer body rows stays at 0 — the same known
// pre-existing red as markdown-table-virtual.test.tsx; tl1596 asserts the
// virtualized state's exports come from original data instead.

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { createComponents } from "../components/markdownComponents";
import { MarkdownTable, VirtualMarkdownSourceTable } from "../components/MarkdownTable";
import {
  buildCsvTableText,
  buildMarkdownTableText,
  escapeCsvCell,
  normalizeTableCellText,
} from "../lib/tableExport";
import { hastBlockToJsx } from "../lib/hastJsx";
import { parseMarkdownToBlocks } from "../lib/markdownPipeline";
import type { VirtualMarkdownTableData } from "../lib/largeMarkdownTable";

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

class NoopResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver = NoopResizeObserver as unknown as typeof ResizeObserver;
dom.window.ResizeObserver = NoopResizeObserver as unknown as typeof ResizeObserver;

// Clipboard channel: CopyButton's writeClipboardText prefers
// navigator.clipboard.writeText; capture what the buttons write.
const clipboardWrites: string[] = [];
Object.defineProperty(dom.window.navigator, "clipboard", {
  configurable: true,
  value: {
    writeText: (text: string) => {
      clipboardWrites.push(text);
      return Promise.resolve();
    },
  },
});

// Download channel: route createObjectURL blobs into a capture list and stub
// anchor clicks so jsdom never attempts navigation. The component must observe
// the same globals as this harness, so route Blob/URL through jsdom too.
const downloadedBlobs: Blob[] = [];
Object.defineProperty(globalThis, "Blob", { configurable: true, value: dom.window.Blob });
Object.defineProperty(globalThis, "URL", { configurable: true, value: dom.window.URL });
(dom.window.URL as unknown as { createObjectURL: (blob: Blob) => string }).createObjectURL = (
  blob: Blob,
) => {
  downloadedBlobs.push(blob);
  return "blob:capture";
};
(dom.window.URL as unknown as { revokeObjectURL: (url: string) => void }).revokeObjectURL = () => {};
dom.window.HTMLAnchorElement.prototype.click = function click() {
  /* captured via downloadedBlobs; suppress jsdom navigation */
};

const flush = () => act(async () => {
  await new Promise((resolve) => setTimeout(resolve, 20));
});

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");

async function withRoot(fn: (root: Root) => Promise<void> | void) {
  const root = createRoot(rootEl);
  await fn(root);
  await act(async () => root.unmount());
}

function exportButtons() {
  return {
    copy: rootEl.querySelector<HTMLButtonElement>('[data-table-export="copy"]'),
    download: rootEl.querySelector<HTMLButtonElement>('[data-table-export="download"]'),
  };
}

function lastClipboardWrite(): string {
  return clipboardWrites[clipboardWrites.length - 1] ?? "";
}

async function lastDownloadStartsWithBom(): Promise<boolean> {
  const blob = downloadedBlobs[downloadedBlobs.length - 1];
  if (!blob) return false;
  const bytes = new Uint8Array(await blob.arrayBuffer());
  return bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf;
}

function lastDownloadText(): Promise<string> {
  const blob = downloadedBlobs[downloadedBlobs.length - 1];
  if (!blob) return Promise.resolve("");
  return blob.text();
}

async function clickCopy(): Promise<boolean> {
  const before = clipboardWrites.length;
  await act(async () => {
    exportButtons().copy?.click();
    await Promise.resolve();
  });
  await flush();
  return clipboardWrites.length > before;
}

async function clickDownload(): Promise<boolean> {
  const before = downloadedBlobs.length;
  await act(async () => {
    exportButtons().download?.click();
    await Promise.resolve();
  });
  await flush();
  return downloadedBlobs.length > before;
}

// ── unit: escapeCsvCell — formula injection demotion (acceptance ②) ──────────
console.log("\nescapeCsvCell: formula-injection guard");
eq(escapeCsvCell("=cmd"), "'=cmd", "=cmd is demoted with a leading apostrophe");
eq(escapeCsvCell("=SUM(A1:B2)"), "'=SUM(A1:B2)", "=SUM(...) is demoted");
eq(escapeCsvCell("+8613800000000"), "'+8613800000000", "leading + is demoted");
eq(escapeCsvCell("-5"), "'-5", "leading - is demoted");
eq(escapeCsvCell("@RISK"), "'@RISK", "leading @ is demoted");
eq(escapeCsvCell(" =cmd"), "' =cmd", "whitespace-prefixed formula is demoted");
eq(escapeCsvCell("\t=cmd"), "'\t=cmd", "tab-prefixed cell is demoted");
eq(escapeCsvCell("plain text"), "plain text", "plain text passes through");
eq(escapeCsvCell("中文内容"), "中文内容", "Chinese text passes through unmangled");
eq(escapeCsvCell("a,b"), '"a,b"', "comma cells are quoted");
eq(escapeCsvCell('say "hi"'), '"say ""hi"""', "quotes are doubled inside quoted cells");
eq(escapeCsvCell("a\nb"), '"a\nb"', "newlines force quoting");

// ── unit: buildCsvTableText — BOM + structure (acceptance ③) ─────────────────
console.log("\nbuildCsvTableText: BOM + payload");
{
  const csv = buildCsvTableText({ header: ["名称", "数量"], rows: [["机密", "=cmd"], ["a", "b,c"]] });
  ok(csv.charCodeAt(0) === 0xfeff, "CSV payload starts with a UTF-8 BOM (U+FEFF)");
  ok(csv.includes("名称,数量"), "Chinese header survives verbatim after the BOM");
  ok(csv.includes("机密,'=cmd"), "formula-shaped body cell is demoted");
  ok(csv.includes('"b,c"'), "comma cells stay quoted");
  eq(csv.split("\r\n").length, 3, "rows are CRLF-separated");
}

// ── unit: buildMarkdownTableText — pipe escaping (acceptance ④) ──────────────
console.log("\nbuildMarkdownTableText: pipe/newline escaping");
{
  const md = buildMarkdownTableText({
    header: ["a|b", "c\\d"],
    rows: [["x\ny", ""]],
  });
  eq(md, "| a\\|b | c\\\\d |\n| --- | --- |\n| x<br>y |  |", "pipes, backslashes and newlines escape for GFM re-parse");
  eq(buildMarkdownTableText({ header: [], rows: [] }), "", "empty table exports nothing");
}

// ── unit: normalizeTableCellText ──────────────────────────────────────────────
console.log("\nnormalizeTableCellText");
eq(normalizeTableCellText("  a   b  "), "a b", "horizontal whitespace collapses");
eq(normalizeTableCellText("a\nb"), "a\nb", "<br>-derived line breaks survive");
eq(normalizeTableCellText("a \n b"), "a\nb", "spaces around line breaks collapse");

// ── state 1: small table — buttons present, exports correct ──────────────────
console.log("\nsmall table: buttons + exports");
await withRoot(async (root) => {
  await act(async () => {
    root.render(
      <MarkdownTable>
        <thead>
          <tr><th>名称</th><th>值</th></tr>
        </thead>
        <tbody>
          <tr><td>a|b</td><td>=cmd</td></tr>
          <tr><td>中文</td><td>2</td></tr>
        </tbody>
      </MarkdownTable>,
    );
  });
  await flush();
  const buttons = exportButtons();
  ok(buttons.copy, "small table exposes the copy button");
  ok(buttons.download, "small table exposes the download button");
  ok(!buttons.copy?.disabled, "copy is enabled for a non-empty table");

  ok(await clickCopy(), "copy click writes to the clipboard");
  eq(
    lastClipboardWrite(),
    "| 名称 | 值 |\n| --- | --- |\n| a\\|b | =cmd |\n| 中文 | 2 |",
    "clipboard Markdown re-escapes pipes and keeps Chinese verbatim",
  );

  ok(await clickDownload(), "download click produces a blob");
  ok(await lastDownloadStartsWithBom(), "downloaded CSV carries a UTF-8 BOM");
  const csv = await lastDownloadText();
  ok(csv.startsWith("名称,值"), "Chinese header is the first decoded CSV text");
  ok(csv.includes("'=cmd"), "downloaded CSV demotes the =cmd cell");

  await act(async () => root.unmount());
});

// ── empty table: copy disabled with an explained reason ──────────────────────
{
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(
      <MarkdownTable>
        <thead />
        <tbody />
      </MarkdownTable>,
    );
  });
  await flush();
  const copy = exportButtons().copy;
  ok(copy?.disabled, "copy is disabled for a table without any rows");
  eq(copy?.getAttribute("title"), "Table has no rows to copy", "disabled copy explains why");
  await act(async () => root.unmount());
}

// ── state 2/3: large table collapsed + virtualized (acceptance ①) ────────────
console.log("\nlarge table: collapsed + virtualized export from original data");
{
  const rowCount = 150;
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(
      <MarkdownTable>
        <thead>
          <tr><th>name</th><th>=值</th></tr>
        </thead>
        <tbody>
          {Array.from({ length: rowCount }, (_, i) => (
            <tr key={i}><td>row {i}</td><td>{i}</td></tr>
          ))}
        </tbody>
      </MarkdownTable>,
    );
  });
  await flush();

  ok(exportButtons().copy && exportButtons().download, "collapsed large table exposes both buttons");
  eq(rootEl.querySelectorAll(".md-table-scroll tbody tr").length, 12, "DOM only mounts the 12 preview rows");

  ok(await clickCopy(), "collapsed state: copy writes to the clipboard");
  const collapsedMd = lastClipboardWrite();
  ok(collapsedMd.includes("| row 149 | 149 |"), "collapsed state exports row 149 (beyond the DOM preview)");
  ok(collapsedMd.includes("| row 13 | 13 |"), "collapsed state exports row 13 (beyond the DOM preview)");
  eq(collapsedMd.split("\n").length, rowCount + 2, "collapsed state exports header + separator + all 150 rows");
  ok(collapsedMd.includes("| =值 |"), "header formula-shaped text round-trips verbatim");

  ok(await clickDownload(), "collapsed state: download produces a blob");
  ok(await lastDownloadStartsWithBom(), "collapsed CSV carries the BOM");
  const collapsedCsv = await lastDownloadText();
  ok(collapsedCsv.includes("\r\nrow 149,149"), "collapsed CSV contains row 149");

  // Expand into the virtualized state: the DOM body only mounts a window of
  // rows (mounting itself is the pre-existing jsdom red noted at the top).
  await act(async () => {
    rootEl.querySelector<HTMLButtonElement>(".md-table-fold__toggle")?.click();
  });
  await flush();
  ok(rootEl.querySelector(".md-table-virtual"), "expanding mounts the virtualized scroller");
  const mounted = rootEl.querySelectorAll(".md-table-virtual tbody tr").length;
  ok(mounted < rowCount, `virtualized body stays bounded (${mounted} < ${rowCount})`);

  ok(await clickCopy(), "virtualized state: copy writes to the clipboard");
  const virtualMd = lastClipboardWrite();
  ok(virtualMd.includes("| row 149 | 149 |"), "virtualized state exports row 149 — original data, not the DOM");
  eq(virtualMd.split("\n").length, rowCount + 2, "virtualized state exports all 150 rows");
  eq(virtualMd, collapsedMd, "virtualized and collapsed exports are identical");

  await act(async () => root.unmount());
}

// ── integration: escaped pipe survives the real markdown pipeline ────────────
console.log("\npipeline integration: \\| round trip");
{
  const source = "| 名称 | 值 |\n| --- | --- |\n| a\\|b | 1 |\n| c | 2 |";
  const blocks = parseMarkdownToBlocks(source);
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(<div className="md">{hastBlockToJsx(blocks[0], createComponents(false))}</div>);
  });
  await flush();
  ok(exportButtons().copy, "pipeline-rendered table exposes the copy button");
  ok(await clickCopy(), "pipeline-rendered table copies");
  eq(
    lastClipboardWrite(),
    "| 名称 | 值 |\n| --- | --- |\n| a\\|b | 1 |\n| c | 2 |",
    "escaped pipe round-trips through render → copy",
  );
  await act(async () => root.unmount());
}

// ── worker path: VirtualMarkdownSourceTable (plain-string data) ──────────────
console.log("\nworker source table: small + large states");
{
  const data: VirtualMarkdownTableData = {
    align: [null, null],
    header: ["命令", "说明"],
    rows: [["=cmd /c del", "危险"], ["dir", "列目录"]],
  };
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(<VirtualMarkdownSourceTable data={data} />);
  });
  await flush();
  ok(exportButtons().copy && exportButtons().download, "small source table exposes both buttons");
  ok(await clickDownload(), "small source table downloads");
  ok(await lastDownloadStartsWithBom(), "source-table CSV carries the BOM");
  const smallCsv = await lastDownloadText();
  ok(smallCsv.startsWith("命令,说明"), "source-table CSV keeps the Chinese header first");
  ok(smallCsv.includes("'=cmd /c del"), "source-table CSV demotes the formula cell");

  await act(async () => root.unmount());
}
{
  const large: VirtualMarkdownTableData = {
    align: [null, null],
    header: ["idx", "note"],
    rows: Array.from({ length: 120 }, (_, i) => [String(i), i === 119 ? "=danger" : `n${i}`]),
  };
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(<VirtualMarkdownSourceTable data={large} />);
  });
  await flush();
  ok(exportButtons().copy && exportButtons().download, "large source table exposes both buttons");
  eq(rootEl.querySelectorAll(".md-table-scroll tbody tr").length, 12, "large source table DOM shows the 12-row preview");

  ok(await clickCopy(), "large source table copies");
  const md = lastClipboardWrite();
  ok(md.includes("| 119 | =danger |"), "collapsed source table exports row 119 (the =danger row) from data");
  eq(md.split("\n").length, 122, "collapsed source table exports all 120 rows");

  await act(async () => {
    rootEl.querySelector<HTMLButtonElement>(".md-table-fold__toggle")?.click();
  });
  await flush();
  const mounted = rootEl.querySelectorAll(".md-table-virtual tbody tr").length;
  ok(mounted < 120, `source table virtualizes with a bounded body (${mounted} < 120)`);
  ok(await clickDownload(), "virtualized source table downloads");
  ok(await lastDownloadStartsWithBom(), "virtualized source-table CSV carries the BOM");
  const csv = await lastDownloadText();
  ok(csv.includes("'=danger"), "virtualized source-table CSV demotes the formula cell");
  ok(csv.includes("\r\n119,'=danger"), "virtualized source-table CSV contains row 119 from data");

  await act(async () => root.unmount());
}

dom.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
