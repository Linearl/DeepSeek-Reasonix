// Task 340 (upstream #10778, fixes #10254): an interrupted or
// recovery_required turn that carries a provider error keeps that error
// visible as a warn notice beside the interrupted guidance - the branch used
// to emit only the generic guidance and swallow e.err/e.detail entirely, so
// an HTTP 402 quota failure read like an unexplained stop. A cancellation
// (diagnostic.kind "cancelled") stays a user stop: no failure row.
//
// Run: tsx src/__tests__/interrupted-turn-provider-error.test.ts

import assert from "node:assert/strict";

import { initialState, reducer } from "../lib/useController";

type S = ReturnType<typeof reducer>;

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

function finish(e: Record<string, unknown>): S {
  const started = reducer(initialState, { type: "event", e: { kind: "turn_started", turnId: "t1", status: "in_progress" } });
  return reducer(started, { type: "event", e: { kind: "turn_done", turnId: "t1", ...e } });
}

function notices(state: S) {
  return state.items.filter((item) => item.kind === "notice");
}

// recovery_required + HTTP 402 quota failure: the real error stays visible.
const quota = finish({
  status: "recovery_required",
  err: "Relay · Chat Completions: status 402: insufficient balance",
  detail: "Connection ID: relay\nRequest path: /v1/chat/completions",
  diagnostic: { kind: "quota", status: 402, providerId: "relay", protocol: "openai" },
  recovery: { state: "recovery_required", reason: "silent_interruption" },
});
const quotaNotices = notices(quota).filter((item) => item.kind === "notice");
const failure = quotaNotices.find((item) => item.level === "warn");
ok(failure !== undefined && failure.text.includes("402"), "provider error stays visible on a recovery_required turn");
ok(failure !== undefined && failure.detail?.includes("Connection ID: relay"), "the diagnostic detail rides along");
ok(quotaNotices.some((item) => item.level === "info"), "interrupted guidance stays alongside the error");
ok(new Set(quota.items.map((item) => item.id)).size === quota.items.length, "notice ids stay unique");

// Plain interrupted turn with a provider failure keeps it too.
const interrupted = finish({
  status: "interrupted",
  err: "Relay · Chat Completions: status 502: bad gateway",
  detail: "Connection ID: relay",
  diagnostic: { kind: "temporary", status: 502, providerId: "relay", protocol: "openai" },
});
ok(notices(interrupted).some((item) => item.level === "warn" && item.text.includes("502")), "provider error stays visible on an interrupted turn");

// A user stop is not a failure: no warn row, guidance only.
const cancelled = finish({
  status: "interrupted",
  err: "context canceled",
  diagnostic: { kind: "cancelled" },
});
ok(!notices(cancelled).some((item) => item.level === "warn"), "a user stop is not reported as a provider failure");
ok(notices(cancelled).some((item) => item.level === "info"), "the interrupted guidance still shows");

// Failed turns keep their existing single error notice (no duplication).
const failedTurn = finish({
  status: "failed",
  err: "Relay · Chat Completions: status 500",
  diagnostic: { kind: "temporary", status: 500 },
});
const failedWarns = notices(failedTurn).filter((item) => item.level === "warn");
ok(failedWarns.length === 1, "non-interrupted failures keep the existing single error notice");

process.stdout.write(`\ninterrupted-turn-provider-error: ${passed} passed, ${failed} failed\n`);
if (failed > 0) { process.exit(1); }
