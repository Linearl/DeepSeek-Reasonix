// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1132-startup-splash-gate.test.tsx
//
// tl 1132：启动遮罩实验开关（experimental_startup_splash，默认关）。
// 两层断言：
//   A. StartupSplash 组件级行为（jsdom 真渲染）——显示结构、hold 不放行、
//      hold 释放后淡出→onDone、Esc/点击跳过、时间注入 props 缺省等价上游。
//   B. 开关全链路契约（源码断言）——App 挂载点门控（flag off = splash 不
//      渲染，与引入前逐字节一致）、applyLabFlags 接线、Go 双视图字段
//      （81/123 教训）、渲染表/固定键/toml 键、双布局全屏覆盖（fixed+inset，
//      不挂在 classic/workbench 条件分支内）、三语六键、双份布局登记。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { StartupSplash } from "../components/StartupSplash";
import { LocaleProvider } from "../lib/i18n";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

const TIMING = { minVisibleMs: 10, fadeOutMs: 20, maxHoldMs: 5000 };

// ── A. 组件级（jsdom）────────────────────────────────────────
let dom: JSDOM | null = null;
let root: Root | null = null;
let container: HTMLElement | null = null;

function installDom() {
  dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  container = dom.window.document.getElementById("root");
}

async function renderSplash(hold: boolean, onDone: () => void) {
  await act(async () => {
    root = createRoot(container!);
    root.render(
      <LocaleProvider>
        <StartupSplash hold={hold} onDone={onDone} timing={TIMING} />
      </LocaleProvider>,
    );
  });
}

async function rerenderSplash(hold: boolean, onDone: () => void) {
  await act(async () => {
    root!.render(
      <LocaleProvider>
        <StartupSplash hold={hold} onDone={onDone} timing={TIMING} />
      </LocaleProvider>,
    );
  });
}

async function unmountSplash() {
  if (!root) return;
  await act(async () => {
    root!.unmount();
  });
  root = null;
}

const splashEl = () => document.querySelector(".startup-splash") as HTMLElement | null;

console.log("\ntl 1132 startup splash gate — component behaviour");

{
  installDom();
  window.sessionStorage.clear();

  let done = 0;
  await renderSplash(true, () => { done += 1; });
  const el = splashEl();
  ok(Boolean(el), "splash renders the .startup-splash overlay on mount");
  ok(el?.dataset.leaving === "false", "fresh splash is not leaving (data-leaving=false)");
  ok(Boolean(el?.querySelector(".startup-splash__mark img")), "splash carries the centred logo mark");
  ok(Boolean(el?.querySelector(".startup-splash__sub")), "splash carries the subtitle line");
  ok(el?.querySelectorAll(".startup-splash__dots span").length === 3, "splash carries the three pulse dots");

  // hold=true：min 时间已过也不放行（等标签栏/草稿/composer/transcript 挂载）。
  await sleep(40);
  ok(el?.dataset.leaving === "false", "hold=true keeps the splash up past the minimum-visible window");

  // hold=false：min 已过 → 立即进入淡出；fadeOut 后 onDone。
  await rerenderSplash(false, () => { done += 1; });
  ok(splashEl()?.dataset.leaving === "true", "hold release enters the fade-out state");
  await sleep(60);
  ok(done === 1, "onDone fires exactly once after the fade-out window");
  ok(window.sessionStorage.getItem("reasonix.splash.shown") === "1", "fade-out marks the per-session shown flag");
  await unmountSplash();
  ok(splashEl() === null, "unmount removes the overlay DOM (caller gate controls visibility)");
}

{
  // Esc / 点击跳过：hold=true 也立即放行（遮罩永不卡死窗口交互）。
  window.sessionStorage.clear();
  let done = 0;
  await renderSplash(true, () => { done += 1; });
  await act(async () => {
    window.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  });
  ok(splashEl()?.dataset.leaving === "true", "Escape skips the splash even while held");
  await act(async () => { await sleep(60); });
  ok(done === 1, "Escape path fires onDone once");
  await unmountSplash();

  window.sessionStorage.clear();
  await renderSplash(true, () => { done += 1; });
  await act(async () => {
    splashEl()!.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
  });
  ok(splashEl()?.dataset.leaving === "true", "clicking the splash skips it even while held");
  await act(async () => { await sleep(60); });
  await unmountSplash();
  ok(done === 2, "skip paths fire onDone exactly once each (Escape + click)");
}

// ── B. 开关全链路契约（源码断言）────────────────────────────
console.log("\ntl 1132 startup splash gate — full-chain contract");

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, "../../../..");
const readRepo = (p: string) => readFileSync(resolve(repoRoot, p), "utf8");
const readSrc = (p: string) => readFileSync(resolve(here, "..", p), "utf8");

{
  const appSrc = readSrc("App.tsx");
  // 门控：flag off（默认）⇒ splash 不渲染。挂载点从上游的
  // `startupSplashVisible &&` 收紧为 `&& startupSplashEnabled`。
  ok(appSrc.includes("{startupSplashVisible && startupSplashEnabled && ("),
    "App gates the splash mount on both the session flag and the lab flag");
  ok(/const \[startupSplashEnabled, setStartupSplashEnabled\] = useState\(labFlagEnabled\("startupSplash"\)\)/.test(appSrc),
    "App subscribes startupSplashEnabled to the lab-flag store");
  ok(appSrc.includes("startupSplash: settings.experimentalStartupSplash ?? false"),
    "applyDesktopPreferences wires the boot-snapshot flag into applyLabFlags");
  // 双布局覆盖：挂载点在根 return 层（挂载点上下文不进 classic/workbench
  // 条件分支），加上 .startup-splash 为 fixed+inset 全屏层 ⇒ 两种布局同覆盖。
  const mountIdx = appSrc.indexOf("{startupSplashVisible && startupSplashEnabled && (");
  ok(mountIdx > appSrc.indexOf("appRef") && !appSrc.slice(mountIdx - 200, mountIdx).includes("sidebarWorkbench ?"),
    "the splash mount sits at the app-root layer, outside classic/workbench branches");
  const stylesSrc = readSrc("styles.css");
  const splashCss = stylesSrc.slice(stylesSrc.indexOf(".startup-splash {"));
  ok(/\.startup-splash \{[^}]*position:\s*fixed/s.test(splashCss) && /\.startup-splash \{[^}]*inset:\s*0/s.test(splashCss),
    ".startup-splash is a fixed inset-0 layer covering both layouts");
  ok(!stylesSrc.includes(".app--workbench .startup-splash") && !stylesSrc.includes(".app--classic .startup-splash") && !stylesSrc.includes(".app--creation .startup-splash"),
    "the splash rules are layout-agnostic (no .app--classic/.app--workbench scoping)");

  const labFlagsSrc = readSrc("lib/labFlags.ts");
  ok(labFlagsSrc.includes('"draftRewrite" | "startupSplash"') && /startupSplash: false,/.test(labFlagsSrc),
    "labFlags registers startupSplash with a default-off value");

  const splashSrc = readSrc("components/StartupSplash.tsx");
  ok(splashSrc.includes("MIN_VISIBLE_MS = 1400") && splashSrc.includes("FADE_OUT_MS = 420") && splashSrc.includes("MAX_HOLD_MS = 6000"),
    "timing injection keeps the upstream defaults (1400/420/6000)");
}

{
  const settingsApp = readRepo("desktop/settings_app.go");
  const viewCount = (settingsApp.match(/ExperimentalStartupSplash bool `json:"experimentalStartupSplash"`/g) ?? []).length;
  ok(viewCount === 2, "both SettingsView and DesktopStartupSettingsView carry the flag (81/123 two-view lesson)");
  ok(settingsApp.includes("view.ExperimentalStartupSplash = cfg.Desktop.ExperimentalStartupSplash") &&
      settingsApp.includes("ExperimentalStartupSplash: cfg.Desktop.ExperimentalStartupSplash"),
    "both readbacks populate the flag from cfg.Desktop");

  const renderGo = readRepo("internal/config/render.go");
  ok(renderGo.includes('{"startupSplash", LabTierOptional, []string{"experimental_startup_splash"}}'),
    "labFeatureTiers registers startupSplash at the optional tier");
  ok(renderGo.includes("experimental_startup_splash = %v"),
    "RenderTOML emits the fixed key (81/123 lost-save rule)");
  const prefsGo = readRepo("internal/config/desktop_preferences.go");
  ok(prefsGo.includes('ExperimentalStartupSplash bool `toml:"experimental_startup_splash"`'),
    "the Desktop config struct carries the toml key");
  const prefsSetter = readRepo("desktop/settings_preferences.go");
  ok(prefsSetter.includes("func (a *App) SetExperimentalStartupSplash(enabled bool) error"),
    "the Wails-bound setter exists");
  const editGo = readRepo("internal/config/edit.go");
  ok(editGo.includes("func (c *Config) SetExperimentalStartupSplash(enabled bool) error"),
    "the config-layer setter exists");
}

{
  // 双份布局登记 + yaml 解析不回退。
  const yamlText = readSrc("lab/lab-layout.yaml");
  ok(yamlText.includes("- id: startupLoading") && yamlText.includes("labelKey: settings.startupLoading"),
    "lab-layout.yaml registers the startupLoading card in the ui group");
  ok(/- id: startupLoading[\s\S]*?onKeys: \[experimentalStartupSplash\][\s\S]*?- id: startupSplash/.test(yamlText),
    "the yaml card wires the light key and the startupSplash member");
  const defaultSrc = readSrc("lab/labLayoutDefault.ts");
  ok(defaultSrc.includes('id: "startupLoading"') && defaultSrc.includes('id: "startupSplash"'),
    "labLayoutDefault mirrors the card (yaml-fallback equivalence)");

  const { resolveLabLayout } = await import("../lab/labLayout");
  const { LAB_LAYOUT_DEFAULT_DATA } = await import("../lab/labLayoutDefault");
  const paneIds = new Set<string>(LAB_LAYOUT_DEFAULT_DATA.groups.flatMap((g) => g.entries.map((e) => e.id)));
  const resolved = resolveLabLayout(yamlText, { allowedEntryIds: paneIds });
  ok(resolved.source === "yaml", `the yaml resolves without falling back (source=${resolved.source}, warnings=${JSON.stringify(resolved.warnings)})`);
  const uiGroup = resolved.layout.groups.find((g) => g.key === "ui");
  const card = uiGroup?.entries.find((e) => e.id === "startupLoading");
  ok(Boolean(card), "the resolved layout carries the startupLoading card in ui");
  ok(card?.members?.some((m) => m.id === "startupSplash" && m.tier === "optional") === true,
    "the card member startupSplash is registered at the optional tier");
  ok(card?.tier === "optional", "the container badge tier is optional (成员最高档)");
}

{
  const panelSrc = readSrc("components/SettingsPanel.tsx");
  ok(panelSrc.includes('"startupLoading"') && panelSrc.includes('selected === "startupLoading"'),
    "SettingsPanel whitelists and renders the startupLoading card branch");
  ok(panelSrc.includes("app.SetExperimentalStartupSplash(on)"),
    "the card row writes through SetExperimentalStartupSplash");
  const bridgeSrc = readSrc("lib/bridge.ts");
  ok(bridgeSrc.includes("SetExperimentalStartupSplash(enabled: boolean): Promise<void>;"),
    "bridge declares the binding");

  // 三语六键。
  for (const [name, file] of [["zh", "locales/zh.ts"], ["zh-TW", "locales/zh-TW.ts"], ["en", "locales/en.ts"]] as const) {
    const src = readSrc(file);
    const keys = ["settings.startupLoading", "settings.startupLoadingHint", "settings.startupSplash", "settings.startupSplashHint", "settings.startupSplash.on", "settings.startupSplash.off"];
    ok(keys.every((k) => src.includes(`"${k}"`)), `${name} locale carries all six startup-splash keys`);
  }
}

console.log(`\n${passed} checks passed${failed ? ` , ${failed} failed` : ""}`);
if (failed) process.exitCode = 1;
else process.stdout.write("tl 1132 startup splash gate: OK\n");
