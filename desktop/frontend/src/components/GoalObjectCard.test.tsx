// Run: tsx src/components/GoalObjectCard.test.tsx

// tl1472 目标对象化·三形态组件钉（UI 面）：边栏 goal 卡在待办列表下展示
// 整理后摘要，且 phase 与 activation 必须同时可见（armed/disarmed 双显——
// 只显 running 会复刻 1459 镜像事故）。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { LocaleProvider } from "../lib/i18n";
import type { GoalObjectInfo } from "../lib/types";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
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
globalThis.SVGElement = dom.window.SVGElement;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

const { GoalObjectCard } = await import("./TodoPanel");

let activeRoot: Root | null = null;
let activeHost: HTMLElement | null = null;

async function renderGoal(goal: GoalObjectInfo) {
  activeHost = document.createElement("div");
  document.body.appendChild(activeHost);
  activeRoot = createRoot(activeHost);
  await act(async () => {
    activeRoot?.render(
      <LocaleProvider>
        <GoalObjectCard goal={goal} />
      </LocaleProvider>,
    );
  });
}

async function cleanup() {
  if (activeRoot) {
    await act(async () => activeRoot?.unmount());
  }
  activeHost?.remove();
  activeRoot = null;
  activeHost = null;
}

function text() {
  return document.body.textContent ?? "";
}

function goal(overrides: Partial<GoalObjectInfo> = {}): GoalObjectInfo {
  return {
    goal: "refactor the parser",
    summary: "Refactor parser for speed",
    phase: "active",
    activation: "armed",
    ...overrides,
  };
}

await act(async () => {
  await renderGoal(goal());
});
ok(text().includes("Refactor parser for speed"), "shows the tidied summary (not the raw text)");
ok(text().includes("armed"), "armed badge visible");
ok(text().includes("active"), "phase label visible");
ok(Boolean(document.querySelector(".todobar__goal[data-activation='armed'][data-phase='active']")), "data attributes carry phase+activation");
await cleanup();

await act(async () => {
  await renderGoal(goal({ activation: "disarmed" }));
});
ok(text().includes("disarmed"), "disarmed badge visible when disarmed (双显钉)");
ok(Boolean(document.querySelector(".todobar__goal[data-activation='disarmed']")), "disarmed data attribute");
await cleanup();

await act(async () => {
  await renderGoal(goal({ phase: "budget_limited", activation: "disarmed", summary: "Out of budget" }));
});
ok(text().includes("budget limited"), "budget_limited phase label");
ok(text().includes("Out of budget"), "budget summary shown");
await cleanup();

await act(async () => {
  await renderGoal(goal({ summary: "", phase: "paused" }));
});
ok(text().includes("refactor the parser"), "falls back to the goal text when no summary");
ok(text().includes("paused"), "paused phase label");
await cleanup();

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
