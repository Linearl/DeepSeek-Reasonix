// Run: tsx src/__tests__/tl1475-streaming-tool-input-preview.test.tsx
// tl1475 流式工具参数预览:pending 卡出现 / 参数填充刷新 / 完成转正式 /
// 失败(discard)与取消(turn_done)清理 / 采样停止(终态不回退+ticker 即停) /
// 既有工具卡行为零回归。

import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { ToolCard } from "../components/ToolCard";
import { LocaleProvider } from "../lib/i18n";
import { streamingToolSubject } from "../lib/tools";
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

const originalNow = Date.now;
let fakeNow = 100_000;
Date.now = () => fakeNow;

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
  globalThis.Event = dom.window.Event;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
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

async function advance(ms: number) {
  fakeNow += ms;
  await act(async () => {
    for (const fire of [...intervals.values()]) fire();
    await flushTimers();
  });
}

function toolOf(s: ReturnType<typeof reducer>, id: string): ToolItem | undefined {
  return s.items.find((it): it is ToolItem => it.kind === "tool" && it.id === id);
}

console.log("\ntl1475 streaming tool input preview — reducer");

// --- pending 卡出现 + 参数逐步填充 -------------------------------------------
let s = reducer(initialState, { type: "event", e: { kind: "turn_started" } });
s = reducer(s, {
  type: "event",
  e: { kind: "tool_dispatch", tool: { id: "w1", name: "write_file", partial: true, argChars: 18, argsPreview: `{"path":"logs/app.log","content":"first li`, readOnly: false, attemptId: "att1" } },
});
{
  const pending = toolOf(s, "w1");
  eq(pending?.status, "running", "partial dispatch creates the pending card in running state");
  eq(pending?.args, "", "pending card holds no full args yet");
  eq(pending?.argChars, 18, "pending card carries the streamed volume");
  eq(pending?.argsPreview, `{"path":"logs/app.log","content":"first li`, "pending card carries the streaming args preview");
}
s = reducer(s, {
  type: "event",
  e: { kind: "tool_dispatch", tool: { id: "w1", name: "write_file", partial: true, argChars: 4200, argsPreview: `{"path":"logs/app.log","content":"first line\nand the newest tail…`, readOnly: false, attemptId: "att1" } },
});
{
  const pending = toolOf(s, "w1");
  eq(pending?.argChars, 4200, "later partial tick refreshes the streamed volume");
  ok(
    (pending?.argsPreview ?? "").includes("newest tail"),
    "later partial tick refreshes the preview (参数填充刷新)",
  );
  eq(pending?.startedAt, 100_000, "sampling refresh keeps the original startedAt");
}

// --- 完成转正式 ---------------------------------------------------------------
s = reducer(s, {
  type: "event",
  e: { kind: "tool_dispatch", tool: { id: "w1", name: "write_file", args: `{"path":"logs/app.log","content":"full body"}`, readOnly: false } },
});
{
  const done = toolOf(s, "w1");
  eq(done?.args, `{"path":"logs/app.log","content":"full body"}`, "full dispatch lands the real args (完成转正式)");
  eq(done?.argsPreview, undefined, "full dispatch drops the streaming preview");
  eq(done?.argChars, undefined, "full dispatch drops the volume counter");
  eq(done?.status, "running", "card stays running until the tool executes");
  ok((done?.summary ?? "").startsWith("1"), `full dispatch builds the settled summary (got ${JSON.stringify(done?.summary)})`);
}

// --- 采样停止语义:终态之后的 stale partial 不得回写 --------------------------
s = reducer(s, {
  type: "event",
  e: { kind: "tool_result", tool: { id: "w1", name: "write_file", readOnly: false, output: "ok", durationMs: 12 } },
});
s = reducer(s, {
  type: "event",
  e: { kind: "tool_dispatch", tool: { id: "w1", name: "write_file", partial: true, argChars: 99, argsPreview: `{"path":"STALE`, readOnly: false } },
});
{
  const settled = toolOf(s, "w1");
  eq(settled?.status, "done", "tool_result settles the card");
  eq(settled?.args, `{"path":"logs/app.log","content":"full body"}`, "stale partial after completion must not rewrite args (采样停止)");
  eq(settled?.argsPreview, undefined, "stale partial must not resurrect the preview");
  eq(settled?.argChars, undefined, "stale partial must not resurrect the volume counter");
}

// --- 失败清理:attempt discard 移除本 attempt 建的 pending 卡 ------------------
s = reducer(s, { type: "event", e: { kind: "stream_attempt", streamAttempt: { id: "att2", action: "begin" } } } as Parameters<typeof reducer>[1]);
s = reducer(s, {
  type: "event",
  e: { kind: "tool_dispatch", tool: { id: "w2", name: "write_file", partial: true, argChars: 50, argsPreview: `{"path":"draft.txt","content":"x`, readOnly: false, attemptId: "att2" } },
});
eq(toolOf(s, "w2")?.status, "running", "failed attempt's pending card exists before the discard");
s = reducer(s, { type: "event", e: { kind: "stream_attempt", streamAttempt: { id: "att2", action: "discard", reason: "interrupted", attempt: 1 } } } as Parameters<typeof reducer>[1]);
eq(toolOf(s, "w2"), undefined, "attempt discard removes the pending card (失败清理)");

// --- 取消清理:turn_done 把 running 的 pending 卡压到 stopped ------------------
s = reducer(s, {
  type: "event",
  e: { kind: "tool_dispatch", tool: { id: "w3", name: "bash", partial: true, argChars: 7, argsPreview: `{"command":"sleep`, readOnly: false } },
});
eq(toolOf(s, "w3")?.status, "running", "pending card exists before cancellation");
s = reducer(s, { type: "event", e: { kind: "turn_done", status: "interrupted" } } as Parameters<typeof reducer>[1]);
eq(toolOf(s, "w3")?.status, "stopped", "turn_done settles the pending card to stopped (取消清理)");
eq(toolOf(s, "w3")?.argsPreview, `{"command":"sleep`, "settled card keeps its last preview as inert display data");

// --- 零回归:无 preview 的 partial(旧后端/extension)照常建卡 ------------------
s = reducer(s, { type: "event", e: { kind: "stream_attempt", streamAttempt: { id: "att4", action: "begin" } } } as Parameters<typeof reducer>[1]);
s = reducer(s, {
  type: "event",
  e: { kind: "tool_dispatch", tool: { id: "w4", name: "read_file", partial: true, argChars: 9, readOnly: false, attemptId: "att4" } },
});
{
  const legacy = toolOf(s, "w4");
  eq(legacy?.status, "running", "partial without preview still creates the pending card");
  eq(legacy?.argsPreview, undefined, "partial without preview carries no preview field");
}

console.log("\ntl1475 streaming tool input preview — subject extraction");

eq(streamingToolSubject("write_file", `{"path":"logs/app.log","content":"a"\n…tail "path":"fake.txt"`), "logs/app.log", "subject scans the HEAD segment only (tail decoy key ignored)");
eq(streamingToolSubject("write_file", `{"path":"a.txt","content":"x`), "a.txt", "complete head field extracts");
eq(streamingToolSubject("bash", `{"command":"git status`), "git status", "unterminated streaming value renders as far as it got");
eq(streamingToolSubject("bash", `{"command":"echo \\"hi\\"`), `echo "hi"`, "escaped quotes decode");
eq(streamingToolSubject("grep", `{"pattern":"tl14`), "tl14", "in-flight pattern extracts");
eq(streamingToolSubject("grep", `{"path":"src","pattern":"foo`), "foo", "pattern outranks path for grep");
eq(streamingToolSubject("write_file", `{"content":"no path yet`), "", "missing subject key yields empty (never guessed)");
eq(streamingToolSubject("write_file", `plain text not json`), "", "non-JSON excerpt yields empty");
eq(streamingToolSubject("web_fetch", `{"url":"https://x`), "https://x", "web_fetch url extracts");

console.log("\ntl1475 streaming tool input preview — card rendering");

{
  const pending: ToolItem = { kind: "tool", id: "ui1", name: "write_file", args: "", readOnly: false, status: "running", startedAt: fakeNow, argChars: 1234, argsPreview: `{"path":"logs/app.log","content":"first line\nnewest tail` };
  const ui = await renderCard(pending);
  const head = document.querySelector(".tool__head")?.textContent ?? "";
  ok(head.includes("logs/app.log"), `pending card shows the streaming subject (got ${JSON.stringify(head)})`);
  ok(head.includes("receiving arguments"), `pending card keeps the volume readout (got ${JSON.stringify(head)})`);
  eq(intervals.size, 1, "pending card registers exactly one ticker");
  await advance(3_000);
  const elapsed = document.querySelector(".tool__duration")?.textContent ?? "";
  ok(elapsed.includes("3s"), `elapsed ticks while streaming (got ${JSON.stringify(elapsed)})`);
  const body = document.querySelector(".tool__body")?.textContent ?? "";
  ok(body.includes("newest tail"), `preview excerpt renders in the card body (got ${JSON.stringify(body.slice(0, 120))})`);
  await ui.cleanup();
  fakeNow = 100_000;
}

{
  // bash 流式命令出现在 subject。
  const bashPending: ToolItem = { kind: "tool", id: "ui2", name: "bash", args: "", readOnly: false, status: "running", startedAt: fakeNow, argChars: 10, argsPreview: `{"command":"git status --short"` };
  const ui = await renderCard(bashPending);
  const head = document.querySelector(".tool__head")?.textContent ?? "";
  ok(head.includes("git status --short"), `bash streaming command shows as subject (got ${JSON.stringify(head)})`);
  await ui.cleanup();
}

{
  // 完成转正式渲染:args 落地后 subject 来自完整 args,preview 不再出现。
  const settled: ToolItem = { kind: "tool", id: "ui3", name: "write_file", args: `{"path":"final.txt","content":"done"}`, readOnly: false, status: "done", durationMs: 55 };
  const ui = await renderCard(settled);
  const head = document.querySelector(".tool__head")?.textContent ?? "";
  ok(head.includes("final.txt"), `settled card subject comes from the full args (got ${JSON.stringify(head)})`);
  ok(!head.includes("newest tail"), "settled card shows no streaming preview");
  eq(intervals.size, 0, "settled card registers no ticker");
  const before = document.querySelector(".tool__head")?.textContent ?? "";
  await advance(60_000);
  eq(document.querySelector(".tool__head")?.textContent ?? "", before, "settled card is frozen — sampling stopped with the stream (流结束定时器即停)");
  await ui.cleanup();
  fakeNow = 100_000;
}

{
  // 零回归:argChars-only 卡(receivingArgs)与 done 卡文案不受影响。
  const counter: ToolItem = { kind: "tool", id: "ui4", name: "read_file", args: "", readOnly: false, status: "running", startedAt: fakeNow, argChars: 77 };
  const ui = await renderCard(counter);
  const head = document.querySelector(".tool__head")?.textContent ?? "";
  ok(head.includes("receiving arguments") && head.includes("77"), `argChars-only card keeps the legacy readout (got ${JSON.stringify(head)})`);
  ok(!head.includes("path"), "argChars-only card shows no subject fallback");
  await ui.cleanup();
}

Date.now = originalNow;
console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
