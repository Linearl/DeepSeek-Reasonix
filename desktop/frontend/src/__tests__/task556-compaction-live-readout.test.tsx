// Run: tsx src/__tests__/task556-compaction-live-readout.test.tsx
//
// 任务 556 验收（前端半）：压缩进行中的实时 token/吞吐读数。
//  1) reducer 三段：compaction_started → compaction_progress（读数/N/M 持续
//     更新）→ compaction_done（最终卡片整体替换，无读数残值）；
//  2) 中止（空 summary 的 done）删卡，无残值；
//  3) 迟到的 progress（卡已结算）被丢弃，不复活旧读数；
//  4) ProcessFoldHeader 渲染：pending + tokens → run-strip 同款读数；无
//     tokens / 非 pending → 无读数（关闭态零行为）。

import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { LocaleProvider } from "../lib/i18n";
import { ProcessFoldHeader } from "../components/ProcessFoldHeader";
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

// ── Part A: reducer ───────────────────────────────────────────────────────────

function progressEvent(c: { done?: number; total?: number; tokens?: number; tokensPerSec?: number }): WireEvent {
  return { kind: "compaction_progress", compaction: c } as unknown as WireEvent;
}

function pendingCard(s: State): Extract<Item, { kind: "compaction" }> {
  const found = [...s.items].reverse().find((it): it is Extract<Item, { kind: "compaction" }> => it.kind === "compaction");
  if (!found) throw new Error("no compaction item in state");
  return found;
}

{
  process.stdout.write("reducer: live compaction readout lifecycle\n");
  let s: State = initialState;

  s = reducer(s, { type: "event", e: { kind: "compaction_started", compaction: { trigger: "auto" } } as unknown as WireEvent });
  const started = pendingCard(s);
  eq(started.pending, true, "started: card is pending");
  eq(started.tokens, undefined, "started: no readout yet");

  s = reducer(s, { type: "event", e: progressEvent({ done: 1, total: 4, tokens: 300, tokensPerSec: 0 }) });
  let card = pendingCard(s);
  eq(card.pending, true, "progress: card still pending");
  eq(card.done, 1, "progress: done advanced");
  eq(card.total, 4, "progress: total set");
  eq(card.tokens, 300, "progress: tokens visible");
  eq(card.tokensPerSec, 0, "progress: tps hidden before the noise floor");

  // 读数随时间变化：下一个事件 tokens 增长、tps 出现。
  s = reducer(s, { type: "event", e: progressEvent({ done: 2, total: 4, tokens: 12300, tokensPerSec: 45 }) });
  card = pendingCard(s);
  eq(card.tokens, 12300, "progress: tokens grow");
  eq(card.tokensPerSec, 45, "progress: tps appears");
  eq(card.done, 2, "progress: done grows");

  // 完成卡片整体替换：读数被最终结果替代，不留残值（验收 ③）。
  s = reducer(s, { type: "event", e: { kind: "compaction_done", compaction: { trigger: "auto", messages: 7, summary: "- goal: ok" } } as unknown as WireEvent });
  card = pendingCard(s);
  eq(card.pending, false, "done: card resolved");
  eq(card.tokens, undefined, "done: no stale token estimate");
  eq(card.tokensPerSec, undefined, "done: no stale tps");
  eq(card.summary, "- goal: ok", "done: summary installed");

  // 中止：空 summary 的 done 删卡。
  s = reducer(s, { type: "event", e: { kind: "compaction_started", compaction: { trigger: "manual" } } as unknown as WireEvent });
  s = reducer(s, { type: "event", e: progressEvent({ tokens: 50, tokensPerSec: 10 }) });
  s = reducer(s, { type: "event", e: { kind: "compaction_done", compaction: { trigger: "manual" } } as unknown as WireEvent });
  eq(s.items.some((it) => it.kind === "compaction" && it.pending), false, "abort: pending card removed");

  // 迟到的 progress：无 pending 卡时整体状态不变（防脏值复活）。
  const frozen = s;
  s = reducer(s, { type: "event", e: progressEvent({ tokens: 99, tokensPerSec: 9 }) });
  eq(s === frozen, true, "late progress with no pending card is a no-op");
}

// ── Part B: ProcessFoldHeader rendering ───────────────────────────────────────

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

try {
  process.stdout.write("component: ProcessFoldHeader live readout\n");
  root = createRoot(container);

  // pending + tokens + tps → run-strip 同款读数。
  const live: Item = { kind: "compaction", id: "c1", pending: true, trigger: "auto", messages: 0, summary: "", archive: "", done: 2, total: 4, tokens: 12300, tokensPerSec: 45 };
  act(() => renderHeader(live));
  let label = headerLabel();
  ok(label.includes("2/4"), `pending label keeps the N/M progress: ${JSON.stringify(label)}`);
  ok(label.includes("12.3K tokens"), `readout shows formatted tokens: ${JSON.stringify(label)}`);
  ok(label.includes("45 tokens/s"), `readout shows tps: ${JSON.stringify(label)}`);

  // tokens=0（后端 500ms 噪声底前）→ 只显示基础标签，无读数。
  const noTokens: Item = { kind: "compaction", id: "c1", pending: true, trigger: "auto", messages: 0, summary: "", archive: "" };
  act(() => renderHeader(noTokens));
  label = headerLabel();
  ok(!label.includes("2/4") && !label.includes("tokens"), `no readout before tokens arrive: ${JSON.stringify(label)}`);

  // 已完成卡 → 标题态，无读数（关闭态零行为）。
  const done: Item = { kind: "compaction", id: "c1", pending: false, trigger: "auto", messages: 7, summary: "- ok", archive: "", tokens: 12300, tokensPerSec: 45 };
  act(() => renderHeader(done));
  label = headerLabel();
  ok(!label.includes("tokens/s") && !label.includes("12.3K"), `resolved card shows no readout: ${JSON.stringify(label)}`);

  await act(async () => {
    root?.unmount();
    root = null;
  });
} catch (err) {
  ok(false, `component test threw: ${err}`);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
