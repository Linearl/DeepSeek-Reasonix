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
//     任务 961 后为 4/6/10/2/1/4/6/2=35（automation 三散条并「智能自动化」
//     卡 6→4、efficiency 三散条并「交互与呈现」卡 8→6、ui renderArtifact
//     并入视图布局卡 11→10——条目级合并净 -5 +2 容器 = 40→35）。
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

const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8") + "\n" + readFileSync(fileURLToPath(new URL("../components/ExperimentalLabSection.tsx", import.meta.url)), "utf8") + "\n" + readFileSync(fileURLToPath(new URL("../lab/labPaneRegistry.ts", import.meta.url)), "utf8");

function cardSlice(startMarker: string, endMarker: string): string {
  const start = panel.indexOf(startMarker);
  const end = panel.indexOf(endMarker);
  return start >= 0 && end > start ? panel.slice(start, end) : "";
}

console.log("\ntask 561 lab regroup + merged cards");

// ① group structure（任务 722/724 修订；任务 992 工具优化并入 infra 后
//    7 组——本 pin 2026-10-19 随 1005 窗口补同步，992 合组时漏跟）：
//    组受控词表在 labLayoutTypes，组序/组键/组标签键全部来自布局文档
//    （yaml 与内置默认一致）。tl1193b：增「记忆与上下文」组（pinnedIndex
//    卡落位）——7 组 → 8 组；tl1256 调序：memory-context 移 infra 之后
//    （原 automation 后 efficiency 前，用户 20261029 令）。
{
  const yaml = readFileSync(fileURLToPath(new URL("../lab/lab-layout.yaml", import.meta.url)), "utf8");
  const labDefault = readFileSync(fileURLToPath(new URL("../lab/labLayoutDefault.ts", import.meta.url)), "utf8");
  const keys = [
    "automation", "efficiency", "ui", "observability", "dev-debug", "storage", "infra", "memory-context",
  ];
  const yamlOrder = keys.map((k) => yaml.indexOf(`key: ${k}`));
  ok(yamlOrder.every((i) => i >= 0) && [...yamlOrder].sort((a, b) => a - b).every((v, i) => v === yamlOrder[i]),
    "lab-layout.yaml carries the 8 groups in the audit-table order (rail order)");
  for (const [key, labelKey] of [
    ["automation", "settings.labGroup.automation"],
    ["memory-context", "settings.labGroup.memoryContext"],
    ["dev-debug", "settings.labGroup.devDebug"],
  ] as const) {
    const block = yaml.slice(yaml.indexOf(`key: ${key}`));
    ok(block.includes(`labelKey: ${labelKey}`), `group ${key} carries locale label key ${labelKey}`);
  }
  const defOrder = keys.map((k) => labDefault.indexOf(`key: "${k}"`));
  ok(defOrder.every((i) => i >= 0) && [...defOrder].sort((a, b) => a - b).every((v, i) => v === defOrder[i]),
    "built-in default layout mirrors the same group order (fallback = same face)");
}

// ① rail census（任务 783 修订）：恰好 35 个 rail 入口，组分布// 5/8/9/2/1/3/6/1（783：可观测性组增「用量监测」卡——GLM Plan 用量 +
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
// 任务 943：multiEditInjection（multi_edit 直注）入 tool-opt 组——
// 任务 944：renderArtifact（对话内产物卡片）入 ui 组——两件先后入布局，
// 合并态总数 38→40，组分布 ui 10→11、tool-opt 1→2（6/8/11/2/1/4/6/2）。
// 任务 961：automation 三散条（dream/decisionPatterns/heartbeatBackground）
// 并「智能自动化」卡（tl1453 后 dream 行随实验退役）、efficiency 三散条（quickCommands/traceAsState/
// outputStyle）并「交互与呈现」卡、ui renderArtifact 并入视图布局卡——
// 条目级合并净 -5 +2 容器 = 40→35，组分布 4/6/10/2/1/4/6/2。
// 任务 971：multiEditInjection 独立卡退役并入工具优化聚合页（restartUpdate
// 自更新反馈卡迁入聚合页，成员计数净零）——tool-opt 组 2→1，总数 35→34。
// 971 基线侧的「补录 renderArtifact 入 yaml」在合并态已由 944 返修/961 收编
// 先行落地（viewLayout 卡内），971 补录版为过时重复条目已删。
// 任务 1097：logManagement 日志管理参数卡入可观测性组——总数 34→35。
// 任务 1132：startupLoading「启动与加载」合并卡入 ui 组——总数 35→36。
// tl-1048：zcodeTaskBus 随 8787 任务总线退役出 rail——总数 36→35。
// 两件各自同步了 562/704/1005/722 镜像 pin 而 561 census 漏跟（本 pin
// 2026-10-25 随 tl1156 补同步）。
// 数据源 = 布局默认数据 + yaml（两份必须同构）。
{
  // 直接导入布局数据模块数真数据（比源码正则稳）。
  const { LAB_LAYOUT_DEFAULT_DATA } = await import("../lab/labLayoutDefault");
  const defEntries = LAB_LAYOUT_DEFAULT_DATA.groups.flatMap((g: { entries: Array<{ id: string }> }) => g.entries.map((e) => e.id));
  // tl1174：jsonlZstd 入存储组时本 pin 漏跟（基线 f3c268b7f 实跑 36/存储 5，
  // pin 钉 35/存储 4 = 预存红）；tl1193b：记忆与上下文新组 + pinnedIndex 卡
  // → 总数 37（实值钉，同步顺带收掉 1174 欠账）。tl1309 卡①：
  // optimisticParallel 散条并入 contextGovernance 卡 → 37→36。
  // tl1309 卡③：infra 四散条 + storage 两参数卡并「资源治理」卡 → 36−6+1=31。
  // tl1309 卡④：todoSidebar 并 tabManagement、三面板并 sessionTaskPanels
  // → 31−4+1=28。
  // tl1487 七组③：pathRules 整卡移除 → 28→27。
  // tl1733：记忆注入瘦身卡入提效组 → 27→28。
  // tl1746 tasklistPanel +1；tl1741r2 打回返修：四卡体回归（散条回收），
  // 三 rail 卡（autopilot/sessionCollab/collabHub）回归为无主开关卡——
  // rail census 以合并态 yaml 实测为准（见 ok 断言值）。
  // tl1772：memoryIndexSlim 独立卡移出提效组——并入「安全/成本控制」卡
  // 成员行（decisionPatternsLayered 为 smartAutomation 卡成员行，不占
  // rail 入口）→ 29→28。tl1772r2：decisionPatternsLayered 成员行二次移组
  // smartAutomation→safetyCostControl 卡——仍是成员行，rail census 不变。
  ok(defEntries.length === 28, `default layout carries exactly 28 rail entries (tl1772 re-homed memoryIndexSlim as a safetyCostControl member; got ${defEntries.length})`);
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
  // 任务 943：multiEditInjection 入 tool-opt 组、任务 944：renderArtifact 入
  // ui 组——两件先后入布局 → 6/8/11/2/1/4/6/2。任务 961：三处条目级收编
  // → 4/6/10/2/1/4/6/2（automation/efficiency 各并一卡、ui renderArtifact
  // 并入视图布局卡）。任务 971：multiEditInjection 独立卡退役并入聚合页
  // → tool-opt 2→1。任务 992：工具优化整体并入 infra（原独立 tool-opt 组
  // 退役，restartUpdate 归位回 updateFeedback 卡，组成员不变）→ 组分布
  // 4/6/10/2/1/4/7（infra 6→7、tool-opt 组移除，总数不变 34；本 pin 随
  // 1005 窗口补同步，992 合组时漏跟）。任务 1097：logManagement 日志管理
  // 参数卡入可观测性组 → observability 2→3。任务 1132：startupLoading
  // 「启动与加载」合并卡入 ui 组 → ui 10→11。两件各自动了 562/704/1005/722
  // 镜像 pin 而 561 census 漏跟（本 pin 2026-10-25 随 tl1156 补同步）
  // → 组分布 4/6/11/3/1/4/7=36。tl-1048：zcodeTaskBus 退役出 infra
  // → infra 7→6，总数 36→35。tl1174：jsonlZstd 入存储组，storage 4→5、
  // 总数 36（本 pin 漏跟=基线预存红）。tl1193b：记忆与上下文新组
  // （pinnedIndex 卡）→ 总数 37（实值钉）；tl1256 调序：memory-context
  // 移 infra 之后（组分布序随 rail 序，各组成员数不变）→ 4/6/11/3/1/5/6/1。
  // tl1309 卡①：efficiency 6→5（optimisticParallel 并卡）→ 4/5/11/3/1/5/6/1。
  // tl1309 卡③：storage 5→3、infra 6→3（七项并「资源治理」卡）。tl1309 卡④：
  // ui 11→8（todoSidebar 并 tabManagement、三面板并 sessionTaskPanels）
  // → 4/5/8/3/1/3/3/1。
  // tl1487 七组③：infra 3→2（pathRules 移除）→ 4/5/8/3/1/3/2/1。
  // tl1733：memoryIndexSlim 卡入提效组 → efficiency 5→6。
  // tl1741：automation 4→2（autopilot/sessionCollab 撤）、efficiency 6→5
  // （collabHub 撤）、infra 拆卡回散条 2→6（orphan/idleRelease/gcPercent/
  // acpListen/localServer/toolOptimizations）。
  // tl1746：tasklistPanel 卡入可观测性组 → observability 3→4 → 2/5/8/4/1/3/6/1。
  // tl1772：memoryIndexSlim 出提效组（并入安全/成本控制卡成员行）→
  // efficiency 6→5 → 2/4/8/4/1/3/6/1。
  ok(groups["automation"] === 4 && groups["efficiency"] === 5 && groups["ui"] === 8 &&
     groups["observability"] === 4 && groups["dev-debug"] === 1 && groups["storage"] === 3 && groups["infra"] === 2 &&
     groups["memory-context"] === 1,
    `group counts are 4/5/8/4/1/3/2/1 (tl1772 memoryIndexSlim re-homed as a safetyCostControl member; got ${JSON.stringify(groups)})`);
  ok(new Set(defEntries).size === defEntries.length, "no duplicate rail ids");
  // 任务 722 六点：safetyCostControl 在提效组；sessionCollabAutoFold /
  // messageMerge / compactModel 以成员身份存在，不再占 rail 入口。
  const eff = LAB_LAYOUT_DEFAULT_DATA.groups.find((g: { key: string }) => g.key === "efficiency");
  const safety = eff!.entries.find((e: { id: string }) => e.id === "safetyCostControl");
  const memberIds = (safety!.members ?? []).map((m: { id: string }) => m.id);
  ok(["messageMerge", "compactModel", "heartbeatRotation"].every((x) => memberIds.includes(x)),
    "safetyCostControl members carry messageMerge + compactModel + heartbeatRotation (722 点5/6 + 727)");
  // tl1772：memoryIndexSlim 移组收口——提效组顶层条目消失、成员行落位
  // 安全/成本控制卡（档位与键不动）。
  ok(memberIds.includes("memoryIndexSlim") && safety!.members!.find((m: { id: string }) => m.id === "memoryIndexSlim")?.tier === "optional",
    "tl1772: memoryIndexSlim rides as an optional safetyCostControl member (tier unchanged)");
  ok(!eff!.entries.some((e: { id: string }) => e.id === "memoryIndexSlim"),
    "tl1772: no standalone memoryIndexSlim entry remains in the efficiency group");
  // tl1741：sessionCollab 卡随撤开关退役（autoFold 随宿主卡入 storage 组）——
  // 原断言「sessionCollab hosts the auto-fold member」随卡摘除。
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
  // tl-1048：zcodeTaskBus pane 随 8787 任务总线退役——devDebug 的 endMarker
  // 随迁到物理下一个卡（旧 marker 不存在会让 cardSlice 返回空、测试假红/假
  // 绿）。tl1741 返修：cdpDebugPort 成员行回归为无开关展示行（主键退役）——
  // field 断言改钉展示行 hint，负钉 setter 不回归（见 M8 撤开关清单）。
  { id: "devDebug", endMarker: '{selected === "toolOptimizations" && (', members: [
    { label: "M5 cdpDebugPort (read-only row)", field: "settings.cdpDebugPortHint", setter: "" },
    { label: "M5 lifecycleNoiseGate", field: "s.experimentalLifecycleNoiseGate", setter: "app.SetExperimentalLifecycleNoiseGate(on)" },
  ] },
  // 任务 785：S1 常驻底座开关详情卡退役——endMarker 随迁到物理下一个卡
  // monitoring（旧 marker 不存在会让 cardSlice 返回空、测试假红/假绿）。
  // tl1309 卡②：monitoring pane 退役并入「运行可观测」合并卡——endMarker
  // 再随迁到 runtimeObservability。
  { id: "updateFeedback", endMarker: '{selected === "runtimeObservability" && (', members: [
    // 任务 971：restartUpdate 迁至工具优化聚合页（toolOptimizations pane）——
    // 本卡不再含该行（键/setter 零变化，仅 UI 落点迁移）。
    { label: "M6 feedback", field: "s.experimentalFeedback", setter: "app.SetExperimentalFeedback(on)" },
    // 任务 670: fork 首启提示（默认开，用户裁决）入卡，独立开关独立 setter。
    { label: "670 forkNotice", field: "s.forkNotice", setter: "app.SetDesktopForkNotice(on)" },
  ] },
  // tl1309 卡③：「资源治理」合并卡（复用池/孤儿回收/闲置释放/GC/ACP 监听/
  // 本地服务六成员）——四散条 + 两参数卡（自存储组迁入）随迁本卡；参数行经
  // labParamFields 注册表绑定读写（值语义单源），field 断言钉绑定引用。
  // tl1741 返修：卡体回归 rail（无主开关卡）——runtimeReuse 主键退役，成员行
  // 回归为无开关展示行（field 改钉展示行 hint，setter 负钉见 M8 清单）。
  { id: "resourceGovernance", endMarker: '{selected === "logManagement" && (', members: [
    { label: "tl1309 runtimeReuse (read-only row)", field: "settings.runtimeReuseHint", setter: "" },
    { label: "tl1309 orphanHandling", field: "s.experimentalOrphanHandling", setter: "app.SetExperimentalOrphanHandling(on)" },
    { label: "tl1309 idleReleaseMinutes", field: "LAB_PARAM_FIELDS.detachedIdleReleaseMinutes.write", setter: "" },
    { label: "tl1309 gcPercent", field: "LAB_PARAM_FIELDS.gcPercent.write", setter: "" },
    { label: "tl1309 acpListen", field: "s.experimentalACPListen", setter: "app.SetExperimentalACPListen(on)" },
    { label: "tl1309 localServer", field: "s.experimentalLocalServer", setter: "app.SetExperimentalLocalServer(on)" },
  ] },
  // tl1309 卡②：「运行可观测」合并卡（monitoring+events 瘦身+轨迹观测）——
  // M7 eventsRotation 行自 sessionStore、M8 监控段自 monitoring、782
  // trajectoryView 行自 viewLayout 三处随迁本卡（键/setter 零变化）。
  { id: "runtimeObservability", endMarker: '{selected === "sessionStore" && (', members: [
    { label: "tl1309 eventsRotation", field: "RotationMode", setter: "" },
    { label: "tl1309 trajectoryView", field: "s.experimentalTrajectoryView", setter: "app.SetExperimentalTrajectoryView(on)" },
  ] },
  // 任务 782：splitView pane 退役并入 viewLayout 合并卡——sessionStore 的
  // endMarker 随迁到源码中的后继分支 viewLayout（旧 marker 不存在会让
  // cardSlice 返回空、测试假红/假绿）。tl1309 卡②：eventsRotation 行迁出本卡
  // （成员清单随迁 runtimeObservability）。
  { id: "sessionStore", endMarker: '{selected === "viewLayout" && (', members: [
    { label: "M7 sessionStorage", field: "storageMode === mode", setter: "app.SetSessionStorage(mode)" },
  ] },
  // 任务 782：界面组三张条目级合并卡（742 tabManagement 先例）——各成员行
  // 读自己的键、写自己的 setter；聚合徽章断言在 562 测试。
  // 任务 856 ③ → tl1741：后继分支原 collabHub 随撤卡移除——endMarker 随迁 sessionTaskPanels。
  // 任务 961：renderArtifact 成员行入卡（944 原独立卡并入，键/setter 零变化）。
  // 任务 1072：renderArtifact 整功能移除，成员行随件退役（不再断言）。
  { id: "viewLayout", endMarker: '{selected === "sessionTaskPanels" && (', members: [
    { label: "782 splitView", field: "s.experimentalSplitView", setter: "app.SetExperimentalSplitView(on)" },
    // tl1309 卡②：trajectoryView 行迁出本卡（成员清单随迁 runtimeObservability）。
  ] },
  // tl1309 卡③：localServer pane 退役入资源治理卡——endMarker 随迁 homePointer。
  { id: "searchHistory", endMarker: '{selected === "homePointer" && (', members: [
    { label: "782 promptHistoryPicker", field: "s.experimentalPromptHistoryPicker", setter: "app.SetExperimentalPromptHistoryPicker(on)" },
    { label: "782 questionSearch", field: "s.experimentalQuestionSearch", setter: "app.SetExperimentalQuestionSearch(on)" },
    { label: "782 autoLoadOlder", field: "s.experimentalAutoLoadOlder", setter: "app.SetExperimentalAutoLoadOlder(on)" },
  ] },
  // tl1309 卡④：completionSummary pane 退役入「会话与任务面板」卡——endMarker 随迁。
  // tl1309 卡④②段修订：completionSummary 分支退役后，inputSelection 的物理
  // 后继分支是 interactionPresentation——endMarker 随迁（sessionTaskPanels 分支
  // 在文件更早位置，作 endMarker 会把 slice 截断为空）。
  { id: "inputSelection", endMarker: '{selected === "interactionPresentation" && (', members: [
    { label: "782 draftPersistence", field: "s.experimentalComposerDraft", setter: "app.SetExperimentalComposerDraft(on)" },
    { label: "782 selectionActions", field: "s.experimentalSelectionActions", setter: "app.SetExperimentalSelectionActions(on)" },
  ] },
  // 任务 783：用量监测卡——GLM Plan 行只读（无键）；tl1487 四组：Go 用量行
  // 亦退役为只读展示行（「不提供开关、始终打开」，SettingsOpenCodeGoUsageCard
  // 无 props 渲染）。
  // 任务 961：quickCommands 独立 pane 退役并入「交互与呈现」卡——本卡
  // endMarker 随迁到后继分支 interactionPresentation（旧 marker 不存在会
  // 让 cardSlice 返回空、测试假红/假绿）。
  { id: "usageMonitoring", endMarker: '{selected === "interactionPresentation" && (', members: [
    { label: "tl1487 opencodeGoUsage (read-only row)", field: "settings.opencodeGoUsageHint", setter: "" },
    { label: "783 glmPlanUsage (read-only row)", field: "settings.glmPlanUsageHint", setter: "" },
  ] },
  // 任务 961：automation/efficiency 两张条目级合并卡（742/782 先例）——
  // dream/decisionPatterns/heartbeatBackground 并「智能自动化」；
  // quickCommands/traceAsState/outputStyle 并「交互与呈现」。各成员行读
  // 自己的键、写自己的 setter；聚合徽章断言在 562 测试。
  { id: "smartAutomation", endMarker: '{selected === "searchHistory" && (', members: [
    // tl1453：dream 成员行随 dream/distill 实验退役（宪法 Q1）摘除。
    { label: "961 decisionPatterns", field: "s.experimentalDecisionPatterns", setter: "app.SetExperimentalDecisionPatterns(on)" },
    // tl1772r2：decisionPatternsLayered 成员行迁出本卡（落位 safetyCostControl，
    // 见其落位钉⑤；本清单随迁摘除）。
    { label: "961 heartbeatBackground", field: "s.experimentalHeartbeatBackground", setter: "app.SetExperimentalHeartbeatBackground(on)" },
  ] },
  { id: "interactionPresentation", endMarker: '{selected === "devDebug" && (', members: [
    { label: "961 quickCommands", field: "s.experimentalQuickCommands", setter: "app.SetExperimentalQuickCommands(on)" },
    { label: "961 traceAsState", field: "s.experimentalTraceAsState", setter: "app.SetExperimentalTraceAsState(on)" },
    { label: "961 outputStyle", field: "s.experimentalOutputStyleUI", setter: "app.SetExperimentalOutputStyleUI(on)" },
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
//    inside the sessionStore card, each with its own setter. tl1287: the
//    selector renders from the v3-only selectable list (v4 support retired).
//    tl1309 卡②：rotation panel 随 eventsRotation 成员行迁出——挂载面断言
//    随迁「运行可观测」卡（setter 仍在 SessionEventsPanel）。
{
  const card = cardSlice('{selected === "sessionStore" && (', '{selected === "viewLayout" && (');
  ok(card.includes("SESSION_STORAGE_SELECTABLE_MODES.map") && card.includes("app.SetSessionStorage(mode)"),
    "M7 sessionStorage: mode selector saves through SetSessionStorage");
  const obs = cardSlice('{selected === "runtimeObservability" && (', '{selected === "sessionStore" && (');
  ok(obs.includes("<SessionEventsPanel"),
    "tl1309 eventsRotation: rotation panel mounts inside the runtimeObservability card (its setters live in SessionEventsPanel)");
}

// ② M2 specifics: modelCapabilityFilter（模型能力过滤）——tl1487 一组③
//    （用户 20261107 八组清单「移除（不展示）」）只读展示行整体摘除，
//    551/564 的 retired 只读范式收尾；无 setter 调用的负钉继续成立。
{
  const card = cardSlice('{selected === "modelStrategy" && (', '{selected === "contextGovernance" && (');
  ok(!card.includes("s.experimentalModelCapabilityFilter"), "tl1487: the retired modelCapabilityFilter display row is gone from the M2 card");
  ok(!card.includes("settings.modelCapabilityFilter.retired"), "tl1487: the retired hint is gone from the M2 card");
  ok(!panel.includes("app.SetExperimentalModelCapabilityFilter("), "retired key has NO setter call anywhere (read-only, still)");
}

// ③ M8 standalone entries（任务 722/724 修订）：rail 入口断言读布局默认
// 数据（入口 id + 自有 onKeys），pane 分支断言仍读 SettingsPanel。
// tl1309 卡①：optimisticParallel 自 M8 清单并入 contextGovernance 卡
// （任务书①，961 先例——独立 rail 入口/pane 分支退役为成员行）。
// tl1309 卡②：monitoring 同款并入 runtimeObservability 卡。
// tl1741 打回返修（20261122 用户裁定「stable 清主选项（常开）、子选项保留
// 配置」）：M8 清单语义 = 「无主开关的 stable 卡」清单而非「撤卡」清单——
// autopilot/sessionCollab/collabHub/resourceGovernance 四卡回归 rail（无主
// 开关行），fullAccess 仍是唯一带主开关的 stable 主卡；七 retired 主键
// setter 全仓负钉（撤开关语义不随卡体回归）。
{
  const { LAB_LAYOUT_DEFAULT_DATA } = await import("../lab/labLayoutDefault");
  const entryIds = new Set(LAB_LAYOUT_DEFAULT_DATA.groups.flatMap((g: { entries: Array<{ id: string }> }) => g.entries.map((e) => e.id)));
  // tl1741：autopilot/sessionCollab 随撤开关出 M8 清单（rail 入口与 pane
  // 分支俱撤）；fullAccess 为唯一 stable 主卡。
  for (const [id, pane] of [
    ["autopilot", '{selected === "autopilot" && ('],
    ["sessionCollab", '{selected === "sessionCollab" && ('],
    ["collabHub", '{selected === "collabHub" && ('],
    ["resourceGovernance", '{selected === "resourceGovernance" && ('],
    ["fullAccess", '{selected === "fullAccess" && ('],
  ] as const) {
    ok(entryIds.has(id), `M8 ${id} keeps its own rail entry (card body restored, no master switch)`);
    ok(panel.includes(pane), `M8 ${id} keeps its own pane branch`);
  }
  // 无主开关语义：四张回归卡的 pane 不含任何 retired 主键开关接线。
  for (const retired of [
    "app.SetDesktopAutopilot(",
    "app.SetExperimentalSessionCollab(",
    "app.SetExperimentalCollabHub(",
    "app.SetExperimentalRuntimeReuse(",
    "app.SetExperimentalCDPDebugPort(",
    "app.SetExperimentalWorkflowPanel(",
    "app.SetExperimentalWorkflowTemplates(",
  ]) {
    ok(!panel.includes(retired), `retired master-switch setter stays gone: ${retired}`);
  }
  // fullAccess：唯一带主开关的 stable 主卡（开关+setter 原样）。
  const fullAccess = cardSlice('{selected === "fullAccess" && (', '{selected === "updateFeedback" && (');
  ok(fullAccess.includes("labStableLocked(\"fullAccess\")") && fullAccess.includes("app.SetExperimentalFullAccess("),
    "fullAccess remains the ONLY stable card with a master switch");
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
  // 任务 961：三处条目级收编——七个独立入口/pane/union id 摘除（成员行+
  // 成员徽章承载，742/782 先例）。tl1453：dream 连成员行一并退役
  // （宪法 Q1），缺席断言由布局数据覆盖，此处不再单列。
  "decisionPatterns",
  "heartbeatBackground",
  "quickCommands",
  "traceAsState",
  "outputStyle",
  // tl1309 卡①：optimisticParallel 并入 contextGovernance 卡（同款先例）。
  "optimisticParallel",
  // tl1309 卡②：monitoring 并入「运行可观测」卡（同款先例）。
  "monitoring",
  // tl1309 卡③ → tl1741 拆卡回散条：runtimeReuse 随撤开关退役（键/pane/
  // union 全摘）；orphanHandling/acpListen/localServer 三散条重入 union+
  // pane（搬出本 folded 清单，改列「union gains」）；idleReleaseMinutes/
  // gcPercent 两参数卡 id 从不在 union——775 参数卡走通用渲染器，无 union 登记。
  "runtimeReuse",
  // tl1309 卡④：todoSidebar 并 tabManagement；三面板并 sessionTaskPanels。
  "todoSidebar",
  "sessionWall",
  "workflowPanel",
  "completionSummary",
  // tl1772：memoryIndexSlim 独立卡/pane/union id 摘除——safetyCostControl 卡
  // 成员行承载（档位与键不动，只移组；messageMerge 722 点6 同款）。
  "memoryIndexSlim",
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
  // 任务 961：两张合并卡容器 id 进 union。
  "smartAutomation", "interactionPresentation",
  // tl1309 卡②：「运行可观测」合并卡容器 id 进 union。tl1309 卡③：「资源
  // 治理」合并卡容器 id 进 union。tl1309 卡④：「会话与任务面板」合并卡容器
  // id 进 union。
  // tl1741 返修：resourceGovernance 容器 id 重入 union（卡体回归 rail）；
  // orphanHandling/acpListen/localServer 三散条 id 出 union（回卡内成员）。
  "runtimeObservability", "sessionTaskPanels",
  "resourceGovernance",
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
  // tl1741 返修：SetExperimentalCDPDebugPort 随主键退役撤出清单（cdpDebugPort
  // 成员行=无开关展示行；负钉在 M8 撤开关清单）。
  "app.SetExperimentalLifecycleNoiseGate(on)",
  "app.SetExperimentalRestartUpdate(on)",
  "app.SetExperimentalFeedback(on)",
  "app.SetSessionStorage(mode)",
  // tl1772：新伴生开关与移组行各自的 setter 接线随行落位（丢 setter=静默丢档）。
  "app.SetExperimentalDecisionPatternsLayered(on)",
  "app.SetMemoryIndexSlim(on)",
]) {
  ok(panel.includes(setter), `setter wiring survives the merge: ${setter}`);
}

// ⑤ tl1772 注入分层开关 + memoryIndexSlim 移组、tl1772r2 注入分层行二次移组
//    的落位钉（pane 行面）。
{
  const smart = cardSlice('{selected === "smartAutomation" && (', '{selected === "searchHistory" && (');
  const safety = cardSlice('{selected === "safetyCostControl" && (', '{selected === "sessionCollab" && (');
  ok(safety.includes('labLabel("memoryIndexSlim"') && safety.includes("app.SetMemoryIndexSlim(on)"),
    "tl1772: the memoryIndexSlim switch row rides inside the safetyCostControl card");
  // tl1772r2：注入分层行 smartAutomation→safetyCostControl 平移（键/setter 零变化）。
  ok(safety.includes('labLabel("decisionPatternsLayered"') && safety.includes("app.SetExperimentalDecisionPatternsLayered(on)"),
    "tl1772r2: the injection-layering row rides inside the safetyCostControl card");
  ok(!smart.includes('labLabel("decisionPatternsLayered"') && !smart.includes("app.SetExperimentalDecisionPatternsLayered(on)"),
    "tl1772r2: no injection-layering remnant stays in the smartAutomation card after the re-home");
  ok(!smart.includes('labLabel("memoryIndexSlim"') && !safety.includes('{selected === "memoryIndexSlim" &&'),
    "tl1772: no standalone memoryIndexSlim rail/pane remnant survives the re-home");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
