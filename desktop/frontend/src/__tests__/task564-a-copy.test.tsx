// Run: npx tsx src/__tests__/task564-a-copy.test.tsx
// Task 564 acceptance harness (A-level copy fixes + modelCapabilityFilter
// read-only retention, on top of task 561's group structure):
//  ① pathRules: REMOVED outright by tl1487 七组③（用户 20261107 裁决，过时
//     技术）— rail entry / union id / pane branch / locale keys all gone, the
//     zero-setter negative pin still holds;
//  ② the 561 M2 modelStrategy card: the retired modelCapabilityFilter
//     read-only block was REMOVED by tl1487 一组③（用户 20261107 八组清单
//     「移除（不展示）」）——the writable highSpeedModel member and the
//     zero-setter negative pin remain;
//  ③ the A-level hint copy matches audited code behavior in all three
//     locales:
//     - feedback: the submit_feedback tool registers unconditionally
//       (internal/tool/builtin/feedback.go init → tool.Builtins), so the
//       hint may not claim "enables the tool";
//     - autonomousIdleTerminate: evaluateIdleStreak has no production call
//       site (desktop/heartbeat.go), so the hint says reserved / not wired;
//     - opencodeGoUsage: the gated-query promise is RETIRED by tl1487 四组
//       (「不提供开关、始终打开」) — the hint now states the always-on
//       behavior and the off-gate wording is banned;
//  ④ the superseded hint keys are gone from every locale
//     (settings.pathRulesHint, settings.modelCapabilityFilterHint), and the
//     retired on/off switch labels (tl1487 四组) are gone too.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

// 任务724/1134:布局树在 lab/labLayoutDefault、union 在 lab/labPaneRegistry、
// 卡分支在 ExperimentalLabSection;四处拼接读源(1134 判例)。
const panel = [
  "../components/SettingsPanel.tsx",
  "../components/ExperimentalLabSection.tsx",
  "../lab/labLayoutDefault.ts",
  "../lab/labPaneRegistry.ts",
].map((rel) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), "utf8")).join("\n");
const en = readFileSync(fileURLToPath(new URL("../locales/en.ts", import.meta.url)), "utf8");
const zh = readFileSync(fileURLToPath(new URL("../locales/zh.ts", import.meta.url)), "utf8");
const zhTW = readFileSync(fileURLToPath(new URL("../locales/zh-TW.ts", import.meta.url)), "utf8");

function paneSlice(startMarker: string, endMarker: string): string {
  const start = panel.indexOf(startMarker);
  const end = panel.indexOf(endMarker);
  return start >= 0 && end > start ? panel.slice(start, end) : "";
}

console.log("\ntask 564 A-level copy fixes + retired-key read-only retention");

// ① pathRules（路径规则）——tl1487 七组③（用户 20261107 20:17 裁决，取代
// 564/551 的 retired 只读范式）：整卡移除——rail 条目/union id/pane 分支/
// locale 键全部摘除；负钉继续成立（零 setter 调用面）。
{
  ok(!panel.includes('{ id: "pathRules"'), "tl1487: the pathRules rail entry is gone from the layout");
  ok(!panel.includes('| "pathRules"'), "tl1487: the pathRules union id is gone from the pane registry");
  ok(!panel.includes('{selected === "pathRules" && ('), "tl1487: the pathRules pane branch is gone");
  ok((panel.match(/SetExperimentalPathRules\(/g) ?? []).length === 0,
    "panel-wide: zero app.SetExperimentalPathRules( call sites (negative still holds)");
}

// ② M2 card: modelCapabilityFilter（模型能力过滤）只读块已随 tl1487 一组③
//    （用户 20261107 八组清单「移除（不展示）」）整体摘除——564 时代的
//    retired 只读范式收尾；可写成员行与零 setter 负钉保持。
{
  const m2 = paneSlice('{selected === "modelStrategy" && (', '{selected === "contextGovernance" && (');
  ok(!m2.includes('t("settings.modelCapabilityFilter.retired")') && !m2.includes('t("settings.modelCapabilityFilter.value"'),
    "tl1487: the modelCapabilityFilter retired block is gone from the M2 card");
  ok(m2.includes("SetExperimentalHighSpeedModel(on)"),
    "M2 card keeps the highSpeedModel setter (writable member intact)");
  ok((panel.match(/SetExperimentalModelCapabilityFilter\(/g) ?? []).length === 0,
    "panel-wide: zero SetExperimentalModelCapabilityFilter( call sites (561 negative still holds)");
}

// ③ copy ↔ code behavior, all three locales.
{
  const pairs: Array<[string, string]> = [["en", en], ["zh", zh], ["zh-TW", zhTW]];

  // feedback: tool is unconditional — the hint must not claim to enable it.
  ok(!/Enables the agent submit_feedback tool/.test(en), "en feedbackHint no longer claims tool enabling");
  for (const [name, src] of pairs) {
    const hint = src.match(/"settings\.feedbackHint": "([^"]+)"/)?.[1] ?? "";
    ok(hint.length > 0, `${name} feedbackHint exists`);
    ok(/always available|始终可用|始終可用/.test(hint), `${name} feedbackHint states the tool is always available`);
    ok(!/Enables the agent/.test(hint), `${name} feedbackHint drops the enable claim`);
  }

  // autonomousIdleTerminate: wired since the call-time evaluation (S4) — the
  // copy must say so. 任务 517 refreshed the stale "not wired" claim: the
  // guard reads the switch at every run, so honest copy states immediate
  // effect and the old reserved/no-effect wording is banned.
  for (const [name, src] of pairs) {
    const hint = src.match(/"settings\.autonomousIdleTerminateHint": "([^"]+)"/)?.[1] ?? "";
    ok(/applies immediately|即时生效|即時生效/.test(hint), `${name} idleTerminateHint states the immediate effect`);
    ok(!/not wired|尚未接入|currently has no effect|当前开启无效果|目前開啟無效果/.test(hint), `${name} idleTerminateHint drops the stale not-wired claim`);
  }

  // pathRules: tl1487 七组③ 整卡移除——全部 pathRules 键出字典；
  // modelCapabilityFilterHint 负钉保持。
  for (const [name, src] of pairs) {
    ok(!src.includes('"settings.pathRules'), `${name} carries no pathRules key (tl1487 整卡移除)`);
    ok(!src.includes('"settings.modelCapabilityFilterHint"'), `${name} no longer carries settings.modelCapabilityFilterHint`);
  }
  // tl1487 四组: the retired on/off switch labels are gone (switch removed).
  for (const [name, src] of pairs) {
    ok(!src.includes('"settings.opencodeGoUsage.on"') && !src.includes('"settings.opencodeGoUsage.off"'),
      `${name} drops the retired opencodeGoUsage on/off labels`);
  }

  // opencodeGoUsage: tl1487 四组 flips the promise — always on, switch gone.
  for (const [name, src] of pairs) {
    const hint = src.match(/"settings\.opencodeGoUsageHint": "([^"]+)"/)?.[1] ?? "";
    ok(/Always on|始终开启|始終開啟/.test(hint), `${name} opencodeGoUsageHint states the always-on behavior (tl1487 四组)`);
    ok(!/only while this switch is on|仅开关开启时|僅開關開啟時/.test(hint), `${name} opencodeGoUsageHint drops the retired gated-query promise`);
  }
}

// ④ tl1487 四组: the Go-side switch gate is RETIRED — the query is always on
// (official-host + key gates remain), no lab switch read anywhere.
{
  const go = readFileSync(fileURLToPath(new URL("../../../opencode_go_usage.go", import.meta.url)), "utf8");
  ok(!go.includes("openCodeGoUsageSwitchOn") && !go.includes("ExperimentalOpenCodeGoUsage"),
    "tl1487: GetOpenCodeGoUsage no longer consults any lab switch (always on)");
}

console.log(`\ntask564: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
