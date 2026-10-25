// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1128-guidance-select-row-grid.test.tsx
//
// tl 1128 — 引导队列多选态单行 grid（用户报告：多选态下条目左下角的回收
// （垃圾桶）按钮掉到第二行）:
//   the row grid was pinned at 6 tracks (Task 266-A/441); 任务998 then added
//   the per-row enqueue <time>, and select mode adds the row checkbox on top
//   of that — icon + check + text + time + edit/merge + guide + dismiss is
//   more than 6 children, so the trailing dismiss control wrapped onto a
//   second grid line. Fix: the base row widens to 7 tracks (time column) and
//   select-mode rows carry `composer-guidance-item--select`, widening to 8
//   tracks. The 1fr text column keeps absorbing narrow-dock squeeze (the
//   __text line-clamp keeps the row single-height), and the 620px breakpoint
//   keeps the same tracks at both widths (padding-only override).

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import { StrictMode } from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ComposerGuidanceShelf, type PendingGuidance } from "../components/ComposerGuidanceShelf";
import { LocaleProvider } from "../lib/i18n";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else {
    failed += 1;
    process.exitCode = 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.Element = dom.window.Element;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: false,
      media: "(prefers-reduced-motion: reduce)",
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeEventListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

function makeItem(id: string, overrides: Partial<PendingGuidance> = {}): PendingGuidance {
  return { id, text: `guidance ${id}`, submitText: "", state: "queued", ...overrides };
}

type ShelfProps = Partial<Parameters<typeof ComposerGuidanceShelf>[0]>;

async function mount(props: ShelfProps) {
  const dom = installDom();
  const root = createRoot(document.getElementById("root")!);
  let current: ShelfProps = {
    recovery: null,
    recoveryDisabled: false,
    items: [makeItem("a"), makeItem("b")],
    expanded: true,
    running: false,
    disabled: false,
    readOnly: false,
    sendingId: null,
    onReview: () => {},
    onRecoveryResumed: () => {},
    onRecoveryError: () => {},
    onToggleExpanded: () => {},
    onSend: () => {},
    onDismiss: () => {},
    ...props,
  };
  const render = async (next: ShelfProps = {}) => {
    current = { ...current, ...next };
    await act(async () => {
      root.render(
        <StrictMode>
          <LocaleProvider>
            <ComposerGuidanceShelf {...(current as Parameters<typeof ComposerGuidanceShelf>[0])} />
          </LocaleProvider>
        </StrictMode>,
      );
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
  };
  await render();
  return { dom, root, render };
}

async function cleanup(dom: JSDOM, root: Root) {
  await act(async () => root.unmount());
  dom.window.close();
}

function rowEls(): HTMLElement[] {
  return [...document.querySelectorAll<HTMLElement>(".composer-guidance-item")];
}

console.log("\ntl 1128 — guidance queue select-mode single-line grid");

// ── 1. select mode mounts the widening class on every row ───────────────────
{
  const { dom, root } = await mount({
    items: [
      makeItem("a", { createdAt: 1761000000000 }), // durable row: checkbox + time + dismiss all present
      makeItem("b", { createdAt: 1761000000000 }),
    ],
    selectMode: true,
    selectedIds: [],
    onToggleSelect: () => {},
  });
  const rows = rowEls();
  eq(rows.length, 2, "both rows render");
  eq(rows.every((el) => el.classList.contains("composer-guidance-item--select")), true,
    "select mode puts the --select widening class on every row");
  eq(document.querySelectorAll<HTMLInputElement>(".composer-guidance-item__check").length, 2,
    "the row checkboxes are mounted");
  eq(document.querySelectorAll(".composer-guidance-item__time").length, 2,
    "the 任务998 enqueue-time columns are mounted (the capacity was sized for them)");
  // no onEdit/onMergeNext here, so the single __action button is the dismiss (trash) control
  eq(document.querySelectorAll(".composer-guidance-item__action").length, 2,
    "the dismiss control is mounted in the same row as its checkbox (the reported wrap pair)");
  await cleanup(dom, root);
}

// ── 2. plain mode keeps the old class signature (no widening) ───────────────
{
  const { dom, root } = await mount({
    items: [makeItem("a", { createdAt: 1761000000000 }), makeItem("b")],
    selectMode: false,
  });
  const rows = rowEls();
  eq(rows.every((el) => !el.classList.contains("composer-guidance-item--select")), true,
    "plain rows never carry the --select class");
  eq(document.querySelectorAll(".composer-guidance-item__check").length, 0,
    "plain mode renders no row checkbox");
  eq(rows.every((el) => el.classList.contains("composer-guidance-item")), true,
    "plain rows keep the base composer-guidance-item class");
  await cleanup(dom, root);
}

// ── 3. the editing row still gets the class (checkbox is gated off, row is not) ──
{
  const { dom, root } = await mount({
    items: [makeItem("a"), makeItem("b")],
    selectMode: true,
    selectedIds: [],
    editingId: "a",
    onToggleSelect: () => {},
  });
  const rows = rowEls();
  eq(rows[0]?.classList.contains("composer-guidance-item--select"), true,
    "the editing row keeps the --select class (badge swaps into the edit slot)");
  eq(rows[0]?.querySelector(".composer-guidance-item__check"), null,
    "the editing row still renders no checkbox (rowSelectable gate unchanged)");
  await cleanup(dom, root);
}

// ── 4. source anchors: track capacity at both widths ────────────────────────
{
  const rootDir = join(dirname(fileURLToPath(import.meta.url)), "..");
  const styles = readFileSync(join(rootDir, "styles.css"), "utf8");
  const shelf = readFileSync(join(rootDir, "components/ComposerGuidanceShelf.tsx"), "utf8").replace(/\n\s*/g, " ");

  const itemRule = styles.slice(styles.indexOf(".composer-guidance-item {"), styles.indexOf(".composer-guidance-item__icon"));
  ok(/grid-template-columns:\s*auto minmax\(0, 1fr\) repeat\(5, auto\)/.test(itemRule),
    "base rows hold 7 tracks (handle + 1fr text + time/edit/merge/guide/dismiss) — one line");
  const selectRule = styles.slice(styles.indexOf(".composer-guidance-item--select {"), styles.indexOf(".composer-guidance-item__icon"));
  ok(/grid-template-columns:\s*auto auto minmax\(0, 1fr\) repeat\(5, auto\)/.test(selectRule),
    "select rows hold 8 tracks (icon + check + 1fr text + 5 auto controls) — one line");
  ok(/min-height: 32px/.test(itemRule), "the single-height baseline is retained");

  // 双宽度断言: the 620px breakpoint only adjusts padding — it must not
  // redefine the tracks, so the one-line guarantee holds at full width and
  // narrow docks alike (the 1fr text column absorbs the squeeze).
  const shelfAt = styles.indexOf(".composer-guidance-shelf {");
  const narrowAt = styles.indexOf("@media (max-width: 620px)", shelfAt);
  const narrowEnd = styles.indexOf(".composer-context {", narrowAt);
  const narrowBlock = styles.slice(narrowAt, narrowEnd);
  ok(narrowAt > shelfAt && /composer-guidance-item/.test(narrowBlock),
    "the guidance 620px block is located and touches the item rows");
  ok(!/grid-template-columns/.test(narrowBlock),
    "the narrow-dock breakpoint redefines NO tracks — both widths share the one-line grid");

  // TSX anchor: the class rides selectMode exactly.
  ok(/selectMode \? " composer-guidance-item--select" : ""/.test(shelf),
    "the widening class is gated on selectMode in the shelf source");
}

console.log(`\n${passed} checks passed, ${failed} failed`);
if (failed > 0) process.exit(1);
