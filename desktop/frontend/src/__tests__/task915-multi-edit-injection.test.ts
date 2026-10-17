// Run: npx tsx src/__tests__/task915-multi-edit-injection.test.ts
// 任务 915 acceptance harness (multi_edit 注入面暴露——工具优化族键 + extras 通道):
//  ① Go boot wiring: providerSurfaceExtras carries multi_edit behind the
//     family switch (extras channel, stacks with restart_update), the
//     addBuiltins readBack flag resolves through ToolOptimizationsEnabled.
//     任务 943 拆分：extras 门改读 MultiEditInjectionEnabled（注入键缺省
//     回落族键），readBack 面仍读族键；
//  ② config *bool panel-layer default-on: the field is a *bool, the render
//     line is conditional (absent key never bakes to an explicit false), and
//     the setter materializes a non-nil value;
//  ③ desktop views: both settings views expose the field as *bool so nil
//     (key absent) reaches the panel;
//  ④ frontend default-on: the card and the lab light render `?? true` for a
//     null/undefined field, and the first-load effect materializes the key
//     by writing true;
//  ⑤ locale hints carry the injection effect and the boot-time timing note
//     in all three dialects（任务 943 后注入面文案迁至 multiEditInjection 卡，
//     文件级 needles 由两张卡任一携带即过）。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

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

console.log("\ntask 915 multi_edit injection (extras channel + panel-layer default-on)");

// ① Go boot wiring.
{
  const boot = readRepo("internal/boot/boot.go");
  // 任务 943：extras 门改读 MultiEditInjectionEnabled（注入键缺省回落族键）。
  ok(boot.includes(`if cfg.MultiEditInjectionEnabled() {
		extras = append(extras, "multi_edit")`), "providerSurfaceExtras appends multi_edit behind MultiEditInjectionEnabled (task-943 split)");
  // readBack 面仍走族键（addBuiltins 参数）。
  ok(boot.includes(`fileWriteReceipt, cfg.ToolOptimizationsEnabled())`), "addBuiltins readBack flag still resolves through the family helper");
  const extrasTest = readRepo("internal/boot/provider_surface_extras_test.go");
  ok(extrasTest.includes("TestProviderSurfaceExtrasCarryMultiEditWithToolOptimizations"), "extras gate has its own two-state pin");
  ok(extrasTest.includes("TestBootRequestCarriesMultiEditWhenFamilyLit"), "real-build capture pins tools + readBack schema");
  ok(extrasTest.includes("TestUseCapabilityDispatchesMultiEditWhileFamilyExplicitlyOff"), "execution-surface-always-available pin exists");
  // 任务 943：正反向独立两态的真实构建捕获。
  ok(extrasTest.includes("TestBootRequestReadBackOnInjectionExplicitlyOff"), "forward-split (readBack on, injection off) real-build capture exists");
  ok(extrasTest.includes("TestBootRequestInjectionOnFamilyExplicitlyOff"), "reverse-split (injection on, readBack off) real-build capture exists");
}

// ② config *bool + conditional render.
{
  const cfg = readRepo("internal/config/config.go");
  ok(cfg.includes("ExperimentalToolOptimizations *bool `toml:\"experimental_tool_optimizations\"`"), "config field is a *bool (panel-layer default-on)");
  ok(cfg.includes("c.Agent.ExperimentalToolOptimizations != nil && *c.Agent.ExperimentalToolOptimizations"), "ToolOptimizationsEnabled resolves nil as off");
  const render = readRepo("internal/config/render.go");
  ok(render.includes("if c.Agent.ExperimentalToolOptimizations != nil {"), "render is conditional (absent key stays absent)");
  const edit = readRepo("internal/config/edit.go");
  ok(edit.includes("c.Agent.ExperimentalToolOptimizations = &enabled"), "setter materializes a non-nil value");
}

// ③ desktop views.
{
  const app = readRepo("desktop/settings_app.go");
  const occurrences = app.split("ExperimentalToolOptimizations *bool `json:\"experimentalToolOptimizations\"`").length - 1;
  ok(occurrences === 2, `both settings views carry the *bool field (found ${occurrences})`);
  ok(app.includes("view.ExperimentalToolOptimizations = cfg.Agent.ExperimentalToolOptimizations"), "Settings() readback passes the pointer through");
}

// ④ frontend default-on display + first-load write.
{
  const panel = read("components/SettingsPanel.tsx");
  ok(panel.includes("(s.experimentalToolOptimizations ?? true) === on"), "card renders the default-on state for an absent key");
  ok(panel.includes("void app.SetExperimentalToolOptimizations(true)"), "first-load effect materializes the key by writing true");
  ok(panel.includes("s.experimentalToolOptimizations != null"), "the write only fires while the key is absent");
  const lightKeys = read("lab/labLightKeys.ts");
  ok(lightKeys.includes("(s: SettingsView) => s.experimentalToolOptimizations ?? true"), "lab light predicate matches the default-on display");
  for (const file of ["lib/settingsViewTypes.ts", "lib/types.ts"]) {
    const src = read(file);
    ok(src.includes("experimentalToolOptimizations?: boolean | null;"), `${file} declares the nullable field`);
  }
}

// ⑤ locale hints (second effect + timing) in all three dialects.
{
  const needles: Record<string, string[]> = {
    zh: ["multi_edit 直注", "启动时定格", "新装默认开启"],
    "zh-TW": ["multi_edit 直注", "啟動時定格", "新裝預設開啟"],
    en: ["multi_edit injection", "decided at boot", "Fresh installs default ON"],
  };
  for (const [locale, keys] of Object.entries(needles)) {
    const src = read(`locales/${locale}.ts`);
    for (const key of keys) {
      ok(src.includes(key), `${locale} hint carries "${key}"`);
    }
  }
}

console.log(`\n  ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
