// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1796-autopilot-composer-offer.test.ts
// tl1796 r2 — autopilot 选项开关回退：恒显落地（装机实测打回）。
//
// 打回根因（双重）：一期 opt-in 键 experimental_autopilot_composer 默认关
// = 选项默认缺失（切 YOLO 后下拉仍无 autopilot；用户口径「任何时候都应该
// 出现」）；config 渲染行把零值 false 显性化 → 观感「该键被异常关闭」
// （调查结论见 Go 侧同号测试文件头：无异常写者，false 即默认值首次渲染）。
//
// r2 语义（tl1741「稳定档不再提供开关，功能常开」口径同源，collab_hub
// 先例）：键链路全撤（config 字段/两级 setter/渲染行/账本登记/双 View 读回
// /App 读源/store 字段/Composer 显隐门三处/lab 卡行级开关恢复 {null} 壳/
// bridge 声明/三语 offer hint），autopilot 档恒显无门；存量手写键由
// config_key_warn deprecated 清单承接（Go 侧钉见
// internal/config/autopilot_composer_tl1796_test.go）。

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

console.log("\ntl1796 r2 composer autopilot always-offered (opt-in switch rolled back)");

// ── ① 恒显：下拉/模式条第四档无条件渲染，无任何开关依赖 ────────
{
  const composer = read("src/components/Composer.tsx");
  ok(!composer.includes("autopilotEnabled"),
    "composer: the autopilotEnabled offer-gate prop is gone entirely");
  ok(composer.includes('{ value: "autopilot", label: t("composer.taskModeAutopilot"), hint: t("composer.taskModeAutopilotHint") },'),
    "composer: the dropdown lists autopilot unconditionally (no spread gate)");
  ok(!composer.includes('...(autopilotEnabled ? [{ value: "autopilot"'),
    "composer: the r1 conditional spread is gone");
  ok(composer.includes('data-autopilot="on"'),
    "composer: the modebar data flag is pinned on (465 anchor, flipped)");
  ok(!composer.includes("{autopilotEnabled && ("),
    "composer: the fourth-tier conditional render is gone (always mounted)");
  ok(composer.includes('composer-modebar__item--autopilot${autopilotModeOn'),
    "composer: the fourth-tier button keeps its per-tab active-state wiring");
}

// ── ② 读源/状态链：App 与 lifecycle store 无键可读、无写者 ─────
{
  const app = read("src/App.tsx");
  ok(!app.includes("experimentalAutopilotComposer"),
    "App.tsx: no read of the retired key (r1 read site removed)");
  ok(!app.includes("setAutopilotEnabled") && !app.includes("autopilotEnabled"),
    "App.tsx: no gate state flows through the app anymore");
  ok(!app.includes("autopilotEnabled={autopilotEnabled}"),
    "App.tsx: the Composer prop pass-through is gone");
  const store = read("src/store/appLifecycle.ts");
  ok(!store.includes("autopilotEnabled"),
    "store: the lifecycle store no longer carries the gate flag");
  ok(store.includes("appearanceReady"),
    "store: the unrelated appearanceReady flag is untouched");
}

// ── ③ lab 卡：行级开关撤净，恢复 {null} 信息壳（tl1741 口径）────
{
  const panel = read("src/components/ExperimentalLabSection.tsx");
  ok(panel.includes('testId="lab-toggle-autopilot"'),
    "lab card: the autopilot row keeps its lab-toggle-autopilot anchor (tl1791 deeplink stable)");
  ok(panel.includes('hint={t("settings.autopilotHint")} icon={<ShieldCheck size={18} />} stacked>{null}</SettingsField>'),
    "lab card: the row is back to the informational {null} shell (pre-r1 form)");
  ok(!panel.includes("SetExperimentalAutopilotComposer"),
    "lab card: no call to the retired setter");
  ok(!panel.includes("experimentalAutopilotComposer"),
    "lab card: no read of the retired view field");
  ok(!panel.includes("settings.autopilotOfferHint"),
    "lab card: the r1 offer hint is gone (row hint is back to autopilotHint)");
}

// ── ④ Go 全链零残留 + deprecated 承接在册 ──────────────────────
{
  const prefs = readRepo("internal/config/desktop_preferences.go");
  ok(!prefs.includes('toml:"experimental_autopilot_composer"'),
    "config: the [desktop] opt-in key is off the struct");
  const edit = readRepo("internal/config/edit.go");
  ok(!edit.includes("func (c *Config) SetExperimentalAutopilotComposer"),
    "config: the flip setter is gone");
  const render = readRepo("internal/config/render.go");
  ok(!render.includes('fmt.Fprintf(&b, "experimental_autopilot_composer'),
    "config: the fixed-key-set render row is gone (81/123 lesson — shrunk, not drifted)");
  ok(!render.includes('"experimental_autopilot_composer":'),
    "config: the labNonFeatureKeys ledger entry is removed (键消亡按账本规则移除)");
  const settings = readRepo("desktop/settings_app.go");
  ok(!settings.includes('json:"experimentalAutopilotComposer"'),
    "desktop: the view readback fields are gone from BOTH views (759 two-view lesson)");
  ok(!settings.includes("view.ExperimentalAutopilotComposer"),
    "desktop: the startup-view populate is gone");
  ok(!settings.includes("ExperimentalAutopilotComposer: cfg.Desktop.ExperimentalAutopilotComposer"),
    "desktop: the full SettingsView populate is gone");
  const wrapper = readRepo("desktop/settings_preferences.go");
  ok(!wrapper.includes("func (a *App) SetExperimentalAutopilotComposer"),
    "desktop: the App-level wails wrapper is gone");
  const warn = readRepo("internal/config/config_key_warn.go");
  ok(warn.includes('"desktop.experimental_autopilot_composer":'),
    "config: the deprecated ledger carries the leftover handwritten key (tl1741 collab_hub precedent)");
}

// ── ⑤ TS 双类型 + bridge + 三语 locale ──────────────────────────
{
  const types = read("src/lib/types.ts");
  ok(!types.includes("experimentalAutopilotComposer?:"),
    "types: SettingsView no longer carries the field");
  const startup = read("src/lib/settingsViewTypes.ts");
  ok(!startup.includes("experimentalAutopilotComposer?:"),
    "types: the startup view no longer carries the field (two-view lesson)");
  const bridge = read("src/lib/bridge.ts");
  ok(!bridge.includes("SetExperimentalAutopilotComposer(enabled: boolean): Promise<void>;"),
    "bridge: AppBindings drops the setter declaration (_CheckGenToApp 生成面同步收口)");
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = read(`src/locales/${locale}.ts`);
    ok(!dict.includes('"settings.autopilotOfferHint"'),
      `${locale}: the r1 offer hint is removed`);
    ok(dict.includes('"settings.autopilotHint"'),
      `${locale}: the {null}-row hint autopilotHint is still in place`);
  }
}

assert.equal(failed, 0, `${failed} tl1796 r2 assertions failed`);
console.log(`\n  ${passed}/${passed + failed} passed`);
