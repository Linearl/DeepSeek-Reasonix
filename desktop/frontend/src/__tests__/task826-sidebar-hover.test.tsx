// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task826-sidebar-hover.test.tsx
// 任务 826 acceptance harness — 实验室·界面·视图布局「左右边栏 hover 展开
// 与延时折叠」（experimental_sidebar_hover_left / _right，左右独立两开关）：
//  ① hook 行为：300ms（测试注口缩短）防误触延迟到点才展开；指针扫过不展开；
//     离开 15s（同上缩短）后只回收「hover 自动展开」的状态——markManual 之后
//     的手动状态永不回收；回收等待期内重新进入则取消回收；开关关全链路 no-op；
//  ② 铁律 2 全链路（config→setter→渲染表→档位注册表→settings view 双侧→
//     bridge→labFlags→渲染表谓词→yaml/内置默认→实验室卡片→App 接线→热区
//     样式→三语 locale）逐环源码断言，默认关两侧钉死。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";
import { useSidebarHoverBehavior, type SidebarHoverBehavior, type SidebarHoverOptions } from "../app-runtime/useSidebarHoverBehavior";
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
const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

console.log("\ntask 826 sidebar hover-to-expand + delayed re-collapse");

// ── ① hook 行为（真实短定时器：展开 20ms / 回收 30ms）────────────────
{
  const dom = new JSDOM("<div id='root'></div>");
  Object.assign(globalThis, { window: dom.window, document: dom.window.document, IS_REACT_ACT_ENVIRONMENT: true });
  const root = createRoot(document.getElementById("root")!);

  let behavior: SidebarHoverBehavior | null = null;
  let expandedCount = 0;
  let collapsedCount = 0;
  const counts = () => ({ expand: expandedCount, collapse: collapsedCount });
  const resetCounts = () => { expandedCount = 0; collapsedCount = 0; };
  const baseOptions: SidebarHoverOptions = {
    enabled: true,
    collapsed: true,
    expand: () => {},
    collapse: () => {},
    expandDelayMs: 200,
    collapseDelayMs: 300,
  };

  // Probe 复刻真实接线：expand/collapse 回调驱动 collapsed state（与 App 里
  // setSidebarCollapsed/setWorkspacePanelOpen → prop 回流同构），定时器回调
  // 的 re-check 读到的才是最新状态。
  function Probe(props: Partial<SidebarHoverOptions> & { initialCollapsed?: boolean }) {
    const { initialCollapsed = true, ...rest } = props;
    const [collapsed, setCollapsed] = useState(initialCollapsed);
    behavior = useSidebarHoverBehavior({
      ...baseOptions,
      ...rest,
      collapsed,
      expand: () => { expandedCount += 1; setCollapsed(false); },
      collapse: () => { collapsedCount += 1; setCollapsed(true); },
    });
    return null;
  }

  try {
    // ①-a 防误触：停留到点才展开，且恰好一次。
    resetCounts();
    await act(async () => root.render(<Probe key="a" />));
    await act(async () => { behavior!.onEnter(); await sleep(50); });
    assert.deepEqual(counts(), { expand: 0, collapse: 0 }, "within the mis-touch window nothing expands yet");
    await act(async () => { await sleep(220); });
    assert.equal(expandedCount, 1, "after the 300ms-class guard elapses the sidebar auto-expands exactly once");

    // ①-b 离开 15s（缩口）后回收，且只回收自动展开的这一次（沿用 ①-a 的
    // 自动展开态——正是要回收的那次）。
    await act(async () => { behavior!.onLeave(); await sleep(80); });
    assert.equal(collapsedCount, 0, "nothing folds while the re-collapse delay is still running");
    await act(async () => { await sleep(280); });
    assert.equal(collapsedCount, 1, "after the pointer has been gone for the delay the auto expansion folds back");

    // ①-c 手动展开不回收：markManual 之后 onLeave 不再触发折叠。
    resetCounts();
    await act(async () => root.render(<Probe key="c" initialCollapsed={false} />));
    await act(async () => {
      behavior!.markManual(); // 用户点了展开按钮 → 手动状态
      behavior!.onEnter(); // 指针移入（已展开，不应挂任何东西）
      behavior!.onLeave();
      await sleep(400);
    });
    assert.equal(collapsedCount, 0, "a manually expanded sidebar is never reclaimed by the leave timer");

    // ①-d 回收等待期内重新进入 ⇒ 取消回收。
    resetCounts();
    await act(async () => root.render(<Probe key="d" initialCollapsed={true} />));
    await act(async () => { behavior!.onEnter(); await sleep(220); });
    assert.equal(expandedCount, 1, "the hover auto-expands again in a fresh scenario");
    await act(async () => { behavior!.onLeave(); await sleep(100); behavior!.onEnter(); await sleep(300); });
    assert.equal(collapsedCount, 0, "re-entering during the re-collapse window cancels the fold-back");

    // ①-e 扫过（进入后立刻离开）不展开。
    resetCounts();
    await act(async () => root.render(<Probe key="e" initialCollapsed={true} />));
    await act(async () => { behavior!.onEnter(); behavior!.onLeave(); await sleep(320); });
    assert.equal(expandedCount, 0, "a fly-over (enter then leave inside the guard window) never expands");

    // ①-f 开关关 ⇒ 全 no-op。
    resetCounts();
    await act(async () => root.render(<Probe key="f" enabled={false} initialCollapsed={true} />));
    await act(async () => { behavior!.onEnter(); await sleep(250); behavior!.onLeave(); await sleep(350); });
    assert.deepEqual(counts(), { expand: 0, collapse: 0 }, "with the switch off neither expand nor collapse ever fires (铁律 2)");

    // ①-g 手动折叠落在回收窗口内 ⇒ 到点 no-op（状态已折叠，不得重复折叠）。
    resetCounts();
    await act(async () => root.render(<Probe key="g" initialCollapsed={true} />));
    await act(async () => { behavior!.onEnter(); await sleep(220); });
    assert.equal(expandedCount, 1, "re-enabled hover auto-expands again");
    await act(async () => { behavior!.onLeave(); });
    // 用户在窗口内手动折叠（同 toggleSidebar：markManual + 状态置折叠——
    // 这里直接换一个已折叠的挂载模拟折叠后的世界）。
    await act(async () => root.render(<Probe key="g2" initialCollapsed={true} />));
    await act(async () => { await sleep(320); });
    assert.equal(collapsedCount, 0, "the timer callback re-checks state and never double-folds");

    await act(async () => root.unmount());
    console.log("  hook behavior suite done");
  } finally {
    dom.window.close();
  }
  ok(true, "① hook: guard window, delayed reclaim, manual immunity, re-enter cancel, off no-op");
}

// ── ② 铁律 2 全链路源码断言 ─────────────────────────────────────────
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
  const styles = readFileSync(`${frontendRoot}/src/styles.css`, "utf8");

  // config 字段（bool 零值 = 默认关，铁律 2）。
  ok(goConfig.includes('ExperimentalSidebarHoverLeft bool `toml:"experimental_sidebar_hover_left"`') &&
     goConfig.includes('ExperimentalSidebarHoverRight bool `toml:"experimental_sidebar_hover_right"`'),
    "② config: both [desktop] fields exist as plain bools (zero value = off)");
  // setter 两级。
  ok(goEdit.includes("func (c *Config) SetExperimentalSidebarHoverLeft(enabled bool) error {") &&
     goEdit.includes("func (c *Config) SetExperimentalSidebarHoverRight(enabled bool) error {"),
    "② setter: Config.SetExperimentalSidebarHoverLeft/Right exist");
  ok(goSettingsPrefs.includes("func (a *App) SetExperimentalSidebarHoverLeft(enabled bool) error {") &&
     goSettingsPrefs.includes("func (a *App) SetExperimentalSidebarHoverRight(enabled bool) error {"),
    "② setter: App.SetExperimentalSidebarHoverLeft/Right exist (applyConfigOnly)");
  // 渲染表：两键无条件渲染（fixed-key-set rule）。
  ok(goRender.includes('fmt.Fprintf(&b, "experimental_sidebar_hover_left = %v') &&
     goRender.includes('fmt.Fprintf(&b, "experimental_sidebar_hover_right = %v'),
    "② render table: both keys render unconditionally (81/123 fixed-key-set rule)");
  // 档位注册表：两条可选档（562 门禁对位）。
  ok(goRender.includes('{"sidebarHoverLeft", LabTierOptional, []string{"experimental_sidebar_hover_left"}}') &&
     goRender.includes('{"sidebarHoverRight", LabTierOptional, []string{"experimental_sidebar_hover_right"}}'),
    "② tier register: sidebarHoverLeft/Right tagged optional in labFeatureTiers");
  // settings view 双侧字段 + 双侧注入（81/123 两视图教训）。
  ok(goSettingsApp.split('ExperimentalSidebarHoverLeft  bool `json:"experimentalSidebarHoverLeft"`').length === 3,
    "② settings views: the field pair is declared in BOTH SettingsView and DesktopStartupSettingsView");
  ok(goSettingsApp.includes("view.ExperimentalSidebarHoverLeft = cfg.Desktop.ExperimentalSidebarHoverLeft") &&
     goSettingsApp.includes("ExperimentalSidebarHoverLeft:      cfg.Desktop.ExperimentalSidebarHoverLeft"),
    "② settings views: both injection sites map the left switch");
  // bridge：声明 + 测试桩。
  ok(bridge.includes("SetExperimentalSidebarHoverLeft(enabled: boolean): Promise<void>;") &&
     bridge.includes("async SetExperimentalSidebarHoverLeft() {}") &&
     bridge.includes("SetExperimentalSidebarHoverRight(enabled: boolean): Promise<void>;") &&
     bridge.includes("async SetExperimentalSidebarHoverRight() {}"),
    "② bridge: both setters declared and stubbed");
  // labFlags：两键默认 false。
  ok(labFlags.includes("sidebarHoverLeft: false,") && labFlags.includes("sidebarHoverRight: false,"),
    "② labFlags: both gates ship off (铁律 2)");
  ok(labFlagEnabled("sidebarHoverLeft") === false && labFlagEnabled("sidebarHoverRight") === false,
    "② labFlags: the module defaults read off before any snapshot");
  // 渲染表谓词注册（labLightKeys 白名单）。
  ok(typeof LAB_LIGHT_KEYS.experimentalSidebarHoverLeft === "function" &&
     typeof LAB_LIGHT_KEYS.experimentalSidebarHoverRight === "function",
    "② labLightKeys: both predicates registered (yaml onKeys whitelist)");
  // yaml + 内置默认同构同值（724 硬约束）。
  ok(yamlText.includes("onKeys: [experimentalSplitView, experimentalTrajectoryView, experimentalSidebarHoverLeft, experimentalSidebarHoverRight]") &&
     labDefault.includes('onKeys: ["experimentalSplitView", "experimentalTrajectoryView", "experimentalSidebarHoverLeft", "experimentalSidebarHoverRight"]'),
    "② layout data: the viewLayout card light covers all four keys in yaml AND the built-in default");
  ok(yamlText.includes('- id: sidebarHoverLeft') && yamlText.includes('- id: sidebarHoverRight') &&
     labDefault.includes('{ id: "sidebarHoverLeft", labelKey: "settings.sidebarHoverLeft", tier: "optional" }') &&
     labDefault.includes('{ id: "sidebarHoverRight", labelKey: "settings.sidebarHoverRight", tier: "optional" }'),
    "② layout data: both member rows present with the optional tier");
  ok(findLabLayoutDefaultRegisterDrift().length === 0,
    "② layout data: default layout tiers agree with the register (no drift)");
  // 前端档位注册表（562 徽章对位）。
  ok(EXPERIMENT_FEATURE_TIERS.sidebarHoverLeft === "optional" && EXPERIMENT_FEATURE_TIERS.sidebarHoverRight === "optional",
    "② tier badges: both features carry the optional tier");
  // 实验室卡片：两行开关（labLabel 各恰好一次的纪律由 task621 兜底，这里钉存在）。
  ok(panel.includes('labLabel("sidebarHoverLeft", t("settings.sidebarHoverLeft"))') &&
     panel.includes('labLabel("sidebarHoverRight", t("settings.sidebarHoverRight"))') &&
     panel.includes("app.SetExperimentalSidebarHoverLeft(on)") &&
     panel.includes("app.SetExperimentalSidebarHoverRight(on)"),
    "② lab card: two viewLayout rows, each reading its own key and writing its own setter");
  // App 接线：applyLabFlags 喂值 + 热区 + aside 事件 + markManual。
  ok(app.includes("sidebarHoverLeft: settings.experimentalSidebarHoverLeft ?? false") &&
     app.includes("sidebarHoverRight: settings.experimentalSidebarHoverRight ?? false"),
    "② App: the boot snapshot feeds both lab flags");
  ok(app.includes('sidebar-hover-zone sidebar-hover-zone--left') &&
     app.includes('sidebar-hover-zone sidebar-hover-zone--right') &&
     app.includes("sidebarHoverLeftEnabled && sidebarCollapsed && !managementActive") &&
     app.includes("sidebarHoverRightEnabled && !effectiveWorkspacePanelRenderable && !managementActive"),
    "② App: edge zones render only while the switch is on AND the side is collapsed (off ⇒ no element)");
  ok(app.includes("{...(sidebarHoverLeftEnabled ? { onMouseEnter: leftSidebarHover.onEnter, onMouseLeave: leftSidebarHover.onLeave } : {})}") &&
     app.includes("{...(sidebarHoverRightEnabled ? { onMouseEnter: rightDockHover.onEnter, onMouseLeave: rightDockHover.onLeave } : {})}"),
    "② App: the sidebar/dock asides arm enter/leave only while the switch is on");
  ok(app.includes("leftSidebarHover.markManual();") &&
     app.includes("rightDockHover.markManual();"),
    "② App: manual toggles mark the state manual (never reclaimed)");
  // 热区样式。
  ok(styles.includes(".sidebar-hover-zone") && styles.includes(".sidebar-hover-zone--left") && styles.includes(".sidebar-hover-zone--right"),
    "② styles: the edge-zone rules exist (transparent 6px strips)");
  // 三语 locale。
  const keys = [
    "settings.sidebarHoverLeft", "settings.sidebarHoverLeftHint", "settings.sidebarHoverLeft.on", "settings.sidebarHoverLeft.off",
    "settings.sidebarHoverRight", "settings.sidebarHoverRightHint", "settings.sidebarHoverRight.on", "settings.sidebarHoverRight.off",
  ];
  const missing: string[] = [];
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(`${frontendRoot}/src/locales/${locale}.ts`, "utf8");
    for (const key of keys) if (!dict.includes(`"${key}"`)) missing.push(`${locale}:${key}`);
  }
  ok(missing.length === 0, `② locales: all 8 keys exist in zh/en/zh-TW (missing: ${JSON.stringify(missing)})`);
}

// applyLabFlags 烟囱：快照喂 true 后两灯读真，喂缺省回落 false。
{
  applyLabFlags({ sidebarHoverLeft: true, sidebarHoverRight: true });
  const onSnapshot = labFlagEnabled("sidebarHoverLeft") && labFlagEnabled("sidebarHoverRight");
  applyLabFlags({});
  const offSnapshot = !labFlagEnabled("sidebarHoverLeft") && !labFlagEnabled("sidebarHoverRight");
  let notified = 0;
  const unsubscribe = onLabFlagsChange(() => { notified += 1; });
  applyLabFlags({ sidebarHoverLeft: true });
  unsubscribe();
  ok(onSnapshot && offSnapshot && notified === 1,
    "② labFlags: snapshot apply/defaults/listener notification behave (settings-save path)");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
