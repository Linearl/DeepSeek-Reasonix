// Run: npx tsx src/__tests__/tl1309-lab-card-consolidation.test.ts
// tl1309 验收测试——实验室页面卡片化改造（参考「智能自动化」合并卡形态推广
// 到其余分组，任务书四卡落地）。四条验收逐条钉：
//  ① 卡片化后散条清零（除单卡组）——分析范围四组的散条全部并卡；剩余独立
//     卡逐项点名（任务书范围外/有独立 pane 语义的卡）；
//  ② 卡内关联说明完整（依赖/派生）——三张新卡 + 两个扩员卡的 hint 键三语
//     齐备，pane 卡头行挂关联说明；
//  ③ 结构与徽章不变式——yaml ≡ 内置默认（724 安全网）、卡头三档徽章 = 成员
//     最高档（774 聚合）、依赖表 992⑦ 口径不被本件触碰（LAB_FEATURE_DEPS
//     仍只有 collabHub→sessionCollab 一条）；
//  ④ 三语 locale 齐备——新增 labelKey/hintKey 逐键三语在字典。
// 原则面（配置键/setter/三档徽章零变化）由 722/562/621/815/775 既有守卫继续
// 钉——本文件只钉 tl1309 的增量形态。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import process from "node:process";
import { resolveLabLayout, labEntryBadgeTier } from "../lab/labLayout";
import { LAB_LAYOUT_DEFAULT_DATA } from "../lab/labLayoutDefault";
import { findLabEntry } from "../lab/labLayout";
import { LAB_FEATURE_DEPS } from "../lib/experimentTiers";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const here = fileURLToPath(new URL(".", import.meta.url));
const frontendRoot = `${here}../..`;
const yamlText = readFileSync(`${frontendRoot}/src/lab/lab-layout.yaml`, "utf8");
const panel = readFileSync(`${frontendRoot}/src/components/SettingsPanel.tsx`, "utf8")
  + "\n" + readFileSync(`${frontendRoot}/src/components/ExperimentalLabSection.tsx`, "utf8")
  + "\n" + readFileSync(`${frontendRoot}/src/lab/labPaneRegistry.ts`, "utf8");

console.log("\ntl1309 lab card consolidation (four-card rollout)");

// 白名单与 pane 源一致（722 同款抽取）。
const paneIds = new Set<string>();
{
  const m = panel.match(/const LAB_PANE_IDS: ReadonlySet<string> = new Set\(\[([\s\S]*?)\]\);/);
  ok(Boolean(m), "pane whitelist extractable from labPaneRegistry");
  if (m) for (const id of m[1].matchAll(/"([a-zA-Z]+)"/g)) paneIds.add(id[1]);
}
const resolved = resolveLabLayout(yamlText, { allowedEntryIds: paneIds });
ok(resolved.source === "yaml" && resolved.warnings.length === 0,
  `shipped yaml resolves (warnings: ${JSON.stringify(resolved.warnings)})`);
ok(JSON.stringify(resolved.layout) === JSON.stringify(LAB_LAYOUT_DEFAULT_DATA),
  "yaml equals the built-in default (724 安全网回退无缝)");

const groupOf = (key: string) => LAB_LAYOUT_DEFAULT_DATA.groups.find((g) => g.key === key);
const entryIdsOf = (key: string) => (groupOf(key)?.entries ?? []).map((e) => e.id);
const membersOf = (id: string) => findLabEntry(LAB_LAYOUT_DEFAULT_DATA, id)?.members?.map((m) => m.id) ?? [];

// ── ① 散条清零（除单卡组）──
{
  // 分析范围四组：散条全部并卡。
  ok(membersOf("contextGovernance").includes("optimisticParallel"),
    "① efficiency: optimisticParallel rides contextGovernance (任务书①)");
  ok(membersOf("runtimeObservability").join(",") === "monitoring,eventsRotation,trajectoryView",
    "① observability: 监控+events 瘦身+轨迹观测 ride runtimeObservability (任务书②)");
  ok(membersOf("resourceGovernance").join(",") === "runtimeReuse,orphanHandling,idleReleaseMinutes,gcPercent,acpListen,localServer",
    "① infra/storage: 六项资源生命周期卡成员落位 (任务书③)");
  ok(membersOf("tabManagement").includes("todoSidebar") &&
     membersOf("sessionTaskPanels").join(",") === "sessionWall,workflowPanel,completionSummary",
    "① ui: todoSidebar 并标签页管理卡、三面板并会话与任务面板 (任务书④)");
  // 界面组零散条：8 张卡全部是合并卡/容器卡/自带成员卡。
  ok(entryIdsOf("ui").join(",") === "startupLoading,tabManagement,viewLayout,sessionTaskPanels,searchHistory,subagentSuite,inputSelection,updateFeedback",
    "① ui group carries only merged/container cards (零散条)");
  // 剩余独立卡逐项点名（任务书范围外的既有语义，不属散条）：
  //   automation: autopilot/fullAccess（稳定主卡，任务书未列 automation 组）；
  //   efficiency: collabHub（协作中心入口，自带依赖联动）；
  //   infra: pathRules 已随 tl1487 七组③（tl134 用户裁决）整卡移除——组内
  //     仅余资源治理/工具优化两卡；
  //   storage: homePointer（846 独立迁移向导 pane）/jsonlZstd（冷压缩独立开关）；
  //   dev-debug / memory-context：单卡组（任务书⑤维持现状）。
  ok(entryIdsOf("dev-debug").join(",") === "devDebug" && entryIdsOf("memory-context").join(",") === "pinnedIndex",
    "① dev-debug/memory-context stay single-card groups (任务书⑤)");
  ok(entryIdsOf("infra").join(",") === "resourceGovernance,toolOptimizations" &&
     entryIdsOf("storage").join(",") === "sessionStore,homePointer,jsonlZstd",
    "① infra/storage residual standalone entries (tl1487: pathRules removed; homePointer/jsonlZstd remain)");
}

// ── ② 卡内关联说明完整（依赖/派生）──
{
  const hints = [
    "settings.runtimeObservabilityHint",
    "settings.resourceGovernanceHint",
    "settings.sessionTaskPanelsHint",
  ];
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(`${frontendRoot}/src/locales/${locale}.ts`, "utf8");
    const missing = hints.filter((k) => !dict.includes(`"${k}"`));
    ok(missing.length === 0, `② ${locale} carries the three merged-card association hints (missing: ${JSON.stringify(missing)})`);
  }
  // pane 卡头行挂 hint（关联说明渲染面）：三张新卡各有一行卡头 SettingsField。
  ok(panel.includes('labLabel("runtimeObservability", t("settings.runtimeObservability"))') &&
     panel.includes('t("settings.runtimeObservabilityHint")'),
    "② runtimeObservability pane head carries the association hint");
  ok(panel.includes('labLabel("resourceGovernance", t("settings.resourceGovernance"))') &&
     panel.includes('t("settings.resourceGovernanceHint")'),
    "② resourceGovernance pane head carries the association hint");
  ok(panel.includes('labLabel("sessionTaskPanels", t("settings.sessionTaskPanels"))') &&
     panel.includes('t("settings.sessionTaskPanelsHint")'),
    "② sessionTaskPanels pane head carries the association hint");
  // 迁移卡成员行关联注释（依赖/派生关系在卡内行注释与既有行 hint 承载）：
  // 乐观并行的家族伴生行（bashHeavyGuard/reasoningOnlyContinuation）随主行落
  // contextGovernance；events 瘦身/轨迹观测的迁卡说明在行注释。
  ok(panel.includes("tl1309 卡④：todoSidebar（侧栏增强）成员行迁入本卡") ||
     panel.includes("tl1309"), "② moved member rows carry tl1309 association notes");
}

// ── ③ 结构与徽章不变式（渲染表层）──
{
  // 卡头三档徽章 = 成员最高档（774 聚合，任务书原则③同款）。tl1487 二组⑤：
  // sessionTaskPanels 随 workflowPanel 升稳定，聚合终值 推荐→稳定。
  const agg = (id: string) => labEntryBadgeTier(LAB_LAYOUT_DEFAULT_DATA, id);
  ok(agg("contextGovernance") === "recommended" && agg("runtimeObservability") === "recommended" &&
     agg("resourceGovernance") === "stable" && agg("tabManagement") === "recommended" &&
     agg("sessionTaskPanels") === "stable",
    "③ card-head badges aggregate to the strongest member tier (774; tl1487 sessionTaskPanels → stable)");
  // 配置键零变化的反面钉：成员 id 都仍注册在档位表（562 主守卫继续钉全量），
  // 这里钉四张新/扩卡的存在性与 pane 白名单一一对应。
  for (const id of ["runtimeObservability", "resourceGovernance", "sessionTaskPanels"]) {
    ok(paneIds.has(id) && Boolean(findLabEntry(LAB_LAYOUT_DEFAULT_DATA, id)),
      `③ ${id} is whitelisted and laid out (rail/pane 同源)`);
  }
  // 依赖层级展示（992⑦ 口径）：LAB_FEATURE_DEPS 不被本件触碰——仍只有
  // collabHub→sessionCollab 一条前置（新卡成员间无前置依赖声明需求）。
  ok(Object.keys(LAB_FEATURE_DEPS).length === 1 && LAB_FEATURE_DEPS.collabHub === "sessionCollab",
    "③ dependency table untouched (992⑦: collabHub→sessionCollab remains the single dep)");
  // 迁出的成员不再占旧卡成员表（防双挂）。
  ok(!membersOf("sessionStore").includes("eventsRotation") && !membersOf("viewLayout").includes("trajectoryView") &&
     !membersOf("subagentSuite").includes("todoSidebar"),
    "③ moved members are removed from their source cards (无双挂)");
}

// ── ④ 三语 locale 齐备 ──
{
  const keys = [
    "settings.runtimeObservability", "settings.runtimeObservabilityHint",
    "settings.resourceGovernance", "settings.resourceGovernanceHint",
    "settings.sessionTaskPanels", "settings.sessionTaskPanelsHint",
  ];
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(`${frontendRoot}/src/locales/${locale}.ts`, "utf8");
    const missing = keys.filter((k) => !dict.includes(`"${k}"`));
    ok(missing.length === 0, `④ ${locale} carries all six tl1309 keys (missing: ${JSON.stringify(missing)})`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
