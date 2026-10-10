// Run: tsx src/__tests__/composer-guidance-collapse.test.tsx
//
// Task 289 — 引导队列显式折叠 + 限高滚动:
//   the card body itself collapses an open preview (the reported "cannot
//   collapse" dead zone), the head carries an explicit collapse button, a long
//   expanded queue caps at 40vh, a failed/hung preview fetch never blocks
//   closing, previewId alone drives every preview render, and the queue-level
//   auto-collapse rules in Composer.tsx stay untouched.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import { StrictMode } from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ComposerGuidanceShelf, PREVIEW_TIMEOUT_MS, type PendingGuidance } from "../components/ComposerGuidanceShelf";
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

function textButton(id: string): HTMLButtonElement | null {
  return document.querySelector(`.composer-guidance-item__text--button`);
}

console.log("\ncomposer guidance collapse (task 289)");

// ── 1. card-body click collapses; the head carries an explicit collapse ─────
{
  const toggles: number[] = [];
  const { dom, root } = await mount({ items: [makeItem("a"), makeItem("b")], onToggleExpanded: () => toggles.push(1) });
  await act(async () => { textButton("a")?.click(); await new Promise((r) => setTimeout(r, 0)); });
  eq(document.querySelector('[role="note"]') !== null, true, "text button opens the preview");

  // Click the card itself (target = the div, i.e. blank space): closes.
  const card = document.querySelector(".composer-guidance-item");
  await act(async () => {
    (card as HTMLElement).click();
    await new Promise((r) => setTimeout(r, 0));
  });
  eq(document.querySelector('[role="note"]'), null, "card body click collapses the open preview (no tab switch needed)");
  eq(document.querySelector(".composer-guidance-item__text--button")?.getAttribute("aria-expanded"), "false", "the row reports collapsed again");

  // Head collapse button (queue expanded) calls onToggleExpanded exactly once.
  const headCollapse = document.querySelector<HTMLButtonElement>(".composer-guidance-head__collapse");
  ok(headCollapse !== null, "the expanded head carries an explicit collapse button");
  await act(async () => { headCollapse?.click(); await new Promise((r) => setTimeout(r, 0)); });
  eq(toggles.length, 1, "the head collapse button toggles the queue exactly once");
  await cleanup(dom, root);
}

// ── 4. row controls never fall through to the card toggle ───────────────────
{
  const dismissed: string[] = [];
  const { dom, root } = await mount({
    items: [makeItem("a")],
    onDismiss: (item) => dismissed.push(item.id),
  });
  await act(async () => { textButton("a")?.click(); await new Promise((r) => setTimeout(r, 0)); });
  eq(document.querySelector('[role="note"]') !== null, true, "preview open before exercising the controls");
  const dismiss = document.querySelector<HTMLButtonElement>('button[aria-label$=""]');
  // lucide-react ≥1.48 exports Trash2 as an alias of Trash: the svg carries
  // `lucide-trash lucide-trash-2` (deps bump 5cb2de739), not `lucide-trash2`.
  const trash = [...document.querySelectorAll<HTMLButtonElement>(".composer-guidance-item button")]
    .find((b) => b.querySelector("svg.lucide-trash-2"));
  ok(trash !== undefined, "the dismiss control exists on the row");
  await act(async () => { trash?.click(); await new Promise((r) => setTimeout(r, 0)); });
  eq(dismissed.join(","), "a", "the dismiss handler fired exactly once");
  eq(document.querySelector('[role="note"]') !== null, true, "clicking a row control does not collapse the preview (closest-guard)");
  // The text button is its own toggle — one click, no double-fire through the card.
  await act(async () => { textButton("a")?.click(); await new Promise((r) => setTimeout(r, 0)); });
  eq(document.querySelector('[role="note"]'), null, "the text button still collapses in a single click (no toggle double-fire)");
  await cleanup(dom, root);
}

// ── 3. a failed preview fetch clears loading and stays collapsible ──────────
{
  const { dom, root } = await mount({
    items: [makeItem("a")],
    onPreviewText: async () => { throw new Error("offline"); },
  });
  await act(async () => { textButton("a")?.click(); await new Promise((r) => setTimeout(r, 0)); });
  const note = document.querySelector<HTMLElement>('[role="note"]');
  eq(note?.getAttribute("aria-busy"), "false", "failed fetch clears the loading flag (finally)");
  const card = document.querySelector(".composer-guidance-item") as HTMLElement;
  await act(async () => { card.click(); await new Promise((r) => setTimeout(r, 0)); });
  eq(document.querySelector('[role="note"]'), null, "a failed-fetch row still collapses on card click");
  await cleanup(dom, root);
}

// ── 3b. a hung fetch loses to the timeout and never blocks the row ──────────
{
  const dom = installDom();
  const realSetTimeout = dom.window.setTimeout;
  // Intercept only the preview timeout (record it, fire on demand); everything
  // else forwards through the saved native with the wrapper swapped out for
  // the duration of the call, so jsdom's internal setTimeout lookup can never
  // re-enter the wrapper.
  let scheduledTimeout: (() => void) | null = null;
  const wrapper = ((fn: () => void, ms?: number, ...rest: unknown[]) => {
    if (ms === PREVIEW_TIMEOUT_MS) {
      scheduledTimeout = fn;
      return 0;
    }
    dom.window.setTimeout = realSetTimeout;
    try {
      return realSetTimeout.call(dom.window, fn as () => void, ms, ...rest);
    } finally {
      dom.window.setTimeout = wrapper;
    }
  }) as typeof setTimeout;
  dom.window.setTimeout = wrapper;
  const root = createRoot(document.getElementById("root")!);
  await act(async () => {
    root.render(
      <StrictMode>
        <LocaleProvider>
          <ComposerGuidanceShelf
            recovery={null} recoveryDisabled={false} items={[makeItem("a")]} expanded
            running={false} disabled={false} readOnly={false} sendingId={null}
            onReview={() => {}} onRecoveryResumed={() => {}} onRecoveryError={() => {}}
            onToggleExpanded={() => {}} onSend={() => {}} onDismiss={() => {}}
            onPreviewText={() => new Promise<string>(() => {})}
          />
        </LocaleProvider>
      </StrictMode>,
    );
    await new Promise((r) => realSetTimeout.call(dom.window, r as () => void, 0));
  });
  await act(async () => { textButton("a")?.click(); await new Promise((r) => realSetTimeout.call(dom.window, r as () => void, 5)); });
  ok(scheduledTimeout !== null, "the hung fetch arms the preview timeout");
  await act(async () => { scheduledTimeout?.(); await new Promise((r) => realSetTimeout.call(dom.window, r as () => void, 0)); });
  eq(document.querySelector<HTMLElement>('[role="note"]')?.getAttribute("aria-busy"), "false",
    "the timeout wins the race and clears the loading flag");
  const card = document.querySelector(".composer-guidance-item") as HTMLElement;
  await act(async () => { card.click(); await new Promise((r) => realSetTimeout.call(dom.window, r as () => void, 0)); });
  eq(document.querySelector('[role="note"]'), null, "a hung-fetch row still collapses after the timeout");
  await act(async () => root.unmount());
  dom.window.close();
}

// ── 2. long expanded queue caps (class gate); short/collapsed stays normal ──
{
  const six = ["1", "2", "3", "4", "5", "6"].map(makeItem);
  const { dom, root, render } = await mount({ items: six, expanded: true });
  ok(document.querySelector(".composer-guidance-list--tall") !== null, "expanded queue with >5 entries gets the capped class");
  await render({ items: six.slice(0, 4) });
  eq(document.querySelector(".composer-guidance-list--tall"), null, "a short queue stays uncapped");
  await render({ items: six, expanded: false });
  eq(document.querySelector(".composer-guidance-list--tall"), null, "the collapsed view never needs the cap");
  await cleanup(dom, root);
}

// ── 4b. queue collapse clears the open preview (previewId single driver) ────
{
  const { dom, root, render } = await mount({ items: [makeItem("a"), makeItem("b"), makeItem("c")], expanded: true });
  await act(async () => { textButton("a")?.click(); await new Promise((r) => setTimeout(r, 0)); });
  eq(document.querySelector('[role="note"]') !== null, true, "preview open before the queue collapses");
  await render({ expanded: false });
  eq(document.querySelector('[role="note"]'), null, "collapsing the queue closes its open preview — no residue for the next expansion");
  await render({ expanded: true });
  eq(document.querySelector('[role="note"]'), null, "re-expanding starts from a clean preview state");
  await cleanup(dom, root);
}

// ── 5. source guards: Composer auto-collapse rules + shared locale key ──────
{
  const thisFile = fileURLToPath(import.meta.url);
  const composerSource = readFileSync(resolve(dirname(thisFile), "../components/Composer.tsx"), "utf8");
  const shelfSource = readFileSync(resolve(dirname(thisFile), "../components/ComposerGuidanceShelf.tsx"), "utf8");
  const autoCollapseCount = (composerSource.match(/setGuidanceExpanded\(false\)/g) ?? []).length;
  eq(autoCollapseCount, 4, "all four queue-level auto-collapse rules in Composer.tsx are untouched");
  const shelfCollapseUses = (shelfSource.match(/t\("composer\.guidanceCollapse"\)/g) ?? []).length;
  eq(shelfCollapseUses, 2, "head collapse button and footer reuse the existing guidanceCollapse locale key (no new keys)");
}

assertNoFailure();
function assertNoFailure() {
  ok(passed >= 15, `expected at least 15 checks, got ${passed}`);
  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}
