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
//  ④ the rail carries exactly 35 entries in 8 groups — 任务 722 后为
//     4/8/15/1/1/1/6/1（safetyCostControl 自 automation 迁 efficiency 并吸收
//     messageMerge；sessionCollabAutoFold 并入 sessionCollab；727
//     heartbeatRotation 以成员入 safetyCostControl，不占 rail 入口）；
//     任务 742 后为 5/8/14/1/1/1/6/1（automation 增 heartbeatBackground，
//     ui 组 tab 二开关合并为 tabManagement——一增一减总数不变）；
//     任务 775 后为 5/8/14/1/1/3/6/1=39（存储组增两张 kind:param 参数卡）；
//     任务 782 后为 5/8/9/1/1/3/6/1=34（界面组三张条目级合并卡：
//     searchHistory/inputSelection/viewLayout 七独立入口并三卡 -5；
//     opencodeGoUsage 迁出界面组 → 783 可观测性组用量监测卡）；
//     任务 783 后为 5/8/9/2/1/3/6/1=35（可观测性组增「用量监测」卡：
//     GLM Plan 用量 + OpenCode Go 用量，opencodeGoUsage 独立 pane 退役为
//     成员行）。
//     任务 785 后为 5/8/9/2/1/3/5/1=34（S1 常驻底座开关条目随底座退役，infra 6→5）。
//     任务 790 后为 5/8/9/2/1/3/6/1=35（acpListen ACP 常驻监听入 rail，infra 5→6）。
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

// ① rail census（任务 783 修订）：恰好 35 个 rail 入口，组分布
// 5/8/9/2/1/3/6/1（783：可观测性组增「用量监测」卡——GLM Plan 用量 +
// OpenCode Go 用量，opencodeGoUsage 独立 pane 退役为成员行；782：界面组
// promptHistoryPicker+questionSearch+autoLoadOlder 并为 searchHistory、
// draftPersistence+selectionActions 并为 inputSelection、splitView+
// trajectoryView 并为 viewLayout——七独立入口并三卡净 -5；775：存储组增
// 两张 kind:param 参数卡；742：automation 增 heartbeatBackground 入口；
// 722：safetyCostControl 迁提效并吸收 messageMerge 入口）。
// 任务 785：S1 常驻底座开关随底座退役，infra 6→5 → 总数 34。
// 任务 790：acpListen 入 rail，infra 5→6 → 总数 35。
// 任务 815（登记窗口 pin 收口）：801 decisionPatterns（automation 5→6）与
// 846 homePointer（storage 3→4）此前已入布局而本 pin 仍钉 783 口径——
// 总数 35→37，组分布 6/8/9/2/1/4/6/1。
// 任务 686：workflowPanel（工作流可视化面板）入 ui 组——总数 37→38，
// 组分布 6/8/10/2/1/4/6/1。
// 任务 944：renderArtifact（对话内产物卡片）入 ui 组——总数 38→39，
// 组分布 ui 10→11（6/8/11/2/1/4/6/1）。
// 数据源 = 布局默认数据 + yaml（两份必须同构）。
{
  // 直接导入布局数据模块数真数据（比源码正则稳）。
  const { LAB_LAYOUT_DEFAULT_DATA } = await import("../lab/labLayoutDefault");
  const defEntries = LAB_LAYOUT_DEFAULT_DATA.groups.flatMap((g: { entries: Array<{ id: string }> }) => g.entries.map((e) => e.id));
  ok(defEntries.length === 39, `default layout carries exactly 39 rail entries (got ${defEntries.length})`);
  // yaml 与内置默认同构同序（yaml 用 6 空格缩进标 entries、10 空格标 members）。
  const yamlText = readFileSync(fileURLToPath(new URL("../lab/lab-layout.yaml", import.meta.url)), "utf8");
  const yamlEntries = [...yamlText.matchAll(/^ {6}- id: (\w+)$/gm)].map((m) => m[1]);
  ok(defEntries.length === yamlEntries.length && defEntries.every((id, i) => id === yamlEntries[i]),
    `yaml and built-in default list the same entries in the same order (yaml ${yamlEntries.length})`);
  const groups: Record<string, number> = {};
  for (const g of LAB_LAYOUT_DEFAULT_DATA.groups) groups[(g as { key: string }).key] = g.entries.length;
  // 任务 783：observability 1→2（用量监测卡落组）。任务 785：S1 常驻底座
  // 开关退役，infra 6→5 → 总数 34。任务 790：acpListen 入 rail，infra 5→6 → 35。
  // 任务 815：登记窗口 pin 收口——801 decisionPatterns（automation 5→6）与
  // 846 homePointer（storage 3→4）此前已入布局，pin 一并跟上 → 6/8/9/2/1/4/6/1。
  // 任务 686：workflowPanel（工作流可视化面板）入 ui 组 → 6/8/10/2/1/4/6/1。
  // 任务 944：renderArtifact（对话内产物卡片）入 ui 组 → 6/8/11/2/1/4/6/1。
  ok(groups["automation"] === 6 && groups["efficiency"] === 8 && groups["ui"] === 11 &&
     groups["observability"] === 2 && groups["dev-debug"] === 1 && groups["storage"] === 4 && groups["infra"] === 6 &&
     groups["tool-opt"] === 1,
    `group counts are 6/8/11/2/1/4/6/1 (got ${JSON.stringify(groups)})`);
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
  // 任务 742：tabCompress pane 退役并入 tabManagement 合并卡——endMarker
  // 随迁到新卡（旧 marker 不存在会让 cardSlice 返回空、测试假红/假绿）。
  { id: "subagentSuite", endMarker: '{selected === "tabManagement" && (', members: [
    { label: "M4 subagentPanel", field: "s.experimentalSubagentPanel", setter: "app.SetExperimentalSubagentPanel(on)" },
    { label: "M4 subagentDetail", field: "s.experimentalSubagentDetail", setter: "app.SetExperimentalSubagentDetail(on)" },
    { label: "M4 subagentPolicy", field: "s.experimentalSubagentPolicy", setter: "app.SetExperimentalSubagentPolicy(on)" },
    { label: "M4 subagentTps", field: "s.experimentalSubagentTps", setter: "app.SetExperimentalSubagentTps(on)" },
  ] },
  { id: "devDebug", endMarker: '{selected === "zcodeTaskBus" && (', members: [
    { label: "M5 cdpDebugPort", field: "s.experimentalCDPDebugPort", setter: "app.SetExperimentalCDPDebugPort(on)" },
    { label: "M5 lifecycleNoiseGate", field: "s.experimentalLifecycleNoiseGate", setter: "app.SetExperimentalLifecycleNoiseGate(on)" },
  ] },
  // 任务 785：S1 常驻底座开关详情卡退役——endMarker 随迁到物理下一个卡
  // monitoring（旧 marker 不存在会让 cardSlice 返回空、测试假红/假绿）。
  { id: "updateFeedback", endMarker: '{selected === "monitoring" && (', members: [
    { label: "M6 restartUpdate", field: "s.experimentalRestartUpdate", setter: "app.SetExperimentalRestartUpdate(on)" },
    { label: "M6 feedback", field: "s.experimentalFeedback", setter: "app.SetExperimentalFeedback(on)" },
    // 任务 670: fork 首启提示（默认开，用户裁决）入卡，独立开关独立 setter。
    { label: "670 forkNotice", field: "s.forkNotice", setter: "app.SetDesktopForkNotice(on)" },
  ] },
  // 任务 782：splitView pane 退役并入 viewLayout 合并卡——sessionStore 的
  // endMarker 随迁到源码中的后继分支 viewLayout（旧 marker 不存在会让
  // cardSlice 返回空、测试假红/假绿）。
  { id: "sessionStore", endMarker: '{selected === "viewLayout" && (', members: [
    { label: "M7 sessionStorage", field: "storageMode === mode", setter: "app.SetSessionStorage(mode)" },
    { label: "M7 eventsRotation", field: "RotationMode", setter: "" },
  ] },
  // 任务 782：界面组三张条目级合并卡（742 tabManagement 先例）——各成员行
  // 读自己的键、写自己的 setter；聚合徽章断言在 562 测试。
  // 任务 856 ③：后继分支随两图标合一迁移改名（collabGroupView → collabHub）。
  { id: "viewLayout", endMarker: '{selected === "collabHub" && (', members: [
    { label: "782 splitView", field: "s.experimentalSplitView", setter: "app.SetExperimentalSplitView(on)" },
    { label: "782 trajectoryView", field: "s.experimentalTrajectoryView", setter: "app.SetExperimentalTrajectoryView(on)" },
  ] },
  { id: "searchHistory", endMarker: '{selected === "localServer" && (', members: [
    { label: "782 promptHistoryPicker", field: "s.experimentalPromptHistoryPicker", setter: "app.SetExperimentalPromptHistoryPicker(on)" },
    { label: "782 questionSearch", field: "s.experimentalQuestionSearch", setter: "app.SetExperimentalQuestionSearch(on)" },
    { label: "782 autoLoadOlder", field: "s.experimentalAutoLoadOlder", setter: "app.SetExperimentalAutoLoadOlder(on)" },
  ] },
  { id: "inputSelection", endMarker: '{selected === "completionSummary" && (', members: [
    { label: "782 draftPersistence", field: "s.experimentalComposerDraft", setter: "app.SetExperimentalComposerDraft(on)" },
    { label: "782 selectionActions", field: "s.experimentalSelectionActions", setter: "app.SetExperimentalSelectionActions(on)" },
  ] },
  // 任务 783：用量监测卡——GLM Plan 行只读（无键）；Go 用量成员行经
  // SettingsOpenCodeGoUsageCard 读自己的开关键、写自己的 setter。
  { id: "usageMonitoring", endMarker: '{selected === "quickCommands" && (', members: [
    { label: "783 opencodeGoUsage", field: "s.experimentalOpenCodeGoUsage", setter: "app.SetExperimentalOpenCodeGoUsage(on)" },
    { label: "783 glmPlanUsage (read-only row)", field: "settings.glmPlanUsageHint", setter: "" },
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
  const card = cardSlice('{selected === "sessionStore" && (', '{selected === "viewLayout" && (');
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
  // 任务 782：界面组三卡合并——七个独立入口/pane/union id 摘除（成员行+
  // 成员徽章承载，742 tabCompress/tabModeTint 先例）。
  "splitView",
  "trajectoryView",
  "promptHistoryPicker",
  "questionSearch",
  "autoLoadOlder",
  "draftPersistence",
  "selectionActions",
  // 任务 783：opencodeGoUsage 独立入口/pane/union id 摘除（用量监测卡成员行
  // 承载，SettingsOpenCodeGoUsageCard 整体搬入卡内）。
  "opencodeGoUsage",
]) {
  ok(!panel.includes(`{ id: "${id}", group:`), `rail drops the folded ${id} entry`);
  ok(!panel.includes(`| "${id}"`), `union drops the folded ${id} id`);
  ok(!panel.includes(`{selected === "${id}" &&`), `pane drops the folded ${id} branch`);
}
for (const id of [
  "safetyCostControl", "modelStrategy", "contextGovernance", "subagentSuite",
  "devDebug", "updateFeedback", "sessionStore",
  // 任务 782：三张合并卡容器 id 进 union。
  "viewLayout", "searchHistory", "inputSelection",
  // 任务 783：用量监测容器 id 进 union。
  "usageMonitoring",
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
