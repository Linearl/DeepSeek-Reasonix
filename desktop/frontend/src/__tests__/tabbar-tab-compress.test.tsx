// Run: tsx src/__tests__/tabbar-tab-compress.test.tsx
// 任务 506：标签栏自适应压缩（experimental_tab_compress，默认关）。
// 判据为溢出驱动（修订）：降档触发条件是「容器可用宽度装不下标签需求
// 宽度」，而不是标签数量——窄窗口少标签溢出即降、宽窗口多标签放得下
// 不降；按数量分档仅作测量不可用时的初始档位参考。验收四条：
// ① 溢出驱动：tierForWidth 纯函数（最小放得下的档位，84px 下限兜底）+
//    组件级 DOM 桩（.tabbar clientWidth 模拟容器宽度），档位以行内
//    --tabbar-tab-width 注入 .tabbar，压过所有样式面；
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

// 溢出驱动判据依赖布局测量：jsdom 无布局，用类名感知桩模拟容器宽度。
// fakeTabBarClientWidth = 0 表示「测量不可用」→ 档位退回数量分档参考。
let fakeTabBarClientWidth = 0;
Object.defineProperty(dom.window.HTMLElement.prototype, "clientWidth", {
  configurable: true,
  get(this: HTMLElement) {
    return this.classList.contains("tabbar") ? fakeTabBarClientWidth : 0;
  },
});
// ResizeObserver 可捕获桩：手动触发回调模拟窗口/容器尺寸变化。
class TestResizeObserver {
  static instances: TestResizeObserver[] = [];
  callback: ResizeObserverCallback;
  constructor(callback: ResizeObserverCallback) {
    this.callback = callback;
    TestResizeObserver.instances.push(this);
  }
  observe() {}
  unobserve() {}
  disconnect() {}
  trigger() {
    this.callback([], this as unknown as ResizeObserver);
  }
}
globalThis.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver;
dom.window.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver;

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

const { tabCompressTier, tabCompressTierForWidth, TAB_COMPRESS_TIERS, TAB_COMPRESS_BASE_WIDTH_PX } = await import(
  "../components/TabBar"
);
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

console.log("\n任务 506：标签栏自适应压缩（溢出驱动）");

// 0a. 溢出驱动纯函数：可用宽度装得下就不降，装不下取最小够用档位。
eq(TAB_COMPRESS_BASE_WIDTH_PX, 176, "基础档宽 176px（对应 .tabbar CSS 变量默认值）");
eq(TAB_COMPRESS_TIERS.map((tier) => tier.widthPx).join(","), "148,122,100,84", "档位宽度 148/122/100/84（下限 84px）");
eq(tabCompressTierForWidth(600, 4), 1, "窄窗口少标签：600px 放不下 4×176 → 降 1 档（148）");
eq(tabCompressTierForWidth(500, 4), 2, "更窄：500px 放不下 4×148 → 降 2 档（122）");
eq(tabCompressTierForWidth(2000, 9), 0, "宽窗口多标签：2000px 放得下 9×176 → 不降（数量档位本会降）");
eq(tabCompressTierForWidth(3000, 15), 0, "宽窗口 15 个：3000px 放得下 15×176 → 不降");
eq(tabCompressTierForWidth(5000, 25), 0, "超宽窗口 25 个：5000px 放得下 25×176 → 不降");
eq(tabCompressTierForWidth(2364, 15), 1, "临界放不下：2364px 装不下 15×176 → 只降到恰好放下的 1 档");
eq(tabCompressTierForWidth(704, 4), 0, "等号边界：704px = 4×176 恰好放得下 → 不降");
eq(tabCompressTierForWidth(592, 4), 1, "等号边界：592px = 4×148 恰好放得下 → 1 档");
eq(tabCompressTierForWidth(591, 4), 2, "差 1px：591px 装不下 4×148 → 2 档");
eq(tabCompressTierForWidth(100, 25), 4, "极窄：连 84px 下限都放不下 → 落底 4 档（残余溢出走横向滚动保底）");
eq(tabCompressTierForWidth(664, 0), 0, "0 个标签：不压缩");
eq(tabCompressTierForWidth(664, 1), 0, "1 个标签：不压缩");
// 测量不可用（宽度 ≤0）→ 回退数量档位参考。
eq(tabCompressTierForWidth(0, 9), 1, "测量不可用回退：9 个 → 数量档位 1");
eq(tabCompressTierForWidth(0, 13), 2, "测量不可用回退：13 个 → 数量档位 2");
eq(tabCompressTierForWidth(0, 25), 4, "测量不可用回退：25 个 → 数量档位 4（下限）");
eq(tabCompressTierForWidth(0, 0), 0, "测量不可用回退：0 个 → 不压缩");
eq(tabCompressTierForWidth(-5, 21), 4, "非法宽度（负数）同样走回退");
// 单调性：可用宽度越宽档位不升（压缩只松不紧）。
{
  let monotonic = true;
  for (const count of [1, 4, 9, 13, 17, 21, 40]) {
    let previous = -1;
    for (let width = 97; width <= 6000; width += 97) {
      const tier = tabCompressTierForWidth(width, count);
      if (previous >= 0 && tier > previous) monotonic = false;
      previous = tier;
    }
  }
  eq(monotonic, true, "单调性：宽度递增时档位只松不紧（7 种标签数 × 61 档宽扫描）");
}

// 0b. 数量档位纯函数：保留为测量不可用时的初始档位参考（等差 9/13/17/21）。
eq(TAB_COMPRESS_TIERS.map((tier) => tier.minTabs).join(","), "9,13,17,21", "数量档位边界为 9/13/17/21（等差步长 4，回退参考）");
eq(tabCompressTier(0), 0, "0 个标签：不压缩");
eq(tabCompressTier(8), 0, "8 个标签：不压缩（数量参考）");
eq(tabCompressTier(9), 1, "9 个标签：进 1 档（数量参考）");
eq(tabCompressTier(12), 1, "12 个标签：维持 1 档（数量参考）");
eq(tabCompressTier(13), 2, "13 个标签：进 2 档（数量参考）");
eq(tabCompressTier(16), 2, "16 个标签：维持 2 档（数量参考）");
eq(tabCompressTier(17), 3, "17 个标签：进 3 档（数量参考）");
eq(tabCompressTier(20), 3, "20 个标签：维持 3 档（数量参考）");
eq(tabCompressTier(21), 4, "21 个标签：进 4 档（数量参考，下限）");
eq(tabCompressTier(40), 4, "40 个标签：维持下限档（数量参考）");

// 1. 验收④：开关默认关——25 个标签也不注入任何压缩痕迹（零行为）。
labFlags.applyLabFlags({});
fakeTabBarClientWidth = 0;
{
  const view = await renderTabBar(makeTabs(25));
  const bar = view.bar();
  eq(bar.getAttribute("data-tab-compress"), null, "默认关：无 data-tab-compress 属性");
  eq(bar.getAttribute("data-tab-tier"), null, "默认关：无 data-tab-tier 属性");
  eq(inlineWidth(bar), "", "默认关：不注入行内宽度（逐像素等价旧行为）");
  eq(container_badge_count(), 0, "默认关：yolo 徽章渲染走旧路径（本组未设 yolo，0 个）");
  await view.unmount();
}

// 2. 验收①（组件级·新判据）：窄窗口少标签——可用宽度装不下即降，
//    与标签数量无关（本组仅 4 个标签，数量档位永不触发）。
labFlags.applyLabFlags({ tabCompress: true });
fakeTabBarClientWidth = 700; // 可用宽 700-36(max-width 保留) = 664px
{
  const view = await renderTabBar(makeTabs(4));
  const bar = view.bar();
  eq(bar.getAttribute("data-tab-compress"), "on", "窄窗口：data-tab-compress=on");
  eq(bar.getAttribute("data-tab-tier"), "1", "窄窗口 4 个标签：溢出即降 tier 1（数量档位对 ≤8 个恒为 0）");
  eq(inlineWidth(bar), "148", "窄窗口 4 个标签：行内宽度 148px");
  await view.unmount();
}

// 3. 验收①（组件级·新判据）：宽窗口多标签——放得下就不降，
//    数量档位（9/13 起降）被溢出判据压过。
fakeTabBarClientWidth = 2400; // 可用宽 2364px
{
  const view = await renderTabBar(makeTabs(12));
  const bar = view.bar();
  eq(bar.getAttribute("data-tab-tier"), null, "宽窗口 12 个标签：放得下不降（数量档位本会降 122px）");
  eq(inlineWidth(bar), "", "宽窗口 12 个标签：不注入行内宽度（保持 176px 基础档）");
  await view.rerender(makeTabs(15));
  eq(view.bar().getAttribute("data-tab-tier"), "1", "宽窗口 15 个：只降到恰好放下的 tier 1（数量档位本为 tier 2/122px）");
  eq(inlineWidth(view.bar()), "148", "宽窗口 15 个：行内宽度 148px");
  await view.unmount();
}

// 4. 验收①（组件级）：容器宽度变化由 ResizeObserver 跟随——
//    窗口拉宽回档、拉窄再降，档位即时翻转。
{
  fakeTabBarClientWidth = 700;
  const view = await renderTabBar(makeTabs(4));
  eq(view.bar().getAttribute("data-tab-tier"), "1", "翻转前：窄窗口 tier 1");
  fakeTabBarClientWidth = 2400;
  await act(async () => {
    TestResizeObserver.instances.at(-1)?.trigger();
  });
  eq(view.bar().getAttribute("data-tab-tier"), null, "窗口拉宽：tier 属性消失（回到不压缩）");
  eq(inlineWidth(view.bar()), "", "窗口拉宽：行内宽度消失");
  fakeTabBarClientWidth = 560;
  await act(async () => {
    TestResizeObserver.instances.at(-1)?.trigger();
  });
  eq(view.bar().getAttribute("data-tab-tier"), "2", "窗口拉窄：再降 tier 2（560-36=524 < 4×148，放得下 4×122）");
  eq(inlineWidth(view.bar()), "122", "窗口拉窄：行内宽度 122px");
  await view.unmount();
}

// 5. 数量档位回退（测量不可用）：jsdom 默认桩=0 → 9/13/17/21 逐级走
//    数量参考档，行内宽度随档位走（首版判据在此环境下的等价行为）。
fakeTabBarClientWidth = 0;
{
  const view = await renderTabBar(makeTabs(9));
  eq(view.bar().getAttribute("data-tab-tier"), "1", "测量不可用：9 个走数量参考 tier 1");
  eq(inlineWidth(view.bar()), "148", "测量不可用：9 个宽度 148px（=既有窄窗口档宽）");
  await view.rerender(makeTabs(13));
  eq(view.bar().getAttribute("data-tab-tier"), "2", "测量不可用：13 个走数量参考 tier 2");
  eq(inlineWidth(view.bar()), "122", "测量不可用：13 个宽度 122px");
  await view.rerender(makeTabs(17));
  eq(view.bar().getAttribute("data-tab-tier"), "3", "测量不可用：17 个走数量参考 tier 3");
  eq(inlineWidth(view.bar()), "100", "测量不可用：17 个宽度 100px");
  await view.rerender(makeTabs(21));
  eq(view.bar().getAttribute("data-tab-tier"), "4", "测量不可用：21 个走数量参考 tier 4");
  eq(inlineWidth(view.bar()), "84", "测量不可用：21 个宽度 84px（下限）");
  await view.rerender(makeTabs(30));
  eq(view.bar().getAttribute("data-tab-tier"), "4", "测量不可用：30 个维持 tier 4");
  eq(inlineWidth(view.bar()), "84", "测量不可用：30 个维持 84px");
  await view.unmount();
}

// 6. 验收③：tier 3+ 文本徽章不再渲染；tier 1 仍渲染（504 背景色代徽章
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

// 7. 验收②：压缩深档下 hover title / aria-label 仍是完整标注标题
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

// 8. 验收④：设置保存把开关翻回关——属性与行内宽度立即消失（即时生效链路）。
{
  const view = await renderTabBar(makeTabs(25));
  eq(inlineWidth(view.bar()), "84", "翻转前：数量参考 tier 4 生效");
  await act(async () => { labFlags.applyLabFlags({ tabCompress: false }); });
  const bar = view.bar();
  eq(bar.getAttribute("data-tab-compress"), null, "翻回关：data-tab-compress 消失");
  eq(bar.getAttribute("data-tab-tier"), null, "翻回关：data-tab-tier 消失");
  eq(inlineWidth(bar), "", "翻回关：行内宽度消失");
  await view.unmount();
}

// 9. 验收④：关闭态不启动测量链路（无 ResizeObserver 实例残留）——
//    零行为不含任何测量副作用。
{
  const before = TestResizeObserver.instances.length;
  const view = await renderTabBar(makeTabs(10));
  eq(TestResizeObserver.instances.length, before, "关闭态：不创建 ResizeObserver（零副作用）");
  await view.unmount();
}

function container_badge_count(): number {
  return document.querySelectorAll(".tabbar__mode-badge").length;
}

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
