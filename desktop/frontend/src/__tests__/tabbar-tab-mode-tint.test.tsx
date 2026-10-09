// Run: tsx src/__tests__/tabbar-tab-mode-tint.test.tsx
// 任务 504→651：标签权限指示三档（badge | off | background，默认 badge）+
// 背景色透明度降 10% + 背景色与徽章统一权威色 token（追加面：同一权限档
// 在两种指示下颜色一致）。验收六条：
// ① 四档区分度：色调阶梯纯函数（autopilot > yolo > auto > goal > plan，
//    询问=不着色基线）+ 色板映射断言（五档色值两两不同、类名/样式映射
//    与 styles.css 任务504段 逐一对应）；
// ② hover 可见完整标题：title/aria-label 恒含「状态 · 工作区 / 完整标题」，
//    三档一致；
// ③ 三档切换 + 默认徽章（A/B 对照）：badge 档无 data-mode-tint、徽章渲染
//    与旧路径逐字一致；background 档徽章被底色代替（含与 506 压缩档叠加）；
//    off 档连徽章一并隐藏；设置保存后经 applyTabPermissionIndicator 即时
//    翻转（持久化由 Go 侧 settings 往返测试钉住）；
// ④ 色源统一（651 追加）：tint 档位 token 与徽章规则同源——软徽章
//    （plan/goal）随 --mode-*-fg、实心徽章（auto/yolo/autopilot）随
//    --mode-*-bg，禁裸色值。

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

console.log("\n任务 651：标签权限指示三档（徽章/关闭/背景色）+ 色源统一");

// 0. 三档归一化：未知/缺省值一律落 badge（默认档=观感最轻，任务书指定）。
eq(labFlags.normalizeTabPermissionIndicator(undefined), "badge", "归一化：undefined → badge（默认档）");
eq(labFlags.normalizeTabPermissionIndicator(""), "badge", "归一化：空串 → badge");
eq(labFlags.normalizeTabPermissionIndicator("hue"), "badge", "归一化：未知值 → badge");
eq(labFlags.normalizeTabPermissionIndicator("badge"), "badge", "归一化：badge 原样");
eq(labFlags.normalizeTabPermissionIndicator("off"), "off", "归一化：off 原样");
eq(labFlags.normalizeTabPermissionIndicator("background"), "background", "归一化：background 原样");

// 1. 验收①：色调阶梯纯函数——审批档压过协作档，autopilot 居首，
//    ask+normal 默认态不着色。
eq(tabModeTintFor("normal", "ask"), null, "默认态（normal+ask）：不着色");
eq(tabModeTintFor("normal", "auto"), "auto", "自动审批：auto（蓝）");
eq(tabModeTintFor("normal", "yolo"), "yolo", "YOLO 审批：yolo（红）");
eq(tabModeTintFor("autopilot", "yolo"), "autopilot", "autopilot（含 yolo）：autopilot 压过 yolo（橙）");
eq(tabModeTintFor("autopilot", "ask"), "autopilot", "autopilot 审批未同步时仍取协作档");
eq(tabModeTintFor("plan", "ask"), "plan", "计划模式：plan（紫）");
eq(tabModeTintFor("goal", "ask"), "goal", "目标模式：goal（青）");
eq(tabModeTintFor("plan", "auto"), "auto", "审批档压过协作档：plan+auto → auto");
eq(tabModeTintFor("goal", "yolo"), "yolo", "审批档压过协作档：goal+yolo → yolo");
eq(tabModeTintFor("plan", "yolo"), "yolo", "审批档压过协作档：plan+yolo → yolo");
eq(tabModeTintFor("goal", "auto"), "auto", "审批档压过协作档：goal+auto → auto");

// 2. 验收③（A/B·默认档 badge）：快照缺省——任何模式都不写 data-mode-tint，
//    徽章渲染走旧路径逐字一致（506 落地后的旧路径：压缩关时 tier=0、
//    徽章本就不渲染，故全签 0 个；压缩开时的旧徽章路径见第 4 组翻回态）。
labFlags.applyLabFlags({});
labFlags.applyTabPermissionIndicator(undefined);
{
  const view = await renderTabBar([
    makeTab({ toolApprovalMode: "yolo" }),
    makeTab({ collaborationMode: "plan" }),
    makeTab({ toolApprovalMode: "auto" }),
    makeTab({ collaborationMode: "autopilot", toolApprovalMode: "yolo" }),
    makeTab({}),
  ]);
  eq(tintAttributes().length, 0, "默认 badge 档：全签无 data-mode-tint 属性（零行为）");
  eq(badgeCount(), 0, "默认 badge 档：徽章走旧路径（压缩关 tier=0，0 个=旧渲染）");
  await view.unmount();
}

// 3. 验收③（background 档）：各档写各自 data-mode-tint；徽章被底色
//    代替（「代替」语义：可着色档位集合 ⊇ 徽章集合，徽章一律不渲染）。
labFlags.applyTabPermissionIndicator("background");
{
  const view = await renderTabBar([
    makeTab({ toolApprovalMode: "yolo" }),
    makeTab({ collaborationMode: "plan" }),
    makeTab({ toolApprovalMode: "auto" }),
    makeTab({ collaborationMode: "autopilot", toolApprovalMode: "yolo" }),
    makeTab({ collaborationMode: "goal" }),
    makeTab({}),
  ]);
  eq(tintAttributes().join(","), "yolo,plan,auto,autopilot,goal", "background 档：五档各写各档位（默认态不写）");
  eq(badgeCount(), 0, "background 档：徽章被底色代替（0 个）");
  await view.rerender([makeTab({})]);
  eq(tintAttributes().length, 0, "background 档：默认态标签不着色");
  await view.unmount();
}

// 4. 验收③（与 506 叠加 + off 档）：background + 压缩 tier 1——506 本会
//    渲染徽章，底色代替之；翻回 badge 档徽章路径恢复；切 off 档连徽章
//    一并隐藏且无 tint（三档互斥完整覆盖）。
labFlags.applyLabFlags({ tabCompress: true });
labFlags.applyTabPermissionIndicator("background");
{
  const view = await renderTabBar(Array.from({ length: 9 }, () => makeTab({ toolApprovalMode: "yolo" })));
  eq(view.bar().getAttribute("data-tab-tier"), "1", "叠加：506 tier 1 生效");
  eq(badgeCount(), 0, "叠加：tier 1 徽章被底色代替（506 单独开会渲染 9 个）");
  await act(async () => { labFlags.applyTabPermissionIndicator("badge"); });
  eq(tintAttributes().length, 0, "翻回 badge 档：色调属性全部消失");
  eq(badgeCount(), 9, "翻回 badge 档：徽章渲染恢复旧路径（9 个）");
  await act(async () => { labFlags.applyTabPermissionIndicator("off"); });
  eq(tintAttributes().length, 0, "off 档：无色调属性");
  eq(badgeCount(), 0, "off 档：徽章一并隐藏（0 个，506 tier 1 仍开）");
  await act(async () => { labFlags.applyTabPermissionIndicator("badge"); });
  eq(badgeCount(), 9, "off 翻回 badge 档：徽章恢复（三档往返完整）");
  await view.unmount();
}
labFlags.applyLabFlags({});

// 5. 验收②：hover 完整标题——三档下 title/aria-label 恒含
//    「状态 · 工作区 / 会话标题」（着色标签不缩水）。
{
  const tabs = [makeTab({ toolApprovalMode: "yolo", running: true, topicTitle: "悬浮定位完整标题", label: "悬浮定位完整标题" })];
  labFlags.applyTabPermissionIndicator("badge");
  const off = await renderTabBar(tabs);
  const offTitle = (off.bar().querySelector(".tabbar__tab") as HTMLElement).getAttribute("title") ?? "";
  await off.unmount();
  labFlags.applyTabPermissionIndicator("background");
  const on = await renderTabBar(tabs);
  const button = on.bar().querySelector(".tabbar__tab") as HTMLElement;
  eq(button.getAttribute("title"), "Running · YOLO approval · 工作区 / 悬浮定位完整标题", "background 档：title 含状态与完整标题");
  eq(button.getAttribute("title"), offTitle, "三档 title 逐字一致（指示档位不改 hover 信息）");
  eq(button.getAttribute("aria-label"), button.getAttribute("title"), "aria-label 与 title 一致");
  await on.unmount();
  labFlags.applyTabPermissionIndicator(undefined);
}

// 6. 验收①+④（色板映射钉 + 色源统一钉）：TabBar 档位名与 styles.css
//    任务504段 的 data-mode-tint 选择器一一对应，五档色值两两不同（四档
//    区分度的物质基础——merge 丢段或改色导致撞色时在此炸）。651 追加：
//    每档 tint token 必须与徽章规则同 token 源（软徽章=--mode-*-fg、
//    实心徽章=--mode-*-bg），禁裸色值——徽章与背景色两指示颜色一致。
{
  const css = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "..", "styles.css"), "utf8");
  const palette: Array<[string, string]> = [];
  for (const match of css.matchAll(/\.tabbar \.tabbar__tab\[data-mode-tint="([a-z]+)"\] \{ --tab-tint-color: ([^;]+); \}/g)) {
    palette.push([match[1], match[2].trim()]);
  }
  eq(palette.map(([name]) => name).join(","), "plan,goal,auto,yolo,autopilot", "styles.css 五档选择器齐全（顺序=文件序）");
  const values = palette.map(([, color]) => color);
  eq(new Set(values).size, values.length, "五档色值两两不同（色相区分度）");
  // 任务614 语义色调钉 + 651 色源统一钉：四档+goal 各自对位且与徽章
  // 同 token（plan=紫随 --mode-plan-fg、goal=青随 --mode-goal-fg、
  // auto=蓝随 --mode-auto-bg=approval 确认档、yolo=红随 --mode-yolo-bg、
  // autopilot=橙随 --mode-autopilot-bg=595/614 专属橙），merge 换向/
  // 丢语义/裸色值回潮在此炸。
  const byName = Object.fromEntries(palette) as Record<string, string>;
  eq(byName.plan, "var(--mode-plan-fg)", "plan=紫（与 plan 徽章文字色同 token）");
  eq(byName.goal, "var(--mode-goal-fg)", "goal=青（与 goal 徽章文字色同 token）");
  eq(byName.autopilot, "var(--mode-autopilot-bg)", "autopilot=橙（随 595/614 专属橙 token，与 autopilot 徽章底色同源）");
  eq(byName.auto, "var(--mode-auto-bg)", "auto=蓝（approval 确认档，与徽章底色同 token）");
  eq(byName.yolo, "var(--mode-yolo-bg)", "yolo=红（警示档，与徽章底色同 token）");
  for (const bare of values) {
    eq(bare.startsWith("#"), false, "tint 档位无裸色值（651 追加：全 token 化）");
  }
  // 徽章规则同源钉：软徽章 color=--mode-*-fg、实心徽章 background=--mode-*-bg
  // （背景色档与徽章档的颜色一致性 = 同一 token 两处消费）。
  eq(css.includes(".tabbar__mode-badge--plan {\n  color: var(--mode-plan-fg);"), true, "plan 徽章文字色=--mode-plan-fg（与 tint 同源）");
  eq(css.includes(".tabbar__mode-badge--goal {\n  color: var(--mode-goal-fg);"), true, "goal 徽章文字色=--mode-goal-fg（与 tint 同源）");
  // 任务614 徽章拆分：plan/goal/auto 原共用 --plan 徽章类，plan 转紫后逐档对位。
  const tabbar = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "..", "components", "TabBar.tsx"), "utf8");
  eq(tabbar.includes('tabbar__mode-badge--goal">goal'), true, "goal 徽章用 --goal 类（614 拆分）");
  eq(tabbar.includes('tabbar__mode-badge--auto">auto'), true, "auto 徽章用 --auto 类（614 拆分）");
  eq(css.includes(".tabbar__mode-badge--goal {"), true, "goal 徽章规则存在");
  eq(css.includes(".tabbar__mode-badge--auto {"), true, "auto 徽章规则存在");
  // 任务504段的三条背景规则存在（651 起 10% 底色 + hover 加深 + active 混底）。
  eq(css.includes(".tabbar .tabbar__tab[data-mode-tint]:not(.tabbar__tab--active)"), true, "非活跃底色规则存在");
  eq(css.includes(".tabbar .tabbar__tab[data-mode-tint]:not(.tabbar__tab--active):hover"), true, "hover 加深规则存在");
  eq(css.includes(".tabbar .tabbar__tab[data-mode-tint]"), true, "tint 规则段存在");
  // 10% 低透明度（651：30%→10%，用户实证观感偏重）钉在非活跃/活跃两条
  // 规则里；hover 按 10/30 比例降为 14%。旧 30%/42% 强度不得残留。
  const tint10 = (css.match(/color-mix\(in srgb, var\(--tab-tint-color\) 10%, (?:transparent|var\(--chat-bg\))\)/g) ?? []).length;
  eq(tint10 >= 2, true, "10% 透明度出现在底色与活跃混底规则中");
  eq(css.includes("var(--tab-tint-color) 14%, transparent"), true, "hover 加深按比例降为 14%");
  eq(css.includes("var(--tab-tint-color) 30%"), false, "旧 30% 强度已清除");
  eq(css.includes("var(--tab-tint-color) 42%"), false, "旧 42% hover 强度已清除");
}

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
