// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1588-compaction-episode-convergence.test.tsx
//
// tl1588 验收（压缩状态显示收敛）：
//  ① 压缩进行中只显示一行进度+一行失败原因（如有）——pending 卡头行/失败卡
//     reason 行保持既有形态（973/867 语义零回退）；
//  ② 内部状态（预填充/中断/续轮/裁剪/恢复提示/压缩等待/流中断）在压缩回合
//     兼容窗内不再逐条落 notice 行，沉淀为压缩卡 episodeNotes 详情区；
//  ③ 兜底动作（裁剪/续轮/中断）在人话 rescue 行说明一条（最近兜底胜出）；
//  ④ rescue 行与详情标题三语齐备。
// 兼容窗 = compactionSessionOpen && 存在压缩卡；窗口外一切行为逐字不变。

import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { LocaleProvider } from "../lib/i18n";
import { CompactionCard } from "../components/TranscriptCards";
import { initialState, reducer, type Item, type State } from "../lib/useController";
import { en } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";
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

function noticeCount(s: State): number {
  return s.items.filter((it) => it.kind === "notice" && !("variant" in it && it.variant)).length;
}

// 开一个压缩回合：started 事件落一张 pending 卡（compactionSessionOpen=true）。
function openEpisode(s: State): State {
  return reducer(s, { type: "event", e: { kind: "compaction_started", compaction: { trigger: "auto", pass: 1 } } as unknown as WireEvent });
}

// ── Part A: reducer 分流——窗口内沉淀，窗口外逐字不变 ─────────────────────────

{
  process.stdout.write("reducer: episode notices sink into the card while the window is open\n");
  let s: State = openEpisode(initialState);
  eq(s.compactionSessionOpen, true, "started: episode window opens");
  const beforeNotices = noticeCount(s);
  // 预填充（929）——内部状态，窗口内不落行。
  s = reducer(s, { type: "event", e: { kind: "notice", level: "info", code: "prefill_slow", text: "模型正在处理超大上下文" } as unknown as WireEvent });
  eq(noticeCount(s), beforeNotices, "prefill_slow inside the window appends NO notice row");
  eq(lastCompaction(s).episodeNotes?.length, 1, "prefill_slow lands as an episode note");
  // 续轮（731）——rescue=resume。
  s = reducer(s, { type: "event", e: { kind: "notice", level: "info", code: "autopilot_resume_fired", text: "无人值守自动续轮" } as unknown as WireEvent });
  eq(noticeCount(s), beforeNotices, "resume notice inside the window appends NO notice row");
  eq(lastCompaction(s).episodeNotes?.length, 2, "resume notice lands as the second note");
  eq(lastCompaction(s).episodeNotes?.[1].rescue, "resume", "resume note carries the resume rescue kind");
  // 恢复提示（1011）——rescue=trim。
  s = reducer(s, { type: "event", e: { kind: "notice", level: "warn", code: "session_compact_fail_streak", text: "压缩连续失败", detail: "streak=3" } as unknown as WireEvent });
  eq(lastCompaction(s).episodeNotes?.[2].rescue, "trim", "streak note carries the trim rescue kind");
  eq(lastCompaction(s).episodeNotes?.[2].detail, "streak=3", "streak note keeps the technical detail");
  // 需要用户行动的告警不吃收敛——照旧落行。
  const withWarn = noticeCount(s);
  s = reducer(s, { type: "event", e: { kind: "notice", level: "warn", code: "provider_rejection", text: "供应商拒绝" } as unknown as WireEvent });
  eq(noticeCount(s), withWarn + 1, "actionable warnings keep their own row (sink eats status churn only)");
}

{
  process.stdout.write("reducer: outside the episode window every notice keeps its row\n");
  let s: State = initialState;
  const before = noticeCount(s);
  s = reducer(s, { type: "event", e: { kind: "notice", level: "info", code: "prefill_slow", text: "模型正在处理超大上下文" } as unknown as WireEvent });
  eq(noticeCount(s), before + 1, "prefill_slow outside the window still renders its row (zero regression)");
  eq(s.items.some((it) => it.kind === "compaction"), false, "no compaction card exists outside the window");

  // 回合间的主动维护（压缩会话已关）同样逐字不变。
  s = reducer(s, { type: "event", e: { kind: "compaction_started", compaction: { trigger: "auto", pass: 1 } } as unknown as WireEvent });
  s = reducer(s, { type: "event", e: { kind: "compaction_done", compaction: { trigger: "auto", pass: 1, outcome: "ok", messages: 5, summary: "- ok" } } as unknown as WireEvent });
  eq(s.compactionSessionOpen, false, "ok done closes the episode window");
  const mid = noticeCount(s);
  s = reducer(s, { type: "event", e: { kind: "notice", level: "info", code: "autopilot_resume_fired", text: "无人值守自动续轮" } as unknown as WireEvent });
  eq(noticeCount(s), mid + 1, "resume notice after the window closes still renders its row");
}

{
  process.stdout.write("reducer: context_maintenance receipts sink inside the window\n");
  let s: State = openEpisode(initialState);
  const before = noticeCount(s);
  s = reducer(s, { type: "event", e: { kind: "context_maintenance", maintenance: { status: "blocked", operationId: "op-1" } } as unknown as WireEvent });
  eq(noticeCount(s), before, "blocked maintenance receipt inside the window appends NO notice row");
  eq(lastCompaction(s).episodeNotes?.length, 1, "blocked receipt lands as a note (摘要未形成短视图)");
  eq(s.seenMaintenanceOps.includes("op-1"), true, "operationId dedupe still records while diverting");
  // 裁剪兜底（applied+truncate）→ rescue=trim。
  s = reducer(s, { type: "event", e: { kind: "context_maintenance", maintenance: { status: "applied", action: "truncate", operationId: "op-2", inputTokens: 1000, resultTokens: 400 } } as unknown as WireEvent });
  eq(lastCompaction(s).episodeNotes?.[1].rescue, "trim", "applied+truncate carries the trim rescue kind");
  // 摘要类 applied（非 truncate）不占 rescue 行。
  s = reducer(s, { type: "event", e: { kind: "context_maintenance", maintenance: { status: "applied", action: "summary", operationId: "op-3" } } as unknown as WireEvent });
  eq(lastCompaction(s).episodeNotes?.[2].rescue, undefined, "plain summary receipt is a detail-only note");
  // 同一 operationId 重放不重复沉淀。
  const notesBefore = lastCompaction(s).episodeNotes?.length;
  s = reducer(s, { type: "event", e: { kind: "context_maintenance", maintenance: { status: "blocked", operationId: "op-1" } } as unknown as WireEvent });
  eq(lastCompaction(s).episodeNotes?.length, notesBefore, "replayed operationId stays a no-op");
}

{
  process.stdout.write("reducer: turn_done interrupted family sinks inside the window\n");
  let s: State = openEpisode(initialState);
  const before = noticeCount(s);
  s = reducer(s, {
    type: "event",
    e: { kind: "turn_done", status: "interrupted", err: "context length exceeded", detail: "provider cut the reply", diagnostic: { kind: "error" } } as unknown as WireEvent,
  });
  const addedNotices = noticeCount(s) - before;
  eq(addedNotices <= 0, true, `interrupted turn_done inside the window adds no new notice rows (added ${addedNotices})`);
  const notes = lastCompaction(s).episodeNotes ?? [];
  ok(notes.length >= 1, "interrupted turn_done lands at least the interrupt note");
  eq(notes[0].rescue, "interrupt", "the interrupt note carries the interrupt rescue kind");
  ok(notes.some((note) => note.label.includes("context length exceeded")), "provider error stays visible inside the details");
}

{
  process.stdout.write("reducer: episode notes survive resolve/fail and flush on abort\n");
  // ok done → 终局卡继承详情区（压缩结束后仍可展开查证）。
  let s = openEpisode(initialState);
  s = reducer(s, { type: "event", e: { kind: "notice", level: "info", code: "prefill_slow", text: "预填充" } as unknown as WireEvent });
  s = reducer(s, { type: "event", e: { kind: "compaction_done", compaction: { trigger: "auto", pass: 1, outcome: "ok", messages: 3, summary: "- done" } } as unknown as WireEvent });
  eq(lastCompaction(s).pending, false, "ok done resolves the card");
  eq((lastCompaction(s).episodeNotes ?? []).length, 1, "terminal card inherits the episode details");

  // failed done → 失败卡继承详情区（重试同一卡，1021/867 语义零回退）。
  let f = openEpisode(initialState);
  f = reducer(f, { type: "event", e: { kind: "notice", level: "info", code: "prefill_slow", text: "预填充" } as unknown as WireEvent });
  f = reducer(f, { type: "event", e: { kind: "compaction_done", compaction: { trigger: "auto", pass: 1, outcome: "failed", attempt: 2, reason: "summarizer request failed" } } as unknown as WireEvent });
  const failedCard = lastCompaction(f);
  eq(failedCard.failed, true, "failed outcome keeps the failed card");
  eq((failedCard.episodeNotes ?? []).length, 1, "failed card inherits the episode details");
  eq(failedCard.reason, "summarizer request failed", "failure reason line unchanged (867/tl1446-6)");

  // aborted（无摘要非失败落卡）→ 沉淀回灌一条合并 notice，内容不丢。
  let a = openEpisode(initialState);
  a = reducer(a, { type: "event", e: { kind: "notice", level: "info", code: "prefill_slow", text: "预填充" } as unknown as WireEvent });
  const notesCount = (lastCompaction(a).episodeNotes ?? []).length;
  ok(notesCount > 0, "abort fixture has notes to preserve");
  const preAbortNotices = noticeCount(a);
  a = reducer(a, { type: "event", e: { kind: "compaction_done", compaction: { trigger: "auto" } } as unknown as WireEvent });
  eq(a.items.some((it) => it.kind === "compaction"), false, "abort drops the card (1021 legacy behavior)");
  eq(noticeCount(a), preAbortNotices + 1, "abort flushes ONE consolidated notice instead of losing the notes");
}

// ── Part B: 有界 FIFO ────────────────────────────────────────────────────────

{
  process.stdout.write("reducer: episode notes bound to 12 (FIFO)\n");
  let s: State = openEpisode(initialState);
  for (let i = 0; i < 15; i += 1) {
    s = reducer(s, { type: "event", e: { kind: "notice", level: "info", code: "prefill_slow", text: `note ${i}` } as unknown as WireEvent });
  }
  eq((lastCompaction(s).episodeNotes ?? []).length, 12, "13+ notes cap at 12");
  eq(lastCompaction(s).episodeNotes?.[0].label.includes("note 3"), true, "oldest notes leave first (FIFO head is note 3)");
}

// ── Part C: 组件渲染 ─────────────────────────────────────────────────────────

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

function renderCard(item: CompactionItem) {
  root!.render(React.createElement(LocaleProvider, null, React.createElement(CompactionCard, { item })));
}

try {
  process.stdout.write("component: episode block renders on the pending card\n");
  root = createRoot(container);
  const pendingWithNotes: CompactionItem = {
    kind: "compaction", id: "c1", pending: true, trigger: "auto", messages: 0, summary: "", archive: "",
    startedAt: Date.now() - 40_000,
    episodeNotes: [
      { at: Date.now() - 30_000, label: "模型正在处理超大上下文（约 1048570 tokens）", rescue: undefined },
      { at: Date.now() - 20_000, label: "无人值守自动续轮：上一回合异常终止", rescue: "resume" },
    ],
  };
  act(() => renderCard(pendingWithNotes));
  let text = container.textContent ?? "";
  // JSDOM navigator=en-US → 组件断言用 en 串；三语键面在 Part D 独立钉。
  ok(text.includes("Compaction details"), `details summary renders: ${JSON.stringify(text.slice(0, 160))}`);
  ok(text.includes("2"), "details summary names the note count");
  ok(text.includes("The previous turn ended unexpectedly"), "rescue line renders the resume fallback in plain language");
  ok(text.includes("模型正在处理超大上下文"), "note labels stay visible inside the details");
  const summaryNode = container.querySelector("summary");
  ok(summaryNode !== null, "details is a <details>/<summary> expander (collapsed by default)");

  // 无沉淀 → 卡面无新增 DOM（973 精确串钉零回退）。
  act(() => renderCard({ kind: "compaction", id: "c2", pending: true, trigger: "auto", messages: 0, summary: "", archive: "", startedAt: Date.now() - 1_000 }));
  text = container.textContent ?? "";
  ok(!text.includes("Compaction details"), "no-note card renders no episode block (973 pin surface unchanged)");
  ok(container.querySelector(".compaction__rescue") === null, "no rescue element without notes");

  // 失败卡：一行失败原因 + rescue 行共存。
  act(() => renderCard({
    kind: "compaction", id: "c3", pending: false, failed: true, attempt: 2, reason: "summarizer output was truncated",
    trigger: "auto", messages: 0, summary: "", archive: "",
    episodeNotes: [{ at: Date.now() - 5_000, label: "已裁剪上下文视图", rescue: "trim" }],
  }));
  text = container.textContent ?? "";
  ok(text.includes("summarizer output was truncated"), "failed card keeps the localized reason line (acceptance ①)");
  ok(text.includes("Earlier context was trimmed automatically"), "trim rescue line renders on the failed card (acceptance ③)");

  // 终局卡继承详情区。
  act(() => renderCard({
    kind: "compaction", id: "c4", pending: false, trigger: "auto", messages: 7, summary: "- ok", archive: "",
    episodeNotes: [{ at: Date.now() - 9_000, label: "本轮已中断", rescue: "interrupt" }],
  }));
  text = container.textContent ?? "";
  ok(text.includes("This turn was interrupted"), "terminal card shows the interrupt rescue line");
  ok(text.includes("Compaction details"), "terminal card inherits the details expander");

  await act(async () => {
    root?.unmount();
    root = null;
  });
} catch (err) {
  ok(false, `component test threw: ${err}`);
}

// ── Part D: ④ 三语 + class-contract 源钉 ─────────────────────────────────────

{
  process.stdout.write("locales: rescue lines and details summary in en/zh/zh-TW\n");
  const keys = ["compaction.rescueTrim", "compaction.rescueResume", "compaction.rescueInterrupt", "compaction.episodeNotes"];
  ok(keys.every((key) => key in zh), "zh carries all four keys");
  ok(keys.every((key) => key in zhTW), "zh-TW carries all four keys");
  ok(keys.every((key) => key in en), "en carries all four keys");
  ok(zh["compaction.rescueTrim"] !== en["compaction.rescueTrim"], "zh and en rescue copy actually differ (no untranslated passthrough)");
  ok(String(zh["compaction.episodeNotes"]).includes("{n}"), "zh details summary interpolates the count");
  // 组件新类名必须落 CSS（class-contract 动态面：这里钉死契约）。
  const css = readFileSync(new URL("../styles.css", import.meta.url), "utf8");
  for (const cls of ["compaction--episode", "compaction__rescue", "compaction__notes", "compaction__notes-list", "compaction__note", "compaction__note-time", "compaction__note-label", "compaction__note-detail"]) {
    ok(css.includes(`.${cls}`), `styles.css defines .${cls}`);
  }
  const cards = readFileSync(new URL("../components/TranscriptCards.tsx", import.meta.url), "utf8");
  for (const cls of ["compaction--episode", "compaction__rescue", "compaction__notes", "compaction__note"]) {
    ok(cards.includes(cls), `CompactionCard references ${cls}`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
