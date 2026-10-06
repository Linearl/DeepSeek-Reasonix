// Run: tsx src/__tests__/question-search-panel.test.tsx
import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { QuestionSearchPanel, filterQuestions } from "../components/QuestionSearchPanel";
import { LocaleProvider } from "../lib/i18n";
import type { QuestionAnchor } from "../lib/transcriptGrouping";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost" });
(globalThis as unknown as { document: Document }).document = dom.window.document;
(globalThis as unknown as { window: Window }).window = dom.window as unknown as Window;
(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
if (!("navigator" in globalThis)) {
  Object.defineProperty(globalThis, "navigator", {
    value: dom.window.navigator,
    configurable: true,
    writable: true,
  });
}
// React 19's input-event polyfill probes the IE-only attach/detachEvent pair;
// without the stub its cleanup path throws inside dispatchEvent.
const elementProto = dom.window.HTMLElement.prototype as unknown as Record<string, unknown>;
elementProto.attachEvent = () => {};
elementProto.detachEvent = () => {};
if (typeof globalThis.requestAnimationFrame !== "function") {
  globalThis.requestAnimationFrame = (cb: FrameRequestCallback) => setTimeout(() => cb(Date.now()), 0) as unknown as number;
  globalThis.cancelAnimationFrame = (id: number) => clearTimeout(id);
}

const questions: QuestionAnchor[] = [
  { id: "q1", text: "How do I implement the parser?", turn: 0 },
  { id: "q2", text: "Explain the git rebase conflict.", turn: 4 },
  { id: "q3", text: "What is the benchmark setup?", turn: 9 },
];

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

async function render(
  open: boolean,
  qs: QuestionAnchor[],
  onJump: (q: QuestionAnchor) => void,
  extra: Record<string, unknown> = {},
) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <QuestionSearchPanel open={open} onClose={() => {}} questions={qs} totalQuestions={qs.length} onJump={onJump} {...extra} />
      </LocaleProvider>,
    );
  });
  await new Promise((resolve) => setTimeout(resolve, 0));
  return { container, root };
}

async function main() {
  // Pure filter helper
  eq(filterQuestions(questions, "").length, 3, "empty query returns all questions");
  eq(filterQuestions(questions, "parser").length, 1, "filter matches substring");
  eq(filterQuestions(questions, "  PARSER  ").length, 1, "filter is case-insensitive and trims");
  eq(filterQuestions(questions, "zzz-no-match").length, 0, "no matches yields empty list");

  // Closed panel renders nothing
  const closed = await render(false, questions, () => {});
  eq(closed.container.querySelector(".question-search-panel") === null, true, "closed panel renders nothing");
  closed.root.unmount();

  // Open panel lists all cards and jumps on click
  const jumps: number[] = [];
  const opened = await render(true, questions, (q) => jumps.push(q.turn));
  eq(opened.container.querySelectorAll(".question-search__card").length, 3, "open panel lists all question cards");
  (opened.container.querySelector(".question-search__card") as HTMLElement)?.click();
  await new Promise((resolve) => setTimeout(resolve, 0));
  eq(jumps.length === 1 && jumps[0] === 0, true, "clicking a card jumps to that question's turn");
  opened.root.unmount();

  // Empty list shows empty state
  const empty = await render(true, [], () => {});
  eq(empty.container.querySelector(".question-search__empty") !== null, true, "empty state shown when no questions");
  empty.root.unmount();

  // ── Task 566: older-history props wiring (footer hints) ────────────────
  // Both locales are accepted because the harness locale follows the machine.
  const OLDER_HINT = /滚动到顶部加载更早|Scroll to top to load earlier/;
  const LOADING_HINT = /正在加载更早历史|Loading earlier history/;
  const footText = (scope: HTMLElement) => scope.querySelector(".question-search__foot")?.textContent ?? "";

  // Default props: no hint at all — the unwired (default-false) path is unchanged.
  const plain = await render(true, questions, () => {});
  eq(OLDER_HINT.test(footText(plain.container)), false, "default footer has no scroll-to-top hint");
  eq(LOADING_HINT.test(footText(plain.container)), false, "default footer has no loading hint");
  plain.root.unmount();

  const withOlder = await render(true, questions, () => {}, { hasOlderHistory: true });
  eq(OLDER_HINT.test(footText(withOlder.container)), true, "footer shows the scroll-to-top hint when older history exists");
  withOlder.root.unmount();

  const withLoading = await render(true, questions, () => {}, { hasOlderHistory: true, loadingOlderHistory: true });
  eq(LOADING_HINT.test(footText(withLoading.container)), true, "footer shows the loading hint while paging");
  eq(OLDER_HINT.test(footText(withLoading.container)), false, "scroll-to-top hint is hidden while loading");
  withLoading.root.unmount();

  // ── Task 566: onReachTop wiring (scroll-to-top latch + rearm) ──────────
  const reachCalls: number[] = [];
  const scrollable = await render(true, questions, () => {}, { onReachTop: () => reachCalls.push(1) });
  const list = scrollable.container.querySelector<HTMLElement>(".question-search__results")!;
  await act(async () => {
    list.dispatchEvent(new dom.window.Event("scroll"));
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  eq(reachCalls.length, 1, "scroll sitting at the very top fires onReachTop");
  await act(async () => {
    list.dispatchEvent(new dom.window.Event("scroll"));
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  eq(reachCalls.length, 1, "the latch keeps onReachTop from refiring while still at the top");
  await act(async () => {
    list.scrollTop = 50;
    list.dispatchEvent(new dom.window.Event("scroll"));
    list.scrollTop = 0;
    list.dispatchEvent(new dom.window.Event("scroll"));
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  eq(reachCalls.length, 2, "scrolling away rearms the latch so a later return fires again");
  scrollable.root.unmount();

  // Wheel-up capture at the top pages older history directly (no scroll event
  // fires when the list cannot scroll further, so this path is separate).
  const wheelCalls: number[] = [];
  const wheelable = await render(true, questions, () => {}, { onReachTop: () => wheelCalls.push(1) });
  const wheelList = wheelable.container.querySelector<HTMLElement>(".question-search__results")!;
  await act(async () => {
    wheelList.dispatchEvent(new dom.window.WheelEvent("wheel", { deltaY: -120, bubbles: true, cancelable: true }));
    wheelList.dispatchEvent(new dom.window.WheelEvent("wheel", { deltaY: -120, bubbles: true, cancelable: true }));
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  eq(wheelCalls.length, 1, "wheel-up at the very top fires onReachTop exactly once (capture intercept + latch)");
  wheelable.root.unmount();

  // Without onReachTop nothing fires and the panel keeps rendering normally.
  const unwired = await render(true, questions, () => {});
  const unwiredList = unwired.container.querySelector<HTMLElement>(".question-search__results")!;
  await act(async () => {
    unwiredList.dispatchEvent(new dom.window.Event("scroll"));
    unwiredList.dispatchEvent(new dom.window.WheelEvent("wheel", { deltaY: -120, bubbles: true, cancelable: true }));
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  eq(unwiredList.querySelectorAll(".question-search__card").length, 3, "unwired panel still renders its question cards");
  unwired.root.unmount();

  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exitCode = 1;
}

void main();
