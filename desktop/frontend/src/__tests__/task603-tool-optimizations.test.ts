// Run: npx tsx src/__tests__/task603-tool-optimizations.test.ts
// 任务 603 acceptance harness (「工具优化」family first member: edit readBack):
//  ① the tier register carries toolOptimizations at the RECOMMENDED tier
//     (783④：编辑读回随 22.5 交付转正，自 unstable 升档；815 收口本测试
//     未跟的过期 pin);
//  ② SettingsPanel wiring: the tool-opt group, the rail entry with its own
//     `on` read, the pane card with its own setter and a visible risk line;
//  ③ locale keys exist in all three dialects (group/label/hint/on/off/risk);
//  ④ the bridge surface: interface method + browser mock + settings view
//     field (experimentalToolOptimizations);
//  ⑤ the Go tier registry (render.go) tags experimental_tool_optimizations
//     recommended — two sides, one source (this entry only).

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import {
  EXPERIMENT_FEATURE_TIERS,
  isTierFeatureId,
  type TierFeatureId,
} from "../lib/experimentTiers";
// 722 起 rail 徽章档位改由 lab 布局数据驱动（railTiersFor 已删），等价物为
// labLayout.railTiersForDefault（内置默认布局上的徽章档位，562 测试同源）。
import { railTiersForDefault } from "../lab/labLayout";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

console.log("\ntask 603 tool-optimizations (edit readBack + gate linkage)");

// ① tier register.
{
  ok(isTierFeatureId("toolOptimizations"), "toolOptimizations is a registered TierFeatureId");
  // 任务 783④：编辑读回（readBack）随 22.5 交付转正——未稳定→推荐（Go 渲染
  // 表同步升档，562 两侧一致断言守护；815 收口本测试的过期 pin）。
  ok(EXPERIMENT_FEATURE_TIERS.toolOptimizations === "recommended", "toolOptimizations badges as RECOMMENDED (783④ 转正)");
  // 任务 789：editEvidenceRelaxed 以成员行入卡（unstable）——卡头聚合 =
  // max(卡 recommended, 成员 unstable) = recommended 单枚（774 口径）。
  // 任务 971：聚合页扩员——multiEditInjection（可选）+ restartUpdate（推荐）
  // 转成员行，聚合输入扩为 [recommended, unstable, optional]，卡头仍单枚 推荐。
  const tiers = railTiersForDefault("toolOptimizations");
  ok(JSON.stringify(tiers) === JSON.stringify(["recommended", "optional", "unstable"]) && tiers[0] === "recommended",
    `rail entry aggregates [recommended, optional, unstable] to the single 推荐 badge (got ${JSON.stringify(tiers)})`);
}

// ② SettingsPanel wiring（722 起 rail 分组/条目数据驱动：组词表在
// labLayoutTypes，条目与其灯读数在 labLayoutDefault——锚点随机制迁移）。
{
  const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");
  const layoutTypes = readFileSync(fileURLToPath(new URL("../lab/labLayoutTypes.ts", import.meta.url)), "utf8");
  const layoutDefault = readFileSync(fileURLToPath(new URL("../lab/labLayoutDefault.ts", import.meta.url)), "utf8");
  ok(layoutTypes.includes('"tool-opt"'), "LabGroupKey carries the tool-opt group");
  ok(layoutDefault.includes('key: "tool-opt"') && layoutDefault.includes('labelKey: "settings.labGroup.toolOpt"'),
    "labGroups renders the tool-opt entry");
  // 任务 789：editEvidenceRelaxed 以成员行入卡——条目变为「主控+成员」形态。
  // 任务 971：聚合页扩员——multiEditInjection + restartUpdate 转成员行，
  // 卡灯三键任一非默认即亮；卡 labelKey 改指聚合页标题 toolOptHub。
  // 任务 979：capabilityGroups（能力组披露+session_hub 聚合件）真开关入卡
  // ——第四灯键+第四成员行（971 占位替换）。
  ok(layoutDefault.includes('{ id: "toolOptimizations", labelKey: "settings.toolOptHub", tier: "recommended", onKeys: ["experimentalToolOptimizations", "experimentalMultiEditInjection", "experimentalRestartUpdate", "experimentalCapabilityGroups"], members: [\n          { id: "editEvidenceRelaxed", labelKey: "settings.editEvidenceRelaxed", tier: "unstable" },\n          { id: "multiEditInjection", labelKey: "settings.multiEditInjection", tier: "optional" },\n          { id: "restartUpdate", labelKey: "settings.restartUpdate", tier: "recommended" },\n          { id: "capabilityGroups", labelKey: "settings.capabilityGroups", tier: "optional" },\n        ] }'),
    "rail entry reads its four switches and hosts the 789/943/971/979 member rows (81/123 lost-save rule; 971 聚合页)");
  ok(panel.includes("on: labEntryLightOn(entry, s)"), "panel derives each rail light from its own onKeys (81/123 lost-save rule)");
  ok(panel.includes('{selected === "toolOptimizations" && ('), "pane card located");
  ok(panel.includes('app.SetExperimentalToolOptimizations(on)'), "card writes through its own setter");
  ok(panel.includes('labLabel("toolOptimizations"'), "card label carries the tier badge");
  ok(panel.includes('t("settings.toolOptimizations.risk")'), "card carries the visible risk line");
  ok(panel.includes('setRestartNeeded(true)'), "card marks restart-needed on save");
}

// ③ locale keys in all three dialects.
{
  const keys = [
    "settings.labGroup.toolOpt",
    "settings.toolOptimizations",
    "settings.toolOptimizationsHint",
    "settings.toolOptimizations.on",
    "settings.toolOptimizations.off",
    "settings.toolOptimizations.risk",
  ];
  for (const locale of ["zh", "en", "zh-TW"]) {
    const src = readFileSync(fileURLToPath(new URL(`../locales/${locale}.ts`, import.meta.url)), "utf8");
    const missing = keys.filter((key) => !src.includes(`"${key}"`));
    ok(missing.length === 0, `${locale} carries all 6 keys (missing: ${JSON.stringify(missing)})`);
  }
}

// ④ bridge surface.
{
  const bridge = readFileSync(fileURLToPath(new URL("../lib/bridge.ts", import.meta.url)), "utf8");
  ok(bridge.includes("SetExperimentalToolOptimizations(enabled: boolean): Promise<void>;"), "bridge interface declares the setter");
  ok(bridge.includes("async SetExperimentalToolOptimizations() {}"), "browser mock implements the setter");
  for (const file of ["../lib/settingsViewTypes.ts", "../lib/types.ts"]) {
    const src = readFileSync(fileURLToPath(new URL(file, import.meta.url)), "utf8");
    ok(src.includes("experimentalToolOptimizations?:"), `${file} declares the settings field`);
  }
}

// ⑤ Go registry agreement for this entry.
{
  const goSrc = readFileSync(fileURLToPath(new URL("../../../../internal/config/render.go", import.meta.url)), "utf8");
  ok(goSrc.includes('{"toolOptimizations", LabTierRecommended, []string{"experimental_tool_optimizations"}}'),
    "Go labFeatureTiers tags toolOptimizations recommended (render.go, 783④ 转正)");
  ok(goSrc.includes("experimental_tool_optimizations = %v"), "render table emits the switch (81/123 lost-save rule)");
}

console.log(`\n  ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
