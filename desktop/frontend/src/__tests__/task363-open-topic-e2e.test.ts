// Run: npx tsx src/__tests__/task363-open-topic-e2e.test.ts
// 任务 363 ⑥（打开历史对话端到端口径）的接缝钉子——源码契约层。hydrate 主链
// 无法在单测里整体驱动（需要完整 controller mock），按 405/649 先例用源码
// 契约钉住接缝；端到端数值本身由 desktop.log 的 "open-topic e2e" 行现场出数。
//   ① e2e 排放只发生在 reason === "open-topic" 的 ancillary 批次之后；
//   ② 上报 stage 名 open-topic:e2e（进 monitor 面板）；
//   ③ desktop.log 行（tab-switch 源）一次带齐 e2e/hydrate/ancillary/effort
//      四段，回答「数秒才能开始对话花在哪」；
//   ④ effort 腿单独计时（合成体感的 can-send 输入），且不改变原有四路并行
//      批次形状（checkpoints/jobs/context 仍是同一 Promise.all）。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), "utf8");
const controller = read("../lib/useController.ts");

function sliceBetween(source: string, startMarker: string, endMarker: string): string {
  const start = source.indexOf(startMarker);
  const end = source.indexOf(endMarker, start + 1);
  return start >= 0 && end > start ? source.slice(start, end) : "";
}

console.log("\ntask 363 ⑥ open-topic 端到端口径（e2e 单值 + 四段拆解）");

// ① e2e 块锚定在 ancillary 批次之后、open-topic 条件内。
{
  const batch = sliceBetween(
    controller,
    "const [effort, jobs, context, checkpoints] = await Promise.all([",
    'dispatchTo(tabId, { type: "context_panel_refresh" });',
  );
  ok(batch.includes('if (reason === "open-topic") {'),
    "the e2e emission lives in the ancillary batch, gated on the open-topic reason");
  ok(batch.includes('reportStageTiming(tabId, "open-topic:e2e", e2eMs)'),
    "the composite number reports as the open-topic:e2e stage (monitor board)");
  ok(batch.includes('"open-topic e2e"') && batch.includes("e2e=${Math.round(e2eMs)}ms")
    && batch.includes("hydrate=${Math.round(hydrateElapsed)}ms")
    && batch.includes("ancillary=${Math.round(Date.now() - ancillaryStartedAt)}ms")
    && batch.includes("effort=${Math.round(effortLegMs)}ms"),
    "the desktop.log line carries e2e/hydrate/ancillary/effort in one greppable line");
}

// ② effort 腿单独计时，包住 EffortForTab，finally 保证慢读也出数。
{
  const effortLeg = sliceBetween(
    controller,
    'loadAncillary("effort", async () => {',
    "loadAncillary(\"jobs\"",
  );
  ok(effortLeg.includes("app.EffortForTab(tabId)") && effortLeg.includes("effortLegMs = Date.now() - effortLegStartedAt;"),
    "the effort leg is timed around EffortForTab (finally: slow reads still report)");
}

// ③ 并行批次形状不变：四路仍属同一 Promise.all（151 的并行语义不被破坏）。
{
  const batch = sliceBetween(
    controller,
    "const [effort, jobs, context, checkpoints] = await Promise.all([",
    "]);",
  );
  for (const leg of ["JobsForTab", "ContextUsageForTab", "CheckpointsForTab"]) {
    ok(batch.includes(`app.${leg}(tabId)`), `${leg} stays in the same parallel batch`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
