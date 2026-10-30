// Run: tsx src/__tests__/tabbar-session-missing-badge.test.tsx
// tl 1266：会话实体查无（session_missing）的「已断开」徽章与 running 残影清除。
// 后端 tabMeta 已把实体查无的 tab 投影为非 running + failed issue；前端 TabBar：
// ① 挂 session_missing issue 的 tab 显示「已断开」徽章（含活跃 tab——断开是
//    状态真相，事故截图正是活跃 tab 挂残影）；
// ② 防御层：即使 running=true 与 session_missing 同帧到达，running 类
//    （绿点）必须让位给断开态，二者不得共存；
// ③ 实体在盘的普通骨架/运行 tab 不受影响（未加载/running 语义逐字不变）。

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

const sessionMissingIssue = { code: "session_missing" as const, message: "会话已断开，请重新打开", retryable: false };
const disconnectedTab = () => makeTab({
  ready: false,
  runtime: { phase: "failed", epoch: "epoch-d", issue: sessionMissingIssue },
});
// 防御用例：后端投影与前端快照之间存在一帧 running=true 的旧数据。
const staleRunningDisconnectedTab = () => makeTab({
  ready: false,
  running: true,
  runtime: { phase: "failed", epoch: "epoch-d", issue: sessionMissingIssue },
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

console.log("\ntl 1266：会话实体查无的「已断开」徽章");

{
  const healthy = makeTab();
  const ghost = disconnectedTab();
  const ui = await renderTabBar([healthy, ghost]);
  const badgeTexts = ui.badges().map((b) => b.textContent).join("|");
  eq(badgeTexts, t("tabBar.disconnected"), "实体查无的骨架 tab 显示一条「已断开」徽章，健康 tab 不显示");
  const ghostButton = ui.tabButtons()[1];
  eq((ghostButton?.getAttribute("aria-label") ?? "").includes(t("tabBar.disconnected")), true,
    "断开文案进 hover title（aria-label）承接压缩深档");
  eq(ghostButton?.className.includes("tabbar__tab--running"), false, "断开 tab 不挂 running 类");
  await ui.unmount();
}

{
  // 活跃 tab 也显示：断开是状态真相（事故现场正是活跃 tab 挂残影）。
  const ghostActive = disconnectedTab();
  const other = makeTab();
  const ui = await renderTabBar([ghostActive, other], ghostActive.id);
  eq(ui.badges().map((b) => b.textContent).join("|"), t("tabBar.disconnected"), "活跃 tab 的断开态照常显示徽章");
  await ui.unmount();
}

{
  // 防御层：旧帧 running=true 与 session_missing 同帧到达时，running 类让位。
  const stale = staleRunningDisconnectedTab();
  const healthy = makeTab();
  const ui = await renderTabBar([healthy, stale]);
  const staleButton = ui.tabButtons()[1];
  eq(staleButton?.className.includes("tabbar__tab--running"), false, "running 残影帧不得挂 tabbar__tab--running");
  eq(staleButton?.querySelector(".tabbar__status--running") === null, true, "running 残影帧不得挂绿点类");
  eq(ui.badges().map((b) => b.textContent).join("|"), t("tabBar.disconnected"), "残影帧仍显示「已断开」徽章");
  await ui.unmount();
}

{
  // 对照组：健康 running tab 与普通骨架 tab 的既有语义逐字不变。
  const running = makeTab({ running: true });
  const lazy = makeTab({ ready: false, runtime: { phase: "starting", epoch: "epoch-1" } });
  const ui = await renderTabBar([running, lazy]);
  eq(ui.tabButtons()[0]?.className.includes("tabbar__tab--running"), true, "健康 running tab 照常挂 running 类");
  eq(ui.tabButtons()[0]?.querySelector(".tabbar__status--running") !== null, true, "健康 running tab 照常挂绿点");
  eq(ui.badges().map((b) => b.textContent).join("|"), t("tabBar.notLoaded"), "普通骨架 tab 照常显示「未加载」而非「已断开」");
  await ui.unmount();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
