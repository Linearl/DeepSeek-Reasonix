// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task940-approval-select.test.tsx
// 任务 940 acceptance harness — composer 权限档位选择器形态二选一
// （experimental_approval_select，实验室-界面-视图布局卡；默认关=按钮组）：
//  ① 两态渲染：开关关（默认）＝composer-modebar--approval 按钮组原样渲染、
//     无下拉框；开关开＝SettingsSelect 下拉框渲染、无按钮组；
//  ② 档位语义同链路：下拉选 ask/auto/yolo → onSetToolApprovalMode（与按
//     钮组 chooseApprovalMode 同一回调）；选 autopilot →
//     onSetCollaborationMode("autopilot")（与按钮组第四档同源）；
//  ③ 禁用态同 approvalBarDisabled；
//  ④ 铁律 2 全链路（config→setter→渲染表→档位注册表→settings view 双侧→
//     bridge→labFlags→灯谓词→yaml/内置默认→实验室卡片→App 接线→CSS→三语
//     locale）逐环源码断言，默认关两侧钉死。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";
import { Composer } from "../components/Composer";
import { LocaleProvider } from "../lib/i18n";
import { ToastProvider } from "../lib/toast";
import type { AppBindings } from "../lib/bridge";
import type { CollaborationMode, ToolApprovalMode } from "../lib/types";
import { LAB_LAYOUT_DEFAULT_DATA } from "../lab/labLayoutDefault";
import { findLabLayoutDefaultRegisterDrift } from "../lab/labLayout";
import { LAB_LIGHT_KEYS } from "../lab/labLightKeys";
import { EXPERIMENT_FEATURE_TIERS } from "../lib/experimentTiers";
import { labFlagEnabled, applyLabFlags, onLabFlagsChange } from "../lib/labFlags";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const here = fileURLToPath(new URL(".", import.meta.url));
const frontendRoot = `${here}../..`;
const repoRoot = `${frontendRoot}/../..`;

console.log("\ntask 940 composer approval-select dropdown");

// ── ① 两态渲染 + ② 同链路 + ③ 禁用态（真实 Composer 挂载）────────────
{
  const dom = new JSDOM("<!doctype html><html><body><div id='root'></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  Object.assign(globalThis, {
    window: dom.window,
    document: dom.window.document,
    HTMLElement: dom.window.HTMLElement,
    Node: dom.window.Node,
    Event: dom.window.Event,
    CustomEvent: dom.window.CustomEvent,
    KeyboardEvent: dom.window.KeyboardEvent,
    InputEvent: dom.window.InputEvent,
    MouseEvent: dom.window.MouseEvent,
    MutationObserver: dom.window.MutationObserver,
    File: dom.window.File,
    FileReader: dom.window.FileReader,
    localStorage: dom.window.localStorage,
    requestAnimationFrame: dom.window.requestAnimationFrame.bind(dom.window),
    cancelAnimationFrame: dom.window.cancelAnimationFrame.bind(dom.window),
    ResizeObserver: class { observe() {} unobserve() {} disconnect() {} },
    IS_REACT_ACT_ENVIRONMENT: true,
  });
  // Same reduced-motion stub the composer suites install (SettingsSelect's
  // AnchoredPopover closes on a timer that prefers-reduced-motion collapses).
  dom.window.matchMedia = (() => ({
    matches: true,
    media: "(prefers-reduced-motion: reduce)",
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeEventListener() {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  dom.window.requestAnimationFrame = ((cb: FrameRequestCallback) => { cb(0); return 0; }) as typeof window.requestAnimationFrame;
  // React's legacy input polyfill probes attachEvent/detachEvent on jsdom;
  // stub them (goal-toggle suite convention) to keep stderr clean.
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });

  const rootEl = document.getElementById("root")!;
  const root = createRoot(rootEl);
  const calls: { approval: ToolApprovalMode[]; collab: CollaborationMode[] } = { approval: [], collab: [] };

  function mockApp(methods: Partial<AppBindings>) {
    (window as unknown as { go: unknown }).go = {
      main: {
        App: {
          Commands: async () => [],
          Models: async () => [],
          ModelsForTab: async () => [],
          SlashArgs: async () => ({ items: [], from: 0 }),
          ...methods,
        } as Partial<AppBindings> as AppBindings,
      },
    };
  }
  mockApp({});

  const baseProps = {
    running: false,
    collaborationMode: "normal" as CollaborationMode,
    toolApprovalMode: "ask" as ToolApprovalMode,
    goal: "",
    cwd: "/repo",
    tabId: "tab-a",
    modelLabel: "DeepSeek-R1",
    onSend: () => {},
    onCancel: async () => ({ discardedItemIds: [] }),
    onCycleMode: () => {},
    onSetMode: () => {},
    onSetCollaborationMode: (mode: CollaborationMode) => calls.collab.push(mode),
    onSetToolApprovalMode: (mode: ToolApprovalMode) => calls.approval.push(mode),
    onToggleYoloApprovalMode: () => {},
    onClearGoal: () => {},
    onSwitchModel: () => {},
    onSetEffort: () => {},
    ready: true,
  };
  // One mounted root for the whole suite: scenario switches re-render with
  // merged props (the goal-toggle suite's pattern — an unmounted root cannot
  // be rendered into again).
  let currentProps: Parameters<typeof Composer>[0] = { ...baseProps };
  const paint = async (next: Partial<Parameters<typeof Composer>[0]> = {}) => {
    currentProps = { ...baseProps, ...next };
    await act(async () => {
      root.render(
        <LocaleProvider>
          <ToastProvider>
            <Composer {...currentProps} />
          </ToastProvider>
        </LocaleProvider>,
      );
      await new Promise((resolve) => setTimeout(resolve, 5));
    });
  };
  const pickOption = async (value: string) => {
    await act(async () => {
      const option = document.querySelector<HTMLElement>(`[role="option"][data-value="${value}"]`);
      assert.ok(option, `option ${value} must render in the open menu`);
      option!.click();
      await new Promise((resolve) => setTimeout(resolve, 5));
    });
  };

  try {
    // ①-a 默认关：按钮组原样，无下拉框。
    applyLabFlags({});
    await paint();
    ok(rootEl.querySelector(".composer-modebar--approval") !== null,
      "① flag off (default): the segmented approval button group renders");
    ok(rootEl.querySelector(".composer-approval-select") === null,
      "① flag off (default): no dropdown trigger renders");

    // ①-b 开关开：下拉框渲染、按钮组消失（同一挂载，订阅即时换态）。
    await act(async () => { applyLabFlags({ approvalSelect: true }); await new Promise((resolve) => setTimeout(resolve, 5)); });
    ok(rootEl.querySelector(".composer-modebar--approval") === null,
      "① flag on: the button group is gone");
    const trigger = rootEl.querySelector<HTMLButtonElement>(".composer-approval-select");
    ok(trigger !== null, "① flag on: the dropdown trigger renders");
    ok(/Ask|询问/.test(trigger!.textContent ?? ""),
      "① flag on: the trigger labels the current tier (ask)");
    ok(trigger!.disabled === false, "③ enabled composer keeps the dropdown clickable");

    // ②-a 打开菜单选 yolo → 与按钮组同一条 onSetToolApprovalMode 链路。
    await act(async () => { trigger!.click(); await new Promise((resolve) => setTimeout(resolve, 5)); });
    const optionValues = Array.from(document.querySelectorAll('[role="option"]')).map((node) => (node as HTMLElement).dataset.value);
    assert.deepEqual(optionValues, ["ask", "auto", "yolo"], "the menu carries exactly the three approval tiers");
    await pickOption("yolo");
    ok(calls.approval.length === 1 && calls.approval[0] === "yolo",
      "② picking yolo routes through the same onSetToolApprovalMode link as the button group");

    // ②-b autopilot 偏好开时第四档入选单，选中走 onSetCollaborationMode。
    await paint({ autopilotEnabled: true });
    const trigger2 = rootEl.querySelector<HTMLButtonElement>(".composer-approval-select");
    ok(trigger2 !== null, "② autopilot preference: the dropdown still renders");
    await act(async () => { trigger2!.click(); await new Promise((resolve) => setTimeout(resolve, 5)); });
    const fourValues = Array.from(document.querySelectorAll('[role="option"]')).map((node) => (node as HTMLElement).dataset.value);
    assert.deepEqual(fourValues, ["ask", "auto", "yolo", "autopilot"], "the menu adds the autopilot fourth tier");
    await pickOption("autopilot");
    ok(calls.collab.length === 1 && calls.collab[0] === "autopilot",
      "② picking autopilot routes through chooseTaskMode (the button group's fourth-tier link)");

    // ③ 禁用态：disabled composer 时触发器 disabled（同 approvalBarDisabled）。
    await paint({ autopilotEnabled: false, disabled: true });
    const trigger3 = rootEl.querySelector<HTMLButtonElement>(".composer-approval-select");
    ok(trigger3 !== null && trigger3!.disabled === true,
      "③ a disabled composer disables the dropdown (same approvalBarDisabled gate)");

    await act(async () => { root.unmount(); });
    console.log("  two-state render suite done");
  } finally {
    applyLabFlags({});
    dom.window.close();
  }
}

// ── ④ 铁律 2 全链路源码断言 ─────────────────────────────────────────
{
  const goConfig = readFileSync(`${repoRoot}/internal/config/desktop_preferences.go`, "utf8");
  const goEdit = readFileSync(`${repoRoot}/internal/config/edit.go`, "utf8");
  const goRender = readFileSync(`${repoRoot}/internal/config/render.go`, "utf8");
  const goSettingsApp = readFileSync(`${repoRoot}/desktop/settings_app.go`, "utf8");
  const goSettingsPrefs = readFileSync(`${repoRoot}/desktop/settings_preferences.go`, "utf8");
  const bridge = readFileSync(`${frontendRoot}/src/lib/bridge.ts`, "utf8");
  const labFlags = readFileSync(`${frontendRoot}/src/lib/labFlags.ts`, "utf8");
  const labLightKeys = readFileSync(`${frontendRoot}/src/lab/labLightKeys.ts`, "utf8");
  const yamlText = readFileSync(`${frontendRoot}/src/lab/lab-layout.yaml`, "utf8");
  const labDefault = readFileSync(`${frontendRoot}/src/lab/labLayoutDefault.ts`, "utf8");
  const panel = readFileSync(`${frontendRoot}/src/components/SettingsPanel.tsx`, "utf8");
  const app = readFileSync(`${frontendRoot}/src/App.tsx`, "utf8");
  const composer = readFileSync(`${frontendRoot}/src/components/Composer.tsx`, "utf8");
  const styles = readFileSync(`${frontendRoot}/src/styles.css`, "utf8");

  // config 字段（bool 零值 = 默认关，铁律 2）。
  ok(goConfig.includes('ExperimentalApprovalSelect bool `toml:"experimental_approval_select"`'),
    "④ config: the [desktop] field exists as a plain bool (zero value = off)");
  // setter 两级。
  ok(goEdit.includes("func (c *Config) SetExperimentalApprovalSelect(enabled bool) error {"),
    "④ setter: Config.SetExperimentalApprovalSelect exists");
  ok(goSettingsPrefs.includes("func (a *App) SetExperimentalApprovalSelect(enabled bool) error {"),
    "④ setter: App.SetExperimentalApprovalSelect exists (applyConfigOnly)");
  // 渲染表：键无条件渲染（fixed-key-set rule）+ 档位登记可选档。
  ok(goRender.includes('fmt.Fprintf(&b, "experimental_approval_select = %v'),
    "④ render table: the key renders unconditionally (81/123 fixed-key-set rule)");
  ok(goRender.includes('{"approvalSelect", LabTierOptional, []string{"experimental_approval_select"}}'),
    "④ tier register: approvalSelect tagged optional in labFeatureTiers");
  // settings view 双侧字段 + 双侧注入（81/123 两视图教训）。
  ok(goSettingsApp.split('ExperimentalApprovalSelect bool `json:"experimentalApprovalSelect"`').length === 3,
    "④ settings views: the field is declared in BOTH SettingsView and DesktopStartupSettingsView");
  ok(goSettingsApp.includes("view.ExperimentalApprovalSelect = cfg.Desktop.ExperimentalApprovalSelect") &&
     goSettingsApp.includes("ExperimentalApprovalSelect") &&
     goSettingsApp.includes("cfg.Desktop.ExperimentalApprovalSelect,"),
    "④ settings views: both injection sites map the switch");
  // bridge：声明 + 测试桩。
  ok(bridge.includes("SetExperimentalApprovalSelect(enabled: boolean): Promise<void>;") &&
     bridge.includes("async SetExperimentalApprovalSelect() {}"),
    "④ bridge: the setter is declared and stubbed");
  // labFlags：默认 false。
  ok(labFlags.includes("approvalSelect: false,"),
    "④ labFlags: the gate ships off (铁律 2)");
  ok(labFlagEnabled("approvalSelect") === false,
    "④ labFlags: the module default reads off before any snapshot");
  // 灯谓词注册（labLightKeys 白名单）。
  ok(typeof LAB_LIGHT_KEYS.experimentalApprovalSelect === "function",
    "④ labLightKeys: the predicate is registered (yaml onKeys whitelist)");
  // yaml + 内置默认同构同值（724 硬约束）。
  ok(yamlText.includes("experimentalApprovalSelect]") &&
     labDefault.includes('"experimentalApprovalSelect"]'),
    "④ layout data: the viewLayout card light covers the new key in yaml AND the built-in default");
  ok(yamlText.includes("- id: approvalSelect") &&
     labDefault.includes('{ id: "approvalSelect", labelKey: "settings.approvalSelect", tier: "optional" }'),
    "④ layout data: the member row is present with the optional tier");
  ok(findLabLayoutDefaultRegisterDrift().length === 0,
    "④ layout data: default layout tiers agree with the register (no drift)");
  // 前端档位注册表（562 徽章对位）。
  ok(EXPERIMENT_FEATURE_TIERS.approvalSelect === "optional",
    "④ tier badges: the feature carries the optional tier");
  // 实验室卡片：二选一行（读自己的键写自己的 setter）。
  ok(panel.includes('labLabel("approvalSelect", t("settings.approvalSelect"))') &&
     panel.includes("app.SetExperimentalApprovalSelect(on)"),
    "④ lab card: the viewLayout row reads its own key and writes its own setter");
  // App 接线：applyLabFlags 喂值。
  ok(app.includes("approvalSelect: settings.experimentalApprovalSelect ?? false"),
    "④ App: the boot snapshot feeds the lab flag");
  // Composer 两态：门读 labFlags、关态按钮组仍在、开态 SettingsSelect。
  ok(composer.includes('labFlagEnabled("approvalSelect")') &&
     composer.includes("onLabFlagsChange"),
    "④ Composer: the two-state render subscribes to the lab flag");
  ok(composer.includes("composer-modebar--approval") && composer.includes("<SettingsSelect"),
    "④ Composer: both the button group (off) and the dropdown (on) branches exist");
  // CSS：下拉形态样式存在。
  ok(styles.includes(".composer-approval-select") &&
     styles.includes(".composer-meta__control--approval--select"),
    "④ styles: the dropdown-shape rules exist");
  // 三语 locale。
  const keys = [
    "settings.approvalSelect", "settings.approvalSelectHint",
    "settings.approvalSelect.on", "settings.approvalSelect.off",
  ];
  const missing: string[] = [];
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(`${frontendRoot}/src/locales/${locale}.ts`, "utf8");
    for (const key of keys) if (!dict.includes(`"${key}"`)) missing.push(`${locale}:${key}`);
  }
  ok(missing.length === 0, `④ locales: all 4 keys exist in zh/en/zh-TW (missing: ${JSON.stringify(missing)})`);
}

// applyLabFlags 烟囱：快照喂 true 后读真，喂缺省回落 false，监听器收到通知。
{
  applyLabFlags({ approvalSelect: true });
  const onSnapshot = labFlagEnabled("approvalSelect");
  applyLabFlags({});
  const offSnapshot = !labFlagEnabled("approvalSelect");
  let notified = 0;
  const unsubscribe = onLabFlagsChange(() => { notified += 1; });
  applyLabFlags({ approvalSelect: true });
  unsubscribe();
  applyLabFlags({});
  ok(onSnapshot && offSnapshot && notified === 1,
    "④ labFlags: snapshot apply/defaults/listener notification behave (settings-save path)");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
