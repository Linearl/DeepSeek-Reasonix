// Run: tsx src/__tests__/task466-guidance-select-all.test.tsx
//
// 任务 466 — 引导队列全选 + 批量丢弃按钮横排:
//   the head select-all is a tri-state mirror of the row checkboxes (checked =
//   every selectable row selected, indeterminate = some), one click sweeps all
//   gate-admitted rows in — hidden rows included, paused / in-flight / editing
//   rows never — a second click clears, and single-row clicks keep the mirror
//   in sync. The batch-bar dismiss button carries its own text-button class:
//   the fixed-24px row icon class it reused wrapped 「丢弃 N 条」 one character
//   per line (the vertical stack from the user report).

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
      removeListener() {},
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

function selectAllBox(): HTMLInputElement | null {
  return document.querySelector<HTMLInputElement>(".composer-guidance-head__selectall input");
}

function rowChecks(): HTMLInputElement[] {
  return [...document.querySelectorAll<HTMLInputElement>(".composer-guidance-item__check")];
}

console.log("\ntask 466 — guidance queue select-all + batch dismiss layout");

// ── 1. sweep content: only gate-admitted rows; collapsed queue sweeps hidden ones ──
{
  const swept: string[][] = [];
  const items = [
    makeItem("a"),
    makeItem("b"),
    makeItem("hidden-c"),
    makeItem("paused-1", { paused: true }),
    makeItem("inflight-1", { state: "steer_accepted" }),
  ];
  const { dom, root, render } = await mount({
    items,
    expanded: false, // only the first two rows render — the sweep must still cover the rest
    selectMode: true,
    onToggleSelectAll: (selectable) => swept.push(selectable.map((item) => item.id)),
  });
  const box = selectAllBox();
  ok(box !== null, "select mode renders the head select-all even at a zero selection");
  eq(box?.checked, false, "nothing selected yet — the box starts unchecked");
  eq(box?.indeterminate, false, "nothing selected yet — the box is not indeterminate");
  await act(async () => { box?.click(); await new Promise((r) => setTimeout(r, 0)); });
  eq(swept.length, 1, "one click fires the sweep exactly once");
  eq(swept[0]?.join(","), "a,b,hidden-c", "the sweep covers hidden rows but never paused/in-flight ones");
  await cleanup(dom, root);
}

// ── 2. the editing row is never swept in ────────────────────────────────────
{
  const swept: string[][] = [];
  const { dom, root } = await mount({
    items: [makeItem("a"), makeItem("b"), makeItem("c")],
    selectMode: true,
    editingId: "a", // loaded into the composer right now
    onToggleSelectAll: (selectable) => swept.push(selectable.map((item) => item.id)),
  });
  await act(async () => { selectAllBox()?.click(); await new Promise((r) => setTimeout(r, 0)); });
  eq(swept[0]?.join(","), "b,c", "the row being edited stays out of the select-all sweep");
  await cleanup(dom, root);
}

// ── 3. tri-state mirror + sync with single-row clicks (stateful parent) ─────
{
  // The parent side mirrors the composer's contract (source-pinned in
  // composer-guidance-batch.test.ts): all-or-none from the same list the
  // checkbox renders from; single toggle adds/removes one id.
  let ids: string[] = [];
  let sweeps = 0;
  const toggleAll = (selectable: PendingGuidance[]) => {
    const allSelected = selectable.length > 0 && selectable.every((item) => ids.includes(item.id));
    ids = allSelected ? [] : selectable.map((item) => item.id);
    sweeps += 1;
  };
  const toggleOne = (item: PendingGuidance) => {
    ids = ids.includes(item.id) ? ids.filter((id) => id !== item.id) : [...ids, item.id];
  };
  const { dom, root, render } = await mount({
    items: [makeItem("a"), makeItem("b"), makeItem("c")],
    selectMode: true,
    selectedIds: ids,
    onToggleSelectAll: toggleAll,
    onToggleSelect: toggleOne,
  });

  // select-all → every row checks, the box checks.
  await act(async () => { selectAllBox()?.click(); await new Promise((r) => setTimeout(r, 0)); });
  await render({ selectedIds: ids });
  eq(ids.join(","), "a,b,c", "select-all swept every selectable row in");
  eq(rowChecks().every((el) => el.checked), true, "all row checkboxes reflect the sweep");
  eq(selectAllBox()?.checked, true, "the box reads checked when every selectable row is selected");
  eq(selectAllBox()?.indeterminate, false, "full selection is never indeterminate");

  // one row off → the box flips to indeterminate (some, not all).
  await act(async () => { rowChecks()[1]?.click(); await new Promise((r) => setTimeout(r, 0)); });
  await render({ selectedIds: ids });
  eq(ids.join(","), "a,c", "the single click removed exactly its own row");
  eq(selectAllBox()?.checked, false, "partial selection reads unchecked");
  eq(selectAllBox()?.indeterminate, true, "partial selection reads indeterminate");
  eq(rowChecks().map((el) => el.checked).join(","), "true,false,true", "row checkboxes stay in sync with the state");

  // the row back on → the box is fully checked again.
  await act(async () => { rowChecks()[1]?.click(); await new Promise((r) => setTimeout(r, 0)); });
  await render({ selectedIds: ids });
  eq(selectAllBox()?.checked, true, "re-checking the last row returns the box to checked");

  // click the checked box → clear (反选出路); everything unchecks.
  await act(async () => { selectAllBox()?.click(); await new Promise((r) => setTimeout(r, 0)); });
  await render({ selectedIds: ids });
  eq(sweeps, 2, "exactly two select-all interactions (sweep in, clear out)");
  eq(ids.join(""), "", "clicking a fully-selected box clears the selection");
  eq(rowChecks().some((el) => el.checked), false, "every row checkbox cleared with it");
  eq(selectAllBox()?.checked, false, "the box reads unchecked after the clear");
  eq(selectAllBox()?.indeterminate, false, "and is not indeterminate at zero");
  await cleanup(dom, root);
}

// ── 4. no selectable rows → no select-all control (nothing to sweep) ───────
{
  const { dom, root } = await mount({
    items: [makeItem("p1", { paused: true }), makeItem("p2", { paused: true })],
    selectMode: true,
  });
  eq(selectAllBox(), null, "an all-unselectable queue renders no select-all control");
  await cleanup(dom, root);
}

// ── 5. batch-bar dismiss button: dedicated text-button class ───────────────
{
  const { dom, root } = await mount({
    items: [makeItem("a"), makeItem("b")],
    selectMode: true,
    selectedIds: ["a"],
    onBatchDismiss: () => {},
  });
  const dismiss = document.querySelector<HTMLButtonElement>(".composer-guidance-batchbar__dismiss");
  ok(dismiss !== null, "the batch bar renders the dismiss button with its own class");
  ok(!dismiss?.classList.contains("composer-guidance-item__action"), "the fixed-24px row icon class is gone from the batch bar");
  ok(dismiss?.querySelector("span") !== null, "the dismiss label renders inline with the icon (single text node, not per-character lines)");
  await cleanup(dom, root);
}

// ── 6. layout contracts the jsdom cannot measure, pinned at the source ─────
{
  const rootDir = join(dirname(fileURLToPath(import.meta.url)), "..");
  const css = readFileSync(join(rootDir, "styles.css"), "utf8");
  const dismissBlock = /\.composer-guidance-batchbar__dismiss\s*\{[^}]*\}/.exec(css)?.[0] ?? "";
  ok(/white-space:\s*nowrap/.test(dismissBlock), "the dismiss pill forbids internal wrapping (nowrap)");
  ok(/flex:\s*0 0 auto/.test(dismissBlock), "the dismiss pill never shrinks into a wrap under bar pressure");
  ok(!/width:\s*24px/.test(dismissBlock), "the dismiss pill has no fixed width (the old vertical-wrap cause)");
  const selectallBlock = /\.composer-guidance-head__selectall\s*\{[^}]*\}/.exec(css)?.[0] ?? "";
  ok(selectallBlock.length > 0, "the head select-all control carries its cluster style");
}

process.stdout.write(`\n${passed} checks passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
