// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task979-capability-groups-switch.test.ts
// 任务 979 acceptance harness（实验室三个新能力开关无 UI 入口收口）：
//  ① UI 开关→config 写入链路：SettingsPanel 真开关读 experimentalCapabilityGroups
//     视图字段、写 app.SetExperimentalCapabilityGroups；bridge 接口+mock 齐；
//     Go 侧 config setter + App setter + settings 视图字段 + Settings() readback
//     四点全在（971 占位 span 退役——占位只显示字符串文本，从未挂接）；
//  ② boot 门控链路（端到端可断言部分）：boot.go 三处门控仍读
//     cfg.Agent.ExperimentalCapabilityGroups（session_hub 注册 / provider
//     可见面 extras / 目录组归属走 catalog CapabilityGroups），Go 侧
//     session_hub_boot_test 已钉开关双态注册+可见面（此处 grep 锚定不重复
//     断言运行时行为，只钉「UI 写的键 = boot 读的键」同键不漂移）；
//  ③ lab 登记：档位注册表（可选档）+ 灯键谓词 + yaml 与内置默认成员行同构
//     + 卡灯第四键 + Go 渲染表入表A（豁免表迁出）；
//  ④ 三语：开关 label/hint/on/off 在 zh / zh-TW / en 齐备；占位键三语退役。

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

const panel = read("components/SettingsPanel.tsx");
const bridge = read("lib/bridge.ts");
const viewTypes = read("lib/settingsViewTypes.ts");
const types = read("lib/types.ts");
const lightKeys = read("lab/labLightKeys.ts");
const yaml = read("lab/lab-layout.yaml");
const labDefault = read("lab/labLayoutDefault.ts");
const tiers = read("lib/experimentTiers.ts");
const goEdit = readFileSync(`${repoRoot}/internal/config/edit.go`, "utf8");
const goPrefs = readFileSync(`${repoRoot}/desktop/settings_preferences.go`, "utf8");
const goSettingsApp = readFileSync(`${repoRoot}/desktop/settings_app.go`, "utf8");
const goRender = readFileSync(`${repoRoot}/internal/config/render.go`, "utf8");
const goBoot = readFileSync(`${repoRoot}/internal/boot/boot.go`, "utf8");

console.log("\ntask 979 capability-groups switch: UI entry → config write → boot gate chain");

// ── ① UI 开关→config 写入链路 ──
{
  const steps = [
    ["pane switch wires the App setter", panel.includes("app.SetExperimentalCapabilityGroups(on)")],
    ["pane switch reads the view field", panel.includes("Boolean(s.experimentalCapabilityGroups) === on")],
    ["pane switch marked restart-needed (boot snapshot)", /SetExperimentalCapabilityGroups[\s\S]{0,200}setRestartNeeded\(true\)/.test(panel)],
    ["pane switch wrapped in labLabel (badge face)", panel.includes('labLabel("capabilityGroups"')],
    ["pane switch carries the risk line", panel.includes('t("settings.capabilityGroups.risk")')],
    ["bridge interface", bridge.includes("SetExperimentalCapabilityGroups(enabled: boolean): Promise<void>;")],
    ["bridge mock", bridge.includes("async SetExperimentalCapabilityGroups() {},")],
    ["view-types field", viewTypes.includes("experimentalCapabilityGroups?: boolean;")],
    ["types field", types.includes("experimentalCapabilityGroups?: boolean;")],
    ["Go config setter", goEdit.includes("func (c *Config) SetExperimentalCapabilityGroups(enabled bool) error")],
    ["Go App setter", goPrefs.includes("func (a *App) SetExperimentalCapabilityGroups(enabled bool) error")],
    ["Go settings-view field + readback", goSettingsApp.includes("ExperimentalCapabilityGroups bool `json:\"experimentalCapabilityGroups\"`") && goSettingsApp.includes("ExperimentalCapabilityGroups: cfg.Agent.ExperimentalCapabilityGroups")],
    ["971 placeholder span retired", !panel.includes('data-testid="session-hub-slot"')],
  ] as const;
  for (const [label, hit] of steps) ok(Boolean(hit), `chain: ${label}`);
}

// ── ② boot 门控链路（UI 写的键 = boot 读的键，同键不漂移）──
{
  const gates = [
    ["session_hub registration gate", goBoot.includes("if cfg.Agent.ExperimentalCapabilityGroups {") && goBoot.includes("agent.NewSessionHubTool(")],
    ["provider-visible surface extras gate", /if cfg\.Agent\.ExperimentalCapabilityGroups \{\s*\n\s*extras = append\(extras, "session_hub"\)/.test(goBoot)],
    ["catalog group disclosure option", goBoot.includes("CapabilityGroups: cfg.Agent.ExperimentalCapabilityGroups")],
    ["use_capability runtime propagation", goBoot.includes("capRuntime.SetCapabilityGroups(cfg.Agent.ExperimentalCapabilityGroups)")],
    ["route-catalog controller propagation", goBoot.includes("ctrl.SetCapabilityGroups(cfg.Agent.ExperimentalCapabilityGroups)")],
  ] as const;
  for (const [label, hit] of gates) ok(Boolean(hit), `boot gate: ${label}`);
  // 运行时双态行为由 Go 侧 session_hub_boot_test.go / provider_surface_extras_test.go
  // 钉住（真实 BuildRuntime 双态断言）——此处不重复，只锚定键名一致。
  const goHubTest = readFileSync(`${repoRoot}/internal/boot/session_hub_boot_test.go`, "utf8");
  ok(goHubTest.includes("experimental_capability_groups = true"),
    "Go boot test drives the SAME key end-to-end (off = unregistered / on = registered + provider-visible)");
}

// ── ③ lab 登记（档位/灯/yaml/渲染表）──
{
  ok(tiers.includes('| "capabilityGroups"') && tiers.includes('capabilityGroups: "optional",'),
    "tier register carries capabilityGroups at the optional tier (用户拍板提效类可选档)");
  ok(lightKeys.includes("experimentalCapabilityGroups: (s: SettingsView) => Boolean(s.experimentalCapabilityGroups)"),
    "light predicate wired");
  ok(yaml.includes("- id: capabilityGroups") && yaml.includes("labelKey: settings.capabilityGroups"),
    "lab-layout.yaml carries the capabilityGroups member row");
  ok(labDefault.includes('id: "capabilityGroups", labelKey: "settings.capabilityGroups", tier: "optional"'),
    "built-in default mirrors the member row (724 fallback is seamless)");
  ok(yaml.includes("onKeys: [experimentalToolOptimizations, experimentalMultiEditInjection, experimentalRestartUpdate, experimentalCapabilityGroups]") &&
     labDefault.includes('onKeys: ["experimentalToolOptimizations", "experimentalMultiEditInjection", "experimentalRestartUpdate", "experimentalCapabilityGroups"]'),
    "hub card light covers the fourth key in both layout sources");
  ok(goRender.includes('{"capabilityGroups", LabTierOptional, []string{"experimental_capability_groups"}}'),
    "Go render table registers the entry in 表A (tool-opt section)");
  ok(!goRender.includes('"experimental_capability_groups":'),
    "the labNonFeatureKeys exemption is dropped (registered features cannot stay exempt)");
}

// ── ④ 三语 + 占位键退役 ──
{
  const keys = [
    "settings.capabilityGroups",
    "settings.capabilityGroupsHint",
    "settings.capabilityGroups.on",
    "settings.capabilityGroups.off",
    "settings.capabilityGroups.risk",
  ];
  for (const locale of ["zh", "zh-TW", "en"]) {
    const src = read(`locales/${locale}.ts`);
    const missing = keys.filter((k) => !src.includes(`"${k}"`));
    ok(missing.length === 0, `${locale} carries all capabilityGroups keys${missing.length ? ` (missing ${missing.join(", ")})` : ""}`);
    ok(!src.includes('"settings.toolOptHub.sessionHubSlot"') && !src.includes('"settings.toolOptHub.sessionHubSlotHint"'),
      `${locale} drops the retired placeholder keys`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
