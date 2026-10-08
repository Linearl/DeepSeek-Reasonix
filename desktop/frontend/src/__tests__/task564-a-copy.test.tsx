// Run: npx tsx src/__tests__/task564-a-copy.test.tsx
// Task 564 acceptance harness (A-level copy fixes + modelCapabilityFilter
// read-only retention, on top of task 561's group structure):
//  ① pathRules retires UI-side (task 551 paradigm): rail entry stays, the
//     pane renders the M2-style read-only block — stored value shows, the
//     setter disappears panel-wide (a dead toggle must not look toggleable);
//  ② the 561 M2 modelStrategy card keeps its retired modelCapabilityFilter
//     read-only block intact (no setter anywhere — user ruling: keep the
//     page, display only);
//  ③ the A-level hint copy matches audited code behavior in all three
//     locales:
//     - feedback: the submit_feedback tool registers unconditionally
//       (internal/tool/builtin/feedback.go init → tool.Builtins), so the
//       hint may not claim "enables the tool";
//     - autonomousIdleTerminate: evaluateIdleStreak has no production call
//       site (desktop/heartbeat.go), so the hint says reserved / not wired;
//     - baseProcess: internal/baseproc/manager.go states the heavy-base
//       hosting is NOT DONE, so no memory/startup savings are promised;
//     - opencodeGoUsage keeps its "off keeps network behavior unchanged"
//       promise — now backed by the task-564 backend gate
//       (desktop/opencode_go_usage.go openCodeGoUsageSwitchOn);
//  ④ the superseded hint keys are gone from every locale
//     (settings.pathRulesHint, settings.modelCapabilityFilterHint).

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");
const en = readFileSync(fileURLToPath(new URL("../locales/en.ts", import.meta.url)), "utf8");
const zh = readFileSync(fileURLToPath(new URL("../locales/zh.ts", import.meta.url)), "utf8");
const zhTW = readFileSync(fileURLToPath(new URL("../locales/zh-TW.ts", import.meta.url)), "utf8");

function paneSlice(startMarker: string, endMarker: string): string {
  const start = panel.indexOf(startMarker);
  const end = panel.indexOf(endMarker);
  return start >= 0 && end > start ? panel.slice(start, end) : "";
}

console.log("\ntask 564 A-level copy fixes + retired-key read-only retention");

// ① pathRules retired read-only (551 paradigm, UI side).
{
  const pane = paneSlice('{selected === "pathRules" && (', '{selected === "traceAsState" && (');
  ok(pane.includes('t("settings.pathRules.retired")') && pane.includes('t("settings.pathRules.value"'),
    "pathRules pane renders the retired read-only block (hint + stored value)");
  ok(pane.includes("Task 564") && pane.includes("task 551 paradigm"),
    "pathRules pane carries the 564/551 retirement comment");
  ok(!pane.includes("SetExperimentalPathRules") && !pane.includes("set-seg"),
    "pathRules pane has no setter and no toggle segment");
  ok(panel.includes('{ id: "pathRules", group: "infra"') && panel.includes('| "pathRules"'),
    "pathRules keeps its rail entry and union member (page kept, display only)");
  ok((panel.match(/SetExperimentalPathRules\(/g) ?? []).length === 0,
    "panel-wide: zero app.SetExperimentalPathRules( call sites (negative, M2 pattern)");
}

// ② 561 M2 card survives: modelCapabilityFilter read-only block untouched.
{
  const m2 = paneSlice('{selected === "modelStrategy" && (', '{selected === "contextGovernance" && (');
  ok(m2.includes('t("settings.modelCapabilityFilter.retired")') && m2.includes('t("settings.modelCapabilityFilter.value"'),
    "M2 card still renders the modelCapabilityFilter retired block");
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

  // baseProcess: hosting NOT DONE — no savings promised.
  for (const [name, src] of pairs) {
    const hint = src.match(/"settings\.baseProcessHint": "([^"]+)"/)?.[1] ?? "";
    ok(/not implemented yet|尚未实现|尚未實現/.test(hint), `${name} baseProcessHint states hosting is not implemented`);
    ok(!/saving memory|lands with S1b|省内存、加速启动。.*S1a/.test(hint), `${name} baseProcessHint drops the savings/S1b promise`);
  }

  // pathRules: retired keys present, superseded hint keys gone.
  for (const [name, src] of pairs) {
    ok(src.includes('"settings.pathRules.retired"') && src.includes('"settings.pathRules.value"'),
      `${name} carries pathRules.retired + pathRules.value`);
    ok(!src.includes('"settings.pathRulesHint"'), `${name} no longer carries settings.pathRulesHint`);
    ok(!src.includes('"settings.modelCapabilityFilterHint"'), `${name} no longer carries settings.modelCapabilityFilterHint`);
  }

  // opencodeGoUsage: the promise stays and is now true (backend gate).
  for (const [name, src] of pairs) {
    const hint = src.match(/"settings\.opencodeGoUsageHint": "([^"]+)"/)?.[1] ?? "";
    ok(/only while this switch is on|仅开关开启时|僅開關開啟時/.test(hint), `${name} opencodeGoUsageHint keeps the gated-query promise`);
  }
}

// ④ the Go-side gate exists (copy is backed by code).
{
  const go = readFileSync(fileURLToPath(new URL("../../../opencode_go_usage.go", import.meta.url)), "utf8");
  ok(go.includes("openCodeGoUsageSwitchOn") && go.includes("ExperimentalOpenCodeGoUsage"),
    "GetOpenCodeGoUsage consults the lab switch before any network I/O");
}

console.log(`\ntask564: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
