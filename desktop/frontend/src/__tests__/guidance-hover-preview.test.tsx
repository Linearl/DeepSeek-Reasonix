// Run: tsx src/__tests__/guidance-hover-preview.test.tsx
//
// Task 446 — 排队引导消息 hover 即浮层预览:
//   hovering a line-clamped queue row pops a floating card (portal to
//   document.body, position: fixed) with the row's full body, while short
//   rows never get one; the click-to-expand preview keeps owning
//   expand/collapse, and the card closes on mouseleave, queue collapse,
//   row removal, drag start, scroll, and when the inline preview takes the
//   same row over. Reuses task 436's CJK-aware line estimator as the
//   layout-free truncation gate.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import { StrictMode } from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import {
  ComposerGuidanceShelf,
  GUIDANCE_ROW_VISIBLE_LINES,
  guidanceRowIsTruncated,
  type PendingGuidance,
} from "../components/ComposerGuidanceShelf";
import { estimateUserMessageLines } from "../lib/messageFold";
import { LocaleProvider } from "../lib/i18n";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

// Failure-path formatter: assertion subjects are often DOM nodes, whose
// circular (React fiber) references crash plain JSON.stringify and masked the
// real assertion failure with a TypeError (seen on trunk 20261003).
function describeValue(value: unknown): string {
  if (value === null) return "null";
  if (typeof value === "string") return JSON.stringify(value);
  if (typeof value === "object" && value instanceof Element) {
    const html = value.outerHTML;
    return `<Element ${html.length > 120 ? `${html.slice(0, 120)}…` : html}>`;
  }
  try {
    return JSON.stringify(value) ?? String(value);
  } catch {
    return String(value);
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${describeValue(expected)}, got ${describeValue(actual)}`);
}

async function flush(ms = 0): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
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

type ShelfProps = Partial<Parameters<typeof ComposerGuidanceShelf>[0]>;

const LONG_BODY =
  "请把这个排队引导消息写得足够长以触发卡片截断：" +
  Array.from({ length: 12 }, (_, i) => `第 ${i + 1} 行是填充内容，用来撑高悬浮卡片。`).join("\n");

function makeItem(id: string, text: string): PendingGuidance {
  return { id, text, submitText: "", state: "queued" };
}

async function mount(props: ShelfProps) {
  const dom = installDom();
  const root = createRoot(document.getElementById("root")!);
  let current: ShelfProps = {
    recovery: null,
    recoveryDisabled: false,
    items: [makeItem("a", LONG_BODY)],
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
      await flush(0);
    });
  };
  await render();
  return { dom, root, render, current: () => current };
}

async function cleanup(dom: JSDOM, root: Root) {
  await act(async () => root.unmount());
  dom.window.close();
}

function textButton(): HTMLButtonElement | null {
  return document.querySelector(".composer-guidance-item__text--button");
}

function hoverCard(): HTMLElement | null {
  return document.querySelector<HTMLElement>(".guidance-hover-preview");
}

async function mouseOver(el: Element | null) {
  await act(async () => {
    el?.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: null }));
    await flush(0);
  });
}

async function mouseOut(el: Element | null) {
  await act(async () => {
    el?.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body }));
    await flush(0);
  });
}

console.log("\nguidance hover preview (task 446)");

// ── 1. layout-free gate: estimator path (jsdom reports no layout) ───────────
{
  const dom = installDom(); // the DOM-probe branch needs a document
  ok(guidanceRowIsTruncated(null, LONG_BODY), "null target falls back to the 436 estimator: long body counts as truncated");
  eq(guidanceRowIsTruncated(null, "short"), false, "null target + short text: no truncation, no card");
  eq(GUIDANCE_ROW_VISIBLE_LINES, 2, "the row-visible line budget stays pinned at the CSS line-clamp (2)");
  eq(estimateUserMessageLines("汉".repeat(110)), 2, "CJK double-width weighting flows through the shared estimator");

  // Element with real layout (non-zero clientHeight) → DOM probe wins.
  const el = document.createElement("div");
  Object.defineProperty(el, "scrollHeight", { value: 400, configurable: true });
  Object.defineProperty(el, "clientHeight", { value: 60, configurable: true });
  eq(guidanceRowIsTruncated(el, "short"), true, "laid-out element: scrollHeight > clientHeight wins over the text");
  Object.defineProperty(el, "scrollHeight", { value: 60, configurable: true });
  eq(guidanceRowIsTruncated(el, LONG_BODY), false, "laid-out element fully visible: no truncation even for long source text");
  dom.window.close();
}

// ── 2. hover a long row → floating card with the full body ──────────────────
{
  const fetched = "【后端全文】" + LONG_BODY;
  const { dom, root } = await mount({
    items: [makeItem("long", LONG_BODY)],
    onPreviewText: async () => fetched,
  });
  ok(hoverCard() === null, "no card before hover");

  await mouseOver(textButton());
  const card = hoverCard();
  ok(card !== null, "hovering a clamped row pops the floating card");
  eq(card?.getAttribute("role"), "tooltip", "the card is announced as a tooltip");
  eq(card?.getAttribute("data-guidance-hover"), "long", "the card is keyed to the hovered row");
  ok(card?.parentElement === document.body, "the card portals to <body> (no clipping ancestors)");
  ok(card?.textContent?.includes("第 3 行是填充内容") === true, "the card carries the row's own body immediately (fallback text)");

  // The async full-body upgrade (same onPreviewText pipeline as the click preview).
  await act(async () => { await flush(10); });
  ok(hoverCard()?.textContent?.includes("【后端全文】") === true, "the card upgrades to the fetched full body");

  await mouseOut(textButton());
  eq(hoverCard(), null, "mouseleave closes the card");

  await cleanup(dom, root);
}

// ── 3. short row → no card on hover ─────────────────────────────────────────
{
  const { dom, root } = await mount({ items: [makeItem("short", "继续")] });
  await mouseOver(textButton());
  eq(hoverCard(), null, "hovering a short row pops no floating card");
  await cleanup(dom, root);
}

// ── 4. click expand/collapse keeps owning the inline preview ────────────────
{
  const { dom, root } = await mount({
    items: [makeItem("a", LONG_BODY)],
    onPreviewText: async () => "全文内容",
  });

  // Hover first → card only, inline preview untouched.
  await mouseOver(textButton());
  ok(hoverCard() !== null, "card open from hover");
  eq(document.querySelector('[role="note"]'), null, "hover does NOT open the inline preview (previewId untouched)");

  // Click → inline preview opens and the card yields to it.
  await act(async () => { textButton()?.click(); await flush(0); });
  ok(document.querySelector('[role="note"]') !== null, "click still opens the inline preview");
  eq(hoverCard(), null, "inline preview takes over: the hover card closes for the same row");

  // Click again → collapses; card must not spontaneously reappear without a re-hover.
  await act(async () => { textButton()?.click(); await flush(0); });
  eq(document.querySelector('[role="note"]'), null, "second click collapses the inline preview as before");
  eq(hoverCard(), null, "collapse alone does not resurrect the card (no re-hover happened)");

  // A fresh hover after collapse brings the card back.
  await mouseOut(textButton());
  await mouseOver(textButton());
  ok(hoverCard() !== null, "re-hover after collapse brings the card back");

  await cleanup(dom, root);
}

// ── 5. the card closes when its row leaves the list ─────────────────────────
{
  const { dom, root, render } = await mount({ items: [makeItem("a", LONG_BODY), makeItem("b", "继续")] });
  await mouseOver(textButton());
  ok(hoverCard() !== null, "card open before the row is dismissed");
  await render({ items: [makeItem("b", "继续")] });
  eq(hoverCard(), null, "row removal closes the card (no orphan floating layer)");

  await cleanup(dom, root);
}

// ── 6. queue collapse closes the card (same rule as the inline preview) ─────
{
  const { dom, root, render } = await mount({ items: [makeItem("a", LONG_BODY)], expanded: true });
  await mouseOver(textButton());
  ok(hoverCard() !== null, "card open before the queue collapses");
  await render({ expanded: false });
  eq(hoverCard(), null, "queue collapse closes the hover card too");

  await cleanup(dom, root);
}

// ── 7. drag start closes the card (441 drag handle owns the row while dragging) ──
{
  const { dom, root } = await mount({ items: [makeItem("a", LONG_BODY)], onMove: () => {} });
  // Task 441 union: the card is no longer draggable — the six-dot handle is the
  // only drag source, so the dragstart simulation must target the handle (the
  // row-body dispatch went stale at the merge and reported a false regression).
  const handle = document.querySelector(".composer-guidance-item__handle");
  ok(handle !== null, "the six-dot handle exists (drag source since the 441 union)");
  await mouseOver(textButton());
  ok(hoverCard() !== null, "card open before the drag starts");
  await act(async () => {
    handle?.dispatchEvent(new MouseEvent("dragstart", { bubbles: true }));
    await flush(0);
  });
  eq(hoverCard(), null, "drag start closes the card (hover yields to the 441 drag surface)");

  await cleanup(dom, root);
}

// ── 8. scroll closes the card (it is pinned to viewport coordinates) ────────
{
  const { dom, root } = await mount({ items: [makeItem("a", LONG_BODY)] });
  await mouseOver(textButton());
  ok(hoverCard() !== null, "card open before the scroll");
  await act(async () => {
    window.dispatchEvent(new Event("scroll"));
    await flush(0);
  });
  eq(hoverCard(), null, "scroll closes the card (coordinates would strand it)");

  await cleanup(dom, root);
}

// ── 9. the card never steals pointer events (pure peek) ─────────────────────
{
  const css = readFileSync(
    resolve(dirname(fileURLToPath(import.meta.url)), "..", "styles.css"),
    "utf8",
  );
  const block = /\.guidance-hover-preview \{[^}]*\}/.exec(css);
  ok(block !== null, "the hover card block exists in styles.css");
  ok(block?.[0].includes("pointer-events: none") === true, "the card is pointer-transparent (row clicks keep working)");
  ok(block?.[0].includes("z-index: var(--z-tooltip)") === true, "z-index uses a --z-* token (check-z-index-tokens contract)");
  ok(/\.guidance-hover-preview\[data-clipped\]::after/.test(css), "the 436-style bottom fade mounts only under [data-clipped]");
}

// ── 10. source contract: hover never writes previewId / click still wires ───
{
  const src = readFileSync(
    resolve(dirname(fileURLToPath(import.meta.url)), "..", "components", "ComposerGuidanceShelf.tsx"),
    "utf8",
  );
  ok(src.includes("onMouseEnter"), "the row text button carries the hover handler");
  ok(src.includes("onMouseLeave"), "the row text button carries the hover leave handler");
  ok(/onMouseEnter=\{[\s\S]{0,240}?openHoverCard\(/.test(src), "hover routes through openHoverCard (single entry)");
  ok(src.includes("previewIdRef.current === item.id") || src.includes("previewIdRef.current !== item.id"),
    "openHoverCard guards against the already-open inline preview");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exitCode = 1;
