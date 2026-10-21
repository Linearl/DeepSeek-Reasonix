// Run: npx tsx src/__tests__/tl1020-heartbeat-units.test.ts
//
// 任务1020（P2）：长工具链心跳报时单位回归——用户截图显示「最后事件
// 1792460705s 前」（恰为 epoch 秒量级）。根因：Composer 心跳公式
// (now - lastTurnEventAt)/1000 要求两侧同为 epoch 毫秒，而 lastTurnEventAt
// 由 promptEventClock()（performance.now()，页面起点毫秒，注释明言「只能与
// 自身比较」）赋值，与 now（useTick 的 Date.now()）相减得出 10^9 量级秒数。
//
// 本文件钉死两层契约：
// ① 源头单位：turn 活动事件刷新的 lastTurnEventAt 必须是 epoch 毫秒
//    （Date.now() 量级），不得回退到 performance.now() 量级；
// ② 兜底：turnHeartbeatSeconds 对缺失 / 非有限值 / 时钟倒挂超容差 / 超过
//    24h 的输入一律返回 null，由调用方呈现「—」，单位错不得静默呈现为大数。

import { initialState, reducer } from "../lib/useController";
import {
  TURN_HEARTBEAT_FUTURE_TOLERANCE_MS,
  TURN_HEARTBEAT_MAX_MS,
  turnHeartbeatSeconds,
} from "../lib/workStatus";

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
function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

// ── 契约①：lastTurnEventAt 源头单位 = epoch 毫秒（Date.now() 量级）──
{
  const before = reducer(initialState, { type: "event", e: { kind: "turn_started", turnId: "t1020", status: "in_progress" } } as never);
  const at = before.lastTurnEventAt;
  ok(at !== undefined, "turn_started 记录 lastTurnEventAt");
  if (at !== undefined) {
    ok(at >= 1e12, `lastTurnEventAt 为 epoch 毫秒量级（>=1e12），实测 ${at}`);
    ok(Math.abs(Date.now() - at) < 5000, "lastTurnEventAt 与 Date.now() 同基准（±5s 内）");
  }
  const after = reducer(before, { type: "event", e: { kind: "tool_dispatch", turnId: "t1020", name: "bash" } } as never);
  ok(after.lastTurnEventAt !== undefined && after.lastTurnEventAt >= 1e12,
    "tool_dispatch 刷新后仍为 epoch 毫秒量级");
}

// ── 契约②：turnHeartbeatSeconds 归一与兜底 ──
{
  const now = Date.now();
  eq(turnHeartbeatSeconds(now, now - 5000), 5, "正常毫秒对：5s 前");
  eq(turnHeartbeatSeconds(now, now - 500), 0, "亚秒钳 0（floor 后不出现负数）");
  eq(turnHeartbeatSeconds(now, now + 1000), 0, "容差内微小未来偏移钳 0");
  eq(turnHeartbeatSeconds(now, undefined), null, "缺字段返回 null（调用方不渲染心跳段）");
  eq(turnHeartbeatSeconds(now, 0), null, "零值返回 null");
  eq(turnHeartbeatSeconds(now, NaN), null, "NaN 返回 null");
  eq(turnHeartbeatSeconds(NaN, now - 1000), null, "now 非有限值返回 null");
  // 秒/毫秒混用（本次回归形态）：epoch 秒被当毫秒喂入 → 差值 ≈ 1.79e9 秒，
  // 不得超过 24h 兜底线，调用方呈现「—」。
  const epochSeconds = Math.floor(Date.now() / 1000);
  eq(turnHeartbeatSeconds(now, epochSeconds), null, "epoch 秒混入（10^9 量级读数）返回 null");
  // 超 24h 的陈旧毫秒同样兜底。
  eq(turnHeartbeatSeconds(now, now - TURN_HEARTBEAT_MAX_MS - 1), null, "超过 24h 返回 null");
  eq(turnHeartbeatSeconds(now, now - TURN_HEARTBEAT_MAX_MS), 86400, "恰在 24h 边界内仍给出读数");
  // 时钟倒挂超容差（明显异常）兜底。
  eq(turnHeartbeatSeconds(now, now + TURN_HEARTBEAT_FUTURE_TOLERANCE_MS + 1), null, "未来时间戳超容差返回 null");
}

process.stdout.write(`\ntl1020 heartbeat units: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
