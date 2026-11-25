// tl1802 性能对照基准：千 turn 会话 reducer dispatch 耗时（验收 5：全 O(1) 不回退）。
// 运行：node scripts/tsx-test.mjs bench/surface-admission-dispatch.ts
// 同一脚本在基线（a2baafa3b9，主仓检出）与闸分支（feat-tl1802）各跑一次，输出对照。
// 表面数据位直接注入（initialState.items），测的是 dispatch 本身而非构造。
import { performance } from "node:perf_hooks";
import { initialState, reducer } from "../src/lib/useController";
import type { Item, State } from "../src/lib/useController";
import { replayDuplicatesRenderedTurn } from "../src/lib/surfaceAdmission";

const TURNS = 1000;

function buildThousandTurnState(): State {
  const items: Item[] = [];
  let seq = 0;
  for (let i = 0; i < TURNS; i += 1) {
    items.push({ kind: "user", id: `u${seq}`, text: `问题 ${i}`, submissionId: `sub-${i}`, createdAt: 0 });
    seq += 1;
    items.push({ kind: "assistant", id: `a:turn-${i}:0`, text: `回答 ${i} —— 一段足够长的文本以贴近真实行的内存形态。`, reasoning: "", streaming: false, wasStreamed: true });
    seq += 1;
  }
  return { ...initialState, items, seq, appliedEventSeq: seq, historyPrefixCount: items.length, historyRevision: 7 } as State;
}

function median(fn: () => void, warmup: number, runs: number, batch: number): number {
  for (let i = 0; i < warmup; i += 1) fn();
  const samples: number[] = [];
  for (let r = 0; r < runs; r += 1) {
    const t0 = performance.now();
    for (let i = 0; i < batch; i += 1) fn();
    samples.push((performance.now() - t0) / batch);
  }
  samples.sort((a, b) => a - b);
  return samples[Math.floor(samples.length / 2)];
}

const state = buildThousandTurnState();
console.log(`setup: turns=${TURNS} items=${state.items.length} appliedEventSeq=${state.appliedEventSeq}`);

// ① 直播热路径：stream_batch 增量（水位线判定 + live 流入，不改 items）
{
  let s = state;
  let nextSeq = s.appliedEventSeq + 1;
  const fn = () => {
    s = reducer(s, { type: "stream_batch", segments: [{ kind: "text", delta: "x", seq: nextSeq }] });
    nextSeq += 1;
    if (nextSeq > s.appliedEventSeq + 4000) { nextSeq = s.appliedEventSeq + 1; }
  };
  console.log(`hot_stream_batch_us_per_dispatch=${(median(fn, 200, 7, 500) * 1000).toFixed(3)}`);
}

// ② 重放热路径：stale seq 全拒（纯判定，零落位）
{
  const staleSeq = 1;
  const fn = () => {
    reducer(state, { type: "stream_batch", segments: [{ kind: "text", delta: "y", seq: staleSeq }] });
    reducer(state, { type: "event", e: { kind: "notice", text: "replay", seq: staleSeq } } as never);
  };
  console.log(`replay_rejected_us_per_pair=${(median(fn, 200, 7, 500) * 1000).toFixed(3)}`);
}

// ③ guidance_bubble 追加（现状 O(n) 幂等扫描 vs 闸 memo 查表+派生）
{
  let s = state;
  let n = 0;
  const fn = () => {
    s = reducer(s, { type: "guidance_bubble", text: `引导 ${n += 1}` });
  };
  console.log(`guidance_bubble_us_per_append=${(median(fn, 20, 7, 100) * 1000).toFixed(3)}`);
}

// ④ history_rebase 落页（K4 门 + 1712 折叠 + 316 并集保留——整体冷路径对照）
{
  const page: Item[] = state.items.slice(0, 400).map((item) => ({ ...item, id: `${item.id}-p` }));
  let s = state;
  const fn = () => {
    s = reducer(state, { type: "history_rebase", items: page, startTurn: 0, totalTurns: TURNS, hasOlder: false, revision: 8 });
  };
  console.log(`history_rebase_us_per_dispatch=${(median(fn, 5, 7, 20) * 1000).toFixed(3)}`);
}

// ⑤ 1739r2 渲染入口 turn-id 去重判定（基线 O(n) 扫描 vs 闸 O(1) memo 命中）
{
  const probe = { items: state.items, activeTurnId: "turn-active" };
  const fn = () => {
    if (!replayDuplicatesRenderedTurn(probe, "turn-500")) throw new Error("probe must dedupe");
  };
  console.log(`replay_turn_dedupe_us_per_call=${(median(fn, 100, 7, 2000) * 1000).toFixed(4)}`);
}

console.log("bench done");
