// Run: tsx src/__tests__/tool-card-ask-pending.test.tsx
//
// Task 567 — ask 工具卡占位态。ask 派发后、用户作答前，卡片读作「等待用户
// 确认」而不是「工具正在干活」：不显 JSON 入参原文、不跑秒；ToolResult 落定
// 后原渲染原样回归（ask 弹窗延时链路调研 20261007 §7）。

import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { ToolCard } from "../components/ToolCard";
import { LocaleProvider } from "../lib/i18n";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";
import { initialState, reducer, type Item } from "../lib/useController";

type ToolItem = Extract<Item, { kind: "tool" }>;

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

const intervals = new Map<number, () => void>();
let nextIntervalId = 1;

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
  globalThis.MouseEvent = dom.window.MouseEvent;
  dom.window.matchMedia = () => ({
    matches: true,
    media: "(prefers-reduced-motion: reduce)",
    onchange: null,
    addListener: () => undefined,
    removeListener: () => undefined,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    dispatchEvent: () => false,
  });
  dom.window.setInterval = ((handler: TimerHandler) => {
    const id = nextIntervalId++;
    if (typeof handler === "function") intervals.set(id, handler as () => void);
    return id;
  }) as typeof dom.window.setInterval;
  dom.window.clearInterval = ((id?: number) => {
    if (id !== undefined) intervals.delete(id);
  }) as typeof dom.window.clearInterval;
  return dom;
}

async function renderCard(item: ToolItem) {
  const dom = installDom();
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(React.createElement(LocaleProvider, null, React.createElement(ToolCard, { item })));
    await flushTimers();
  });
  return {
    async cleanup() {
      await act(async () => {
        root.unmount();
      });
      dom.window.close();
    },
  };
}

const ASK_ARGS = `{"questions":[{"id":"q1","header":"Release","prompt":"Ship now?","options":[{"label":"Yes"}]}]}`;

console.log("\ntask 567 ask card waiting placeholder");

// Dispatch → AskRequest 窗口：ask 卡处于 running（等待用户）。
let s = reducer(initialState, { type: "event", e: { kind: "turn_started" } });
s = reducer(s, {
  type: "event",
  e: { kind: "tool_dispatch", tool: { id: "ask-1", name: "ask", args: ASK_ARGS, readOnly: true } },
});
const pendingAsk = s.items.find((it): it is ToolItem => it.kind === "tool" && it.id === "ask-1");
eq(pendingAsk?.status, "running", "dispatch creates a running ask card");

{
  const ui = await renderCard(pendingAsk!);
  const summary = document.querySelector(".tool__summary")?.textContent ?? "";
  eq(summary, "waiting for your answer…", "pending ask card shows the waiting placeholder instead of a working readout");
  eq(document.querySelector(".tool__duration")?.textContent ?? null, null, "pending ask card runs no stopwatch");
  eq(intervals.size, 0, "pending ask card registers no ticker");
  eq(document.querySelector(".tool__chevron"), null, "pending ask card exposes no expandable body");
  ok(!document.body.textContent?.includes("Ship now?"), "pending ask card does not render the raw args JSON");
  await ui.cleanup();
}

// 用户作答 → ToolResult 落定：原渲染回归。
s = reducer(s, {
  type: "event",
  e: { kind: "tool_result", tool: { id: "ask-1", name: "ask", readOnly: true, output: "answered", durationMs: 1234 } },
});
const settledAsk = s.items.find((it): it is ToolItem => it.kind === "tool" && it.id === "ask-1");
eq(settledAsk?.status, "done", "tool_result settles the ask card");

{
  const ui = await renderCard(settledAsk!);
  const summary = document.querySelector(".tool__summary")?.textContent ?? "";
  ok(summary !== "waiting for your answer…", "settled ask card no longer shows the waiting placeholder");
  eq(document.querySelector(".tool__duration")?.textContent, "1234 ms", "settled ask card shows the final duration");
  await ui.cleanup();
}

// 落定后原渲染不回归：带内存 args 的 settled 卡（未归档）JSON 原文照常渲染。
{
  const settledInMemory: ToolItem = { kind: "tool", id: "ask-2", name: "ask", args: ASK_ARGS, readOnly: true, status: "done", durationMs: 999 };
  const ui = await renderCard(settledInMemory);
  ok(document.body.textContent?.includes("Ship now?") ?? false, "settled ask card with in-memory args renders the original args JSON");
  const summary = document.querySelector(".tool__summary")?.textContent ?? "";
  ok(summary !== "waiting for your answer…", "settled ask card with in-memory args shows no placeholder");
  await ui.cleanup();
}

// 三语占位文案齐备。
eq(zh["tool.askWaiting"], "等待用户确认…", "zh placeholder copy");
eq(zhTW["tool.askWaiting"], "等待使用者確認…", "zh-TW placeholder copy");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
