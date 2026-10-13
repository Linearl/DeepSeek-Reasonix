// Run: npx tsx src/__tests__/task815-lab-entry-completeness.test.ts
// 任务 815 acceptance harness (实验室 UI 完整性补全——「config 键有渲染表
// 但无 lab 卡片」检查条目的锚测试)：
//  ① bash_heavy_guard（575 本尊）全链路：Go setter/视图/readback、bridge
//     接口+mock、pane 行、灯谓词、yaml+default onKeys、Go renderKeys 家族
//     登记（自豁免表迁入 optimisticParallel）、三语 locale；
//  ② editEvidenceRelaxed（789 预留登记窗口）：同上全链路 + Go 侧转正入表
//     （editEvidenceRelaxed 条目，未稳定档）；
//  ③ 补灯四键（半缺口收口——pane 行早已交付、卡灯漏接）：cascade_approval /
//     active_tab_resident / fallback_model 三键的 yaml onKeys + labLightKeys
//     谓词；collab_background_delivery 定档不建卡（boot-resolved 豁免理由）；
//  ④ REASONIX.md 交付检查条目在位（config 键有渲染表必须有实验室入口）。

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
const goRender = readFileSync(`${repoRoot}/internal/config/render.go`, "utf8");
const goEdit = readFileSync(`${repoRoot}/internal/config/edit.go`, "utf8");
const goPrefs = readFileSync(`${repoRoot}/desktop/settings_preferences.go`, "utf8");
const goSettingsApp = readFileSync(`${repoRoot}/desktop/settings_app.go`, "utf8");
const reasonix = readFileSync(`${repoRoot}REASONIX.md`, "utf8");

console.log("\ntask 815 lab entry completeness (config key ↔ lab entry)");

// ── ① bash_heavy_guard 全链路 ──
{
  const steps = [
    ["Go config setter", goEdit.includes("func (c *Config) SetExperimentalBashHeavyGuard(enabled bool) error")],
    ["Go App setter", goPrefs.includes("func (a *App) SetExperimentalBashHeavyGuard(enabled bool) error")],
    ["Go settings-view field + readback", goSettingsApp.includes("ExperimentalBashHeavyGuard bool `json:\"experimentalBashHeavyGuard\"`") && goSettingsApp.includes("view.ExperimentalBashHeavyGuard = cfg.Sandbox.ExperimentalBashHeavyGuard")],
    ["bridge interface", bridge.includes("SetExperimentalBashHeavyGuard(enabled: boolean): Promise<void>;")],
    ["bridge mock", bridge.includes("async SetExperimentalBashHeavyGuard() {},")],
    ["view-types field", viewTypes.includes("experimentalBashHeavyGuard?: boolean;")],
    ["types field", types.includes("experimentalBashHeavyGuard?: boolean;")],
    ["pane row wires the setter", panel.includes("app.SetExperimentalBashHeavyGuard(on)")],
    ["pane row reads the view field", panel.includes("Boolean(s.experimentalBashHeavyGuard) === on")],
    ["light predicate", lightKeys.includes("experimentalBashHeavyGuard: (s: SettingsView) => Boolean(s.experimentalBashHeavyGuard)")],
    ["yaml light ref", yaml.includes("onKeys: [optimisticWrite, experimentalBashHeavyGuard]")],
    ["default light ref", labDefault.includes('"optimisticWrite", "experimentalBashHeavyGuard"')],
    ["Go renderKeys family (exemption dropped)", goRender.includes('"optimistic_write", "experimental_bash_heavy_guard"') && !goRender.includes('"experimental_bash_heavy_guard":')],
    ["locale zh/en/zh-TW", ["locales/zh.ts", "locales/en.ts", "locales/zh-TW.ts"].every((f) => read(f).includes('"settings.bashHeavyGuardHint"'))],
  ] as const;
  for (const [label, hit] of steps) ok(Boolean(hit), `bash_heavy_guard: ${label}`);
}

// ── ② editEvidenceRelaxed（789 登记窗口收口）──
{
  const steps = [
    ["Go config setter", goEdit.includes("func (c *Config) SetExperimentalEditEvidenceRelaxed(enabled bool) error")],
    ["Go App setter", goPrefs.includes("func (a *App) SetExperimentalEditEvidenceRelaxed(enabled bool) error")],
    ["Go settings-view field + readback", goSettingsApp.includes("ExperimentalEditEvidenceRelaxed bool `json:\"experimentalEditEvidenceRelaxed\"`") && goSettingsApp.includes("view.ExperimentalEditEvidenceRelaxed = cfg.Agent.ExperimentalEditEvidenceRelaxed")],
    ["bridge interface + mock", bridge.includes("SetExperimentalEditEvidenceRelaxed(enabled: boolean): Promise<void>;") && bridge.includes("async SetExperimentalEditEvidenceRelaxed() {},")],
    ["view-types + types fields", viewTypes.includes("experimentalEditEvidenceRelaxed?: boolean;") && types.includes("experimentalEditEvidenceRelaxed?: boolean;")],
    ["pane row wrapped in labLabel (badge face)", panel.includes('labLabel("editEvidenceRelaxed"')],
    ["pane row wires the setter", panel.includes("app.SetExperimentalEditEvidenceRelaxed(on)")],
    ["Go register entry (exemption dropped)", goRender.includes('{"editEvidenceRelaxed", LabTierUnstable, []string{"experimental_edit_evidence_relaxed"}}') && !goRender.includes('"experimental_edit_evidence_relaxed":')],
    ["locale on/off in all three", ["locales/zh.ts", "locales/en.ts", "locales/zh-TW.ts"].every((f) => read(f).includes('"settings.editEvidenceRelaxed.on"'))],
  ] as const;
  for (const [label, hit] of steps) ok(Boolean(hit), `editEvidenceRelaxed: ${label}`);
}

// ── ③ 补灯四键与定档 ──
{
  ok(yaml.includes("onKeys: [experimentalSessionCollab, experimentalCascadeApproval]") && lightKeys.includes("experimentalCascadeApproval: (s: SettingsView) => Boolean(s.experimentalCascadeApproval)"),
    "cascade_approval light wired (sessionCollab card, Task 225 row)");
  ok(yaml.includes("experimentalCacheTuning, experimentalActiveTabResident]") && lightKeys.includes("experimentalActiveTabResident: (s: SettingsView) => Boolean(s.experimentalActiveTabResident)"),
    "active_tab_resident light wired (contextGovernance card, Task 347 row)");
  ok(yaml.includes("onKeys: [experimentalHighSpeedModel, experimentalFallbackModel]") && lightKeys.includes("experimentalFallbackModel: (s: SettingsView) => Boolean(s.experimentalFallbackModel)"),
    "fallback_model light wired (modelStrategy card, 722 点1 row)");
  // collab_background_delivery：定档不建卡——boot-resolved（collab_drain_gate
  // 快照，翻转重启才生效），config-only 是合理形态（B 档口径）。
  ok(goRender.includes("experimental_collab_background_delivery") && goRender.includes("boot-resolved"),
    "collab_background_delivery stays config-only with the boot-resolved reason");
  // homePointer 卡文案不再带「（实验）」标记（621 徽章口径：档位只走 TierBadge）。
  ok(!["locales/zh.ts", "locales/en.ts", "locales/zh-TW.ts"].some((f) => /（实验）|\(experimental\)|（實驗）/.test(read(f))),
    "no experimental-text marker in any locale (621 badge discipline)");
}

// ── ④ REASONIX.md 交付检查条目 ──
{
  ok(reasonix.includes("must have a lab") && reasonix.includes("task 815"),
    "REASONIX.md carries the render-table→lab-entry delivery check");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
