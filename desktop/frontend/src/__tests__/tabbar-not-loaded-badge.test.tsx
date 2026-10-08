// Run: tsx src/__tests__/tabbar-not-loaded-badge.test.tsx
// 任务 619：后台 tab 惰性恢复的「未加载」占位徽标。
// 启动只构建上次前台（+autopilot）tab；其余骨架 tab（ready=false 且
// runtime.phase="starting"）在标签条显示「未加载」徽标，点击触发加载。
// 验收三条：
// ① 骨架后台 tab 显示徽标（文案来自 tabBar.notLoaded，随语言走）；
// ② 已就绪 tab、活跃 tab、split 副栏 tab 不显示（活跃/split 有自己的
//    transcript 占位，不再叠加徽标）；
// ③ 徽标随 hover title（aria-label）承接状态文案——压缩深档隐藏徽章时
//    状态信息不丢失。

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

const lazyTab = () => makeTab({
  ready: false,
  runtime: { phase: "starting", epoch: "epoch-1" },
});

interface RenderResult {
  badges: () => Element[];
  tabButtons: () => Element[];
  rerender: (tabs: TabMeta[], activeTabId?: string) => Promise<void>;
  unmount: () => Promise<void>;
}

async function renderTabBar(tabs: TabMeta[], activeTabId?: string): Promise<RenderResult> {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const render = async (nextTabs: TabMeta[], nextActiveId?: string) => {
    await act(async () => {
      root.render(createElement(LocaleProvider, null, createElement(TabBar, {
        tabs: nextTabs,
        activeTabId: nextActiveId ?? nextTabs[0]?.id,
        onTabChange: () => {},
        onTabClose: () => {},
        onTabsClose: () => {},
        onTabsReorder: () => {},
        onNewTab: () => {},
      })));
    });
  };
  await render(tabs, activeTabId);
  return {
    badges: () => Array.from(container.querySelectorAll(".tabbar__mode-badge")),
    tabButtons: () => Array.from(container.querySelectorAll(".tabbar__tab")),
    rerender: render,
    unmount: async () => {
      await act(async () => { root.unmount(); });
      container.remove();
    },
  };
}

console.log("\n任务 619：后台 tab「未加载」徽标");

{
  const ready = makeTab();
  const lazy = lazyTab();
  const ui = await renderTabBar([ready, lazy]);
  const badgeTexts = ui.badges().map((b) => b.textContent).join("|");
  eq(badgeTexts, t("tabBar.notLoaded"), "骨架后台 tab 显示一条「未加载」徽标，已就绪 tab 不显示");
  const lazyButton = ui.tabButtons()[1];
  eq((lazyButton?.getAttribute("aria-label") ?? "").includes(t("tabBar.notLoaded")), true,
    "徽标文案同时进 hover title（aria-label）承接压缩深档");
  await ui.unmount();
}

{
  const lazyActive = lazyTab();
  const other = makeTab();
  // 活跃 tab 本身未构建：transcript 有自己的加载占位，不叠加徽标。
  const ui = await renderTabBar([lazyActive, other], lazyActive.id);
  eq(ui.badges().length, 0, "活跃 tab 即使 starting 也不显示徽标");
  await ui.unmount();
}

{
  const ready = makeTab();
  const lazy = lazyTab();
  // split 副栏视为可见面，不叠加徽标。
  const ui = await renderTabBar([ready, lazy]);
  await act(async () => {
    // 直接管 props：splitTabId 传 lazy.id 再渲染一次。
  });
  // rerender with splitTabId isn't part of this harness; instead assert via a
  // fresh render passing splitTabId through TabBar props.
  await ui.unmount();

  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(createElement(LocaleProvider, null, createElement(TabBar, {
      tabs: [ready, lazy],
      activeTabId: ready.id,
      splitTabId: lazy.id,
      onTabChange: () => {},
      onTabClose: () => {},
      onTabsClose: () => {},
      onTabsReorder: () => {},
      onNewTab: () => {},
    })));
  });
  eq(container.querySelectorAll(".tabbar__mode-badge").length, 0, "split 副栏 tab 不显示徽标");
  await act(async () => { root.unmount(); });
  container.remove();
}

{
  // 载入中态（runtime.phase 缺失或 ready=true）不误报。
  const rebuilding = makeTab({ ready: false });
  const ui = await renderTabBar([makeTab(), rebuilding]);
  eq(ui.badges().length, 0, "runtime 相位缺失的未就绪 tab 不误报徽标");
  await ui.unmount();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
