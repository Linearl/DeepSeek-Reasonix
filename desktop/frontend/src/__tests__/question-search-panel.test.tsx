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

async function render(open: boolean, qs: QuestionAnchor[], onJump: (q: QuestionAnchor) => void) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <QuestionSearchPanel open={open} onClose={() => {}} questions={qs} totalQuestions={qs.length} onJump={onJump} />
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

  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exitCode = 1;
}

void main();
