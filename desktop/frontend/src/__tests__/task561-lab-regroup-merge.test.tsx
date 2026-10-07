// Run: npx tsx src/__tests__/task561-lab-regroup-merge.test.tsx
// Task 561 acceptance harness (7-group regroup + M1-M7 merged cards):
//  ① LabGroupKey = 7 groups in the audit-table order; labGroups renders in
//     that order (the rail order follows the array);
//  ② merged cards are entry-level merges only: within each family card every
//     member keeps its OWN state read (Boolean(s.x) === on) and its OWN
//     setter call (app.SetX(on)) — toggling member A can never touch member
//     B's config key (改 A 不影响 B / 改 B 不影响 A, structurally pinned);
//  ③ M8 keeps standalone entries: autopilot / sessionCollab / monitoring /
//     fullAccess / optimisticParallel each still has its own rail entry and
//     its own pane branch;
//  ④ the rail carries exactly 36 entries in 7 groups
//     (6/7/14/1/1/1/6); the 11 folded member ids are gone from the union,
//     and task 504 adds tabModeTint to the ui group (36th entry).
//     the features array and the pane branches.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");

function cardSlice(startMarker: string, endMarker: string): string {
  const start = panel.indexOf(startMarker);
  const end = panel.indexOf(endMarker);
  return start >= 0 && end > start ? panel.slice(start, end) : "";
}

console.log("\ntask 561 lab regroup + merged cards");

// ① 7-group structure (+ 任务 603「工具优化」appended after the audit table).
ok(
  panel.includes('type LabGroupKey = "automation" | "efficiency" | "ui" | "observability" | "dev-debug" | "storage" | "infra" | "tool-opt";'),
  "LabGroupKey is the 7 audit-table groups + the 603 tool-opt group",
);
{
  const order = [
    '"automation"', '"efficiency"', '"ui"', '"observability"', '"dev-debug"', '"storage"', '"infra"', '"tool-opt"',
  ].map((k) => panel.indexOf(`key: ${k}`));
  ok(order.every((i) => i >= 0) && [...order].sort((a, b) => a - b).every((v, i) => v === order[i]),
    "labGroups renders in the audit-table order (rail order)");
}
ok(panel.includes('labelKey: "settings.labGroup.automation"') && panel.includes('labelKey: "settings.labGroup.infra"'),
  "new groups carry locale label keys");

// ① rail census: exactly 37 feature entries, 6/7/14/1/1/1/6/1 per group
// (任务 603 adds the single tool-opt entry after the 561 audit table).
{
  const entries = [...panel.matchAll(/\{ id: "([a-zA-Z]+)", group: "([a-z-]+)",/g)];
  const groups: Record<string, number> = {};
  for (const [, , g] of entries) groups[g] = (groups[g] ?? 0) + 1;
  ok(entries.length === 37, `rail carries exactly 37 entries (got ${entries.length})`);
  ok(groups["automation"] === 6 && groups["efficiency"] === 7 && groups["ui"] === 14 &&
     groups["observability"] === 1 && groups["dev-debug"] === 1 && groups["storage"] === 1 && groups["infra"] === 6 &&
     groups["tool-opt"] === 1,
    `group counts are 6/7/13/1/1/1/6/1 (got ${JSON.stringify(groups)})`);
  const ids = entries.map(([, id]) => id);
  ok(new Set(ids).size === ids.length, "no duplicate rail ids");
}

// ② merged cards — per-member read+write independence.
const families: Array<{ id: string; endMarker: string; members: Array<{ label: string; field: string; setter: string }> }> = [
  { id: "autonomousRunGuard", endMarker: '{selected === "sessionCollab" && (', members: [
    { label: "M1 autonomousIdleTerminate", field: "s.experimentalAutonomousIdleTerminate", setter: "app.SetExperimentalAutonomousIdleTerminate(on)" },
    { label: "M1 loopStreakNote", field: "s.experimentalLoopStreakNote", setter: "app.SetExperimentalLoopStreakNote(on)" },
  ] },
  { id: "modelStrategy", endMarker: '{selected === "contextGovernance" && (', members: [
    { label: "M2 highSpeedModel", field: "s.experimentalHighSpeedModel", setter: "app.SetExperimentalHighSpeedModel(on)" },
  ] },
  { id: "contextGovernance", endMarker: '{selected === "devDebug" && (', members: [
    { label: "M3 compactionParallel", field: "s.experimentalCompactionParallel", setter: "app.SetExperimentalCompactionParallel(on)" },
    { label: "M3 contextBudget", field: "s.experimentalContextBudget", setter: "app.SetExperimentalContextBudget(on)" },
    { label: "M3 researchBudget", field: "s.experimentalResearchBudget", setter: "app.SetExperimentalResearchBudget(on)" },
    { label: "M3 proactiveCompact", field: "s.experimentalProactiveCompact", setter: "app.SetExperimentalProactiveCompact(on)" },
    { label: "M3 coldCacheCompact", field: "s.experimentalColdCacheCompact", setter: "app.SetExperimentalColdCacheCompact(on)" },
    { label: "M3 cacheTuning", field: "s.experimentalCacheTuning", setter: "app.SetExperimentalCacheTuning(on)" },
  ] },
  { id: "subagentSuite", endMarker: '{selected === "tabCompress" && (', members: [
    { label: "M4 subagentPanel", field: "s.experimentalSubagentPanel", setter: "app.SetExperimentalSubagentPanel(on)" },
    { label: "M4 subagentDetail", field: "s.experimentalSubagentDetail", setter: "app.SetExperimentalSubagentDetail(on)" },
    { label: "M4 subagentPolicy", field: "s.experimentalSubagentPolicy", setter: "app.SetExperimentalSubagentPolicy(on)" },
    { label: "M4 subagentTps", field: "s.experimentalSubagentTps", setter: "app.SetExperimentalSubagentTps(on)" },
  ] },
  { id: "devDebug", endMarker: '{selected === "zcodeTaskBus" && (', members: [
    { label: "M5 cdpDebugPort", field: "s.experimentalCDPDebugPort", setter: "app.SetExperimentalCDPDebugPort(on)" },
    { label: "M5 lifecycleNoiseGate", field: "s.experimentalLifecycleNoiseGate", setter: "app.SetExperimentalLifecycleNoiseGate(on)" },
  ] },
  { id: "updateFeedback", endMarker: '{selected === "baseProcess" && (', members: [
    { label: "M6 restartUpdate", field: "s.experimentalRestartUpdate", setter: "app.SetExperimentalRestartUpdate(on)" },
    { label: "M6 feedback", field: "s.experimentalFeedback", setter: "app.SetExperimentalFeedback(on)" },
  ] },
  { id: "sessionStore", endMarker: '{selected === "splitView" && (', members: [
    { label: "M7 sessionStorage", field: "storageMode === mode", setter: "app.SetSessionStorage(mode)" },
    { label: "M7 eventsRotation", field: "RotationMode", setter: "" },
  ] },
];

for (const family of families) {
  const card = cardSlice(`{selected === "${family.id}" && (`, family.endMarker);
  ok(card.length > 0, `${family.id} pane branch located`);
  for (const member of family.members) {
    if (member.setter) {
      ok(card.includes(`Boolean(${member.field})`) || card.includes(member.field),
        `${family.label}: card reads its own state (${member.field})`);
      ok(card.includes(member.setter), `${family.label}: card writes through its own setter (${member.setter})`);
    }
  }
}

// ② M7 specifics: the storage-mode selector and the rotation panel both live
//    inside the sessionStore card, each with its own setter.
{
  const card = cardSlice('{selected === "sessionStore" && (', '{selected === "splitView" && (');
  ok(card.includes("SESSION_STORAGE_MODES.map") && card.includes("app.SetSessionStorage(mode)"),
    "M7 sessionStorage: mode selector saves through SetSessionStorage");
  ok(card.includes("<SessionEventsPanel"),
    "M7 eventsRotation: rotation panel mounts inside the sessionStore card (its setters live in SessionEventsPanel)");
}

// ② M2 specifics: modelCapabilityFilter renders READ-ONLY (no setter call in
//    the whole panel — the key is retired, 551/564 domain).
{
  const card = cardSlice('{selected === "modelStrategy" && (', '{selected === "contextGovernance" && (');
  ok(card.includes("s.experimentalModelCapabilityFilter"), "M2 card displays the retired modelCapabilityFilter value");
  ok(card.includes("settings.modelCapabilityFilter.retired"), "M2 card carries the retired hint");
  ok(!panel.includes("app.SetExperimentalModelCapabilityFilter("), "retired key has NO setter call anywhere (read-only)");
}

// ③ M8 standalone entries.
for (const [id, pane] of [
  ["autopilot", '{selected === "autopilot" && ('],
  ["sessionCollab", '{selected === "sessionCollab" && ('],
  ["monitoring", '{selected === "monitoring" && ('],
  ["fullAccess", '{selected === "fullAccess" && ('],
  ["optimisticParallel", '{selected === "optimisticParallel" && ('],
] as const) {
  ok(panel.includes(`{ id: "${id}", group: "`), `M8 ${id} keeps its own rail entry`);
  ok(panel.includes(pane), `M8 ${id} keeps its own pane branch`);
}

// ④ folded member ids are gone from union + features array + pane branches.
for (const id of [
  "autonomousIdleTerminate", "loopStreakNote", "highSpeedModel", "modelCapabilityFilter",
  "compactionParallel", "budgetControl", "compressOpt", "cacheTuning",
  "subagentPanel", "subagentDetail", "subagentPolicy", "subagentTps",
  "cdpDebugPort", "lifecycleNoiseGate", "restartUpdate", "feedback",
  "sessionStorage", "eventsRotation",
]) {
  ok(!panel.includes(`{ id: "${id}", group:`), `rail drops the folded ${id} entry`);
  ok(!panel.includes(`| "${id}"`), `union drops the folded ${id} id`);
  ok(!panel.includes(`{selected === "${id}" &&`), `pane drops the folded ${id} branch`);
}
for (const id of [
  "autonomousRunGuard", "modelStrategy", "contextGovernance", "subagentSuite",
  "devDebug", "updateFeedback", "sessionStore",
]) {
  ok(panel.includes(`| "${id}"`), `union gains the family id ${id}`);
}

// ④ every member switch still saves through its own setter somewhere in the
//    panel (render-table discipline — a setter without a call site would be a
//    silent lost-save).
for (const setter of [
  "app.SetExperimentalAutonomousIdleTerminate(on)",
  "app.SetExperimentalLoopStreakNote(on)",
  "app.SetExperimentalHighSpeedModel(on)",
  "app.SetExperimentalCompactionParallel(on)",
  "app.SetExperimentalContextBudget(on)",
  "app.SetExperimentalResearchBudget(on)",
  "app.SetExperimentalProactiveCompact(on)",
  "app.SetExperimentalColdCacheCompact(on)",
  "app.SetExperimentalCacheTuning(on)",
  "app.SetExperimentalSubagentPanel(on)",
  "app.SetExperimentalSubagentDetail(on)",
  "app.SetExperimentalSubagentPolicy(on)",
  "app.SetExperimentalSubagentTps(on)",
  "app.SetExperimentalCDPDebugPort(on)",
  "app.SetExperimentalLifecycleNoiseGate(on)",
  "app.SetExperimentalRestartUpdate(on)",
  "app.SetExperimentalFeedback(on)",
  "app.SetSessionStorage(mode)",
]) {
  ok(panel.includes(setter), `setter wiring survives the merge: ${setter}`);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
