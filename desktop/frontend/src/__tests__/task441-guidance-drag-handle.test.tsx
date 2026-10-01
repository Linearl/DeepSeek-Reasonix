// Run: tsx src/__tests__/task441-guidance-drag-handle.test.tsx
//
// Task 441 — 排队引导消息六点手柄拖拽排序（替换上移/下移按钮）:
//   ① movable rows render a six-dot drag handle and a handle dragstart +
//      drop on another row reorders through onMove;
//   ② the up/down arrow buttons are gone;
//   ③ the move reaches the durable persistence path (MoveInboxItem stays
//      the UI's only ordering write — onMove IS that path);
//   ④ edit / delete buttons remain and keep working.
// Harness follows the task 289 collapse test (JSDOM + createRoot).

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import React, { StrictMode } from "react";
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

function makeItem(id: string): PendingGuidance {
  return { id, text: `guidance ${id}`, submitText: "", state: "queued" };
}

type ShelfProps = Partial<Parameters<typeof ComposerGuidanceShelf>[0]>;

async function mount(props: ShelfProps) {
  const dom = installDom();
  const root = createRoot(document.getElementById("root")!);
  let current: ShelfProps = {
    recovery: null,
    recoveryDisabled: false,
    items: [makeItem("a")],
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

function handles(): HTMLButtonElement[] {
  return [...document.querySelectorAll<HTMLButtonElement>(".composer-guidance-item__handle")];
}

function cards(): HTMLElement[] {
  return [...document.querySelectorAll<HTMLElement>(".composer-guidance-item")];
}

async function dispatchDrag(el: Element, type: string) {
  await act(async () => {
    el.dispatchEvent(new window.Event(type, { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

console.log("\ntask 441 guidance drag handle");

// ── ① six-dot handle: one per movable row; handle dragstart + drop reorders ─
{
  const moves: Array<{ id: string; toIndex: number }> = [];
  const { dom, root } = await mount({
    items: [makeItem("a"), makeItem("b"), makeItem("c")],
    onMove: (item, toIndex) => moves.push({ id: item.id, toIndex }),
  });
  eq(handles().length, 3, "each movable row renders exactly one six-dot drag handle");
  // The card itself is NOT draggable anymore — only the handle carries DnD.
  ok(cards().every((card) => !card.hasAttribute("draggable")), "the card body is not draggable (handle-only drag, task 441)");
  eq(handles()[0].getAttribute("draggable"), "true", "the handle is the draggable element");

  await dispatchDrag(handles()[0], "dragstart");
  eq(cards()[0].className.includes("composer-guidance-item--dragging"), true, "dragstart marks the dragged row");
  await dispatchDrag(cards()[2], "dragover");
  await dispatchDrag(cards()[2], "drop");
  eq(moves.length, 1, "dropping on the third row issues exactly one move");
  eq(moves[0]?.id, "a", "the move carries the dragged entry");
  eq(moves[0]?.toIndex, 2, "the drop lands at the hovered row's index (a -> index 2)");
  eq(cards()[0].className.includes("composer-guidance-item--dragging"), false, "drop clears the dragging marker");
  await cleanup(dom, root);
}

// ── ② the up/down arrow buttons are removed ──────────────────────────────────
{
  const { dom, root } = await mount({ items: [makeItem("a"), makeItem("b")] });
  const labels = [...document.querySelectorAll<HTMLButtonElement>(".composer-guidance-item button")]
    .map((b) => b.getAttribute("aria-label") ?? "");
  ok(!labels.some((label) => label === "上移" || label === "下移" || label === "Move up" || label === "Move down"),
    "no up/down arrow buttons render on any row (task 441 removal)");
  ok(document.querySelector(".composer-guidance-item__reorder") === null, "the old reorder span is gone from the DOM");
  await cleanup(dom, root);

  const shelfSource = readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), "../components/ComposerGuidanceShelf.tsx"), "utf8");
  ok(!shelfSource.includes("guidanceMoveUp") && !shelfSource.includes("guidanceMoveDown"),
    "the shelf no longer references the arrow locale keys");
}

// ── ③ order persistence: onMove is the durable path (MoveInboxItem) ─────────
{
  const moves: Array<{ id: string; toIndex: number }> = [];
  const { dom, root } = await mount({
    items: [makeItem("a"), makeItem("b")],
    onMove: (item, toIndex) => moves.push({ id: item.id, toIndex }),
  });
  await dispatchDrag(handles()[1], "dragstart");
  await dispatchDrag(cards()[0], "dragover");
  await dispatchDrag(cards()[0], "drop");
  eq(moves.length, 1, "a reverse-order drop (b onto a) issues one move");
  eq(moves[0]?.id, "b", "the move targets the durable row id");
  eq(moves[0]?.toIndex, 0, "the move lands at the dropped index");
  await cleanup(dom, root);

  const composerSource = readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), "../components/Composer.tsx"), "utf8");
  ok(composerSource.includes("await app.MoveInboxItem(targetTabId, item.id, toIndex)"),
    "onMove is wired to the durable persistence call (MoveInboxItem) — drag order is persisted");
}

// ── ④ edit / delete buttons remain and keep working ──────────────────────────
{
  const edited: string[] = [];
  const dismissed: string[] = [];
  const { dom, root } = await mount({
    items: [makeItem("a")],
    onEdit: (item) => edited.push(item.id),
    onDismiss: (item) => dismissed.push(item.id),
  });
  const rowButtons = [...document.querySelectorAll<HTMLButtonElement>(".composer-guidance-item button")];
  const pencil = rowButtons.find((b) => b.querySelector("svg.lucide-pencil"));
  const trash = rowButtons.find((b) => b.querySelector("svg.lucide-trash2"));
  ok(pencil !== undefined, "the edit (pencil) button still renders on the row");
  ok(trash !== undefined, "the delete (trash) button still renders on the row");
  await act(async () => { pencil?.click(); await new Promise((r) => setTimeout(r, 0)); });
  await act(async () => { trash?.click(); await new Promise((r) => setTimeout(r, 0)); });
  eq(edited.join(","), "a", "the edit handler fires unchanged");
  eq(dismissed.join(","), "a", "the delete handler fires unchanged");
  await cleanup(dom, root);
}

// ── non-movable rows keep the glyph, no handle; local rows never move ────────
{
  const local: PendingGuidance = { id: "local-1", text: "local draft", submitText: "", state: "queued" };
  const durable = makeItem("d");
  const { dom, root } = await mount({
    items: [local, durable],
    onMove: () => {},
  });
  eq(handles().length, 1, "a local (unsent) row shows no handle — it has no queue position to persist");
  ok(cards()[0].querySelector("svg.lucide-corner-down-right") !== null, "non-movable rows keep the row glyph");
  await cleanup(dom, root);
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
