// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1796-autopilot-composer-offer.test.ts
// tl1796 — composer 模式条 autopilot 档永久消失（模式下拉不再出现 autopilot，
// 关 tab 重开无效）。
//
// 根因（验收件①，三选一判据 = 写点缺失 + 读源随键消亡）：tl1741 撤
// [desktop] autopilot 偏好键时连带撤了 SettingsView.Autopilot 镜像与
// SetDesktopAutopilot 写点，但前端门（App.tsx setAutopilotEnabled）仍读
// settings.autopilot === true——字段永远 undefined ⇒ autopilotEnabled 恒
// false ⇒ Composer 下拉（6597 options spread / 6645 模式条按钮）永久不含
// autopilot 档；关 tab 重开/重启均无效（读源是全局 settings，与 per-tab
// profile 无关）。用户侧观感「切 yolo 后消失」= 切档前审批选择器显示当前
// 档 autopilot 值，切走后选项消失（该值从来不在 options 清单里）。
//
// 修法（验收件②③）：新 opt-in 写点 experimental_autopilot_composer 全链
// 贯通——config 字段+setter+固定键集渲染行+双 View 字段+双 populate+App
// 包装+lab 卡行级开关+App.tsx 读源改新键。默认关（铁律 2：关态下拉与引入前
// 逐字节一致）；开总闸→切 yolo→下拉仍含 autopilot→关 tab 重开仍在（全局键
// 不随 per-tab 档位动作变化）；总闸 off 选项隐藏（opt-in 语义不变）。
// 持久化往返由 internal/config/autopilot_composer_tl1796_test.go 钉死。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const here = fileURLToPath(new URL(".", import.meta.url));
const frontendRoot = `${here}../..`;
const repoRoot = `${frontendRoot}/../..`;
const read = (p: string) => readFileSync(`${frontendRoot}/${p}`, "utf8");
const readRepo = (p: string) => readFileSync(`${repoRoot}/${p}`, "utf8");

console.log("\ntl1796 composer autopilot offer gate (write-point repair after tl1741 retirement)");

// ── ① 读源换新键：死读清零（根因落点）──────────────────────────
{
  const app = read("src/App.tsx");
  ok(app.includes("setAutopilotEnabled(settings.experimentalAutopilotComposer === true)"),
    "App.tsx: the offer gate reads the opt-in config key (fresh write point)");
  ok(!app.includes("setAutopilotEnabled(settings.autopilot === true)"),
    "App.tsx: the dead read of the retired settings.autopilot mirror is gone (call form)");
  // 回放面：load/save 回放与新键同路（applyDesktopPreferences 唯一写点语义不变）。
  ok(app.includes("autopilotEnabled = useAppLifecycleStore((s) => s.autopilotEnabled)"),
    "App.tsx: the gate still flows through the lifecycle store (no second writer)");
}

// ── ② 写点：lab 卡行级开关接线（新 setter 落 config）───────────
{
  const panel = read("src/components/ExperimentalLabSection.tsx");
  ok(panel.includes('testId="lab-toggle-autopilot"'),
    "lab card: the autopilot row keeps its lab-toggle-autopilot anchor (tl1791 deeplink stable)");
  ok(panel.includes("app.SetExperimentalAutopilotComposer("),
    "lab card: the row-level switch wires the fresh setter (tl1741 退役 setter 不回潮由 561 负钉)");
  ok(panel.includes("Boolean(s.experimentalAutopilotComposer)"),
    "lab card: the switch state reads the fresh view field");
  ok(!panel.includes("app.SetDesktopAutopilot("),
    "lab card: the retired three-key master setter stays gone (tl1741 non-goal)");
}

// ── ③ 消费面：下拉/模式条门不变（465 两维矩阵形态保持）─────────
{
  const composer = read("src/components/Composer.tsx");
  ok(composer.includes('...(autopilotEnabled ? [{ value: "autopilot"'),
    "composer: the dropdown option spread stays gated on autopilotEnabled");
  ok(composer.includes('data-autopilot={autopilotEnabled ? "on" : "off"}'),
    "composer: the modebar data flag stays gated on autopilotEnabled (465 pin)");
}

// ── ④ Go 全链：字段/setter/渲染行/双 View/双 populate/包装/账本 ──
{
  const prefs = readRepo("internal/config/desktop_preferences.go");
  ok(prefs.includes('ExperimentalAutopilotComposer bool `toml:"experimental_autopilot_composer"`'),
    "config: the [desktop] opt-in key exists on the struct");
  const edit = readRepo("internal/config/edit.go");
  ok(edit.includes("func (c *Config) SetExperimentalAutopilotComposer(enabled bool) error"),
    "config: the flip setter exists");
  const render = readRepo("internal/config/render.go");
  ok(render.includes('fmt.Fprintf(&b, "experimental_autopilot_composer = %v'),
    "config: the fixed-key-set render line exists (81/123 lesson — unlisted keys drop on save)");
  ok(render.includes('"experimental_autopilot_composer":'),
    "config: the key is registered in labNonFeatureKeys (tl1598 宪法 IV 账本门禁)");
  const settings = readRepo("desktop/settings_app.go");
  ok((settings.match(/ExperimentalAutopilotComposer bool\s+`json:"experimentalAutopilotComposer"`/g) ?? []).length === 2,
    "desktop: the view field is carried by BOTH SettingsView and DesktopStartupSettingsView (759 two-view lesson)");
  ok(settings.includes("view.ExperimentalAutopilotComposer = cfg.Desktop.ExperimentalAutopilotComposer"),
    "desktop: the startup-view populate exists (startup replay feeds the gate)");
  ok(settings.includes("ExperimentalAutopilotComposer: cfg.Desktop.ExperimentalAutopilotComposer"),
    "desktop: the full SettingsView populate exists (load/save replay feeds the gate)");
  const wrapper = readRepo("desktop/settings_preferences.go");
  ok(wrapper.includes("func (a *App) SetExperimentalAutopilotComposer(enabled bool) error"),
    "desktop: the App-level wrapper exposes the setter to wails");
}

// ── ⑤ TS 双类型 + locale ────────────────────────────────────────
{
  const types = read("src/lib/types.ts");
  ok(types.includes("experimentalAutopilotComposer?: boolean"),
    "types: SettingsView carries the fresh field");
  const startup = read("src/lib/settingsViewTypes.ts");
  ok(startup.includes("experimentalAutopilotComposer?: boolean"),
    "types: the startup view carries the fresh field (two-view lesson)");
  ok(read("src/lib/bridge.ts").includes("SetExperimentalAutopilotComposer(enabled: boolean): Promise<void>;"),
    "bridge: AppBindings carries the setter declaration (_CheckGenToApp 防漂移面同签)");
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = read(`src/locales/${locale}.ts`);
    ok(dict.includes('"settings.autopilotOfferHint"') && dict.includes('"settings.autopilot.on"') && dict.includes('"settings.autopilot.off"'),
      `${locale}: the offer hint + on/off labels are in place`);
  }
}

assert.equal(failed, 0, `${failed} tl1796 assertions failed`);
console.log(`\n  ${passed}/${passed + failed} passed`);
