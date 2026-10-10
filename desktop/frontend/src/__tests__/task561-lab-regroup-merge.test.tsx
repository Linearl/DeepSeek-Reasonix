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
//  ④ the rail carries exactly 37 entries in 8 groups — 任务 722 后为
//     4/8/15/1/1/1/6/1（safetyCostControl 自 automation 迁 efficiency 并吸收
//     messageMerge；sessionCollabAutoFold 并入 sessionCollab；727
//     heartbeatRotation 以成员入 safetyCostControl，不占 rail 入口）。
//     任务 722/724：render table 移交布局默认数据（labLayoutDefault.ts），
//     分组/成员/徽章/排序由 lab-layout.yaml（校验回退默认）驱动——本测试的
//     分组断言读布局数据与 yaml，卡片独立性断言仍读 SettingsPanel 源。

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

// ① 8-group structure（任务 722/724 修订）：组受控词表在 labLayoutTypes，
//    组序/组键/组标签键全部来自布局文档（yaml 与内置默认一致）。
{
  const yaml = readFileSync(fileURLToPath(new URL("../lab/lab-layout.yaml", import.meta.url)), "utf8");
  const labDefault = readFileSync(fileURLToPath(new URL("../lab/labLayoutDefault.ts", import.meta.url)), "utf8");
  const keys = [
    "automation", "efficiency", "ui", "observability", "dev-debug", "storage", "infra", "tool-opt",
  ];
  const yamlOrder = keys.map((k) => yaml.indexOf(`key: ${k}`));
  ok(yamlOrder.every((i) => i >= 0) && [...yamlOrder].sort((a, b) => a - b).every((v, i) => v === yamlOrder[i]),
    "lab-layout.yaml carries the 8 groups in the audit-table order (rail order)");
  for (const [key, labelKey] of [
    ["automation", "settings.labGroup.automation"],
    ["tool-opt", "settings.labGroup.toolOpt"],
    ["dev-debug", "settings.labGroup.devDebug"],
  ] as const) {
    const block = yaml.slice(yaml.indexOf(`key: ${key}`));
    ok(block.includes(`labelKey: ${labelKey}`), `group ${key} carries locale label key ${labelKey}`);
  }
  const defOrder = keys.map((k) => labDefault.indexOf(`key: "${k}"`));
  ok(defOrder.every((i) => i >= 0) && [...defOrder].sort((a, b) => a - b).every((v, i) => v === defOrder[i]),
    "built-in default layout mirrors the same group order (fallback = same face)");
}

// ① rail census（任务 722 修订）：恰好 37 个 rail 入口，组分布
// 4/8/15/1/1/1/6/1（safetyCostControl 迓提效并吸收 messageMerge 入口；
// sessionCollabAutoFold 并入 sessionCollab——两入口消失；其余组不动）。
// 数据源 = 布局默认数据 + yaml（两份必须同构）。
{
  // 直接导入布局数据模块数真数据（比源码正则稳）。
  const { LAB_LAYOUT_DEFAULT_DATA } = await import("../lab/labLayoutDefault");
  const defEntries = LAB_LAYOUT_DEFAULT_DATA.groups.flatMap((g: { entries: Array<{ id: string }> }) => g.entries.map((e) => e.id));
  ok(defEntries.length === 37, `default layout carries exactly 37 rail entries (got ${defEntries.length})`);
  // yaml 与内置默认同构同序（yaml 用 6 空格缩进标 entries、10 空格标 members）。
  const yamlText = readFileSync(fileURLToPath(new URL("../lab/lab-layout.yaml", import.meta.url)), "utf8");
  const yamlEntries = [...yamlText.matchAll(/^ {6}- id: (\w+)$/gm)].map((m) => m[1]);
  ok(defEntries.length === yamlEntries.length && defEntries.every((id, i) => id === yamlEntries[i]),
    `yaml and built-in default list the same entries in the same order (yaml ${yamlEntries.length})`);
  const groups: Record<string, number> = {};
  for (const g of LAB_LAYOUT_DEFAULT_DATA.groups) groups[(g as { key: string }).key] = g.entries.length;
  ok(groups["automation"] === 4 && groups["efficiency"] === 8 && groups["ui"] === 15 &&
     groups["observability"] === 1 && groups["dev-debug"] === 1 && groups["storage"] === 1 && groups["infra"] === 6 &&
     groups["tool-opt"] === 1,
    `group counts are 4/8/15/1/1/1/6/1 (got ${JSON.stringify(groups)})`);
  ok(new Set(defEntries).size === defEntries.length, "no duplicate rail ids");
  // 任务 722 六点：safetyCostControl 在提效组；sessionCollabAutoFold /
  // messageMerge / compactModel 以成员身份存在，不再占 rail 入口。
  const eff = LAB_LAYOUT_DEFAULT_DATA.groups.find((g: { key: string }) => g.key === "efficiency");
  const safety = eff!.entries.find((e: { id: string }) => e.id === "safetyCostControl");
  const memberIds = (safety!.members ?? []).map((m: { id: string }) => m.id);
  ok(["messageMerge", "compactModel", "heartbeatRotation"].every((x) => memberIds.includes(x)),
    "safetyCostControl members carry messageMerge + compactModel + heartbeatRotation (722 点5/6 + 727)");
  const collab = LAB_LAYOUT_DEFAULT_DATA.groups.find((g: { key: string }) => g.key === "automation")!
    .entries.find((e: { id: string }) => e.id === "sessionCollab");
  ok((collab!.members ?? []).some((m: { id: string }) => m.id === "sessionCollabAutoFold"),
    "sessionCollab hosts the auto-fold member (722 点3)");
}

// ② merged cards — per-member read+write independence.
// 任务 517：M1 autonomousRunGuard 卡不复存在——B1/B2/B3 合并为单键卡
// safetyCostControl（键级合并，非入口级），卡内唯一可写面是总开关。
const families: Array<{ id: string; endMarker: string; members: Array<{ label: string; field: string; setter: string }> }> = [
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
    // 任务 670: fork 首启提示（默认开，用户裁决）入卡，独立开关独立 setter。
    { label: "670 forkNotice", field: "s.forkNotice", setter: "app.SetDesktopForkNotice(on)" },
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

// ③ M8 standalone entries（任务 722/724 修订）：rail 入口断言读布局默认
// 数据（入口 id + 自有 onKeys），pane 分支断言仍读 SettingsPanel。
{
  const { LAB_LAYOUT_DEFAULT_DATA } = await import("../lab/labLayoutDefault");
  const entryIds = new Set(LAB_LAYOUT_DEFAULT_DATA.groups.flatMap((g: { entries: Array<{ id: string }> }) => g.entries.map((e) => e.id)));
  for (const [id, pane] of [
    ["autopilot", '{selected === "autopilot" && ('],
    ["sessionCollab", '{selected === "sessionCollab" && ('],
    ["monitoring", '{selected === "monitoring" && ('],
    ["fullAccess", '{selected === "fullAccess" && ('],
    ["optimisticParallel", '{selected === "optimisticParallel" && ('],
  ] as const) {
    ok(entryIds.has(id), `M8 ${id} keeps its own rail entry`);
    ok(panel.includes(pane), `M8 ${id} keeps its own pane branch`);
  }
}

// ④ folded member ids are gone from union + features array + pane branches.
for (const id of [
  "autonomousIdleTerminate", "loopStreakNote", "highSpeedModel", "modelCapabilityFilter",
  "compactionParallel", "budgetControl", "compressOpt", "cacheTuning",
  "subagentPanel", "subagentDetail", "subagentPolicy", "subagentTps",
  "cdpDebugPort", "lifecycleNoiseGate", "restartUpdate", "feedback",
  "sessionStorage", "eventsRotation",
  // 任务 517：eventWaitRecheck 并入 safetyCostControl，autonomousRunGuard 卡消亡。
  "eventWaitRecheck",
  // 任务 722：messageMerge 并入安全/成本控制卡；sessionCollabAutoFold 并入
  // 跨会话协作卡——两个独立入口/pane/union id 摘除（成员行+成员徽章承载）。
  "messageMerge",
  "sessionCollabAutoFold",
]) {
  ok(!panel.includes(`{ id: "${id}", group:`), `rail drops the folded ${id} entry`);
  ok(!panel.includes(`| "${id}"`), `union drops the folded ${id} id`);
  ok(!panel.includes(`{selected === "${id}" &&`), `pane drops the folded ${id} branch`);
}
for (const id of [
  "safetyCostControl", "modelStrategy", "contextGovernance", "subagentSuite",
  "devDebug", "updateFeedback", "sessionStore",
]) {
  ok(panel.includes(`| "${id}"`), `union gains the family id ${id}`);
}

// ④ every member switch still saves through its own setter somewhere in the
//    panel (render-table discipline — a setter without a call site would be a
//    silent lost-save).
for (const setter of [
  // 任务 517：B1/B2/B3 三个旧 setter 撤销，写路径并入 SetExperimentalSafetyCostControl。
  "app.SetExperimentalSafetyCostControl(on)",
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
