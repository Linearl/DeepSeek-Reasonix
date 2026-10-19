// Run: tsx src/__tests__/task973-compaction-card-live-readout.test.tsx
//
// 任务 973 验收（前端半）：压缩期间的实时数字不再只活在折叠头——pending
// 压缩卡（用户视线落点的「正在压缩对话…」块）也带「已 Xs · XX tokens ·
// X t/s」读数，且 3s 内就有数字（elapsed 由 compaction_started 戳驱动，
// 先于首个 token 估计出现）。
//  1) reducer：compaction_started 打 startedAt 戳；progress 保留戳；
//     done 整体替换卡（无戳残值）；失败重试复用卡时戳刷新；
//  2) compactionLiveReadout 纯函数：elapsed/tokens/tps 三腿拼装与降级
//     （tps=0 隐藏、无戳无 tokens 返回空、非 pending 返回空、分钟格式）；
//  3) 组件：CompactionCard pending 渲染读数；resolved 卡无读数。

import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { LocaleProvider } from "../lib/i18n";
import { CompactionCard } from "../components/TranscriptCards";
import { compactionLiveReadout } from "../lib/compactionLive";
import { initialState, reducer, type Item, type State } from "../lib/useController";
import { en } from "../locales/en";
import type { WireEvent } from "../lib/types";
import type { Translator } from "../lib/i18n";

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

// ── Part A: reducer — startedAt 戳的完整生命周期 ─────────────────────────────

{
  process.stdout.write("reducer: compaction_started stamps startedAt\n");
  let s: State = initialState;
  const before = Date.now();
  s = reducer(s, { type: "event", e: { kind: "compaction_started", compaction: { trigger: "auto" } } as unknown as WireEvent });
  const card = lastCompaction(s);
  eq(card.pending, true, "started: card is pending");
  ok(typeof card.startedAt === "number" && card.startedAt >= before, "started: startedAt stamped at now");

  // progress 保留 startedAt（读数不因 token 事件丢失 elapsed 基准）。
  s = reducer(s, { type: "event", e: { kind: "compaction_progress", compaction: { tokens: 300, tokensPerSec: 12 } } as unknown as WireEvent });
  const progressed = lastCompaction(s);
  eq(progressed.tokens, 300, "progress: tokens visible");
  eq(progressed.startedAt, card.startedAt, "progress: startedAt preserved");

  // done 整体替换卡：resolved 卡不带戳（水合同款无残值语义）。
  s = reducer(s, { type: "event", e: { kind: "compaction_done", compaction: { trigger: "auto", messages: 7, summary: "- goal: ok" } } as unknown as WireEvent });
  const resolved = lastCompaction(s);
  eq(resolved.pending, false, "done: card resolved");
  eq(resolved.startedAt, undefined, "done: no stale startedAt on the resolved card");
}

{
  process.stdout.write("reducer: retry after failure re-stamps startedAt\n");
  let s: State = initialState;
  s = reducer(s, { type: "event", e: { kind: "compaction_started", compaction: { trigger: "auto" } } as unknown as WireEvent });
  const firstStamp = lastCompaction(s).startedAt;
  s = reducer(s, {
    type: "event",
    e: { kind: "compaction_done", compaction: { trigger: "auto", outcome: "failed", attempt: 1, reason: "x" } } as unknown as WireEvent,
  });
  // 模拟重试：新一轮 started 复用失败卡，戳必须刷新（旧戳会让「已 Xs」
  // 跨尝试累加成假时长）。
  const later = Date.now();
  s = reducer(s, { type: "event", e: { kind: "compaction_started", compaction: { trigger: "auto" } } as unknown as WireEvent });
  const restarted = lastCompaction(s);
  eq(restarted.pending, true, "retry: card pending again");
  ok(typeof restarted.startedAt === "number" && restarted.startedAt >= later, "retry: startedAt re-stamped");
  ok(restarted.startedAt !== firstStamp || later === firstStamp, "retry: stamp differs from the failed attempt's");
}

// ── Part B: compactionLiveReadout 纯函数 ─────────────────────────────────────

// en 字典直灌的最小 Translator（LocaleProvider 之外复用同一拼装契约）。
const t: Translator = ((key: keyof typeof en, vars?: Record<string, string | number>) => {
  let out: string = en[key] ?? String(key);
  if (vars) {
    for (const [k, v] of Object.entries(vars)) out = out.replaceAll(`{${k}}`, String(v));
  }
  return out;
}) as Translator;

{
  process.stdout.write("compactionLiveReadout: legs and degradation\n");
  const now = 1_000_000;

  // 全腿：elapsed + tokens + tps。
  eq(
    compactionLiveReadout({ pending: true, startedAt: now - 40_000, tokens: 12300, tokensPerSec: 45 }, now, t),
    " · 40s · 12.3K tokens · 45 tokens/s",
    "full readout: elapsed · tokens · tps",
  );

  // tps=0（后端 500ms 噪声底前）→ 隐藏 tps 腿，保留 tokens 腿。
  eq(
    compactionLiveReadout({ pending: true, startedAt: now - 5_000, tokens: 800, tokensPerSec: 0 }, now, t),
    " · 5s · 800 tokens",
    "tps hidden below the noise floor",
  );

  // 无戳无 tokens（水合 pending 卡理论态）→ 空串，卡片保持静态文案。
  eq(compactionLiveReadout({ pending: true }, now, t), "", "no stamp and no tokens → no readout");

  // 只有戳没有 tokens（3s 验收的关键态：elapsed 先于 token 估计出现）。
  eq(
    compactionLiveReadout({ pending: true, startedAt: now - 1_000 }, now, t),
    " · 1s",
    "elapsed leads within 3s even before the first token estimate",
  );

  // 分钟格式与 run-strip 同款。
  eq(
    compactionLiveReadout({ pending: true, startedAt: now - 90_000 }, now, t),
    " · 1m 30s",
    "minute-formatted elapsed matches the run-strip",
  );

  // 非 pending（resolved / failed 卡）→ 空串，零残值。
  eq(compactionLiveReadout({ pending: false, startedAt: now - 40_000, tokens: 12300 }, now, t), "", "resolved card shows no readout");
}

// ── Part C: CompactionCard 组件渲染 ──────────────────────────────────────────

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
  process.stdout.write("component: CompactionCard pending readout\n");
  root = createRoot(container);

  const pending: CompactionItem = {
    kind: "compaction", id: "c1", pending: true, trigger: "auto", messages: 0, summary: "", archive: "",
    done: 2, total: 4, tokens: 12300, tokensPerSec: 45, startedAt: Date.now() - 40_000,
  };
  act(() => renderCard(pending));
  let text = container.textContent ?? "";
  ok(text.includes("2/4"), `pending card keeps the N/M progress: ${JSON.stringify(text)}`);
  ok(text.includes("12.3K tokens"), `pending card shows formatted tokens: ${JSON.stringify(text)}`);
  ok(text.includes("45 tokens/s"), `pending card shows tps: ${JSON.stringify(text)}`);
  ok(/\b40s\b/.test(text), `pending card shows the elapsed leg: ${JSON.stringify(text)}`);

  // 无 tokens 但有戳 → 仅 elapsed（3s 验收态）。
  const elapsedOnly: CompactionItem = {
    kind: "compaction", id: "c2", pending: true, trigger: "auto", messages: 0, summary: "", archive: "", startedAt: Date.now() - 1_000,
  };
  act(() => renderCard(elapsedOnly));
  text = container.textContent ?? "";
  ok(/ · 1s/.test(text), `elapsed-only card ticks within 3s: ${JSON.stringify(text)}`);
  ok(!text.includes("tokens"), `no token text before the first estimate: ${JSON.stringify(text)}`);

  // resolved 卡 → 标题态，无读数（数字定格归位，不残留）。
  const resolved: CompactionItem = {
    kind: "compaction", id: "c3", pending: false, trigger: "auto", messages: 7, summary: "- ok", archive: "",
  };
  act(() => renderCard(resolved));
  text = container.textContent ?? "";
  ok(!text.includes("tokens/s") && !text.includes("12.3K"), `resolved card shows no readout: ${JSON.stringify(text)}`);

  await act(async () => {
    root?.unmount();
    root = null;
  });
} catch (err) {
  ok(false, `component test threw: ${err}`);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
