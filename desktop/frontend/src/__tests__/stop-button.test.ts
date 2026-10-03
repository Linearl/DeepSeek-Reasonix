// Run: tsx src/__tests__/stop-button.test.ts
// 任务461-P7 三级终止: the stop button's view-state machine (pure helper).
// Levels mirror the backend RuntimeStatus; the L1 click arms locally; the
// grace renders the countdown from the backend's authoritative deadline.

import assert from "node:assert/strict";
import { stopButtonView } from "../lib/stopButton";

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
const graceLast = stopButtonView({ stopLevel: 2, stopDeadlineUnix: NOW / 1000, stopInitiatedAt: null, running: true, now: NOW });
ok(graceLast.phase === "grace" && graceLast.countdownSeconds === 0, "an expired deadline clamps the countdown to 0");
const graceNoDeadline = stopButtonView({ stopLevel: 2, stopDeadlineUnix: 0, stopInitiatedAt: null, running: true, now: NOW });
ok(graceNoDeadline.phase === "grace" && graceNoDeadline.countdownSeconds === null, "L2 without a deadline keeps the countdown hidden");

// L3 force.
ok(stopButtonView({ stopLevel: 3, stopDeadlineUnix: 0, stopInitiatedAt: null, running: true, now: NOW }).phase === "force", "backend L3 → force");

// The backend level wins over the local arm: a surface re-attached mid-grace.
ok(stopButtonView({ stopLevel: 2, stopDeadlineUnix: (NOW + 5_000) / 1000, stopInitiatedAt: NOW - 900, running: true, now: NOW }).phase === "grace", "an attached surface shows grace, not the local arm");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
