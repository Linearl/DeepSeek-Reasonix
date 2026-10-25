// Run: npx tsx src/__tests__/user-input-timestamp-replay.test.ts
//
// tl 1152: 消息气泡时间戳错乱。现象：多条夜间实际发送的消息（00:41、01:01）
// 气泡时间戳同标次日晨间的一个时刻（09:34），与 jsonl/provider transcript 里的
// createdAt 不符。
//
// 根因：跨会话注入消息成为 turn 输入时前端只收到 `user_input` wire 事件（任务
// 580 的唯一渲染通道），事件不带消息时间，reducer 只能用「事件处理时刻」打气泡
// 戳。次日晨间启动时，落盘 turn 经 turn-event 重放 / 事件通道补刷（1150 实锤的
// 延迟刷出面）集中重投影夜间消息，同一重放批的全部 user_input 都以「重放时刻」
// 打戳——多条同刻 09:34。后端已让事件携带 inbox item 的持久化入队时刻（at）。
//
// 本文件钉前端契约：
//   - 重放批的多条 user_input 各自保留 at（同批处理、时间互不相同、不等于处
//     理时刻）——「同刻 09:34」不再出现；
//   - at 缺失（旧后端）回退 Date.now()，行为与改动前一致；
//   - steer 通知同样优先 at（>emittedAt>处理时刻），steer 发射方尚未打 at 时
//     走 emittedAt，零回归。

import { initialState, reducer } from "../lib/useController";
import type { Item, State } from "../lib/useController";
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
    process.exitCode = 1;
  }
}

function ev(e: Record<string, unknown>): { type: "event"; e: WireEvent } {
  return { type: "event", e: { turnId: "t1", ...e } as unknown as WireEvent };
}

function userRows(s: State): Array<Extract<Item, { kind: "user" }>> {
  return s.items.filter((it): it is Extract<Item, { kind: "user" }> => it.kind === "user");
}

// 夜间真实发送时刻（+08:00 的本地墙上时钟口径，只用于互异性与「早于处理时刻」
// 断言，不依赖时区换算）。
const NIGHT_0041 = 1792958460000; // 2026-10-25 00:41 附近
const NIGHT_0101 = 1792959660000; // 2026-10-25 01:01 附近
const MORNING_0934 = Date.now(); // 重放批的处理时刻（晨间启动/补刷）

console.log("\nuser_input bubble timestamps (tl 1152)");

// ── 1. 重放批：同一处理时刻落下的两条夜间消息各自保留原时间 ──────────────────
{
  // 模拟晨间补刷：两条 user_input（00:41 / 01:01 入队）在同一个「现在」被
  // reducer 先后处理——正是「多条消息同标一个时间点」的事故形态。
  let s = reducer(initialState, ev({ kind: "user_input", text: "夜间消息甲", itemId: "msg-a", at: NIGHT_0041 }));
  s = reducer(s, ev({ kind: "user_input", text: "夜间消息乙", itemId: "msg-b", at: NIGHT_0101 }));
  const rows = userRows(s);
  ok(rows.length === 2, "replay batch renders both user_input rows");
  ok(rows[0]?.createdAt === NIGHT_0041, "row 1 keeps its own enqueue time (00:41), not the processing instant");
  ok(rows[1]?.createdAt === NIGHT_0101, "row 2 keeps its own enqueue time (01:01), not the processing instant");
  ok(rows[0]?.createdAt !== rows[1]?.createdAt, "the two rows no longer share one timestamp");
  const stamps = rows.map((r) => r.createdAt ?? 0);
  ok(stamps.every((t) => t <= MORNING_0934), "every stamp predates the replay processing time (no 09:34 collapse)");
}

// ── 2. 旧后端无 at：回退处理时刻（改动前行为逐字节保留） ─────────────────────
{
  const before = Date.now();
  const s = reducer(initialState, ev({ kind: "user_input", text: "旧后端消息", itemId: "msg-c" }));
  const rows = userRows(s);
  ok(rows.length === 1 && typeof rows[0]?.createdAt === "number", "legacy event (no at) still stamps a number");
  ok((rows[0]?.createdAt ?? 0) >= before, "legacy fallback uses the processing clock as before");
}

// ── 3. steer 通知：at 优先，其次 emittedAt（后端时刻），本机时钟垫底 ──────────
{
  function noticeTimes(e: Record<string, unknown>): number | undefined {
    return reducer(initialState, ev(e))
      .items.filter((it): it is Extract<Item, { kind: "notice" }> => it.kind === "notice")[0]?.createdAt;
  }
  ok(noticeTimes({ kind: "steer", text: "引导甲", at: NIGHT_0041, emittedAt: MORNING_0934 }) === NIGHT_0041,
    "steer notice prefers the durable domain time when stamped");
  const emitted = noticeTimes({ kind: "steer", text: "引导乙", emittedAt: NIGHT_0101 });
  ok(emitted === NIGHT_0101, "steer notice without at keeps the backend emittedAt (no regression)");
  ok(noticeTimes({ kind: "steer", text: "引导丙" }) !== undefined, "steer notice with neither field still stamps (local clock)");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
