// Run: tsx src/__tests__/tabbar-waiting-not-running.test.tsx
// 任务 730：会话「待确认」（pendingPrompt）不是「运行中」——tab 条与项目栏
// 同一时刻同一结论。
//
// 现场回归（20261010 用户截图）：调研-1-通用 完成工作后停在待确认，项目栏
// 显示琥珀「待确认」pill（不旋转），tab 条却因 running 定义含 PendingPrompt
// 一直转运行标记——两处对同一状态给出矛盾结论。
//
// 验收：
// ① 投影层：pendingPrompt 的 tab 投影后 running=false（后台 tab 用快照
//    pendingPrompt，可见 tab 用 live pendingPrompt 且压过快照）；
// ② 呈现层：TabBar 对 waiting tab 不渲染运行标记类，hover title 出现
//    「待确认」（复用 projectTree.status.waitingConfirmation，零新增词条）；
//    真运行中的 tab 两个标记类都保留、无待确认文案；
// ③ TabBar 自洽防御：即使调用方漏投影（running=true + pendingPrompt=true
//    原样传入），旋转标记与待确认也不并存。

import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.window.requestAnimationFrame = ((cb: FrameRequestCallback) =>
  setTimeout(() => cb(dom.window.performance.now()), 0)) as unknown as typeof window.requestAnimationFrame;
globalThis.window.cancelAnimationFrame = ((id: number) => clearTimeout(id)) as unknown as typeof window.cancelAnimationFrame;
(dom.window.Element.prototype as unknown as { scrollIntoView?: () => void }).scrollIntoView = () => {};

let passed = 0;
let failed = 0;
function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

const React = await import("react");
const { createElement } = React;
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");
const { LocaleProvider, t } = await import("../lib/i18n");
const { TabBar } = await import("../components/TabBar");
const { projectVisibleTabs } = await import("../app-runtime/controllerProfileOwner");
import type { ComposerProfile } from "../lib/composerProfile";
import type { TabMeta } from "../lib/types";

let nextIndex = 0;
function makeTab(overrides: Partial<TabMeta> = {}): TabMeta {
  nextIndex += 1;
  return {
    id: `tab-${nextIndex}`,
    scope: "project",
    workspaceRoot: "/ws",
    workspaceName: "工作区",
    topicId: `topic-${nextIndex}`,
    topicTitle: `会话标题${nextIndex}`,
    label: `会话标题${nextIndex}`,
    ready: true,
    running: false,
    mode: "normal",
    toolApprovalMode: "ask",
    active: false,
    cwd: "/ws",
    ...overrides,
  } as TabMeta;
}

const noProfiles: Record<string, ComposerProfile> = {};
const project = (tabs: TabMeta[], input: { visibleTabId?: string; running?: boolean; pendingPrompt?: boolean } = {}) =>
  projectVisibleTabs({
    tabs, orderIds: tabs.map((tab) => tab.id), profiles: noProfiles,
    visibleTabId: input.visibleTabId, running: Boolean(input.running), pendingPrompt: input.pendingPrompt,
  });

console.log("\n任务 730：待确认 tab 不再显示运行标记");

{
  // ① 投影层：后台 tab 快照 pendingPrompt=true → running=false
  const waiting = makeTab({ running: true, pendingPrompt: true });
  const idle = makeTab();
  const [projectedWaiting, projectedIdle] = project([waiting, idle], { visibleTabId: idle.id });
  eq(projectedWaiting.running, false, "后台待确认 tab 投影后 running=false（等待用户≠运行中）");
  eq(projectedWaiting.pendingPrompt, true, "后台待确认 tab 保留 pendingPrompt 供呈现层使用");
  eq(projectedIdle.running, false, "空闲 tab 保持 running=false");
}
{
  // ① 真运行中的后台 tab 不受影响
  const busy = makeTab({ running: true });
  const [projected] = project([busy], {});
  eq(projected.running, true, "真运行中的后台 tab 保持 running=true");
}
{
  // ① 可见 tab：live running=true + live pendingPrompt=true → 等待压过运行
  const visible = makeTab({ running: false });
  const [projected] = project([visible], { visibleTabId: visible.id, running: true, pendingPrompt: true });
  eq(projected.running, false, "可见 tab 合并 live running 后仍被 live pendingPrompt 压成非运行");
  eq(projected.pendingPrompt, true, "可见 tab 的 pendingPrompt 以 live 值为准");
}
{
  // ① 可见 tab：快照 pendingPrompt 滞后（已回答），live=false → 恢复运行标记
  const visible = makeTab({ running: true, pendingPrompt: true });
  const [projected] = project([visible], { visibleTabId: visible.id, running: true, pendingPrompt: false });
  eq(projected.running, true, "live pendingPrompt=false 压过滞后快照，运行标记恢复");
  eq(projected.pendingPrompt, false, "可见 tab 的滞后快照 pendingPrompt 被 live 值纠正");
}

// 呈现层 harness（同 tabbar-not-loaded-badge 模式）
async function renderTabBar(tabs: TabMeta[]) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(createElement(LocaleProvider, null, createElement(TabBar, {
      tabs,
      activeTabId: tabs[0]?.id,
      onTabChange: () => {},
      onTabClose: () => {},
      onTabsClose: () => {},
      onTabsReorder: () => {},
      onNewTab: () => {},
    })));
  });
  return {
    buttons: () => Array.from(container.querySelectorAll(".tabbar__tab")),
    unmount: async () => {
      await act(async () => { root.unmount(); });
      container.remove();
    },
  };
}

{
  // ② 投影后的待确认 tab：无运行标记类，title 带「待确认」
  const waiting = makeTab({ running: false, pendingPrompt: true });
  const ui = await renderTabBar([waiting]);
  const button = ui.buttons()[0];
  eq(button?.className.includes("tabbar__tab--running"), false, "待确认 tab 不渲染 tabbar__tab--running");
  eq(button?.querySelector(".tabbar__status--running") === null, true, "待确认 tab 不渲染 tabbar__status--running");
  eq((button?.getAttribute("aria-label") ?? "").includes(t("projectTree.status.waitingConfirmation")), true,
    "hover title 出现「待确认」，与项目栏 pill 同一词");
  await ui.unmount();
}
{
  // ② 真运行中的 tab：两个标记类保留，无待确认文案
  const busy = makeTab({ running: true });
  const ui = await renderTabBar([busy]);
  const button = ui.buttons()[0];
  eq(button?.className.includes("tabbar__tab--running"), true, "运行中 tab 保留 tabbar__tab--running");
  eq(button?.querySelector(".tabbar__status--running") !== null, true, "运行中 tab 保留 tabbar__status--running");
  eq((button?.getAttribute("aria-label") ?? "").includes(t("projectTree.status.waitingConfirmation")), false,
    "运行中 title 不出现「待确认」");
  await ui.unmount();
}
{
  // ③ 自洽防御：漏投影的 running+pendingPrompt 原样进 TabBar 也不转圈
  const raw = makeTab({ running: true, pendingPrompt: true });
  const ui = await renderTabBar([raw]);
  const button = ui.buttons()[0];
  eq(button?.className.includes("tabbar__tab--running"), false, "漏投影时 TabBar 自身仍不渲染运行标记");
  eq((button?.getAttribute("aria-label") ?? "").includes(t("projectTree.status.waitingConfirmation")), true,
    "漏投影时 title 仍标注「待确认」");
  await ui.unmount();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
