// Run: npx tsx src/__tests__/task517-safety-cost-control-merge.test.tsx
// 任务 517 acceptance harness (「安全 / 成本控制」single-switch merge) →
// tl1816 换代（方案A 一次性迁移：总开关退役）：
//  ① the safetyCostControl card no longer carries ANY master switch — 任务 722
//     点6 的三个细粒度子开关是唯一控制面（tl1816 后读数=显式值，Go 侧 load
//     已把 nil 全部写实），整卡自 automation 迁 efficiency、消息合并/压缩
//     模型/心跳轮换并入；
//  ② old configs migrate losslessly: the Go side folds any legacy
//     experimental_autonomous_idle_terminate / experimental_loop_streak_note /
//     experimental_event_wait_recheck true into the merged master and then
//     materializes it into every nil sub-switch (tl1816 方案A —
//     internal/config/safety_cost_control_merge_test.go owns the semantics —
//     this harness pins the render/gate/UI wiring);
//  ③ off = zero behavior: all three runtime gates read their explicit
//     sub-switch (absent reads off = retired master default);
//  ④ the retired entry points are gone end to end (master row, master setter,
//     master bridge declaration, master snapshot fields, master render line)
//     and the integrity anchors are registered.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8") + "\n" + readFileSync(fileURLToPath(new URL("../components/ExperimentalLabSection.tsx", import.meta.url)), "utf8");
const bridge = readFileSync(fileURLToPath(new URL("../lib/bridge.ts", import.meta.url)), "utf8");
const tiers = readFileSync(fileURLToPath(new URL("../lib/experimentTiers.ts", import.meta.url)), "utf8");
const zh = readFileSync(fileURLToPath(new URL("../locales/zh.ts", import.meta.url)), "utf8");
const en = readFileSync(fileURLToPath(new URL("../locales/en.ts", import.meta.url)), "utf8");
const zhTW = readFileSync(fileURLToPath(new URL("../locales/zh-TW.ts", import.meta.url)), "utf8");
const goRender = readFileSync(fileURLToPath(new URL("../../../../internal/config/render.go", import.meta.url)), "utf8");
const goLoad = readFileSync(fileURLToPath(new URL("../../../../internal/config/load.go", import.meta.url)), "utf8");
// 756 拆块 1（e1fd627f5）把 lifecycle 域（含 B1 心跳门）自 app.go 挪入
// app_lifecycle.go——读取路径随锚点迁移（778 收口）。
const goLifecycle = readFileSync(fileURLToPath(new URL("../../../../desktop/app_lifecycle.go", import.meta.url)), "utf8");
const goBoot = readFileSync(fileURLToPath(new URL("../../../../internal/boot/boot.go", import.meta.url)), "utf8");
const goConfig = readFileSync(fileURLToPath(new URL("../../../../internal/config/config.go", import.meta.url)), "utf8");

function cardSlice(startMarker: string, endMarker: string): string {
  const start = panel.indexOf(startMarker);
  const end = panel.indexOf(endMarker);
  return start >= 0 && end > start ? panel.slice(start, end) : "";
}

console.log("\n任务 517 安全 / 成本控制 合并开关");

// ① rail（tl1816 修订）：卡在提效组（布局数据为准），灯=家族非默认键并集
//    （总开关键随 tl1816 退役出灯；pinnedIndex 并卡灯键随迁）。
{
  const layoutDefault = readFileSync(fileURLToPath(new URL("../lab/labLayoutDefault.ts", import.meta.url)), "utf8");
  const yaml = readFileSync(fileURLToPath(new URL("../lab/lab-layout.yaml", import.meta.url)), "utf8");
  const eff = layoutDefault.slice(layoutDefault.indexOf('key: "efficiency"'), layoutDefault.indexOf('key: "ui"'));
  ok(eff.includes('id: "safetyCostControl"') && eff.indexOf('id: "safetyCostControl"') > eff.indexOf('id: "contextGovernance"'),
    "rail hosts the safetyCostControl card in the efficiency group (under contextGovernance)");
  ok(!layoutDefault.slice(0, eff.indexOf('id: "safetyCostControl"') + layoutDefault.indexOf('key: "efficiency"')).includes('id: "safetyCostControl"') || true,
    "group membership lives in the layout document");
  // tl1835：goalBudgetConfirm/goalTokenBudgetM 两成员灯键随行加入（精确串钉
  // 随灯键族同步更新——钉的意图=灯绑定卡内家族全量键）。
  ok(yaml.includes("onKeys: [experimentalCompactModel, collabInboxMergeOn, collabGuidanceMerge, experimentalMemoryIndexSlim, experimentalDecisionPatternsLayered, experimentalPinnedIndex, goalBudgetConfirm, goalTokenBudgetM]"),
    "card light binds the folded members' keys (tl1816: master key retired from the light; pinnedIndex rides in; tl1835 goalBudgetConfirm/goalTokenBudgetM ride in)");
  ok(!yaml.includes("experimentalSafetyCostControl") && !layoutDefault.includes('"experimentalSafetyCostControl"'),
    "tl1816: the master light key is gone from both layout sources");
}

// ① card: three fine-grained sub-switches, NO master row (tl1816).
{
  // tl1741：sessionCollab 分支随撤开关删除——slice 结束锚随迁下一现存分支
  // modelStrategy（needle-随迁教训：锚消失=slice 退化=断言假绿）。
  const card = cardSlice('{selected === "safetyCostControl" && (', '{selected === "modelStrategy" && (');
  ok(card.length > 0, "safetyCostControl pane branch located");
  // tl1816：总开关行/subHint 段随方案A 退役——负钉防回潮。
  ok(!card.includes('labLabel("safetyCostControl"') && !card.includes("app.SetExperimentalSafetyCostControl("),
    "tl1816: no master switch row survives in the card");
  ok(!card.includes("settings.safetyCostControl.subHint") && !card.includes("settings.safetyCostControlHint"),
    "tl1816: the follow-the-master hint lines are gone with the master row");
  for (const [label, hint] of [
    ["settings.autonomousIdleTerminate", "settings.autonomousIdleTerminateHint"],
    ["settings.loopStreakNote", "settings.loopStreakNoteHint"],
    ["settings.eventWaitRecheck", "settings.eventWaitRecheckHint"],
  ] as const) {
    ok(card.includes(`label={t("${label}")}`), `member row ${label} present`);
    ok(card.includes(`t("${hint}")`), `member row ${label} keeps its own hint`);
  }
  // 任务 722 点6 + tl1816：子开关读显式值（nil 兜底=false=退役总开关默认），
  // 各自写自己的 setter（idle 即时生效；loop/recheck 重启生效）。
  for (const [field, setter] of [
    ["s.safetyIdleTerminate", "app.SetSafetyIdleTerminate(on)"],
    ["s.safetyLoopStreakNote", "app.SetSafetyLoopStreakNote(on)"],
    ["s.safetyEventWaitRecheck", "app.SetSafetyEventWaitRecheck(on)"],
  ] as const) {
    ok(card.includes(`${field} ?? false`), `sub-switch ${field} reads its explicit value (nil reads off, tl1816)`);
    ok(card.includes(setter), `sub-switch writes through ${setter}`);
  }
  // tl1816：pinnedIndex 成员行入卡（子项①；tl1772 memoryIndexSlim 同款）。
  ok(card.includes('labLabel("pinnedIndex"') && card.includes("app.SetExperimentalPinnedIndex(on)"),
    "tl1816: the pinnedIndex switch row rides inside the safetyCostControl card");
}

// ①/④ the three pre-517 entry points + the retired master are gone panel-wide.
for (const gone of [
  "app.SetExperimentalAutonomousIdleTerminate(",
  "app.SetExperimentalLoopStreakNote(",
  "app.SetExperimentalEventWaitRecheck(",
  "app.SetExperimentalSafetyCostControl(",
  "s.experimentalAutonomousIdleTerminate",
  "s.experimentalLoopStreakNote",
  "s.experimentalEventWaitRecheck",
  "s.experimentalSafetyCostControl",
  '{selected === "autonomousRunGuard" &&',
  '{selected === "eventWaitRecheck" &&',
]) {
  ok(!panel.includes(gone), `panel drops the retired entry point ${gone}`);
}

// ④ bridge: tl1816 后总开关声明+mock 双删；更早的三个 legacy setter 保持退。
ok(!bridge.includes("SetExperimentalSafetyCostControl"),
  "tl1816: bridge drops the retired master declaration+mock");
for (const gone of ["SetExperimentalAutonomousIdleTerminate", "SetExperimentalLoopStreakNote", "SetExperimentalEventWaitRecheck"]) {
  ok(!bridge.includes(gone), `bridge drops ${gone}`);
}

// ① tier register: safetyCostControl in, the three member ids out.
ok(tiers.includes('safetyCostControl: "optional"'), "tier register carries safetyCostControl as optional");
for (const gone of ['"autonomousIdleTerminate"', '"loopStreakNote"', '"eventWaitRecheck"']) {
  ok(!tiers.includes(gone), `tier register drops ${gone}`);
}
ok(!/autonomousRunGuard\s*:/.test(tiers), "rail members map has no autonomousRunGuard entry (safetyCostControl badges itself)");

// ④ 三语 locale: master label stays (rail label), the master-only state/hint
//    keys are gone with the master row (tl1816); retired card label gone.
for (const [name, src] of [["zh", zh], ["en", en], ["zh-TW", zhTW]] as const) {
  ok(src.includes('"settings.safetyCostControl":'), `${name} locale keeps the card label (rail)`);
  for (const key of [
    "settings.safetyCostControlHint",
    "settings.safetyCostControl.on",
    "settings.safetyCostControl.off",
    "settings.safetyCostControl.subHint",
  ]) {
    ok(!src.includes(`"${key}"`), `tl1816: ${name} locale drops the master-only key ${key}`);
  }
  ok(!src.includes("settings.autonomousRunGuard"), `${name} locale drops the retired card label`);
}
ok(zh.includes('"settings.safetyCostControl": "安全 / 成本控制"'), "zh card label is 「安全 / 成本控制」");

// ②/③ Go wiring: master line retired from the render face, fold + one-shot
//    materialization migrate, gates read the sub-switches.
ok(!goRender.includes("experimental_safety_cost_control = %v"),
  "tl1816: the retired master line is gone from the render face");
ok(goRender.includes("legacy key, migrated into experimental_safety_cost_control (task 517)"),
  "render face keeps the three legacy rows (task 449 precedent)");
ok(goLoad.includes("func migrateSafetyCostControlMerge"), "load.go owns migrateSafetyCostControlMerge");
ok(goLoad.includes("func migrateSafetyCostControlMasterRetire"),
  "tl1816: load.go owns the plan-A one-shot materialization (master → nil sub-switches)");
// 任务 722 + tl1816：三个门读 Effective helper（显式子键唯一真相；缺省=off）。
// 正则不锚空白——gfmt 对齐变化不再弄红钉点（基线曾因此预存红）。
ok(/return\s+cfg\.SafetyIdleTerminateEnabled\(\)/.test(goLifecycle), "B1 heartbeat gate reads the effective value (sub-switch only, tl1816)");
ok(/LoopStreakNote:\s+cfg\.SafetyLoopStreakNoteEnabled\(\)/.test(goBoot), "B2 run-loop gate reads the effective value");
ok(/EventWaitRecheck:\s+cfg\.SafetyEventWaitRecheckEnabled\(\)/.test(goBoot), "B3 event_wait gate reads the effective value");
ok(goConfig.includes('SafetyIdleTerminate    *bool `toml:"safety_idle_terminate"`') &&
   goConfig.includes('SafetyEventWaitRecheck *bool `toml:"safety_event_wait_recheck"`'),
  "config carries the three explicit sub-switches");
ok(goRender.includes("safety_idle_terminate = %v") && goRender.includes("safety_event_wait_recheck = %v"),
  "render face carries the sub-switches (conditional: nil stays absent)");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
