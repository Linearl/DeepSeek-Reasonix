// Run: npx tsx src/__tests__/turn-runtime-state.test.ts
// 任务1015 会话运行态 SSOT：五态状态机（idle/running/stopping/queued_followup/
// unknown）全部迁移 + 四信号谓词派生 + 迁移留痕。

import assert from "node:assert/strict";
import {
  deriveTurnRuntime,
  turnRuntimeSignals,
  recordTurnRuntimeTransition,
  turnRuntimeTransitionLog,
  resetTurnRuntimeTransitionLog,
  type TurnRuntimeInput,
} from "../lib/turnRuntimeState";

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

const base: TurnRuntimeInput = {
  known: true,
  freshnessSynced: true,
  backendRunning: false,
  localRunning: false,
  cancelRequested: false,
  stopLevel: 0,
  queuedFollowup: false,
};
const derive = (over: Partial<TurnRuntimeInput>) => deriveTurnRuntime({ ...base, ...over });

resetTurnRuntimeTransitionLog();

// ---- idle ----
ok(derive({}).state === "idle", "投影 synced + 双源 idle → idle");
ok(derive({}).reason === "all-idle", "idle 的触发事实记 all-idle");

// ---- running（后端权威 + 并集防闪断）----
ok(derive({ backendRunning: true }).state === "running", "后端投影 running → running（后端权威）");
ok(derive({ backendRunning: true, localRunning: false }).state === "running",
  "验收2 权威性：人为延迟前端 turn-end（本地已 finalize）而后端仍 running → 仍 running，本地不得推断结束");
ok(derive({ backendRunning: false, localRunning: true }).state === "running",
  "本地 turn_started 已到、投影未跟上（合批/推送延迟）→ 仍 running，不闪断");
ok(derive({ backendRunning: true }).reason === "backend-running", "后端驱动 running 记 backend-running");
ok(derive({ localRunning: true }).reason === "local-running", "本地事件镜像驱动 running 记 local-running");
ok(derive({ backendRunning: true, localRunning: true }).state === "running", "双源一致 running → running");

// ---- stopping（461-P7：终止流程已开）----
ok(derive({ backendRunning: true, cancelRequested: true }).state === "stopping", "CancelRequested → stopping");
ok(derive({ backendRunning: true, stopLevel: 1 }).state === "stopping", "StopLevel=1（L1 停止）→ stopping");
ok(derive({ backendRunning: true, stopLevel: 3 }).state === "stopping", "StopLevel=3（强制）→ stopping");
ok(derive({ backendRunning: true, cancelRequested: true }).reason === "cancel-requested", "stopping 触发事实记 cancel-requested");
ok(derive({ backendRunning: true, stopLevel: 2 }).reason === "stop-level", "stopping 触发事实记 stop-level");

// ---- queued_followup（投递层降级回执）----
ok(derive({ backendRunning: true, queuedFollowup: true }).state === "queued_followup",
  "steer 注入失败降级排队（turn 在跑）→ queued_followup，与 running 可区分");
ok(derive({ backendRunning: true, queuedFollowup: true, localRunning: true }).state === "queued_followup",
  "排队态优先于 running 单值呈现（输入区排队可见）");
ok(derive({ backendRunning: true, queuedFollowup: true }).reason === "queued-receipt", "排队态触发事实记 queued-receipt");

// ---- unknown（510：投影不可信/未同步；停止出口常驻）----
ok(derive({ freshnessSynced: false, backendRunning: true, localRunning: true }).state === "unknown",
  "投影未同步 → unknown（即便已知在跑，510 降级优先）");
ok(derive({ known: false, localRunning: true }).state === "unknown",
  "legacy 后端无投影 → unknown（回落并集仍保守）");
ok(derive({ freshnessSynced: false, backendRunning: false, localRunning: false }).state === "unknown",
  "投影未同步且双源 idle → unknown（降级提示）");
ok(derive({ freshnessSynced: false }).reason === "projection-unsynced", "unknown 触发事实记 projection-unsynced");
ok(derive({ known: false }).reason === "projection-legacy", "legacy 触发事实记 projection-legacy");

// ---- 迁移优先级（互斥序）----
ok(derive({ backendRunning: true, cancelRequested: true, queuedFollowup: true }).state === "stopping",
  "stopping 优先于 queued_followup（终止流程压过排队提示）");
ok(derive({ freshnessSynced: false, cancelRequested: true }).state === "unknown",
  "unknown 优先于 stopping（投影不可信时终止档位也不可信）");

// ---- 四信号谓词 ----
{
  const signalsOf = (over: Partial<TurnRuntimeInput>) => turnRuntimeSignals(derive(over));

  const idle = signalsOf({});
  ok(idle.settled === true, "idle → settled（可复制/操作行挂载）");
  ok(idle.stopVisible === false && idle.turnActive === false, "idle → 无停止按钮、非 turnActive");
  ok(idle.steerForm === false && idle.queuedNotice === false && idle.degraded === false, "idle → 普通形态、无排队/降级提示");

  const running = signalsOf({ backendRunning: true });
  ok(running.turnActive === true && running.stopVisible === true, "running → 停止按钮在场");
  ok(running.steerForm === true, "running → send 形态 steer + steer placeholder");
  ok(running.settled === false, "running → 不可复制（操作行不挂载）");

  const stopping = signalsOf({ backendRunning: true, cancelRequested: true });
  ok(stopping.stopVisible === true, "stopping → 停止按钮在场（461-P7 升级档位可点）");
  ok(stopping.steerForm === true && stopping.settled === false, "stopping → 仍 steer 形态、不可复制");

  const queued = signalsOf({ backendRunning: true, queuedFollowup: true });
  ok(queued.queuedNotice === true, "queued_followup → 输入区排队态提示");
  ok(queued.stopVisible === true, "queued_followup → 停止按钮在场（turn 真在跑，四信号不得自相矛盾）");
  ok(queued.steerForm === true && queued.settled === false, "queued_followup → 仍 steer 形态、不可复制");

  const unknownRunning = signalsOf({ freshnessSynced: false, backendRunning: true, localRunning: true });
  ok(unknownRunning.state === "unknown" && unknownRunning.stopVisible === true,
    "unknown 且已知在跑 → 停止出口常驻（510）");
  ok(unknownRunning.degraded === true, "unknown → 降级提示");

  const unknownIdle = signalsOf({ freshnessSynced: false });
  ok(unknownIdle.turnActive === false && unknownIdle.stopVisible === false,
    "unknown 且无任何在跑事实 → 不凭空常驻停止按钮（普通会话不误显）");
}

// ---- 迁移留痕 ----
{
  resetTurnRuntimeTransitionLog();
  recordTurnRuntimeTransition({ at: 1, from: "idle", to: "running", reason: "backend-running" });
  recordTurnRuntimeTransition({ at: 2, from: "running", to: "stopping", reason: "cancel-requested" });
  recordTurnRuntimeTransition({ at: 3, from: "running", to: "running", reason: "backend-running" }); // 同态不记
  const log = turnRuntimeTransitionLog();
  ok(log.length === 2, "留痕只记真实迁移（同态翻转不记）");
  ok(log[0].from === "idle" && log[0].to === "running" && log[0].reason === "backend-running", "首条迁移字段完整");
  ok(log[1].from === "running" && log[1].to === "stopping", "迁移序可追溯");

  for (let index = 0; index < 120; index += 1) {
    recordTurnRuntimeTransition({ at: index, from: index % 2 ? "idle" : "running", to: index % 2 ? "running" : "idle", reason: "test" });
  }
  ok(turnRuntimeTransitionLog().length === 50, "留痕环形缓冲封顶 50 条（诊断可查且不无限增长）");
  resetTurnRuntimeTransitionLog();
  ok(turnRuntimeTransitionLog().length === 0, "测试隔离重置生效");
}

// ---- 派生器总体健壮 ----
ok(derive({ stopLevel: 0, cancelRequested: false }).state === "idle", "零值缺省不误判 stopping");
assert.ok(true);

process.stdout.write(`\nturn runtime state (任务1015 五态 SSOT): ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exitCode = 1;
