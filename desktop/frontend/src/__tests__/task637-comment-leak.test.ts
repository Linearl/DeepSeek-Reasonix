// Run: npx tsx src/__tests__/task637-comment-leak.test.ts
// 任务 637 acceptance harness (实验室注释泄漏机制性彻查):
//  ① 守卫自检：check-comment-leak.mjs --self-test 的 10 例全过——其中 5 组是
//     故意违规样例（JSX 裸行注释 / 值内 // / Task N:·任务 N：·任務 N：署名），
//     每组都必须报错——「守卫拦截新泄漏」的可执行证明；
//  ② 守卫全量扫现状树：518 tsx + 14364 locale 值 + 102 yaml 值 0 泄漏；
//  ③ 机制修复锚定：SettingsPanel.tsx 六处（604 三处 Task 254/254/277 + 637
//     两处 Task 342/377 + ThemePreviewSurface 刻意代码样例豁免）——泄漏块的
//     JSX children 裸 // 形态必须已转为 {/* */} 注释；
//  ④ 三语文案锚定：被清理的 17 个描述键在 zh/en/zh-TW 都不再以任务号署名
//     开头（604 同族 R3 清零的字面锚定）。

import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { zh } from "../locales/zh";
import { en } from "../locales/en";
import { zhTW } from "../locales/zh-TW";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

function scriptPath(name: string): string {
  return fileURLToPath(new URL(`../../scripts/${name}`, import.meta.url));
}

console.log("\ntask 637 comment-leak guard");

// ① 守卫自检——故意违规样例必须报错（验收第 2 条）。
{
  const selfTest = spawnSync(process.execPath, [scriptPath("check-comment-leak.mjs"), "--self-test"], { encoding: "utf8" });
  ok(selfTest.status === 0, `guard self-test exits 0 (got ${selfTest.status})`);
  ok((selfTest.stdout ?? "").includes("10 例全过"), "guard self-test reports all 10 cases pass");
}

// ② 守卫全量扫现状树——清零（验收第 1 条）。
{
  const scan = spawnSync(process.execPath, [scriptPath("check-comment-leak.mjs")], { encoding: "utf8" });
  ok(scan.status === 0, `guard full scan exits 0 (got ${scan.status})`);
  ok((scan.stdout ?? "").includes("0 leak(s)"), "guard full scan reports 0 leak(s)");
}

// ③ 机制修复锚定：JSX children 裸 // 注释已转 {/* */}（604 三处 + 637 两处）。
{
  const panelSource = fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url));
  const src = (await import("node:fs")).readFileSync(panelSource, "utf8");
  ok(!/(^|\n)\s*\/\/ Task 254: the agent-facing half/.test(src), "SettingsPanel: Task 254 block is no longer bare JSX text");
  ok(!/(^|\n)\s*\/\/ Task 254 \(user ruling\)/.test(src), "SettingsPanel: Task 254 user-ruling block is no longer bare JSX text");
  ok(!/(^|\n)\s*\/\/ Task 277: update-complete chime/.test(src), "SettingsPanel: Task 277 block is no longer bare JSX text");
  ok(!/(^|\n)\s*\/\/ Task 342: WebView2 CDP debug endpoint/.test(src), "SettingsPanel: Task 342 block is no longer bare JSX text");
  ok(!/(^|\n)\s*\/\/ Task 377: crash-report lifecycle noise triage/.test(src), "SettingsPanel: Task 377 block is no longer bare JSX text");
  ok(src.includes("{/* Task 342: WebView2 CDP debug endpoint."), "SettingsPanel: Task 342 note kept as {/* */} comment");
  ok(src.includes("{/* Task 377: crash-report lifecycle noise triage."), "SettingsPanel: Task 377 note kept as {/* */} comment");
}

// ④ 三语文案锚定：17 个被清理键不以任务号署名开头（R3 清零的字面锚定）。
{
  const taskAttribution = /^\s*(?:Task|任务|任務)\s*\d/;
  const keys = [
    "settings.toolOptimizationsHint",
    "settings.traceAsStateHint",
    "settings.autoLoadOlderHint",
    "settings.dreamHint",
    "settings.sessionCollabHint",
    "settings.autonomousIdleTerminateHint",
    "settings.loopStreakNoteHint",
    "settings.eventWaitRecheckHint",
    "settings.orphanHandlingHint",
    "settings.collabInboxMergeHint",
    "settings.collabGuidanceMergeHint",
    "settings.sessionCollabReplyNudgeHint",
    "settings.sessionCollabGatesHint",
    "settings.sessionCollabDailySendLimitHint",
    "settings.cascadeApprovalHint",
    "settings.fallbackModelSwitchHint",
    "settings.lifecycleNoiseGateHint",
  ];
  for (const key of keys) {
    for (const [dialect, table] of [["zh", zh], ["en", en], ["zh-TW", zhTW]] as const) {
      const value = (table as Record<string, string>)[key];
      ok(typeof value === "string" && !taskAttribution.test(value),
        `${dialect} ${key} carries no task-attribution prefix`);
    }
  }
}

// ⑤ 守卫接线锚定：build 并行检查组与 package.json 都挂了 check:comment-leak。
{
  const parallelSrc = (await import("node:fs")).readFileSync(scriptPath("check-all-parallel.mjs"), "utf8");
  const pkgSrc = (await import("node:fs")).readFileSync(fileURLToPath(new URL("../../package.json", import.meta.url)), "utf8");
  ok(parallelSrc.includes('"check:comment-leak"'), "check-all-parallel runs check:comment-leak");
  ok(pkgSrc.includes('"check:comment-leak"'), "package.json exposes check:comment-leak");
}

process.stdout.write(`\ntask 637: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
