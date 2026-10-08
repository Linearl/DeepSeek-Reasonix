// Run: tsx src/__tests__/plan-usage.test.ts
//
// Task 287 — pure plan-usage formatting contracts: window label keys, the
// tone thresholds (>=80 notice, >=100 critical), the note → copy mapping,
// and the five-hour exhaustion readout that feeds the status bar warning and
// the task-242 fallback-model data source. Zero skips — every branch is a
// constructed case.

import assert from "node:assert/strict";
import {
  PLAN_USAGE_WINDOW_KEYS,
  planFiveHourExhausted,
  planUsageNoteText,
  planUsageTone,
  planWindowLabelKey,
  type PlanUsageResult,
} from "../lib/planUsage";

let passed = 0;
let failed = 0;
function ok(condition: unknown, label: string) {
  if (condition) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

function view(partial: Partial<PlanUsageResult>): PlanUsageResult {
  return { supported: true, provider: "GLM", region: "cn", windows: [], note: "", queriedAt: 0, ...partial };
}

// ── window keys ──
ok(PLAN_USAGE_WINDOW_KEYS.join(",") === "five_hour,weekly,monthly", "window keys match the Go wire names");
ok(planWindowLabelKey("five_hour") === "planUsage.window.fiveHour", "five_hour maps to its display key");
ok(planWindowLabelKey("weekly") === "planUsage.window.weekly", "weekly maps to its display key");
ok(planWindowLabelKey("monthly") === "planUsage.window.monthly", "monthly maps to its display key");
ok(planWindowLabelKey("mystery") === "mystery", "unknown window falls through to its raw name (visible, not hidden)");

// ── tone thresholds ──
ok(planUsageTone(null) === undefined, "null percent is neutral");
ok(planUsageTone(undefined) === undefined, "undefined percent is neutral");
ok(planUsageTone(0) === undefined, "0% is neutral");
ok(planUsageTone(79.9) === undefined, "just under 80 is neutral");
ok(planUsageTone(80) === "notice", "80% is notice (window closing)");
ok(planUsageTone(99.9) === "notice", "99.9% is still notice");
ok(planUsageTone(100) === "critical", "100% is critical (5h exhausted warning)");
ok(planUsageTone(140) === "critical", "over 100 clamps to critical");
ok(planUsageTone(Number.NaN) === undefined, "NaN is neutral, never critical");

// ── note mapping ──
const notes: Record<string, string> = {};
const translator = (key: string) => {
  notes[key] = (notes[key] ?? 0) + 1;
  return key;
};
ok(planUsageNoteText("", translator) === "", "empty note maps to empty copy");
ok(planUsageNoteText("no-key", translator) === "planUsage.note.noKey", "no-key maps to setup guidance");
ok(planUsageNoteText("auth-failed", translator) === "planUsage.note.authFailed", "auth-failed maps to its copy");
ok(planUsageNoteText("unsupported", translator) === "planUsage.note.unsupported", "unsupported maps to its copy");
for (const generic of ["network", "parse", "http-500", "api-error"]) {
  ok(planUsageNoteText(generic, translator) === "planUsage.note.failed", `${generic} degrades to the honest generic line`);
}

// ── five-hour exhaustion (the 242 data source) ──
ok(planFiveHourExhausted(null) === false, "no view → not exhausted");
ok(planFiveHourExhausted(view({})) === false, "no windows → not exhausted");
ok(planFiveHourExhausted(view({ windows: [{ window: "five_hour", percent: 99, resetsAt: "" }] })) === false, "99% is not exhausted");
ok(planFiveHourExhausted(view({ windows: [{ window: "five_hour", percent: 100, resetsAt: "" }] })) === true, "100% five-hour is exhausted");
ok(planFiveHourExhausted(view({ windows: [{ window: "weekly", percent: 100, resetsAt: "" }] })) === false, "weekly exhaustion alone does not trip the five-hour readout");
ok(planFiveHourExhausted(view({ windows: [{ window: "five_hour", percent: null, resetsAt: "" }] })) === false, "unparseable percent is not exhausted");

process.stdout.write(`\nplan-usage: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
