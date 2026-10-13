// Run: tsx src/__tests__/task867-compaction-failed-outcome.test.tsx
//
// 任务 867 验收（前端半）：压缩失败不再静默 + 三态显性化。
//  1) outcome="failed" 的 compaction_done 不删卡：失败卡带 attempt/reason；
//  2) 历史 aborted（空 summary 且无 outcome）仍删卡（向后兼容）；
//  3) 失败后的 compaction_started 复用失败卡回到 pending（不堆叠）；
//  4) 成功 done 整体替换卡片，failed/attempt/reason 清除；
//  5) ProcessFoldHeader：失败卡渲染「第 N 次」标签。

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

type CompactionCard = Extract<Item, { kind: "compaction" }>;

function lastCompaction(s: State): CompactionCard {
  const found = [...s.items].reverse().find((it): it is CompactionCard => it.kind === "compaction");
  if (!found) throw new Error("no compaction item in state");
  return found;
}

function started(trigger = "auto"): WireEvent {
  return { kind: "compaction_started", compaction: { trigger } } as unknown as WireEvent;
}

// ── Part A: reducer ───────────────────────────────────────────────────────────

{
  process.stdout.write("reducer: failed outcome stays visible\n");
  let s: State = initialState;
  s = reducer(s, { type: "event", e: started() });
  s = reducer(s, {
    type: "event",
    e: { kind: "compaction_done", compaction: { trigger: "auto", outcome: "failed", attempt: 1, reason: "context changed during compaction; the pass was discarded and will retry" } } as unknown as WireEvent,
  });
  const card = lastCompaction(s);
  eq(card.pending, false, "failed: card resolved");
  eq(card.failed, true, "failed: card marked failed");
  eq(card.attempt, 1, "failed: attempt recorded");
  ok((card.reason ?? "").includes("discarded"), "failed: reason carried");
  eq(s.items.filter((it) => it.kind === "compaction").length, 1, "failed: card not removed");
}

{
  process.stdout.write("reducer: legacy abort still drops the card\n");
  let s: State = initialState;
  s = reducer(s, { type: "event", e: started("manual") });
  s = reducer(s, { type: "event", e: { kind: "compaction_done", compaction: { trigger: "manual" } } as unknown as WireEvent });
  eq(s.items.some((it) => it.kind === "compaction"), false, "abort: card removed as before");
}

{
  process.stdout.write("reducer: retry reuses the failed card\n");
  let s: State = initialState;
  s = reducer(s, { type: "event", e: started() });
  s = reducer(s, {
    type: "event",
    e: { kind: "compaction_done", compaction: { trigger: "auto", outcome: "failed", attempt: 2, reason: "x" } } as unknown as WireEvent,
  });
  const failedId = lastCompaction(s).id;
  s = reducer(s, { type: "event", e: started() });
  const card = lastCompaction(s);
  eq(s.items.filter((it) => it.kind === "compaction").length, 1, "retry: no stacked card");
  eq(card.id, failedId, "retry: same card id");
  eq(card.pending, true, "retry: card pending again");
  eq(card.failed, false, "retry: failed flag cleared");
  eq(card.attempt, undefined, "retry: stale attempt cleared");
}

{
  process.stdout.write("reducer: success replaces the card wholesale\n");
  let s: State = initialState;
  s = reducer(s, { type: "event", e: started() });
  s = reducer(s, {
    type: "event",
    e: { kind: "compaction_done", compaction: { trigger: "auto", outcome: "failed", attempt: 1, reason: "x" } } as unknown as WireEvent,
  });
  s = reducer(s, { type: "event", e: started() });
  s = reducer(s, {
    type: "event",
    e: { kind: "compaction_done", compaction: { trigger: "auto", outcome: "ok", messages: 5, summary: "- goal: ok" } } as unknown as WireEvent,
  });
  const card = lastCompaction(s);
  eq(card.pending, false, "success: resolved");
  eq(card.failed, undefined, "success: failed flag absent");
  eq(card.attempt, undefined, "success: attempt absent");
  eq(card.reason, undefined, "success: reason absent");
  eq(card.summary, "- goal: ok", "success: summary installed");
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
  const el = container.querySelector(".turn-collapse__label");
  return el ? (el.textContent ?? "") : "";
}

root = createRoot(container);
try {
  process.stdout.write("render: failed card label names the attempt\n");
  act(() => {
    renderHeader({ kind: "compaction", id: "c1", pending: false, failed: true, attempt: 3, reason: "boom", trigger: "auto", messages: 0, summary: "", archive: "" });
  });
  const label = headerLabel();
  ok(label.includes("第 3 次") || label.includes("attempt 3"), `failed label carries the attempt counter (got ${JSON.stringify(label)})`);
  ok(!label.includes("已压缩") && !label.includes("compacted"), "failed label is not the success title");

  act(() => {
    renderHeader({ kind: "compaction", id: "c2", pending: false, trigger: "auto", messages: 4, summary: "- ok", archive: "" });
  });
  ok(headerLabel().length > 0, "success card still renders the compacted title");
} finally {
  act(() => {
    root!.unmount();
  });
  dom.window.close();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
