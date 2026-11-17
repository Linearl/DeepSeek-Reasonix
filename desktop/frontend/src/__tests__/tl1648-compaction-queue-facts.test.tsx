// Run: tsx src/__tests__/tl1648-compaction-queue-facts.test.tsx
//
// tl1648 验收（前端半）：压缩期间在途输入处理的面四——
//  1) 成功压缩留痕：终局卡不再被后续 pass 复用改写（20261116 现场
//     13:07/13:35/14:04 三次成功全被 14:45 失败卡覆盖的实证形态）；
//  2) 14:50 形态回归钉：失败 → 下次触发成功后同卡迁移（867 重试语义保留）；
//  3) 失败卡事实：foldInstalled=false（读数不降明示）+ lastSuccessAt
//     （上次成功压缩时间）；lastSuccessAt=0 不渲染；
//  4) 存档重建：archived failed Done 重建为失败卡而非空摘要伪成功卡；
//  5) 渲染：失败卡事实行可见。

import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { LocaleProvider } from "../lib/i18n";
import { CompactionCard } from "../components/TranscriptCards";
import { historyMessagesToItems, initialState, reducer, type Item, type State } from "../lib/useController";
import type { HistoryMessage, WireEvent } from "../lib/types";

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

type CompactionCardItem = Extract<Item, { kind: "compaction" }>;

function compactionCards(s: State): CompactionCardItem[] {
  return s.items.filter((it): it is CompactionCardItem => it.kind === "compaction");
}

function started(pass?: number): WireEvent {
  return { kind: "compaction_started", compaction: pass ? { trigger: "auto", pass } : { trigger: "auto" } } as unknown as WireEvent;
}

const STALE_REASON = "context changed during compaction; the pass was discarded and retries at the next context check";

// ── 1: 成功留痕 ───────────────────────────────────────────────────────────────

{
  process.stdout.write("reducer: terminal card survives the next pass (success keeps its trace)\n");
  let s: State = initialState;
  s = reducer(s, { type: "event", e: started(1) });
  s = reducer(s, { type: "event", e: { kind: "compaction_done", compaction: { trigger: "auto", outcome: "ok", pass: 1, messages: 5, summary: "- one" } } as unknown as WireEvent });
  eq(compactionCards(s).length, 1, "first success: one terminal card");
  const firstId = compactionCards(s)[0].id;

  s = reducer(s, { type: "event", e: started(2) });
  const cards = compactionCards(s);
  eq(cards.length, 2, "second pass opens a NEW card instead of rewriting the terminal one");
  const terminal = cards.find((c) => c.id === firstId);
  ok(terminal && !terminal.pending && terminal.summary === "- one", "first success card stays resolved with its summary");
  const pending = cards.find((c) => c.id !== firstId);
  ok(pending && pending.pending, "second pass runs on its own pending card");

  s = reducer(s, { type: "event", e: { kind: "compaction_done", compaction: { trigger: "auto", outcome: "ok", pass: 2, messages: 7, summary: "- two" } } as unknown as WireEvent });
  const done = compactionCards(s);
  eq(done.length, 2, "two successful compactions leave two visible terminal cards");
  ok(done.every((c) => !c.pending), "both terminal");
  ok(done.some((c) => c.summary === "- one") && done.some((c) => c.summary === "- two"), "both summaries readable");
}

{
  process.stdout.write("reducer: multi-pass session still shares ONE pending card (1021 kept)\n");
  let s: State = initialState;
  s = reducer(s, { type: "event", e: started(1) });
  // Pass 1 ok but a later pass already started (session pass 2): interim path.
  s = reducer(s, { type: "event", e: started(2) });
  s = reducer(s, { type: "event", e: { kind: "compaction_done", compaction: { trigger: "auto", outcome: "ok", pass: 1, messages: 5, summary: "- interim" } } as unknown as WireEvent });
  const cards = compactionCards(s);
  eq(cards.length, 1, "interim ok inside a live session keeps the single card");
  eq(cards[0].pending, true, "card stays pending for the session's last pass");
}

// ── 2: 14:50 形态回归钉 ──────────────────────────────────────────────────────

{
  process.stdout.write("reducer: failed -> retry migrates the SAME card to terminal (867 kept)\n");
  let s: State = initialState;
  s = reducer(s, { type: "event", e: started(1) });
  s = reducer(s, {
    type: "event",
    e: { kind: "compaction_done", compaction: { trigger: "auto", outcome: "failed", pass: 1, attempt: 1, reason: STALE_REASON, foldInstalled: false, lastSuccessAt: 1700000000000 } } as unknown as WireEvent,
  });
  const failedCard = compactionCards(s)[0];
  eq(failedCard.failed, true, "failure lands on the card");
  eq(failedCard.foldInstalled, false, "failure card carries foldInstalled=false");
  eq(failedCard.lastSuccessAt, 1700000000000, "failure card carries lastSuccessAt");

  s = reducer(s, { type: "event", e: started(2) });
  const retrying = compactionCards(s)[0];
  eq(retrying.id, failedCard.id, "retry reuses the SAME card");
  eq(retrying.pending, true, "retry card pending");
  eq(retrying.foldInstalled, undefined, "stale fold fact cleared on restart");
  eq(retrying.lastSuccessAt, undefined, "stale success stamp cleared on restart");

  s = reducer(s, { type: "event", e: { kind: "compaction_done", compaction: { trigger: "auto", outcome: "ok", pass: 2, messages: 9, summary: "- recovered" } } as unknown as WireEvent });
  const settled = compactionCards(s)[0];
  eq(settled.id, failedCard.id, "state migrates correctly after the next trigger succeeds");
  eq(settled.pending, false, "card resolved");
  eq(settled.failed, undefined, "failed flag cleared");
  eq(settled.attempt, undefined, "attempt cleared");
  eq(settled.summary, "- recovered", "summary installed");
}

// ── 3: lastSuccessAt=0 不携带 ────────────────────────────────────────────────

{
  process.stdout.write("reducer: zero lastSuccessAt stays absent (unknown, not epoch)\n");
  let s: State = initialState;
  s = reducer(s, { type: "event", e: started() });
  s = reducer(s, {
    type: "event",
    e: { kind: "compaction_done", compaction: { trigger: "auto", outcome: "failed", attempt: 1, reason: "boom", foldInstalled: false, lastSuccessAt: 0 } } as unknown as WireEvent,
  });
  const card = compactionCards(s)[0];
  eq(card.lastSuccessAt, undefined, "zero stamp maps to absent");
  eq(card.foldInstalled, false, "fold fact carried");
}

// ── 4: 存档重建 ──────────────────────────────────────────────────────────────

{
  process.stdout.write("history: archived failed Done rebuilds as the failed card\n");
  const messages: HistoryMessage[] = [
    { role: "user", content: "go" },
    {
      role: "compaction",
      content: "",
      failed: true,
      attempt: 1,
      reason: STALE_REASON,
      foldInstalled: false,
      lastSuccessAt: 1700000000000,
      trigger: "auto",
    },
    { role: "compaction", content: "", trigger: "auto", messages: 4, summary: "- archived ok" },
  ];
  const { items } = historyMessagesToItems(messages, "h", 0);
  const cards = items.filter((it): it is CompactionCardItem => it.kind === "compaction");
  eq(cards.length, 2, "both archived compaction rows rebuild");
  eq(cards[0].failed, true, "failed row rebuilds as failed card");
  eq(cards[0].summary, "", "failed card carries no pseudo summary");
  eq(cards[0].foldInstalled, false, "fold fact survives the archive round-trip");
  eq(cards[0].lastSuccessAt, 1700000000000, "success stamp survives the archive round-trip");
  eq(cards[1].failed, undefined, "legacy terminal row keeps the terminal shape");
  eq(cards[1].summary, "- archived ok", "terminal summary intact");
}

// ── 5: 渲染 ──────────────────────────────────────────────────────────────────

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

function renderCard(item: Item) {
  root!.render(React.createElement(LocaleProvider, null, React.createElement(CompactionCard, { item })));
}

function cardText(): string {
  return container.querySelector(".compaction--failed")?.textContent ?? "";
}

root = createRoot(container);
try {
  process.stdout.write("render: failed card shows the no-fold fact and the last-success anchor\n");
  act(() => {
    renderCard({ kind: "compaction", id: "c1", pending: false, failed: true, attempt: 1, reason: STALE_REASON, foldInstalled: false, lastSuccessAt: Date.now() - 46 * 60 * 1000, trigger: "auto", messages: 0, summary: "", archive: "" });
  });
  const text = cardText();
  ok(text.includes("nothing was installed this pass"), `no-fold line rendered (got ${JSON.stringify(text)})`);
  ok(text.includes("last successful compaction"), "last-success line rendered");
  ok(text.includes("retries at the next context check"), "retry promise names the next context check");
  ok(!text.includes("retries automatically later") && !text.includes("稍后自动重试"), "false 'later' promise removed");

  process.stdout.write("render: failed card without facts keeps the 867 face\n");
  act(() => {
    renderCard({ kind: "compaction", id: "c2", pending: false, failed: true, attempt: 2, reason: "boom", trigger: "auto", messages: 0, summary: "", archive: "" });
  });
  const plain = cardText();
  ok(!plain.includes("nothing was installed this pass"), "no no-fold line without the fact");
  ok(!plain.includes("last successful compaction"), "no last-success line without a stamp");
  ok(plain.includes("attempt 2") || plain.includes("第 2 次"), "attempt line unchanged");
} finally {
  act(() => {
    root!.unmount();
  });
  dom.window.close();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
