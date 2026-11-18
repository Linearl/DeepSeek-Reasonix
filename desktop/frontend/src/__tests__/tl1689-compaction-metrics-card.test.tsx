// tl1689 验收：快速压缩 tps 过程帧（后端 seal 帧）+ 压缩卡 hover 数据卡。
//
// 现场（20261118 08:50）：26s 压缩只有一帧 progress（tokens=1 tps=0），卡片
// 全程无吞吐读数；压缩完成后 hover 无任何数据。两半修复：
// ① 后端 seal 帧（internal/agent compact_live_tl1689_test.go 钉）——pass
//    收口 Done 前发一帧绕过间隔门的最终读数；
// ② 前端——compaction_done 终局卡继承 pending 卡的最后一次读数（tokens/
//    tokensPerSec/startedAt）并打 endedAt 戳，CompactionMetricsCard 以既有
//    Tooltip 载体（tl1671 同款）挂在终局卡头部。
//
// 钉五件事：
// ① reducer：started→progress→done 链上终局卡继承 tokens/tps/startedAt/
//    endedAt；
// ② reducer：无 progress 直落 done（水合/丢帧现场）时终局卡 endedAt 有戳、
//    读数 0 值不造噪音；
// ③ CompactionMetricsCard：全数据 4 行、部分数据只显有值行、零数据 null；
// ④ CompactionCard 终局卡面零变化（973 精确串钉不回归：Tooltip label 只在
//    hover 时渲染，静态渲染不含 tokens/s）；
// ⑤ 三语键齐备且都带占位符。

import assert from "node:assert/strict";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { initialState, reducer, type State } from "../lib/useController";
import type { CompactionItem } from "../lib/transcriptRows";
import { CompactionCard } from "../components/TranscriptCards";
import { CompactionMetricsCard } from "../components/CompactionMetricsCard";
import { LocaleProvider } from "../lib/i18n";
import { en } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";

let passed = 0;
const ok = (cond: boolean, msg: string) => {
  if (!cond) {
    console.error(`FAIL: ${msg}`);
    process.exitCode = 1;
  } else {
    passed += 1;
  }
};

type Action = Parameters<typeof reducer>[1];
const ev = (e: unknown) => ({ type: "event", e } as unknown as Action);
const started = (pass = 1) => ev({ kind: "compaction_started", compaction: { trigger: "auto", pass } });
const progress = (tokens: number, tps: number) => ev({ kind: "compaction_progress", compaction: { trigger: "auto", pass: 1, tokens, tokensPerSec: tps } });
const done = () => ev({ kind: "compaction_done", compaction: { trigger: "auto", pass: 1, outcome: "ok", messages: 7, summary: "- final", sourceTokens: 821_800, projectionTokens: 215_600, summaryHitTokens: 800_000, summaryMissTokens: 21_800, durationMs: 26_600 } });

const lastCompaction = (s: State) =>
  [...s.items].reverse().find((it): it is CompactionItem => it.kind === "compaction");

// ── ① 全链：终局卡继承 seal 帧读数 + 时间戳 ──────────────────────────────────
{
  process.stdout.write("reducer: terminal card inherits the last live readout and stamps\n");
  let s: State = initialState;
  s = reducer(s, started());
  s = reducer(s, progress(976, 19));
  s = reducer(s, done());
  const card = lastCompaction(s)!;
  ok(card.pending === false, "done resolves the card");
  ok(card.tokens === 976, `terminal card inherits tokens=976, got ${card.tokens}`);
  ok(card.tokensPerSec === 19, `terminal card inherits tps=19, got ${card.tokensPerSec}`);
  ok(typeof card.startedAt === "number" && card.startedAt > 0, "terminal card keeps the startedAt stamp");
  ok(typeof card.endedAt === "number" && card.endedAt >= (card.startedAt ?? 0), "terminal card carries the endedAt stamp");
  // tl1689 补强: the release metrics ride the Done event onto the card.
  ok(card.sourceTokens === 821_800 && card.projectionTokens === 215_600, "release figures inherited (821.8K→215.6K shape)");
  ok(card.summaryHitTokens === 800_000 && card.summaryMissTokens === 21_800, "summary input hit/miss split inherited");
  ok(card.durationMs === 26_600, "pass duration inherited");
}

// ── ② 丢帧直落：读数 0 值、时间戳仍然成立 ─────────────────────────────────────
{
  process.stdout.write("reducer: done without progress — zero readout, stamps intact\n");
  let s: State = initialState;
  s = reducer(s, started());
  s = reducer(s, done());
  const card = lastCompaction(s)!;
  ok(card.pending === false && card.messages === 7, "card resolves with the summary");
  ok((card.tokens ?? 0) === 0 && (card.tokensPerSec ?? 0) === 0, "no inherited readout stays zero (hover hides those rows)");
  ok(typeof card.endedAt === "number", "endedAt stamped even without live data");
}

// ── ③ CompactionMetricsCard：行随数据显隐 ─────────────────────────────────────
{
  process.stdout.write("component: metrics card renders one row per available datum\n");
  const now = Date.now();
  const full = renderToStaticMarkup(React.createElement(LocaleProvider, null,
    React.createElement(CompactionMetricsCard, { item: { kind: "compaction", id: "c", pending: false, trigger: "auto", messages: 7, summary: "", archive: "", startedAt: now - 26_600, endedAt: now, tokens: 976, tokensPerSec: 19, sourceTokens: 821_800, projectionTokens: 215_600, summaryHitTokens: 800_000, summaryMissTokens: 21_800, durationMs: 26_600 } as CompactionItem })));
  ok((full.match(/tooltip__thoughtcard-row/g) ?? []).length === 6, `full data renders 6 rows (耗时/吞吐/消息/释放/输入/输出), got ${(full.match(/tooltip__thoughtcard-row/g) ?? []).length}`);
  ok(/26\.\d/.test(full), `duration renders the 26.6s-shaped figure: ${full}`);
  ok(full.includes("19"), "tps figure present");
  ok(full.includes("821.8K") && full.includes("215.6K"), "release figures render formatted (821.8K → 215.6K)");
  ok(full.includes("800K") && full.includes("21.8K"), "summary input hit/miss render formatted");

  const partial = renderToStaticMarkup(React.createElement(LocaleProvider, null,
    React.createElement(CompactionMetricsCard, { item: { kind: "compaction", id: "c", pending: false, trigger: "auto", messages: 7, summary: "", archive: "", startedAt: now - 1_000, endedAt: now } as CompactionItem })));
  ok((partial.match(/tooltip__thoughtcard-row/g) ?? []).length === 2, `duration+messages only, got ${(partial.match(/tooltip__thoughtcard-row/g) ?? []).length}`);

  const none = renderToStaticMarkup(React.createElement(LocaleProvider, null,
    React.createElement(CompactionMetricsCard, { item: { kind: "compaction", id: "c", pending: false, trigger: "auto", messages: 0, summary: "", archive: "" } as CompactionItem })));
  ok(none === "", "zero-data item renders nothing (no zero-value noise)");
}

// ── ④ 终局卡面零变化（973 串钉形态） ─────────────────────────────────────────
{
  process.stdout.write("component: terminal card face unchanged — tooltip label stays hover-only\n");
  const now = Date.now();
  const face = renderToStaticMarkup(React.createElement(LocaleProvider, null,
    React.createElement(CompactionCard, { item: { kind: "compaction", id: "c", pending: false, trigger: "auto", messages: 7, summary: "- final", archive: "", startedAt: now - 26_600, endedAt: now, tokens: 976, tokensPerSec: 19 } as CompactionItem })));
  ok(!face.includes("tokens/s") && !face.includes("Avg throughput"), "metrics stay off the card face (hover-only)");
  ok(face.includes("compaction__head"), "head button intact");
  const bare = renderToStaticMarkup(React.createElement(LocaleProvider, null,
    React.createElement(CompactionCard, { item: { kind: "compaction", id: "c", pending: false, trigger: "auto", messages: 7, summary: "- final", archive: "" } as CompactionItem })));
  ok(!bare.includes("tooltip__thoughtcard"), "zero-data card mounts no tooltip");
}

// ── ⑤ 三语键齐备 ─────────────────────────────────────────────────────────────
{
  process.stdout.write("i18n: the metrics keys exist in all three locales\n");
  const keys = [
    "compaction.metricsDurationLabel", "compaction.metricsDurationValue",
    "compaction.metricsTpsLabel", "compaction.metricsTpsValue",
    "compaction.metricsMessagesLabel", "compaction.metricsMessagesValue",
    "compaction.metricsTokensLabel", "compaction.metricsTokensValue",
    "compaction.metricsReleasedLabel", "compaction.metricsReleasedValue",
    "compaction.metricsInputLabel", "compaction.metricsInputValue",
  ] as const;
  const dicts: Array<[string, Record<string, string>]> = [
    ["en", en as unknown as Record<string, string>],
    ["zh", zh as unknown as Record<string, string>],
    ["zh-TW", zhTW as unknown as Record<string, string>],
  ];
  for (const [name, dict] of dicts) {
    for (const key of keys) {
      ok(Boolean(dict[key]), `${name} carries ${key}`);
    }
  }
  const dupFree = new Set([
    zh["compaction.metricsDurationLabel"], zhTW["compaction.metricsDurationLabel"], en["compaction.metricsDurationLabel"],
  ]);
  ok(dupFree.size === 3, "the three locales carry distinct duration labels (直译不算三语)");
}

process.stdout.write(`\ntl1689 compaction metrics: ${passed} asserts passed\n`);
if (process.exitCode) process.exit(1);
assert.ok(passed >= 30, `expected >=30 asserts, got ${passed}`);
