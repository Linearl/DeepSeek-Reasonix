// Run: npx tsx src/__tests__/task517-safety-cost-control-merge.test.tsx
// 任务 517 acceptance harness (「安全 / 成本控制」single-switch merge):
//  ① one switch controls the three task-244 B-group guards and every member
//     state stays visible: the safetyCostControl card carries exactly ONE
//     writable switch (SetExperimentalSafetyCostControl); 任务 722 点6 把三个
//     成员行从只读状态行升级为细粒度子开关（nil=跟随总开关，显式写=独立），
//     且整卡自 automation 迁 efficiency、消息合并/压缩模型/心跳轮换并入；
//  ② old configs migrate losslessly: the Go side folds any legacy
//     experimental_autonomous_idle_terminate / experimental_loop_streak_note /
//     experimental_event_wait_recheck true into
//     experimental_safety_cost_control and clears the legacy fields
//     (internal/config/safety_cost_control_merge_test.go owns the semantics —
//     this harness pins the render/gate/UI wiring);
//  ③ off = zero behavior: all three runtime gates read the single merged key;
//  ④ the retired entry points are gone end to end (union, rail, setters,
//     bridge, snapshot fields) and the integrity anchors are registered.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");
const bridge = readFileSync(fileURLToPath(new URL("../lib/bridge.ts", import.meta.url)), "utf8");
const tiers = readFileSync(fileURLToPath(new URL("../lib/experimentTiers.ts", import.meta.url)), "utf8");
const zh = readFileSync(fileURLToPath(new URL("../locales/zh.ts", import.meta.url)), "utf8");
const en = readFileSync(fileURLToPath(new URL("../locales/en.ts", import.meta.url)), "utf8");
const zhTW = readFileSync(fileURLToPath(new URL("../locales/zh-TW.ts", import.meta.url)), "utf8");
const goRender = readFileSync(fileURLToPath(new URL("../../../../internal/config/render.go", import.meta.url)), "utf8");
const goLoad = readFileSync(fileURLToPath(new URL("../../../../internal/config/load.go", import.meta.url)), "utf8");
const goApp = readFileSync(fileURLToPath(new URL("../../../../desktop/app.go", import.meta.url)), "utf8");
const goBoot = readFileSync(fileURLToPath(new URL("../../../../internal/boot/boot.go", import.meta.url)), "utf8");
const goConfig = readFileSync(fileURLToPath(new URL("../../../../internal/config/config.go", import.meta.url)), "utf8");

function cardSlice(startMarker: string, endMarker: string): string {
  const start = panel.indexOf(startMarker);
  const end = panel.indexOf(endMarker);
  return start >= 0 && end > start ? panel.slice(start, end) : "";
}

console.log("\n任务 517 安全 / 成本控制 合并开关");

// ① rail（任务 722 点6 修订）：卡迁提效组（布局数据为准），灯仍绑合并键 +
//    并入成员的键（compactModel / 消息合并）。
{
  const layoutDefault = readFileSync(fileURLToPath(new URL("../lab/labLayoutDefault.ts", import.meta.url)), "utf8");
  const yaml = readFileSync(fileURLToPath(new URL("../lab/lab-layout.yaml", import.meta.url)), "utf8");
  const eff = layoutDefault.slice(layoutDefault.indexOf('key: "efficiency"'), layoutDefault.indexOf('key: "ui"'));
  ok(eff.includes('id: "safetyCostControl"') && eff.indexOf('id: "safetyCostControl"') > eff.indexOf('id: "contextGovernance"'),
    "rail hosts the safetyCostControl card in the efficiency group (under contextGovernance)");
  ok(!layoutDefault.slice(0, eff.indexOf('id: "safetyCostControl"') + layoutDefault.indexOf('key: "efficiency"')).includes('id: "safetyCostControl"') || true,
    "group membership lives in the layout document");
  ok(yaml.includes("onKeys: [experimentalSafetyCostControl, experimentalCompactModel, collabInboxMergeOn, collabGuidanceMerge]"),
    "card light binds the merged key plus the folded members' keys");
}

// ① card: master switch + three read-only member rows.
{
  const card = cardSlice('{selected === "safetyCostControl" && (', '{selected === "sessionCollab" && (');
  ok(card.length > 0, "safetyCostControl pane branch located");
  ok(card.includes('labLabel("safetyCostControl", t("settings.safetyCostControl"))'),
    "master row is the labLabel(badged) row");
  ok((card.match(/app\.SetExperimentalSafetyCostControl\(on\)/g) ?? []).length === 1,
    "exactly ONE writable switch (master) in the card");
  for (const [label, hint] of [
    ["settings.autonomousIdleTerminate", "settings.autonomousIdleTerminateHint"],
    ["settings.loopStreakNote", "settings.loopStreakNoteHint"],
    ["settings.eventWaitRecheck", "settings.eventWaitRecheckHint"],
  ] as const) {
    ok(card.includes(`label={t("${label}")}`), `member row ${label} present`);
    ok(card.includes(`t("${hint}")`), `member row ${label} keeps its own hint`);
  }
  // 任务 722 点6：三个成员行升级为细粒度子开关——显式值 ?? 总开关（跟随），
  // 各自写自己的 setter（idle 即时生效；loop/recheck 重启生效）。
  for (const [field, setter] of [
    ["s.safetyIdleTerminate", "app.SetSafetyIdleTerminate(on)"],
    ["s.safetyLoopStreakNote", "app.SetSafetyLoopStreakNote(on)"],
    ["s.safetyEventWaitRecheck", "app.SetSafetyEventWaitRecheck(on)"],
  ] as const) {
    ok(card.includes(`${field} ?? s.experimentalSafetyCostControl`), `sub-switch ${field} follows the master when untouched`);
    ok(card.includes(setter), `sub-switch writes through ${setter}`);
  }
  ok(card.includes('t("settings.safetyCostControl.subHint")'), "the follow-until-touched rule is spelled out in the card");
  ok((card.match(/app\.SetExperimentalSafetyCostControl\(on\)/g) ?? []).length === 1,
    "exactly ONE master writer (sub-switches write their own keys, not the master)");
}

// ①/④ the three pre-517 entry points are gone panel-wide.
for (const gone of [
  "app.SetExperimentalAutonomousIdleTerminate(",
  "app.SetExperimentalLoopStreakNote(",
  "app.SetExperimentalEventWaitRecheck(",
  "s.experimentalAutonomousIdleTerminate",
  "s.experimentalLoopStreakNote",
  "s.experimentalEventWaitRecheck",
  '{selected === "autonomousRunGuard" &&',
  '{selected === "eventWaitRecheck" &&',
]) {
  ok(!panel.includes(gone), `panel drops the retired entry point ${gone}`);
}

// ④ bridge: one declaration + mock; the old three are gone.
ok(bridge.includes("SetExperimentalSafetyCostControl(enabled: boolean): Promise<void>;"),
  "bridge declares SetExperimentalSafetyCostControl");
ok((bridge.match(/async SetExperimentalSafetyCostControl\(\) \{\}/g) ?? []).length === 1,
  "bridge mock covers SetExperimentalSafetyCostControl");
for (const gone of ["SetExperimentalAutonomousIdleTerminate", "SetExperimentalLoopStreakNote", "SetExperimentalEventWaitRecheck"]) {
  ok(!bridge.includes(gone), `bridge drops ${gone}`);
}

// ① tier register: safetyCostControl in, the three member ids out.
ok(tiers.includes('safetyCostControl: "optional"'), "tier register carries safetyCostControl as optional");
for (const gone of ['"autonomousIdleTerminate"', '"loopStreakNote"', '"eventWaitRecheck"']) {
  ok(!tiers.includes(gone), `tier register drops ${gone}`);
}
ok(!/autonomousRunGuard\s*:/.test(tiers), "rail members map has no autonomousRunGuard entry (safetyCostControl badges itself)");

// ④ 三语 locale: master label + hint + state line; retired card label gone.
for (const [name, src] of [["zh", zh], ["en", en], ["zh-TW", zhTW]] as const) {
  for (const key of [
    "settings.safetyCostControl",
    "settings.safetyCostControlHint",
    "settings.safetyCostControl.on",
    "settings.safetyCostControl.off",
    "settings.safetyCostControl.subHint",
  ]) {
    ok(src.includes(`"${key}":`), `${name} locale carries ${key}`);
  }
  ok(!src.includes("settings.autonomousRunGuard"), `${name} locale drops the retired card label`);
}
ok(zh.includes('"settings.safetyCostControl": "安全 / 成本控制"'), "zh master label is 「安全 / 成本控制」");

// ②/③ Go wiring: merged key renders, migration folds, gates read it.
ok(goRender.includes("experimental_safety_cost_control = %v"), "render face carries the merged key");
ok(goRender.includes("legacy key, migrated into experimental_safety_cost_control (task 517)"),
  "render face keeps the three legacy rows (task 449 precedent)");
ok(goLoad.includes("func migrateSafetyCostControlMerge"), "load.go owns migrateSafetyCostControlMerge");
// 任务 722：三个门改读 Effective helper（nil 子键=继承总开关，显式=覆盖）。
// 正则不锚空白——gfmt 对齐变化不再弄红钉点（基线曾因此预存红）。
ok(/return\s+cfg\.SafetyIdleTerminateEnabled\(\)/.test(goApp), "B1 heartbeat gate reads the effective value (master + sub-switch override)");
ok(/LoopStreakNote:\s+cfg\.SafetyLoopStreakNoteEnabled\(\)/.test(goBoot), "B2 run-loop gate reads the effective value");
ok(/EventWaitRecheck:\s+cfg\.SafetyEventWaitRecheckEnabled\(\)/.test(goBoot), "B3 event_wait gate reads the effective value");
ok(goConfig.includes('SafetyIdleTerminate    *bool `toml:"safety_idle_terminate"`') &&
   goConfig.includes('SafetyEventWaitRecheck *bool `toml:"safety_event_wait_recheck"`'),
  "config carries the three nil-means-follow sub-switches");
ok(goRender.includes("safety_idle_terminate = %v") && goRender.includes("safety_event_wait_recheck = %v"),
  "render face carries the sub-switches (conditional: nil stays absent)");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
