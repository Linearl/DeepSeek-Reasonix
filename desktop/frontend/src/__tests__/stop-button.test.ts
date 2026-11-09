// Run: tsx src/__tests__/stop-button.test.ts
// 任务461-P7 三级终止: the stop button's view-state machine (pure helper).
// Levels mirror the backend RuntimeStatus; the L1 click arms locally; the
// grace renders the countdown from the backend's authoritative deadline.
// tl1420 档位显形: the button itself carries the tier — label spec + CSS tier
// class derive purely from the phase; the L3/settled 「已终止」 hold is timed.

import assert from "node:assert/strict";
import {
  stopButtonView,
  stopButtonTier,
  stopButtonLabelSpec,
  STOP_SETTLE_LINGER_MS,
} from "../lib/stopButton";

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

const NOW = 1_000_000;

// Idle: no stop in flight.
ok(stopButtonView({ stopLevel: 0, stopDeadlineUnix: 0, stopInitiatedAt: null, running: true, now: NOW }).phase === "idle", "no stop → idle");
ok(stopButtonView({ stopLevel: 1, stopDeadlineUnix: 0, stopInitiatedAt: null, running: false, now: NOW }).phase === "idle", "a level without a running turn → idle");

// L1 normal: the surface has not clicked yet (backend level from another surface).
ok(stopButtonView({ stopLevel: 1, stopDeadlineUnix: 0, stopInitiatedAt: null, running: true, now: NOW }).phase === "normal", "backend L1 without a local click reads normal");

// L1 armed: the click is older than the graceful window → 强制停止.
ok(stopButtonView({ stopLevel: 0, stopDeadlineUnix: 0, stopInitiatedAt: NOW - 1500, running: true, now: NOW, armAfterMs: 1000 }).phase === "armed", "1.5s after the click → armed (强制停止)");
ok(stopButtonView({ stopLevel: 0, stopDeadlineUnix: 0, stopInitiatedAt: NOW - 400, running: true, now: NOW, armAfterMs: 1000 }).phase === "normal", "0.4s after the click → still normal (graceful window)");

// L2 grace: the countdown reads from the backend deadline (ceil).
const grace = stopButtonView({ stopLevel: 2, stopDeadlineUnix: (NOW + 14_300) / 1000, stopInitiatedAt: null, running: true, now: NOW });
ok(grace.phase === "grace", "backend L2 → grace");
ok(grace.countdownSeconds === 15, `grace countdown ceils to 15, got ${grace.countdownSeconds}`);
// tl1514 兜底分支: an expired deadline reads the TERMINAL tier, not a stuck
// 「强制停止 · 0s」 — the backend normally pushes L3 at countdown zero, but a
// wedged publish lane can swallow that push; the view must not sit in grace
// past zero. The next click routes through onEscalateStop and the backend
// (still at Grace) fires the force immediately.
const graceExpired = stopButtonView({ stopLevel: 2, stopDeadlineUnix: NOW / 1000, stopInitiatedAt: null, running: true, now: NOW });
ok(graceExpired.phase === "force" && graceExpired.countdownSeconds === null, "an expired grace deadline reads 已终止 (force), never 强制停止 · 0s");
const graceLast = stopButtonView({ stopLevel: 2, stopDeadlineUnix: (NOW + 900) / 1000, stopInitiatedAt: null, running: true, now: NOW });
ok(graceLast.phase === "grace" && graceLast.countdownSeconds === 1, "a deadline within the current second keeps the grace countdown at 1");
const graceNoDeadline = stopButtonView({ stopLevel: 2, stopDeadlineUnix: 0, stopInitiatedAt: null, running: true, now: NOW });
ok(graceNoDeadline.phase === "grace" && graceNoDeadline.countdownSeconds === null, "L2 without a deadline keeps the countdown hidden");

// L3 force.
ok(stopButtonView({ stopLevel: 3, stopDeadlineUnix: 0, stopInitiatedAt: null, running: true, now: NOW }).phase === "force", "backend L3 → force");

// The backend level wins over the local arm: a surface re-attached mid-grace.
ok(stopButtonView({ stopLevel: 2, stopDeadlineUnix: (NOW + 5_000) / 1000, stopInitiatedAt: NOW - 900, running: true, now: NOW }).phase === "grace", "an attached surface shows grace, not the local arm");

// tl1420 档位显形: tier → CSS modifier class pairing (三色阶).
ok(stopButtonTier("normal") === "stopping" && stopButtonTier("armed") === "stopping", "L1 normal/armed → stopping tier (琥珀)");
ok(stopButtonTier("grace") === "grace", "L2 grace → grace tier (深红+倒计时)");
ok(stopButtonTier("force") === "force" && stopButtonTier("settled") === "settled", "L3 force/settled → force/settled tier");
ok(stopButtonTier("idle") === null, "idle keeps the resting look (no tier class)");

// tl1420: label spec — the on-button text IS the main tier feedback (三文案).
ok(stopButtonLabelSpec("normal", null).key === "composer.stopStopping", "L1 reads 停止中… on the button");
ok(stopButtonLabelSpec("armed", null).key === "composer.stopStopping", "armed still reads 停止中… (escalation hint stays in the tooltip)");
const graceSpec = stopButtonLabelSpec("grace", 14);
ok(graceSpec.key === "composer.stopForceCountdown" && graceSpec.n === 14, "L2 reads 强制停止 · Ns with the countdown var");
ok(stopButtonLabelSpec("force", null).key === "composer.stopTerminated", "L3 force reads 已终止");
ok(stopButtonLabelSpec("settled", null).key === "composer.stopTerminated", "settled keeps reading 已终止 during the hold");

// tl1420 L3 定格: settled hold after an engaged stop settles the turn…
const settled = stopButtonView({
  stopLevel: 1, stopDeadlineUnix: 0, stopInitiatedAt: null,
  running: false, now: NOW,
  stopEverEngaged: true, stopSettledAt: NOW - 600,
});
ok(settled.phase === "settled" && settled.countdownSeconds === null, "within the linger the button holds 已终止 (settled)");
// …and returns to idle once the hold expires.
const settledDone = stopButtonView({
  stopLevel: 1, stopDeadlineUnix: 0, stopInitiatedAt: null,
  running: false, now: NOW + STOP_SETTLE_LINGER_MS,
  stopEverEngaged: true, stopSettledAt: NOW,
});
ok(settledDone.phase === "idle", "after the linger the button is back to idle (unmounts)");
// A turn that ends without any stop engagement skips the hold entirely.
const naturalEnd = stopButtonView({
  stopLevel: 0, stopDeadlineUnix: 0, stopInitiatedAt: null,
  running: false, now: NOW, stopEverEngaged: false, stopSettledAt: NOW - 100,
});
ok(naturalEnd.phase === "idle", "a non-stop ending does not hold 已终止");
// Engagement missing → no hold even with a fresh settle stamp.
const stampWithoutEngagement = stopButtonView({
  stopLevel: 0, stopDeadlineUnix: 0, stopInitiatedAt: null,
  running: false, now: NOW, stopEverEngaged: false, stopSettledAt: NOW,
});
ok(stampWithoutEngagement.phase === "idle", "no engagement → settle stamp alone does not hold");
// A running turn never reads settled (the hold is a post-turn state only).
const runningNotSettled = stopButtonView({
  stopLevel: 1, stopDeadlineUnix: 0, stopInitiatedAt: null,
  running: true, now: NOW, stopEverEngaged: true, stopSettledAt: NOW - 100,
});
ok(runningNotSettled.phase === "normal", "a running turn never shows settled");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
