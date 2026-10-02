// Run: tsx src/__tests__/transcript-find-bar.test.tsx
// Task 399: the find bar UI — counter, empty state, keyboard nav, Escape.

import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost" });
(globalThis as unknown as { document: Document }).document = dom.window.document;
(globalThis as unknown as { window: Window }).window = dom.window as unknown as Window;
if (!("navigator" in globalThis)) {
  Object.defineProperty(globalThis, "navigator", {
    value: dom.window.navigator,
    configurable: true,
    writable: true,
  });
}
if (typeof globalThis.requestAnimationFrame !== "function") {
  globalThis.requestAnimationFrame = (cb: FrameRequestCallback) => setTimeout(() => cb(Date.now()), 0) as unknown as number;
  globalThis.cancelAnimationFrame = (id: number) => clearTimeout(id);
}

import { TranscriptFindBar } from "../components/TranscriptFindBar";
import { LocaleProvider } from "../lib/i18n";

let passed = 0;
let failed = 0;
function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

type BarProps = React.ComponentProps<typeof TranscriptFindBar>;

function Harness(props: { initial: Partial<BarProps>; onPrev: () => void; onNext: () => void; onClose: () => void }) {
  const [query, setQuery] = React.useState(props.initial.query ?? "");
  return (
    <LocaleProvider>
      <TranscriptFindBar
        open
        query={query}
        onQueryChange={setQuery}
        activeIndex={1}
        matchCount={0}
        capped={false}
        noMatches={false}
        onPrev={props.onPrev}
        onNext={props.onNext}
        onClose={props.onClose}
        {...props.initial}
        query={query}
      />
    </LocaleProvider>
  );
}

async function render(initial: Partial<BarProps>) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root: Root = createRoot(container);
  const calls = { prev: 0, next: 0, close: 0 };
  await act(async () => {
    root.render(
      <Harness
        initial={initial}
        onPrev={() => { calls.prev += 1; }}
        onNext={() => { calls.next += 1; }}
        onClose={() => { calls.close += 1; }}
      />,
    );
  });
  // Let the open-focus rAF fire.
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 10));
  });
  return {
    container,
    root,
    calls,
    input: container.querySelector("input.transcript-find__input") as HTMLInputElement,
    counter: container.querySelector(".transcript-find__count") as HTMLElement,
    buttons: Array.from(container.querySelectorAll("button.transcript-find__btn")),
    unmount: async () => {
      await act(async () => root.unmount());
      container.remove();
    },
  };
}

async function keydown(target: Element, init: KeyboardEventInit) {
  const event = new dom.window.KeyboardEvent("keydown", { bubbles: true, cancelable: true, ...init });
  await act(async () => {
    target.dispatchEvent(event);
  });
  return event;
}

console.log("\ntranscript find bar (task 399)");

// 1. Counter + count display
{
  const h = await render({ matchCount: 7, activeIndex: 3 });
  eq(h.counter.textContent, "3/7", "counter shows active/total");
  await h.unmount();
}
{
  const h = await render({ matchCount: 7, activeIndex: 1, capped: true });
  eq(h.counter.textContent, "1/7+", "capped counts render with a + suffix");
  await h.unmount();
}

// 2. Explicit empty state
{
  const h = await render({ matchCount: 0, activeIndex: 0, noMatches: true });
  // Locale follows the environment; assert the empty-state copy, not a language.
  eq(["No matches", "无匹配", "無匹配"].includes(h.counter.textContent ?? ""), true, "no-match query shows the explicit empty state");
  eq(h.buttons[0].disabled, true, "prev is disabled without hits");
  eq(h.buttons[1].disabled, true, "next is disabled without hits");
  await h.unmount();
}

// 3. Enter → next, Shift+Enter → prev, ArrowDown/ArrowUp
{
  const h = await render({ matchCount: 3 });
  await keydown(h.input, { key: "Enter" });
  eq(h.calls.next, 1, "Enter steps to the next match");
  eq(h.calls.prev, 0, "Enter does not step backward");
  await keydown(h.input, { key: "Enter", shiftKey: true });
  eq(h.calls.prev, 1, "Shift+Enter steps to the previous match");
  await keydown(h.input, { key: "ArrowDown" });
  eq(h.calls.next, 2, "ArrowDown steps forward");
  await keydown(h.input, { key: "ArrowUp" });
  eq(h.calls.prev, 2, "ArrowUp steps backward");
  await h.unmount();
}

// 4. Escape closes (document-level)
{
  const h = await render({ matchCount: 2 });
  await act(async () => {
    document.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  });
  eq(h.calls.close, 1, "Escape closes the bar");
  await h.unmount();
}

// 5. Buttons fire handlers
{
  const h = await render({ matchCount: 5 });
  await act(async () => {
    h.buttons[0].dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    h.buttons[1].dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    h.buttons[2].dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
  });
  eq(h.calls.prev, 1, "prev button steps backward");
  eq(h.calls.next, 1, "next button steps forward");
  eq(h.calls.close, 1, "close button closes");
  await h.unmount();
}

// 6. Typing flows through onQueryChange
{
  const h = await render({ matchCount: 0 });
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value")?.set;
    setter?.call(h.input, "needle");
    h.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
  });
  eq(h.input.value, "needle", "typing updates the input through controlled state");
  await h.unmount();
}

// 7. Focus lands in the input when open
{
  const h = await render({ matchCount: 0 });
  eq(document.activeElement === h.input, true, "opening the bar focuses the search input");
  await h.unmount();
}

// 8. Focus signal re-selects on repeat chord
{
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <TranscriptFindBar open query="abc" onQueryChange={() => {}} activeIndex={1} matchCount={2}
          capped={false} noMatches={false} onPrev={() => {}} onNext={() => {}} onClose={() => {}} focusSignal={1} />
      </LocaleProvider>,
    );
  });
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 10)); });
  const input = container.querySelector("input.transcript-find__input") as HTMLInputElement;
  eq(document.activeElement === input, true, "focusSignal=1 focuses the input");
  eq(input.selectionEnd, 3, "repeat Ctrl+F selects the whole query");
  await act(async () => root.unmount());
  container.remove();
}

console.log(`\n${passed}/${passed + failed} passed`);
if (failed > 0) process.exit(1);
