// Run: tsx src/__tests__/tabbar-tab-mode-tint.test.tsx
// 任务 504：对话标签视觉——背景色（低透明度 ~30%）代替审批模式徽章
// （experimental_tab_mode_tint，默认关）。验收四条：
// ① 四档区分度：色调阶梯纯函数（autopilot > yolo > auto > goal > plan，
//    询问=不着色基线）+ 色板映射断言（五档色值两两不同、类名/样式映射
//    与 styles.css 任务504 段逐一对应）；
// ② hover 可见完整标题：title/aria-label 恒含「状态 · 工作区 / 完整标题」，
//    开关两态一致；
// ③ 开关默认关 + 关闭零行为（A/B 对照）：关闭态无 data-mode-tint 属性、
//    徽章渲染与旧路径逐字一致；开启时徽章被底色代替（含与 506 压缩档
//    叠加的场景）；设置保存后经 applyLabFlags 即时翻转。

import { JSDOM } from "jsdom";
import { readFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

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

const { tabModeTintFor, TabBar } = await import("../components/TabBar");
const labFlags = await import("../lib/labFlags");
const React = await import("react");
const { createElement } = React;
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");
const { LocaleProvider } = await import("../lib/i18n");
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
        activeTabId: undefined,
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

function tintAttributes(): string[] {
  return Array.from(document.querySelectorAll(".tabbar__tab[data-mode-tint]"))
    .map((node) => node.getAttribute("data-mode-tint") ?? "");
}

function badgeCount(): number {
  return document.querySelectorAll(".tabbar__mode-badge").length;
}

console.log("\n任务 504：标签模式色调（背景色代替审批模式徽章）");

// 1. 验收①：色调阶梯纯函数——审批档压过协作档，autopilot 居首，
//    ask+normal 默认态不着色。
eq(tabModeTintFor("normal", "ask"), null, "默认态（normal+ask）：不着色");
eq(tabModeTintFor("normal", "auto"), "auto", "自动审批：auto（蓝）");
eq(tabModeTintFor("normal", "yolo"), "yolo", "YOLO 审批：yolo（红）");
eq(tabModeTintFor("autopilot", "yolo"), "autopilot", "autopilot（含 yolo）：autopilot 压过 yolo（紫）");
eq(tabModeTintFor("autopilot", "ask"), "autopilot", "autopilot 审批未同步时仍取协作档");
eq(tabModeTintFor("plan", "ask"), "plan", "计划模式：plan（琥珀）");
eq(tabModeTintFor("goal", "ask"), "goal", "目标模式：goal（青）");
eq(tabModeTintFor("plan", "auto"), "auto", "审批档压过协作档：plan+auto → auto");
eq(tabModeTintFor("goal", "yolo"), "yolo", "审批档压过协作档：goal+yolo → yolo");
eq(tabModeTintFor("plan", "yolo"), "yolo", "审批档压过协作档：plan+yolo → yolo");
eq(tabModeTintFor("goal", "auto"), "auto", "审批档压过协作档：goal+auto → auto");

// 2. 验收③（A/B·关闭态）：开关默认关——任何模式都不写 data-mode-tint，
//    徽章渲染走旧路径逐字一致（506 落地后的旧路径：压缩关时 tier=0、
//    徽章本就不渲染，故全签 0 个；压缩开时的旧徽章路径见第 4 组翻回态）。
labFlags.applyLabFlags({});
{
  const view = await renderTabBar([
    makeTab({ toolApprovalMode: "yolo" }),
    makeTab({ collaborationMode: "plan" }),
    makeTab({ toolApprovalMode: "auto" }),
    makeTab({ collaborationMode: "autopilot", toolApprovalMode: "yolo" }),
    makeTab({}),
  ]);
  eq(tintAttributes().length, 0, "默认关：全签无 data-mode-tint 属性（零行为）");
  eq(badgeCount(), 0, "默认关：徽章走旧路径（压缩关 tier=0，0 个=旧渲染）");
  await view.unmount();
}

// 3. 验收①（组件级）：开关开启——各档写各自 data-mode-tint；徽章被底色
//    代替（「代替」语义：可着色档位集合 ⊇ 徽章集合，徽章一律不渲染）。
labFlags.applyLabFlags({ tabModeTint: true });
{
  const view = await renderTabBar([
    makeTab({ toolApprovalMode: "yolo" }),
    makeTab({ collaborationMode: "plan" }),
    makeTab({ toolApprovalMode: "auto" }),
    makeTab({ collaborationMode: "autopilot", toolApprovalMode: "yolo" }),
    makeTab({ collaborationMode: "goal" }),
    makeTab({}),
  ]);
  eq(tintAttributes().join(","), "yolo,plan,auto,autopilot,goal", "开启：五档各写各档位（默认态不写）");
  eq(badgeCount(), 0, "开启：徽章被底色代替（0 个）");
  await view.rerender([makeTab({})]);
  eq(tintAttributes().length, 0, "开启：默认态标签不着色");
  await view.unmount();
}

// 4. 验收③（与 506 叠加）：色调开启 + 压缩 tier 1——506 本会渲染徽章，
//    504 底色代替之；翻回关闭后徽章路径恢复。
labFlags.applyLabFlags({ tabModeTint: true, tabCompress: true });
{
  const view = await renderTabBar(Array.from({ length: 9 }, () => makeTab({ toolApprovalMode: "yolo" })));
  eq(view.bar().getAttribute("data-tab-tier"), "1", "叠加：506 tier 1 生效");
  eq(badgeCount(), 0, "叠加：tier 1 徽章被底色代替（506 单独开会渲染 9 个）");
  await act(async () => { labFlags.applyLabFlags({ tabModeTint: false, tabCompress: true }); });
  eq(tintAttributes().length, 0, "翻回关：色调属性全部消失");
  eq(badgeCount(), 9, "翻回关：徽章渲染恢复旧路径（9 个）");
  await view.unmount();
}

// 5. 验收②：hover 完整标题——开关两态下 title/aria-label 恒含
//    「状态 · 工作区 / 会话标题」（着色标签不缩水）。
{
  const tabs = [makeTab({ toolApprovalMode: "yolo", running: true, topicTitle: "悬浮定位完整标题", label: "悬浮定位完整标题" })];
  labFlags.applyLabFlags({ tabModeTint: false });
  const off = await renderTabBar(tabs);
  const offTitle = (off.bar().querySelector(".tabbar__tab") as HTMLElement).getAttribute("title") ?? "";
  await off.unmount();
  labFlags.applyLabFlags({ tabModeTint: true });
  const on = await renderTabBar(tabs);
  const button = on.bar().querySelector(".tabbar__tab") as HTMLElement;
  eq(button.getAttribute("title"), "Running · YOLO approval · 工作区 / 悬浮定位完整标题", "开启：title 含状态与完整标题");
  eq(button.getAttribute("title"), offTitle, "两态 title 逐字一致（开关不改 hover 信息）");
  eq(button.getAttribute("aria-label"), button.getAttribute("title"), "aria-label 与 title 一致");
  await on.unmount();
  labFlags.applyLabFlags({});
}

// 6. 验收①（色板映射钉）：TabBar 档位名与 styles.css 任务504 段的
//    data-mode-tint 选择器一一对应，五档色值两两不同（四档区分度的
//    物质基础——merge 丢段或改色导致撞色时在此炸）。
{
  const css = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "..", "styles.css"), "utf8");
  const palette: Array<[string, string]> = [];
  for (const match of css.matchAll(/\.tabbar \.tabbar__tab\[data-mode-tint="([a-z]+)"\] \{ --tab-tint-color: ([^;]+); \}/g)) {
    palette.push([match[1], match[2].trim()]);
  }
  eq(palette.map(([name]) => name).join(","), "plan,goal,auto,yolo,autopilot", "styles.css 五档选择器齐全（顺序=文件序）");
  const values = palette.map(([, color]) => color);
  eq(new Set(values).size, values.length, "五档色值两两不同（色相区分度）");
  // 任务614 语义色调钉：四档+goal 各自对位（plan=紫、autopilot=橙随
  // --mode-autopilot-* 修色、auto=蓝=approval 确认档、yolo=红、goal=青），
  // merge 换向/丢语义在此炸。
  const byName = Object.fromEntries(palette) as Record<string, string>;
  eq(byName.plan, "#7c3aed", "plan=紫（614 语义映射，紫归 plan）");
  eq(byName.autopilot, "var(--mode-autopilot-bg)", "autopilot=橙（随 595/614 专属橙 token，明暗随主题）");
  eq(byName.auto, "var(--mode-auto-bg)", "auto=蓝（approval 确认档）");
  eq(byName.yolo, "var(--mode-yolo-bg)", "yolo=红（警示档）");
  eq(byName.goal, "#0d9488", "goal=青（协作档补充）");
  // 任务614 徽章拆分：plan/goal/auto 原共用 --plan 徽章类，plan 转紫后逐档对位。
  const tabbar = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "..", "components", "TabBar.tsx"), "utf8");
  eq(tabbar.includes('tabbar__mode-badge--goal">goal'), true, "goal 徽章用 --goal 类（614 拆分）");
  eq(tabbar.includes('tabbar__mode-badge--auto">auto'), true, "auto 徽章用 --auto 类（614 拆分）");
  eq(css.includes(".tabbar__mode-badge--goal {"), true, "goal 徽章规则存在");
  eq(css.includes(".tabbar__mode-badge--auto {"), true, "auto 徽章规则存在");
  // 任务504 段的三条背景规则存在（~30% 底色 + hover 加深 + active 混底）。
  eq(css.includes(".tabbar .tabbar__tab[data-mode-tint]:not(.tabbar__tab--active)"), true, "非活跃底色规则存在");
  eq(css.includes(".tabbar .tabbar__tab[data-mode-tint]:not(.tabbar__tab--active):hover"), true, "hover 加深规则存在");
  eq(css.includes(".tabbar .tabbar__tab--active[data-mode-tint]"), true, "活跃混底规则存在");
  // 30% 低透明度（用户点名的强度）钉在非活跃/活跃两条规则里。
  const tint30 = (css.match(/color-mix\(in srgb, var\(--tab-tint-color\) 30%, (?:transparent|var\(--chat-bg\))\)/g) ?? []).length;
  eq(tint30 >= 2, true, "~30% 透明度出现在底色与活跃混底规则中");
}

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
