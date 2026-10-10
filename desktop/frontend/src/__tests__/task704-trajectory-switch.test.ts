// Run: tsx src/__tests__/task704-trajectory-switch.test.ts
// 任务 704: the trajectory view lab switch must survive a settings save and
// ship OFF (铁律 2) — with it off the topicbar renders exactly the pre-704
// layout and the transcript is the only surface. This harness pins the whole
// chain: Go config key + setter + both settings views + boot readback +
// render-table emission + tier registration (optional, counts bumped both
// sides), frontend SettingsView/bridge/labFlags/App wiring, lab render-table
// entry + detail card, three-locale keys, and the default-off guarantees.
import assert from "node:assert";
import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

let passed = 0;
function ok(cond: boolean, label: string) {
  if (!cond) {
    process.stderr.write(`FAIL ${label}\n`);
    process.exitCode = 1;
    return;
  }
  passed += 1;
  process.stdout.write(`  PASS  ${label}\n`);
}

const here = path.dirname(fileURLToPath(import.meta.url));
const frontendRoot = path.resolve(here, "..", "..");
const repoRoot = path.resolve(frontendRoot, "..", "..");
const panel = fs.readFileSync(path.join(frontendRoot, "src/components/SettingsPanel.tsx"), "utf8");
const view = fs.readFileSync(path.join(frontendRoot, "src/lib/settingsViewTypes.ts"), "utf8");
const types = fs.readFileSync(path.join(frontendRoot, "src/lib/types.ts"), "utf8");
const bridge = fs.readFileSync(path.join(frontendRoot, "src/lib/bridge.ts"), "utf8");
const tiers = fs.readFileSync(path.join(frontendRoot, "src/lib/experimentTiers.ts"), "utf8");
const labFlags = fs.readFileSync(path.join(frontendRoot, "src/lib/labFlags.ts"), "utf8");
const appTsx = fs.readFileSync(path.join(frontendRoot, "src/App.tsx"), "utf8");
const zh = fs.readFileSync(path.join(frontendRoot, "src/locales/zh.ts"), "utf8");
const en = fs.readFileSync(path.join(frontendRoot, "src/locales/en.ts"), "utf8");
const zhTw = fs.readFileSync(path.join(frontendRoot, "src/locales/zh-TW.ts"), "utf8");
const settingsApp = fs.readFileSync(path.join(repoRoot, "desktop/settings_app.go"), "utf8");
const settingsPrefs = fs.readFileSync(path.join(repoRoot, "desktop/settings_preferences.go"), "utf8");
const goConfig = fs.readFileSync(path.join(repoRoot, "internal/config/desktop_preferences.go"), "utf8");
const goEdit = fs.readFileSync(path.join(repoRoot, "internal/config/edit.go"), "utf8");
const goRender = fs.readFileSync(path.join(repoRoot, "internal/config/render.go"), "utf8");
const goTierTest = fs.readFileSync(path.join(repoRoot, "internal/config/render_lab_tiers_test.go"), "utf8");
// 任务 742 修复预存红：724 起 rail 布局由 lab-layout.yaml + 内置默认数据驱动，
// SettingsPanel 不再硬编码 render table——trajectoryView 的布局锚随迁两文件。
const labDefaultLayout = fs.readFileSync(path.join(frontendRoot, "src/lab/labLayoutDefault.ts"), "utf8");
const labYaml = fs.readFileSync(path.join(frontendRoot, "src/lab/lab-layout.yaml"), "utf8");

console.log("\n任务 704 trajectory view: lab switch persistence + default-off contract");

// 1. Go config: the key exists on the desktop preferences struct with a
//    zero-value false default (铁律 2 — a plain bool field ships off).
ok(goConfig.includes('ExperimentalTrajectoryView bool `toml:"experimental_trajectory_view"`'),
  "Go DesktopPreferences carries experimental_trajectory_view");
ok(goEdit.includes("func (c *Config) SetExperimentalTrajectoryView(enabled bool) error {") &&
  goEdit.includes("c.Desktop.ExperimentalTrajectoryView = enabled"),
  "Go config setter writes the desktop key");

// 2. Render table emits the key (fixed-key-set rule: a missing line would
//    flip the switch back to off on every save — 81/123 lesson).
ok(goRender.includes('fmt.Fprintf(&b, "experimental_trajectory_view = %v'),
  "render table emits experimental_trajectory_view");

// 3. Tier register (Go side): trajectoryView registered, optional tier.
ok(/\{"trajectoryView", LabTierOptional, \[\]string\{"experimental_trajectory_view"\}\}/.test(goRender),
  "Go labFeatureTiers registers trajectoryView as optional");
// 计数 pin 随每次表A 变更步进（704 落地时 21/50；727 → 22/51；742 → 22/51，
// Go 侧只收 heartbeatBackground、不收纯前端容器 tabManagement）。
ok(goTierTest.includes("LabTierOptional: 22") && goTierTest.includes("len(labFeatureTiers) != 51"),
  "Go tier-count test bumped to optional 22 / total 51 (742 口径)");

// 4. Both settings views carry the field (81/123: a view that omits it would
//    read permanently off) and the boot snapshot fills it.
const structFields = settingsApp.split('ExperimentalTrajectoryView bool   `json:"experimentalTrajectoryView"`').length - 1;
ok(structFields === 2, `both SettingsView and DesktopStartupSettingsView carry the field (got ${structFields})`);
ok(settingsApp.includes("view.ExperimentalTrajectoryView = cfg.Desktop.ExperimentalTrajectoryView"),
  "boot snapshot readback fills ExperimentalTrajectoryView");
ok(settingsApp.includes("ExperimentalTrajectoryView:      cfg.Desktop.ExperimentalTrajectoryView"),
  "Settings() literal fills ExperimentalTrajectoryView");
ok(settingsPrefs.includes("func (a *App) SetExperimentalTrajectoryView(enabled bool) error {"),
  "desktop app exposes SetExperimentalTrajectoryView");

// 5. Frontend view types + bridge.
ok(types.includes("experimentalTrajectoryView?: boolean;"),
  "SettingsView type carries experimentalTrajectoryView");
ok(view.includes("experimentalTrajectoryView?: boolean;"),
  "settingsViewTypes carries experimentalTrajectoryView");
ok(bridge.includes("SetExperimentalTrajectoryView(enabled: boolean): Promise<void>;"),
  "bridge interface declares SetExperimentalTrajectoryView");
ok(bridge.includes("async SetExperimentalTrajectoryView() {}"),
  "bridge mock implements SetExperimentalTrajectoryView");

// 6. Lab flag feed: default false + App wiring (铁律 2 — the frontend default
//    must not depend on the backend snapshot arriving).
ok(/trajectoryView: false,/.test(labFlags), "labFlags defaults trajectoryView to false");
ok(labFlags.includes("trajectoryView=${flags.trajectoryView}"),
  "labFlags boot log includes trajectoryView");
ok(appTsx.includes("trajectoryView: settings.experimentalTrajectoryView ?? false"),
  "App applyDesktopPreferences feeds the trajectoryView flag");
ok(appTsx.includes("experimentalTrajectoryView?: boolean"),
  "App applyDesktopPreferences accepts experimentalTrajectoryView");

// 7. Lab rail entry + detail card (81/123 lost-save rule).
// 任务 742 修复预存红：724 起 render table 移交 lab-layout.yaml + 内置默认
// 数据，SettingsPanel 不再硬编码条目——布局锚改为钉两份数据源。
ok(labDefaultLayout.includes('{ id: "trajectoryView", labelKey: "settings.trajectoryView", tier: "optional", onKeys: ["experimentalTrajectoryView"] }'),
  "built-in default layout has the trajectoryView entry in the ui group (724 数据源)");
ok(labYaml.includes("- id: trajectoryView") && labYaml.includes("labelKey: settings.trajectoryView"),
  "lab-layout.yaml carries the trajectoryView entry (724 数据源)");
ok(panel.includes('| "trajectoryView"'), "ExperimentFeatureId union includes trajectoryView");
ok(panel.includes('selected === "trajectoryView"') &&
  panel.includes("app.SetExperimentalTrajectoryView(on)"),
  "detail card wires the setter");
// 成员行在 union 中部（后随注释行），行尾分号可有可无——按行锚定，不再
// 锁死「union 最后一位」的旧形态（727 插入 heartbeatRotation 后位移）。
ok(/^\s*\| "trajectoryView";?\s*$/m.test(tiers),
  "TierFeatureId union includes trajectoryView");
ok(tiers.includes('trajectoryView: "optional"'), "frontend tier register: trajectoryView is optional");
// 计数 pin 随每次表A 变更步进（704 落地时 21；727 → 22；742 → 24，含
// tabManagement 容器 + heartbeatBackground 两个可选档）。
ok(/recommended: 15,\s*\n\s*optional: 24,/.test(tiers),
  "LAB_TIER_COUNTS bumped to optional 24 (742 口径)");

// 8. Three-locale keys.
for (const [name, src] of [["zh", zh], ["en", en], ["zh-TW", zhTw]] as const) {
  ok(src.includes('"settings.trajectoryView"') &&
    src.includes('"settings.trajectoryViewHint"') &&
    src.includes('"settings.trajectoryView.on"') &&
    src.includes('"settings.trajectoryView.off"'),
    `${name} locale carries the four trajectoryView keys`);
}

// 9. experimentTiers test counts moved with the register (the 562 harness
//    pins the distribution — a silent mismatch would fail CI elsewhere).
//    计数 pin 随每次表A 变更步进（704 落地时 21/50；727 → 22/51；742 → 24/53）。
const tierTest = fs.readFileSync(path.join(frontendRoot, "src/__tests__/task562-experiment-tiers.test.ts"), "utf8");
ok(tierTest.includes("counts.optional === 24") && tierTest.includes("length === 53"),
  "task562 test pins the 53-entry register (742 口径)");

console.log(`\n${passed} checks passed${process.exitCode ? " (with failures)" : ""}`);
if (!process.exitCode) process.stdout.write("task 704 trajectory switch contract: OK\n");
