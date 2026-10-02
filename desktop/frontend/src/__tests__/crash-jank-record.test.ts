// Run: tsx src/__tests__/crash-jank-record.test.ts
//
// Task 360 — 卡顿检测事件自动落盘。三个触发源（long task / js heap /
// event loop lag）任一触发即整理一条 jank 记录（reason + 快照 + 帧采样 +
// breadcrumbs）经 ReportJankRecord 落 logs/perf/jank-YYYYMMDD.jsonl。
// 这里覆盖：记录构造（字段齐全 / 抑制计数 / 无字段噪音）、节流（60s 每
// label，抑制触发不丢——并入下一条）、接线守卫（记录在弹窗门控之前、可选
// 绑定调用、Go 侧落盘文件存在）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  buildJankRecord,
  jankRecordingDue,
  markJankRecordedForTest,
  performanceLabelForReason,
  resetJankRecordingForTest,
  type Breadcrumb,
  type PerformanceSnapshot,
} from "../lib/crash";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else {
    failed += 1;
    process.exitCode = 1;
  }
}

function snapshotOf(reason: string): PerformanceSnapshot {
  return {
    reason,
    uptimeMs: 42_000,
    visibility: "visible",
    focused: true,
    online: true,
    hardwareConcurrency: 16,
    jsHeap: { usedMb: 300, totalMb: 400, limitMb: 4096, usagePercent: 7.3 },
    longTasks: { count: 3, totalMs: 400, maxMs: 220, recent: [{ startMs: 41_000, durationMs: 220 }] },
    longTaskFrames: [{ label: "renderChat", samples: 5 }],
  };
}

console.log("\ncrash jank record (task 360)");
{
  const crumbs: Breadcrumb[] = [
    { t: 1_000, cat: "performance", msg: "long task 220ms" },
    { t: 1_500, cat: "bridge", msg: "GetProjectTreeSnapshot" },
  ];
  const record = buildJankRecord("long task", snapshotOf("long task 220ms"), crumbs, 4, "2026-10-02T05:00:00.000Z");
  ok(record.label === "long task" && record.reason === "long task 220ms", "record carries label and reason");
  ok(record.recordedAt === "2026-10-02T05:00:00.000Z", "record carries the trigger timestamp");
  ok(record.suppressedSinceLast === 4, "suppressed trigger count rides along");
  ok(record.snapshot.longTaskFrames?.length === 1 && record.breadcrumbs.length === 2, "frames and breadcrumbs included");
  const parsed = JSON.parse(JSON.stringify(record)) as Record<string, unknown>;
  ok(typeof parsed.recordedAt === "string" && typeof parsed.snapshot === "object", "record serializes losslessly (JSONL line)");
  const quiet = buildJankRecord("heap", snapshotOf("js heap 90% of limit"), [], 0, "2026-10-02T05:01:00.000Z");
  ok(!("suppressedSinceLast" in quiet), "no suppressed field when nothing was folded");
}
{
  resetJankRecordingForTest();
  const base = 1_700_000_000_000; // realistic clock: the "never written" baseline is 0
  const label = performanceLabelForReason("long task 220ms");
  ok(jankRecordingDue(label, base), "first trigger is due");
  markJankRecordedForTest(label, base);
  ok(jankRecordingDue(label, base + 59_999) === false, "same label within 60s is throttled");
  ok(jankRecordingDue(label, base + 60_000), "same label after 60s is due again");
  const other = performanceLabelForReason("js heap 90% of limit");
  ok(jankRecordingDue(other, base), "a different label is independent");
  resetJankRecordingForTest();
}
{
  // Wiring guards: the recorder must run BEFORE the prompt gate (cooldown /
  // hidden / handled labels still land a record), go through the optional
  // binding, and have a Go sink writing logs/perf/jank-*.jsonl.
  const testDir = dirname(fileURLToPath(import.meta.url));
  const crashSource = readFileSync(resolve(testDir, "../lib/crash.ts"), "utf8");
  const gateIndex = crashSource.indexOf("if (!shouldPromptForPerformance(now, label)) return;");
  const recordIndex = crashSource.indexOf("recordJankEvent(reason, label, currentLagMs);");
  ok(gateIndex > 0 && recordIndex > 0 && recordIndex < gateIndex, "recording happens before the prompt gate");
  ok(crashSource.includes("app.ReportJankRecord?.(JSON.stringify(record))?.catch(() => {})"), "record goes through the optional binding (older backends drop it)");
  const bridgeSource = readFileSync(resolve(testDir, "../lib/bridge.ts"), "utf8");
  ok(bridgeSource.includes("ReportJankRecord?(record: string): Promise<void>;"), "AppBindings declares the optional jank binding");
  const goSource = readFileSync(resolve(testDir, "../../../../desktop/jank_log.go"), "utf8");
  ok(goSource.includes("jank-\" + now.Format(\"20060102\")") || goSource.includes("jank-"), "Go sink writes jank-YYYYMMDD.jsonl next to the perf samples");
  ok(goSource.includes("payload[\"ts\"] = now.UTC().Format(time.RFC3339Nano)"), "Go sink stamps server-side arrival time (same clock as perf samples)");
}

console.log(`\ntotal: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
