// Run: npx tsx src/__tests__/task517-safety-cost-control-merge.test.tsx
// 任务 517 acceptance harness (「安全 / 成本控制」single-switch merge):
//  ① one switch controls the three task-244 B-group guards and every member
//     state stays visible: the safetyCostControl card carries exactly ONE
//     writable switch (SetExperimentalSafetyCostControl) plus three read-only
//     member rows, each showing its own 开/关 state and its own hint;
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

function cardSlice(startMarker: string, endMarker: string): string {
  const start = panel.indexOf(startMarker);
  const end = panel.indexOf(endMarker);
  return start >= 0 && end > start ? panel.slice(start, end) : "";
}

console.log("\n任务 517 安全 / 成本控制 合并开关");

// ① rail: one automation-group entry for the merged card.
ok(panel.includes('{ id: "safetyCostControl", group: "automation", label: t("settings.safetyCostControl"), on: Boolean(s.experimentalSafetyCostControl) }'),
  "rail carries the safetyCostControl entry bound to the merged key");

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
  ok((card.match(/settings\.safetyCostControl\.memberState/g) ?? []).length === 3,
    "all three member rows render their state line (各态可见)");
  ok((card.match(/Boolean\(s\.experimentalSafetyCostControl\)/g) ?? []).length >= 4,
    "master + members all read the single merged key");
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
    "settings.safetyCostControl.memberState",
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
ok(goApp.includes("cfg.Agent.ExperimentalSafetyCostControl"), "B1 heartbeat gate reads the merged key");
ok(goBoot.includes("LoopStreakNote:     cfg.Agent.ExperimentalSafetyCostControl"), "B2 run-loop gate reads the merged key");
ok(goBoot.includes("EventWaitRecheck:  cfg.Agent.ExperimentalSafetyCostControl"), "B3 event_wait gate reads the merged key");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
