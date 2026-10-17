// Run: npx tsx src/__tests__/task943-split-tool-optimizations.test.ts
// 任务 943 acceptance harness (实验开关拆分：编辑读回 readBack × multi_edit 直注直注):
//  ① Go config: the new key experimental_multi_edit_injection is a *bool with
//     the migration fallback (absent key falls back to the legacy family key
//     so legacy family-on configs keep the injection face), the setter
//     materializes a non-nil value, the render line is conditional, and the
//     tier register carries the new entry (optional) while the family entry
//     stays recommended;
//  ② Go boot: providerSurfaceExtras reads MultiEditInjectionEnabled; the
//     readBack face (addBuiltins / Workspace) still resolves through the
//     family key; the forward/reverse real-build captures exist;
//  ③ desktop bridge: both settings views carry the *bool field, the App
//     setter exists, and bridge.ts declares + mocks it;
//  ④ frontend panel: the pane union + whitelist carry the new card id, the
//     card renders the SAME fallback chain as the Go side
//     (injection ?? family ?? true) and writes through its own setter, the
//     lab light predicate matches, and the lab layout (default + yaml)
//     carries the standalone rail entry at the optional tier;
//  ⑤ locale: all three dialects carry the new card keys (label/hint/on/off/
//     risk) and the family hint no longer claims the injection effect.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { EXPERIMENT_FEATURE_TIERS, isTierFeatureId } from "../lib/experimentTiers";
import { railTiersForDefault } from "../lab/labLayout";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const frontendSrc = fileURLToPath(new URL("../", import.meta.url));
const repoRoot = fileURLToPath(new URL("../../../..", import.meta.url));
const read = (p: string) => readFileSync(`${frontendSrc}${p}`, "utf8");
const readRepo = (p: string) => readFileSync(`${repoRoot}${p}`, "utf8");

console.log("\ntask 943 split: readBack × multi_edit injection as independent switches");

// ① Go config: field + fallback + setter + conditional render + tier entry.
{
  const cfg = readRepo("internal/config/config.go");
  ok(cfg.includes("ExperimentalMultiEditInjection *bool `toml:\"experimental_multi_edit_injection\"`"),
    "config carries the new *bool field");
  ok(cfg.includes("func (c *Config) MultiEditInjectionEnabled() bool"),
    "MultiEditInjectionEnabled helper exists");
  ok(cfg.includes("return c.ToolOptimizationsEnabled()"),
    "absent injection key falls back to the family key (migration)");
  // 族键注释收窄为 readBack 面（迁移注释存在）。
  ok(cfg.includes("943（迁移收窄）"), "family key carries the task-943 migration note");
  const edit = readRepo("internal/config/edit.go");
  ok(edit.includes("c.Agent.ExperimentalMultiEditInjection = &enabled"),
    "setter materializes a non-nil value");
  const render = readRepo("internal/config/render.go");
  ok(render.includes("if c.Agent.ExperimentalMultiEditInjection != nil {"),
    "render is conditional (absent key never bakes the fallback)");
  ok(render.includes("experimental_multi_edit_injection = %v"),
    "render table emits the new key (81/123 lost-save rule)");
  ok(render.includes(`{"multiEditInjection", LabTierOptional, []string{"experimental_multi_edit_injection"}}`),
    "Go tier register carries multiEditInjection at the optional tier");
  ok(render.includes(`{"toolOptimizations", LabTierRecommended, []string{"experimental_tool_optimizations"}}`),
    "family tier entry stays recommended (783④ 转正不变)");
}

// ② Go boot: extras gate + readBack face + real-build captures.
{
  const boot = readRepo("internal/boot/boot.go");
  ok(boot.includes(`if cfg.MultiEditInjectionEnabled() {
		extras = append(extras, "multi_edit")`), "extras gate reads the split injection helper");
  ok(boot.includes(`fileWriteReceipt, cfg.ToolOptimizationsEnabled())`),
    "readBack face still resolves through the family key");
  const ws = readRepo("internal/tool/builtin/workspace.go");
  ok(ws.includes("readBack: w.ToolOptimizations}"),
    "Workspace edit tools still light readBack from the family field");
  const extrasTest = readRepo("internal/boot/provider_surface_extras_test.go");
  ok(extrasTest.includes("TestBootRequestReadBackOnInjectionExplicitlyOff"),
    "forward-split real-build capture exists (readBack on, injection off)");
  ok(extrasTest.includes("TestBootRequestInjectionOnFamilyExplicitlyOff"),
    "reverse-split real-build capture exists (injection on, readBack off)");
  ok(extrasTest.includes("task-943 migration fallback"),
    "extras matrix re-pins the migration fallback");
  const cfgTest = readRepo("internal/config/multi_edit_injection_943_test.go");
  ok(cfgTest.includes("TestMultiEditInjectionFallbackSemantics"),
    "config-layer fallback matrix test exists");
  ok(cfgTest.includes("TestRenderEmitsSplitKeysConditionally"),
    "config-layer conditional render test exists");
}

// ③ desktop views + bridge.
{
  const app = readRepo("desktop/settings_app.go");
  const occurrences = app.split("ExperimentalMultiEditInjection *bool `json:\"experimentalMultiEditInjection\"`").length - 1;
  ok(occurrences === 2, `both settings views carry the new *bool field (found ${occurrences})`);
  ok(app.includes("view.ExperimentalMultiEditInjection = cfg.Agent.ExperimentalMultiEditInjection"),
    "Settings() readback passes the pointer through");
  ok(app.includes("ExperimentalMultiEditInjection: cfg.Agent.ExperimentalMultiEditInjection"),
    "startup view readback passes the pointer through");
  const prefs = readRepo("desktop/settings_preferences.go");
  ok(prefs.includes("func (a *App) SetExperimentalMultiEditInjection(enabled bool) error"),
    "App setter exists");
  const bridge = read("lib/bridge.ts");
  ok(bridge.includes("SetExperimentalMultiEditInjection(enabled: boolean): Promise<void>;"),
    "bridge interface declares the setter");
  ok(bridge.includes("async SetExperimentalMultiEditInjection() {}"),
    "browser mock implements the setter");
  for (const file of ["lib/settingsViewTypes.ts", "lib/types.ts"]) {
    const src = read(file);
    ok(src.includes("experimentalMultiEditInjection?: boolean | null;"), `${file} declares the nullable field`);
  }
  const desktopTest = readRepo("desktop/settings_multi_edit_injection_943_test.go");
  ok(desktopTest.includes("TestSettingsViewMultiEditInjectionThreeStates"),
    "desktop three-state view test exists");
  ok(desktopTest.includes("TestMultiEditInjectionFallsBackToLegacyFamilyKey"),
    "desktop migration fallback test exists");
}

// ④ frontend panel wiring: union + whitelist + card + light + layout + tiers.
{
  const panel = read("components/SettingsPanel.tsx");
  ok(panel.includes(`  | "multiEditInjection"`), "pane id union carries multiEditInjection");
  ok(panel.includes(`  "multiEditInjection",`), "pane whitelist carries multiEditInjection");
  ok(panel.includes('{selected === "multiEditInjection" && ('), "standalone card branch located");
  // 卡片显示与 Go 侧同一条回落链。
  ok(panel.includes("(s.experimentalMultiEditInjection ?? (s.experimentalToolOptimizations ?? true)) === on"),
    "card renders the SAME fallback chain as the Go side");
  ok(panel.includes('app.SetExperimentalMultiEditInjection(on)'),
    "card writes through its own setter");
  ok(panel.includes('labLabel("multiEditInjection"'), "card label carries the tier badge");
  ok(panel.includes('t("settings.multiEditInjection.risk")'), "card carries its own risk line");
  ok(panel.includes("setRestartNeeded(true)"), "card marks restart-needed on save");
  // A 卡不再提注入面（收窄为 readBack）。
  ok(!panel.includes("settings.multiEditInjectionHint\")}") || panel.indexOf('selected === "multiEditInjection"') < panel.indexOf("settings.multiEditInjectionHint"),
    "the injection hint lives on the new card, not the family card");
  const lightKeys = read("lab/labLightKeys.ts");
  ok(lightKeys.includes("s.experimentalMultiEditInjection ?? (s.experimentalToolOptimizations ?? true)"),
    "lab light predicate matches the fallback chain");
  const layoutDefault = read("lab/labLayoutDefault.ts");
  ok(layoutDefault.includes('{ id: "multiEditInjection", labelKey: "settings.multiEditInjection", tier: "optional", onKeys: ["experimentalMultiEditInjection"] }'),
    "built-in default layout carries the standalone rail entry at the optional tier");
  const yaml = read("lab/lab-layout.yaml");
  ok(yaml.includes("id: multiEditInjection") && yaml.includes("onKeys: [experimentalMultiEditInjection]"),
    "lab-layout.yaml carries the standalone rail entry");
  // 徽章注册表（前端镜像）。
  ok(isTierFeatureId("multiEditInjection"), "multiEditInjection is a registered TierFeatureId");
  ok(EXPERIMENT_FEATURE_TIERS.multiEditInjection === "optional", "multiEditInjection badges as OPTIONAL");
  ok(EXPERIMENT_FEATURE_TIERS.toolOptimizations === "recommended", "toolOptimizations keeps the RECOMMENDED badge");
  const tiers = railTiersForDefault("multiEditInjection");
  ok(JSON.stringify(tiers) === JSON.stringify(["optional"]),
    `standalone entry badges itself (got ${JSON.stringify(tiers)})`);
}

// ⑤ locale keys in all three dialects + family hint narrowed.
{
  const keys = [
    "settings.multiEditInjection",
    "settings.multiEditInjectionHint",
    "settings.multiEditInjection.on",
    "settings.multiEditInjection.off",
    "settings.multiEditInjection.risk",
  ];
  for (const locale of ["zh", "zh-TW", "en"]) {
    const src = read(`locales/${locale}.ts`);
    const missing = keys.filter((key) => !src.includes(`"${key}"`));
    ok(missing.length === 0, `${locale} carries all 5 new keys (missing: ${JSON.stringify(missing)})`);
  }
  // 族卡 hint 不再宣称「族开启时 multi_edit 进入模型可见列表」的旧双效果口径。
  const zh = read("locales/zh.ts");
  ok(!zh.includes("「工具优化」族（两项效果）"), "zh family hint no longer claims the two-effect family");
  ok(zh.includes("本卡不再控制模型可见工具列表"), "zh family hint states the split");
  const en = read("locales/en.ts");
  ok(!en.includes("Tool-optimization family (two effects)"), "en family hint no longer claims the two-effect family");
  const tw = read("locales/zh-TW.ts");
  ok(!tw.includes("「工具最佳化」族（兩項效果）"), "zh-TW family hint no longer claims the two-effect family");
}

console.log(`\n  ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
