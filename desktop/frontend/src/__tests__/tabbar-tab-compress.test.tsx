// Run: tsx src/__tests__/tabbar-tab-compress.test.tsx
// 任务 506：标签栏自适应压缩（experimental_tab_compress，默认关）。验收四条：
// ① 标签 ≥9 个起逐级降宽（9→148 / 13→122 / 17→100 / 21→84px 下限），档位
//    以行内 --tabbar-tab-width 注入 .tabbar，压过所有样式面；
// ② hover 完整标题——TabBar 始终把「状态 · 工作区 / 标题」挂在 title /
//    aria-label，压缩态不缩水（此处钉住）；
// ③ 与 504 兼容：压缩深档（tier 3+）不再渲染文本徽章（plan/goal/auto/yolo），
//    模式信息由 title 承接——背景色代徽章（504）落地时无布局冲突；
// ④ 开关默认关 + 关闭零行为：不注入行内宽度、无 data-tab-* 属性、
//    徽章渲染与旧路径逐字节一致；设置保存后经 applyLabFlags 即时翻转。

import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
// TabBar 的活跃标签滚动效果用到 rAF / scrollIntoView，jsdom 默认不提供。
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

const { tabCompressTier, TAB_COMPRESS_TIERS } = await import("../components/TabBar");
const labFlags = await import("../lib/labFlags");
const React = await import("react");
const { createElement } = React;
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");
const { LocaleProvider } = await import("../lib/i18n");
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

function makeTabs(count: number, overrides: Partial<TabMeta> = {}): TabMeta[] {
  return Array.from({ length: count }, () => makeTab(overrides));
}

interface RenderResult {
  bar: () => Element;
  rerender: (tabs: TabMeta[]) => Promise<void>;
  unmount: () => Promise<void>;
}

async function renderTabBar(tabs: TabMeta[]): Promise<RenderResult> {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const render = async (nextTabs: TabMeta[]) => {
    await act(async () => {
      root.render(createElement(LocaleProvider, null, createElement(TabBar, {
        tabs: nextTabs,
        activeTabId: nextTabs[0]?.id,
        onTabChange: () => {},
        onTabClose: () => {},
        onTabsClose: () => {},
        onTabsReorder: () => {},
        onNewTab: () => {},
      })));
    });
  };
  await render(tabs);
  return {
    bar: () => container.querySelector(".tabbar") as Element,
    rerender: render,
    unmount: async () => {
      await act(async () => { root.unmount(); });
      container.remove();
    },
  };
}

function inlineWidth(bar: Element): string {
  const match = /--tabbar-tab-width:\s*(\d+)px/.exec(bar.getAttribute("style") ?? "");
  return match ? match[1] : "";
}

console.log("\n任务 506：标签栏自适应压缩");

// 0. 档位纯函数：等差边界 9/13/17/21，步长 4。
eq(TAB_COMPRESS_TIERS.map((tier) => tier.minTabs).join(","), "9,13,17,21", "档位边界为 9/13/17/21（等差步长 4）");
eq(TAB_COMPRESS_TIERS.map((tier) => tier.widthPx).join(","), "148,122,100,84", "档位宽度 148/122/100/84（下限 84px）");
eq(tabCompressTier(0), 0, "0 个标签：不压缩");
eq(tabCompressTier(8), 0, "8 个标签：不压缩（176px 全宽容量）");
eq(tabCompressTier(9), 1, "9 个标签：进 1 档");
eq(tabCompressTier(12), 1, "12 个标签：维持 1 档");
eq(tabCompressTier(13), 2, "13 个标签：进 2 档");
eq(tabCompressTier(16), 2, "16 个标签：维持 2 档");
eq(tabCompressTier(17), 3, "17 个标签：进 3 档");
eq(tabCompressTier(20), 3, "20 个标签：维持 3 档");
eq(tabCompressTier(21), 4, "21 个标签：进 4 档（下限）");
eq(tabCompressTier(40), 4, "40 个标签：维持下限档");

// 1. 验收④：开关默认关——25 个标签也不注入任何压缩痕迹（零行为）。
labFlags.applyLabFlags({});
{
  const view = await renderTabBar(makeTabs(25));
  const bar = view.bar();
  eq(bar.getAttribute("data-tab-compress"), null, "默认关：无 data-tab-compress 属性");
  eq(bar.getAttribute("data-tab-tier"), null, "默认关：无 data-tab-tier 属性");
  eq(inlineWidth(bar), "", "默认关：不注入行内宽度（逐像素等价旧行为）");
  eq(container_badge_count(), 0, "默认关：yolo 徽章渲染走旧路径（本组未设 yolo，0 个）");
  await view.unmount();
}

// 2. 验收①：开启后 8 个标签仍是 tier 0（不提前降宽）。
labFlags.applyLabFlags({ tabCompress: true });
{
  const view = await renderTabBar(makeTabs(8));
  const bar = view.bar();
  eq(bar.getAttribute("data-tab-compress"), "on", "开启：data-tab-compress=on");
  eq(bar.getAttribute("data-tab-tier"), null, "开启但 ≤8 个：无档位属性（不降宽）");
  eq(inlineWidth(bar), "", "开启但 ≤8 个：不注入行内宽度");
  await view.unmount();
}

// 3. 验收①：9/13/17/21 个标签逐级降宽，行内宽度随档位走。
{
  const view = await renderTabBar(makeTabs(9));
  eq(view.bar().getAttribute("data-tab-tier"), "1", "9 个：tier 1");
  eq(inlineWidth(view.bar()), "148", "9 个：宽度 148px（=既有窄窗口档宽）");
  await view.rerender(makeTabs(13));
  eq(view.bar().getAttribute("data-tab-tier"), "2", "13 个：tier 2");
  eq(inlineWidth(view.bar()), "122", "13 个：宽度 122px");
  await view.rerender(makeTabs(17));
  eq(view.bar().getAttribute("data-tab-tier"), "3", "17 个：tier 3");
  eq(inlineWidth(view.bar()), "100", "17 个：宽度 100px");
  await view.rerender(makeTabs(21));
  eq(view.bar().getAttribute("data-tab-tier"), "4", "21 个：tier 4");
  eq(inlineWidth(view.bar()), "84", "21 个：宽度 84px（下限）");
  await view.rerender(makeTabs(30));
  eq(view.bar().getAttribute("data-tab-tier"), "4", "30 个：维持 tier 4");
  eq(inlineWidth(view.bar()), "84", "30 个：维持 84px");
  await view.unmount();
}

// 4. 验收③：tier 3+ 文本徽章不再渲染；tier 1 仍渲染（504 背景色代徽章
//    落地时占用的是背景层，与本档位隐藏逻辑无布局冲突）。
{
  const view = await renderTabBar(makeTabs(9, { toolApprovalMode: "yolo" }));
  eq(container_badge_count(), 9, "tier 1：yolo 徽章照常渲染（9 个标签各 1 个）");
  await view.rerender(makeTabs(17, { toolApprovalMode: "yolo" }));
  eq(container_badge_count(), 0, "tier 3：文本徽章不再渲染");
  await view.rerender(makeTabs(21, { toolApprovalMode: "yolo" }));
  eq(container_badge_count(), 0, "tier 4：文本徽章不再渲染");
  await view.unmount();
}

// 5. 验收②：压缩深档下 hover title / aria-label 仍是完整标注标题
//    （状态 · 工作区 / 会话标题），信息不因降宽丢失。
{
  const view = await renderTabBar(makeTabs(21, {
    toolApprovalMode: "yolo",
    running: true,
    topicTitle: "悬浮定位完整标题",
    label: "悬浮定位完整标题",
  }));
  const button = view.bar().querySelector(".tabbar__tab") as HTMLElement;
  eq(button.getAttribute("title"), "Running · YOLO approval · 工作区 / 悬浮定位完整标题", "tier 4：title 含状态与完整标题");
  eq(button.getAttribute("aria-label"), button.getAttribute("title"), "tier 4：aria-label 与 title 一致");
  await view.unmount();
}

// 6. 验收④：设置保存把开关翻回关——属性与行内宽度立即消失（即时生效链路）。
{
  const view = await renderTabBar(makeTabs(25));
  eq(inlineWidth(view.bar()), "84", "翻转前：tier 4 生效");
  await act(async () => { labFlags.applyLabFlags({ tabCompress: false }); });
  const bar = view.bar();
  eq(bar.getAttribute("data-tab-compress"), null, "翻回关：data-tab-compress 消失");
  eq(bar.getAttribute("data-tab-tier"), null, "翻回关：data-tab-tier 消失");
  eq(inlineWidth(bar), "", "翻回关：行内宽度消失");
  await view.unmount();
}

function container_badge_count(): number {
  return document.querySelectorAll(".tabbar__mode-badge").length;
}

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
