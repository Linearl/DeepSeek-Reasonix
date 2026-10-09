// Run: tsx src/__tests__/history-auto-retry.test.ts
// 任务676: busy-class transient history read failures (a writer holding the
// session files mid-hydrate) must self-heal through a BOUNDED silent
// auto-retry instead of surfacing the "failed to load history — retry" banner.
// These tests exercise the retry primitive itself with an injected clock, so
// the busy scenarios below cost zero real time.

import assert from "node:assert/strict";
import {
  HISTORY_AUTO_RETRY_DELAY_MS,
  HISTORY_AUTO_RETRY_LIMIT,
  runBoundedAutoRetry,
} from "../lib/historyAutoRetry";

let passed = 0;
let failed = 0;
function ok(value: unknown, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}
function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label} (got ${JSON.stringify(actual)})`);
}

// Deterministic fake clock: records each backoff instead of waiting it.
function fakeSleep() {
  const waits: number[] = [];
  const sleep = (ms: number) => { waits.push(ms); return Promise.resolve(); };
  return { waits, sleep };
}

interface AttemptLog {
  failures: unknown[];
  attempts: number;
}

/** Busy scenario: the first `failTimes` reads fail (writer holds the lock), then the file frees up. */
function busyThenSuccess(failTimes: number, value: string) {
  const log: AttemptLog = { failures: [], attempts: 0 };
  const attempt = async () => {
    log.attempts += 1;
    if (log.attempts <= failTimes) {
      const err = new Error(`open session transcript: busy (lock held by writer), attempt ${log.attempts}`);
      log.failures.push(err);
      return undefined;
    }
    return value;
  };
  return { attempt, log };
}

process.stdout.write("\n任务676 history auto-retry (bounded, silent)\n");

// ── 1. Boundedness is anchored: 3 retries @ 500ms, constants not knobs ──────
eq(HISTORY_AUTO_RETRY_LIMIT, 3, "auto-retry budget is exactly 3 (user decision 20261009, 禁无界)");
eq(HISTORY_AUTO_RETRY_DELAY_MS, 500, "backoff is exactly 500ms per retry");

// ── 2. Busy window heals silently: fail twice, third read succeeds ──────────
{
  const { attempt, log } = busyThenSuccess(2, "page-1");
  const { waits, sleep } = fakeSleep();
  const retries: number[] = [];
  const value = await runBoundedAutoRetry(attempt, { sleep, onRetry: (n) => retries.push(n) });
  eq(value, "page-1", "busy-then-free read returns the loaded page");
  eq(log.attempts, 3, "two transient failures consumed two of the three retries");
  eq(retries.length, 2, "onRetry fired once per automatic retry");
  assert.deepEqual(waits, [500, 500], "each retry backed off exactly 500ms");
}

// ── 3. Persistent busy exhausts the budget and hands back undefined ─────────
// The caller maps undefined to the manual-retry banner exactly as before —
// asserted at the wiring level in history-auto-retry-contract.test.ts.
{
  const { attempt, log } = busyThenSuccess(Number.POSITIVE_INFINITY, "page-1");
  const { waits, sleep } = fakeSleep();
  const value = await runBoundedAutoRetry(attempt, { sleep });
  eq(value, undefined, "persistently busy read exhausts the budget to undefined (banner path)");
  eq(log.attempts, 1 + HISTORY_AUTO_RETRY_LIMIT, "initial attempt + 3 retries = 4 reads, never more");
  eq(waits.length, HISTORY_AUTO_RETRY_LIMIT, "exactly 3 backoffs, then give up");
}

// ── 4. Abort predicate: a tab left mid-retry neither retries nor reports ────
{
  const { attempt, log } = busyThenSuccess(Number.POSITIVE_INFINITY, "page-1");
  const { waits, sleep } = fakeSleep();
  let current = false; // user switched away right after the first failed read
  const value = await runBoundedAutoRetry(attempt, { sleep, shouldContinue: () => current });
  eq(value, undefined, "stale hydrate aborts silently");
  eq(log.attempts, 1, "no retry attempted once shouldContinue went false");
  eq(waits.length, 0, "no backoff scheduled for an aborted hydrate");
}

// ── 5. Fast path untouched: first-read success costs zero backoffs ──────────
{
  const { attempt, log } = busyThenSuccess(0, "page-1");
  const { waits, sleep } = fakeSleep();
  const retries: number[] = [];
  const value = await runBoundedAutoRetry(attempt, { sleep, onRetry: (n) => retries.push(n) });
  eq(value, "page-1", "healthy read passes through unchanged");
  eq(log.attempts, 1, "healthy read performs exactly one read");
  eq(retries.length, 0, "no retry callback on the happy path");
  eq(waits.length, 0, "no backoff on the happy path");
}

// ── 6. Default clock exists (production wiring needs no injection) ──────────
{
  const value = await runBoundedAutoRetry(async () => "ok", { delayMs: 0 });
  eq(value, "ok", "production defaults (setTimeout clock) work without injection");
}

process.stdout.write(`\n${passed}/${passed + failed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
