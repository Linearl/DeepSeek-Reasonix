// Run: tsx src/__tests__/user-message-fold.test.tsx
// Task 436: overly long user messages (typed, steered, or injected guidance)
// default to a height-clamped card with an expand/collapse chevron. Display
// layer only — the full text must stay in the DOM in both states.

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { UserMessage, estimateUserMessageLines, USER_MSG_FOLD_LINE_THRESHOLD } from "../components/Message";
import { LocaleProvider } from "../lib/i18n";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    ok.failed = true;
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}
(ok as { failed?: boolean }).failed = false;

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

function flushTimers(ms = 0): Promise<void> {
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
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  globalThis.Event = dom.window.Event;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
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
      matches: true,
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

function makeLongText(lines: number): string {
  return Array.from({ length: lines }, (_, i) => `第 ${i + 1} 行：这是一条为了触发折叠限高而存在的较长内容。`).join("\n");
}

async function renderUserMessage(text: string): Promise<{ root: ReturnType<typeof createRoot>; dom: JSDOM }> {
  const dom = installDom();
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(<LocaleProvider><UserMessage id="h1" text={text} /></LocaleProvider>);
    await flushTimers();
  });
  return { root, dom };
}

async function main() {
  // --- estimateUserMessageLines: line counting + CJK width weighting ---
  eq(estimateUserMessageLines(""), 0, "empty text estimates zero lines");
  eq(estimateUserMessageLines("short"), 1, "single short line estimates one line");
  eq(estimateUserMessageLines("a\nb\nc"), 3, "three newline-separated lines estimate three lines");
  ok(estimateUserMessageLines(makeLongText(40)) >= 40, "long multi-line text exceeds the threshold");
  // One CJK char renders ≈2× wider than one Latin char: 110 CJK chars ≈ 220
  // Latin-char units ≈ 2 estimated lines.
  eq(estimateUserMessageLines("汉".repeat(110)), 2, "CJK chars count double width units");
  eq(estimateUserMessageLines("a".repeat(220)), 2, "very long single line folds by wrap estimate");

  // --- long message: default collapsed + expandable + content integrity ---
  {
    const longText = makeLongText(40);
    const { root, dom } = await renderUserMessage(longText);
    const toggle = document.querySelector<HTMLButtonElement>(".msg-fold__toggle");
    ok(toggle !== null, "long message renders the fold toggle");
    eq(toggle?.getAttribute("aria-expanded"), "false", "fold toggle starts collapsed (aria-expanded=false)");
    ok(document.querySelector(".msg-fold--clamped") !== null, "long message body is clamped by default");
    ok(document.querySelector(".msg-fold--clamped .msg__text")?.textContent?.includes("第 40 行") === true,
      "collapsed clamp still keeps the full text in the DOM (tail included)");
    const foldButton = document.querySelector<HTMLButtonElement>(".msg-fold__toggle");
    await act(async () => {
      foldButton?.click();
      await flushTimers();
    });
    eq(document.querySelector(".msg-fold__toggle")?.getAttribute("aria-expanded"), "true", "click expands (aria-expanded=true)");
    ok(document.querySelector(".msg-fold--clamped") === null, "expanded body drops the clamp class");
    ok(document.querySelector(".msg-fold")?.textContent?.includes("第 1 行") === true, "expanded view keeps the head of the text");
    ok(document.querySelector(".msg-fold")?.textContent?.includes("第 40 行") === true, "expanded view keeps the tail of the text");
    const collapseButton = document.querySelector<HTMLButtonElement>(".msg-fold__toggle");
    await act(async () => {
      collapseButton?.click();
      await flushTimers();
    });
    ok(document.querySelector(".msg-fold--clamped") !== null, "second click re-collapses the body");

    await act(async () => root.unmount());
    dom.window.close();
  }

  // --- short message: untouched by the fold ---
  {
    const { root, dom } = await renderUserMessage("只是一件小事");
    ok(document.querySelector(".msg-fold__toggle") === null, "short message renders no fold toggle");
    ok(document.querySelector(".msg-fold--clamped") === null, "short message is not clamped");
    ok(document.querySelector(".msg__text")?.textContent === "只是一件小事", "short message body renders unchanged");
    await act(async () => root.unmount());
    dom.window.close();
  }

  // --- threshold boundary: message just under the threshold stays open ---
  {
    const underThreshold = makeLongText(USER_MSG_FOLD_LINE_THRESHOLD - 1);
    const { root, dom } = await renderUserMessage(underThreshold);
    ok(document.querySelector(".msg-fold__toggle") === null, `message under ${USER_MSG_FOLD_LINE_THRESHOLD} lines renders no fold toggle`);
    await act(async () => root.unmount());
    dom.window.close();
  }

  // --- steered/injected guidance: same clamp as typed messages ---
  {
    const steerText = `[系统引导]\n${makeLongText(30)}`;
    const { root, dom } = await renderUserMessage(steerText);
    ok(document.querySelector(".msg-fold--clamped") !== null, "long injected guidance clamps like typed messages");
    ok(document.querySelector(".msg-fold__toggle") !== null, "long injected guidance gets the fold toggle");
    await act(async () => root.unmount());
    dom.window.close();
  }

  // --- summary ---
  if (failed > 0) {
    process.stdout.write(`\n${failed} FAILED, ${passed} passed\n`);
    process.exit(1);
  }
  process.stdout.write(`\nall ${passed} checks passed\n`);
  process.exit(0);
}

void main();
