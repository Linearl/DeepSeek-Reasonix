// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1657-compaction-resume-card.test.tsx
//
// tl1657 验收矩阵（卡片生命周期 + resume 压缩可见性三态）：
// ① resume 现场复刻：CompactionStarted 在 webview 订阅前丢失（Wails 事件无
//    重放），后续 progress 到达时无 pending 卡 → 必须被丢弃（现状行为钉住，
//    防脏读数复活旧卡）；
// ② 合成建卡（tl1657 修复链路）：refresh 批次查询到在飞 pass → dispatch 合成
//    compaction_started → 建 pending 卡；随后的真实 progress（tokens/tps/fold）
//    正常挂卡——重启 resume 后触发的压缩同样实时显示进度；
// ③ 生命周期对齐：合成卡被 done(failed) 正常收尾为 failed 卡（attempt/reason
//    落卡，不悬挂）；新一轮 started（任务 867）把 failed 卡翻回 pending。
// Go 侧 emit 数据源三态（0 值抑制/lazy tps/pass 贯通）由
// internal/agent/compact_live_tl1657_test.go 钉。

import { initialState, reducer, type Item, type State } from "../lib/useController";
import { compactionLiveReadout } from "../lib/compactionLive";
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

function started(pass?: number, trigger = "pressure"): WireEvent {
  return { kind: "compaction_started", compaction: { trigger, pass } } as unknown as WireEvent;
}

function progress(pass: number, tokens: number, tps: number, doneN = 0, totalN = 0): WireEvent {
  return { kind: "compaction_progress", compaction: { trigger: "pressure", pass, tokens, tokensPerSec: tps, done: doneN, total: totalN } } as unknown as WireEvent;
}

function failedDone(pass: number, reason: string, attempt = 1): WireEvent {
  return { kind: "compaction_done", compaction: { trigger: "pressure", pass, outcome: "failed", reason, attempt } } as unknown as WireEvent;
}

// ── 态①：resume 现场——started 丢失，progress 无卡必须丢弃 ────────────────
{
  process.stdout.write("reducer: progress with no pending card stays dropped (20261117 shape)\n");
  const s0 = initialState;
  const s1 = reducer(s0, { type: "event", e: progress(1, 976, 9, 0, 0) });
  eq(s1.items.filter((it) => it.kind === "compaction").length, 0, "no card is created by a lone progress event");
  eq(s1.items.length, s0.items.length, "dropped progress leaves the transcript untouched");
}

// ── 态②：合成建卡 → 真实 progress 挂卡 ─────────────────────────────────────
{
  process.stdout.write("reducer: synthesized started builds the card; live progress attaches\n");
  let s: State = initialState;

  // reconcile 的合成 dispatch 与 wire 事件同构（trigger+pass）。
  s = reducer(s, { type: "event", e: started(2) });
  const card = lastCompaction(s);
  eq(card.pending, true, "synthesized started: pending card built");
  eq(card.pass, 2, "synthesized started: backend pass number on the card");
  eq(card.trigger, "pressure", "synthesized started: trigger carried");

  // 101 秒排队后的第一批真实读数（0 值已被 Go 侧抑制，这里直接是有值形态）。
  s = reducer(s, { type: "event", e: progress(2, 976, 19, 1, 4) });
  const live = lastCompaction(s);
  eq(live.pending, true, "progress: card still pending");
  eq(live.tokens, 976, "progress: tokens attached");
  eq(live.tokensPerSec, 19, "progress: tps attached");
  eq(live.done, 1, "progress: fold done attached");
  eq(live.total, 4, "progress: fold total attached");
  eq(live.pass, 2, "progress: pass unchanged (event pass echo kept)");

  // 读数渲染契约：tokens>0 走 liveReadout 腿（tokens/tps）；全 0 读数只剩
  // elapsed 腿——20261117 现场那种全 0 progress 即使挂上卡也不会渲染 0 值。
  const t = (k: string) => k;
  const readout = compactionLiveReadout({ pending: true, startedAt: Date.now() - 30_000, tokens: 976, tokensPerSec: 19 }, Date.now(), t);
  ok(readout.includes("compaction.liveReadout"), `visible readout takes the tokens/tps leg: ${readout}`);
  const empty = compactionLiveReadout({ pending: true, startedAt: Date.now() - 30_000, tokens: 0, tokensPerSec: 0 }, Date.now(), t);
  ok(!empty.includes("liveReadout") && !empty.includes("liveEstimating"), `all-zero readout renders no numeric leg: "${empty}"`);
}

// ── 态③：合成卡被 failed done 收尾（不悬挂）→ 新 started 翻回 pending ──────
{
  process.stdout.write("reducer: synthesized card resolves on failed done; restart flips pending\n");
  let s: State = initialState;
  s = reducer(s, { type: "event", e: started(1) });
  s = reducer(s, { type: "event", e: progress(1, 400, 12) });
  s = reducer(s, { type: "event", e: failedDone(1, "summarizer returned empty output") });
  const failedCard = lastCompaction(s);
  eq(failedCard.pending, false, "failed done: card stops pending (no hang)");
  eq(failedCard.failed, true, "failed done: failed card carries the outcome");
  eq(failedCard.attempt, 1, "failed done: attempt counter recorded");
  eq(failedCard.reason, "summarizer returned empty output", "failed done: reason recorded");

  // 重试轮（同一 pass 会话 +1）：任务 867 同卡重启语义保持——合成卡与
  // wire 卡在这里汇合为同一张状态机。tl1648 语义：attempt（连续失败计数）
  // 与 failed 标记跨重启保留，下次 ok done / fresh failed done 才刷新。
  s = reducer(s, { type: "event", e: started(2) });
  const restarted = lastCompaction(s);
  eq(restarted.pending, true, "retry started: failed card flips back to pending");
  eq(restarted.attempt, 1, "retry started: attempt counter persists across the restart (867)");
  eq(restarted.tokens, 0, "retry started: live readout reset for the new pass");
}

// ── 幂等守卫说明 ────────────────────────────────────────────────────────────
// 已有 pending 卡时 reconcileCompactionStateForTab（useController）短路合成
// dispatch（refresh 与事件竞态不重复建卡）。reducer 层对同 pass 重复 started
// 的行为（任务 1021 的 tracked-session 记账）不属于本件契约，不在重复断言——
// 守卫本身在 useController 单元中，接线由 tsc + refresh 批次审查保证。

process.stdout.write(`\ntl1657 compaction resume card: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
