// Run: npx tsx src/__tests__/task603-tool-optimizations.test.ts
// 任务 603 acceptance harness (「工具优化」family first member: edit readBack):
//  ① the tier register carries toolOptimizations at the UNSTABLE tier (用户
//     定档 20261007: gate 联动是新行为变更，测试完再转其他徽章);
//  ② SettingsPanel wiring: the tool-opt group, the rail entry with its own
//     `on` read, the pane card with its own setter and a visible risk line;
//  ③ locale keys exist in all three dialects (group/label/hint/on/off/risk);
//  ④ the bridge surface: interface method + browser mock + settings view
//     field (experimentalToolOptimizations);
//  ⑤ the Go tier registry (render.go) tags experimental_tool_optimizations
//     unstable — two sides, one source (this entry only; the 545/504/551
//     mirror drift is the pre-existing task562 red, not this task's scope).

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import {
  EXPERIMENT_FEATURE_TIERS,
  isTierFeatureId,
  railTiersFor,
  type TierFeatureId,
} from "../lib/experimentTiers";

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
  ok(EXPERIMENT_FEATURE_TIERS.toolOptimizations === "unstable", "toolOptimizations badges as UNSTABLE (用户定档)");
  const tiers = railTiersFor("toolOptimizations");
  ok(tiers.length === 1 && tiers[0] === "unstable", `standalone rail entry shows [unstable] (got ${JSON.stringify(tiers)})`);
}

// ② SettingsPanel wiring.
{
  const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");
  ok(panel.includes('type LabGroupKey = "automation" | "efficiency" | "ui" | "observability" | "dev-debug" | "storage" | "infra" | "tool-opt";'),
    "LabGroupKey carries the tool-opt group");
  ok(panel.includes('{ key: "tool-opt", labelKey: "settings.labGroup.toolOpt" }'), "labGroups renders the tool-opt entry");
  ok(panel.includes('{ id: "toolOptimizations", group: "tool-opt", label: t("settings.toolOptimizations"), on: Boolean(s.experimentalToolOptimizations) }'),
    "rail entry reads its own switch (81/123 lost-save rule)");
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
  ok(goSrc.includes('{"toolOptimizations", LabTierUnstable, []string{"experimental_tool_optimizations"}}'),
    "Go labFeatureTiers tags toolOptimizations unstable (render.go)");
  ok(goSrc.includes("experimental_tool_optimizations = %v"), "render table emits the switch (81/123 lost-save rule)");
}

console.log(`\n  ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
