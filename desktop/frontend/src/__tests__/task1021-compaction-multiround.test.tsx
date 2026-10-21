// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task1021-compaction-multiround.test.tsx
//
// 任务 1021 验收（前端半）：多轮压缩状态误报——首轮即显示「上下文已压缩」而
// 内部仍在压缩（终态与进行中并存）。
//  1) 多轮会话全程单卡 pending：pass 2 的 started 复用 pass 1 的卡（不堆叠），
//     pass 1 的 ok done 不结算卡片（interimSummary 暂存，卡保持 pending），
//     pass 2 的 ok done 才落终态；
//  2) 终态卡标注总轮数（「共 N 轮」），摘要取最后一轮；
//  3) 单轮压缩零回归：无 pass 号（legacy 发送方）的 started→done 与旧行为
//     逐字段一致（终态卡立即出现，无 pass 后缀）；
//  4) 失败轮不结算会话：failed done 后同一会话 retry 的 started 仍复用同一张
//     卡（任务 867 语义保持）；
//  5) CompactionCard/ProcessFoldHeader 渲染：pending 第 2 轮带「第 2 轮」，
//     终态卡带「共 2 轮」，单轮卡无轮次后缀。

import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { LocaleProvider } from "../lib/i18n";
import { ProcessFoldHeader } from "../components/ProcessFoldHeader";
import { CompactionCard } from "../components/TranscriptCards";
import { initialState, reducer, type Item, type State } from "../lib/useController";
import type { WireEvent } from "../lib/types";

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

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

type CompactionItem = Extract<Item, { kind: "compaction" }>;

function lastCompaction(s: State): CompactionItem {
  const found = [...s.items].reverse().find((it): it is CompactionItem => it.kind === "compaction");
  if (!found) throw new Error("no compaction item in state");
  return found;
}

function compactionCount(s: State): number {
  return s.items.filter((it) => it.kind === "compaction").length;
}

function started(pass?: number): WireEvent {
  return { kind: "compaction_started", compaction: { trigger: "pressure", pass } } as unknown as WireEvent;
}

function done(pass: number, summary: string, messages = 7): WireEvent {
  return { kind: "compaction_done", compaction: { trigger: "pressure", pass, outcome: "ok", messages, summary } } as unknown as WireEvent;
}

// ── Part A: reducer — 多轮会话单卡状态机 ─────────────────────────────────────

{
  process.stdout.write("reducer: multi-pass session stays one pending card\n");
  let s: State = initialState;

  // Pass 1 开启会话。
  s = reducer(s, { type: "event", e: started(1) });
  eq(lastCompaction(s).pending, true, "pass1 started: pending");
  eq(lastCompaction(s).pass, 1, "pass1 started: pass number on card");

  // Pass 2 的 started 在 pass 1 的 done 之前到达（轮次交替）：复用同一张卡。
  s = reducer(s, { type: "event", e: started(2) });
  eq(compactionCount(s), 1, "pass2 started: still ONE card (no stacking)");
  eq(lastCompaction(s).pending, true, "pass2 started: card pending");
  eq(lastCompaction(s).pass, 2, "pass2 started: card carries pass 2");

  // Pass 1 的 ok done 到达（晚于 pass 2 started）：不结算——卡保持 pending。
  s = reducer(s, { type: "event", e: done(1, "- interim: round one summary", 9) });
  eq(lastCompaction(s).pending, true, "pass1 done: card STILL pending (session not over)");
  eq(lastCompaction(s).summary, "", "pass1 done: no terminal summary surfaced yet");
  ok(!s.items.some((it) => it.kind === "compaction" && !it.pending && !it.failed), "pass1 done: no terminal card appears while a pass still runs");

  // Pass 2 的 ok done 到达：会话结束，终态卡出现。
  s = reducer(s, { type: "event", e: done(2, "- final: round two summary", 5) });
  const terminal = lastCompaction(s);
  eq(terminal.pending, false, "pass2 done: terminal card resolves");
  eq(terminal.summary, "- final: round two summary", "pass2 done: final summary wins");
  eq(terminal.pass, 2, "terminal card keeps the total pass count");
  eq(compactionCount(s), 1, "terminal: still one card for the whole session");
}

// ── Part B: done 先于下一轮 started 到达（顺序交错） ─────────────────────────

{
  process.stdout.write("reducer: done-before-next-started ordering\n");
  let s: State = initialState;

  s = reducer(s, { type: "event", e: started(1) });
  // Pass 1 done 正常结算（后端只在会话结束时才可能这样发——但乱序/回放场景
  // 也可能出现 pass 1 done 晚于 pass 2 started：上一个 block 已覆盖。这里
  // 覆盖的是 pass 1 done 先到、pass 2 started 后到）。
  s = reducer(s, { type: "event", e: done(1, "- interim", 9) });
  eq(lastCompaction(s).pending, false, "pass1 done first: resolves (no later pass known yet)");
  eq(lastCompaction(s).summary, "- interim", "pass1 done first: summary installed");

  // Pass 2 的 started 复用终态卡回到 pending（新会话 pass 1 语义）。
  s = reducer(s, { type: "event", e: started(2) });
  eq(compactionCount(s), 1, "pass2 started: reuses the terminal card, no stack");
  eq(lastCompaction(s).pending, true, "pass2 started: card pending again");
  s = reducer(s, { type: "event", e: done(2, "- final", 4) });
  eq(lastCompaction(s).pending, false, "pass2 done: terminal again");
  eq(lastCompaction(s).summary, "- final", "pass2 done: new summary installed");
}

// ── Part C: legacy 单轮（无 pass 号）零回归 ──────────────────────────────────

{
  process.stdout.write("reducer: legacy single-pass keeps old behavior\n");
  let s: State = initialState;

  s = reducer(s, { type: "event", e: started() });
  const startedCard = lastCompaction(s);
  eq(startedCard.pending, true, "legacy started: pending");
  eq(startedCard.pass, 1, "legacy started: defaults to pass 1");

  s = reducer(s, {
    type: "event",
    e: { kind: "compaction_done", compaction: { trigger: "manual", messages: 7, summary: "- goal: ok" } } as unknown as WireEvent,
  });
  const card = lastCompaction(s);
  eq(card.pending, false, "legacy done: resolves immediately");
  eq(card.summary, "- goal: ok", "legacy done: summary installed");
  eq(card.pass, undefined, "legacy done: no pass suffix on the terminal card");

  // Abort（空 summary 的 done）仍删卡。
  s = reducer(s, { type: "event", e: started() });
  s = reducer(s, { type: "event", e: { kind: "compaction_done", compaction: { trigger: "manual" } } as unknown as WireEvent });
  eq(compactionCount(s), 0, "legacy abort: pending card removed");
}

// ── Part D: 失败轮保持会话，retry 复用同一卡（任务 867 不回归） ──────────────

{
  process.stdout.write("reducer: failed pass keeps the session, retry reuses the card\n");
  let s: State = initialState;

  s = reducer(s, { type: "event", e: started(1) });
  s = reducer(s, {
    type: "event",
    e: { kind: "compaction_done", compaction: { trigger: "pressure", pass: 1, outcome: "failed", attempt: 1, reason: "x" } } as unknown as WireEvent,
  });
  const failedCard = lastCompaction(s);
  eq(failedCard.pending, false, "failed: card resolved");
  eq(failedCard.failed, true, "failed: failed flag set");
  eq(failedCard.pass, 1, "failed: keeps its pass number");

  // 同一会话的 retry（pass 2）复用失败卡回到 pending。
  s = reducer(s, { type: "event", e: started(2) });
  eq(compactionCount(s), 1, "retry: no stacked card");
  const retried = lastCompaction(s);
  eq(retried.pending, true, "retry: pending again");
  eq(retried.failed, true, "retry: attempt counter kept (867 semantics)");
  eq(retried.attempt, 1, "retry: attempt value kept");
  eq(retried.pass, 2, "retry: card now shows pass 2");

  // Retry 成功后会话结束，failed 标记清除。
  s = reducer(s, { type: "event", e: done(2, "- recovered", 3) });
  const ok2 = lastCompaction(s);
  eq(ok2.pending, false, "retry ok: terminal");
  eq(ok2.failed, undefined, "retry ok: failed flag cleared");
  eq(ok2.summary, "- recovered", "retry ok: summary installed");
}

// ── Part E: 组件渲染——轮次可见 ───────────────────────────────────────────────

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
  return dom;
}

const dom = installDom();
const container = dom.window.document.getElementById("root") as HTMLElement;
let root: Root | null = null;

function renderHeader(compactionItem: Item) {
  const segment = {
    key: "seg-1",
    processItems: [compactionItem],
    outsideItems: [],
    displayItems: [compactionItem],
    hasOutsideContent: false,
    foldActive: false,
    hasRunningWork: compactionItem.kind === "compaction" ? compactionItem.pending : false,
    durationMs: 0,
    labelStyle: "full",
    turnItems: [],
  } as unknown as Parameters<typeof ProcessFoldHeader>[0]["segment"];
  const header = React.createElement(ProcessFoldHeader, {
    segment,
    open: false,
    onToggle: () => {},
  });
  root!.render(React.createElement(LocaleProvider, null, header));
}

function headerLabel(): string {
  const el = container.querySelector("[data-creation-label]");
  return el ? el.getAttribute("data-creation-label") ?? "" : "";
}

function renderCard(compactionItem: Item) {
  root!.render(React.createElement(LocaleProvider, null, React.createElement(CompactionCard, { item: compactionItem as CompactionItem })));
}

try {
  process.stdout.write("component: round visibility on card and fold header\n");
  root = createRoot(container);

  // Pending 第 2 轮：标签带「第 2 轮」（en locale：(round 2)）。
  const pendingRound2: Item = { kind: "compaction", id: "c1", pending: true, trigger: "pressure", messages: 0, summary: "", archive: "", pass: 2 };
  act(() => renderHeader(pendingRound2));
  const headerPending = headerLabel();
  ok(/第 2 轮|\(round 2\)/.test(headerPending), `pending round 2 header shows the round: ${JSON.stringify(headerPending)}`);

  act(() => renderCard(pendingRound2));
  const cardPending = container.textContent ?? "";
  ok(/第 2 轮|\(round 2\)/.test(cardPending), `pending round 2 card shows the round: ${JSON.stringify(cardPending)}`);

  // 终态卡（多轮）：带「共 2 轮」（en locale：2 rounds）。
  const terminal: Item = { kind: "compaction", id: "c1", pending: false, trigger: "pressure", messages: 5, summary: "- final", archive: "", pass: 2 };
  act(() => renderHeader(terminal));
  const headerTerminal = headerLabel();
  ok(/共 2 轮|2 rounds/.test(headerTerminal), `terminal header shows total rounds: ${JSON.stringify(headerTerminal)}`);
  ok(headerTerminal.includes("已压缩") || headerTerminal.toLowerCase().includes("compacted"), `terminal header keeps the compacted title: ${JSON.stringify(headerTerminal)}`);

  act(() => renderCard(terminal));
  const cardTerminal = container.textContent ?? "";
  ok(/共 2 轮|2 rounds/.test(cardTerminal), `terminal card shows total rounds: ${JSON.stringify(cardTerminal)}`);

  // 单轮终态卡：无轮次后缀（零回归）。
  const single: Item = { kind: "compaction", id: "c2", pending: false, trigger: "manual", messages: 7, summary: "- ok", archive: "" };
  act(() => renderCard(single));
  const cardSingle = container.textContent ?? "";
  ok(!cardSingle.includes("轮") && !cardSingle.includes("round"), `single-pass card has no round suffix: ${JSON.stringify(cardSingle)}`);

  await act(async () => {
    root?.unmount();
    root = null;
  });
} catch (err) {
  ok(false, `component test threw: ${err}`);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
