// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/composer-tools-default-fold.test.tsx
//
// tl1718 acceptance harness (composer 工具组默认折叠):
//  ① 开关关（默认）：.composer-toolbar-send 不挂 --tools-default-fold 修饰类
//     ——折叠行为仍只由宽度驱动（窄 ≤559px 折叠、宽平铺，CSS 容器断点裁定），
//     DOM 形态与引入前逐字节一致（铁律 2）；四钮（表情/截图/历史/改写）照常
//     在 DOM；
//  ② 开关开：.composer-toolbar-send 挂上修饰类，折叠入口（⋯ 工具）与四钮
//     同在 DOM（显隐交由 CSS：类规则四钮 display:none、入口 inline-flex，
//     宽窗口同样保持折叠为单图标）；点开二级菜单可见工具全集（同一入口同一
//     菜单本体，菜单内容零改动）；
//  ③ 开关切换：applyLabFlags 广播后类即时出现/消失（免重启、免重挂载，
//     1067 单击换态契约同款）；
//  ④ 既有窄宽折叠零回归（源码级）：styles.css 仍保留 @container 559px 容器
//     断点块（选择器组与折叠入口规则原样），类门控块复用同一选择器组且置于
//     容器块之后（同特异性后者胜、窄宽两态终值一致）；
//  ⑤ config/数据面：设置行读自己的键写自己的 setter（labLabel 恰好一次），
//     三语四键齐全。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { Composer } from "../components/Composer";
import { LocaleProvider } from "../lib/i18n";
import { ToastProvider } from "../lib/toast";
import { applyLabFlags } from "../lib/labFlags";
import type { CollaborationMode, ToolApprovalMode } from "../lib/types";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  globalThis.Event = dom.window.Event;
  globalThis.CustomEvent = dom.window.CustomEvent;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.InputEvent = dom.window.InputEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.File = dom.window.File;
  globalThis.FileReader = dom.window.FileReader;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = TestResizeObserver;
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: true,
      media: "(prefers-reduced-motion: reduce)",
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

type ComposerProps = Partial<Parameters<typeof Composer>[0]>;

async function renderComposer(props: ComposerProps = {}) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  let currentProps: Parameters<typeof Composer>[0] = {
    running: false,
    collaborationMode: "normal" as CollaborationMode,
    toolApprovalMode: "ask" as ToolApprovalMode,
    goal: "",
    cwd: "/repo",
    modelLabel: "mimo-v2.6",
    onSend: () => {},
    onCancel: () => undefined,
    onCycleMode: () => {},
    onSetMode: () => {},
    onSetCollaborationMode: () => {},
    onSetToolApprovalMode: () => {},
    onToggleYoloApprovalMode: () => {},
    onClearGoal: () => {},
    onSwitchModel: () => {},
    onSetEffort: () => {},
    ready: true,
    ...props,
  };
  const paint = async (nextProps: ComposerProps = {}) => {
    currentProps = { ...currentProps, ...nextProps };
    await act(async () => {
      root.render(
        <LocaleProvider>
          <ToastProvider>
            <Composer {...currentProps} />
          </ToastProvider>
        </LocaleProvider>,
      );
      await flushTimers();
    });
  };
  await paint();
  return { root };
}

// 桥接桩：与 composer-draft-rewrite 同款——挂到 window.go.main.App 上（Proxy
// realApp 优先路径），其余方法走 mock。
function stubBridge() {
  const w = window as unknown as { go?: { main?: { App?: unknown } } };
  w.go = {
    main: {
      App: {
        Commands: async () => [],
        Models: async () => [],
        ModelsForTab: async () => [],
        SlashArgs: async () => ({ items: [], from: 0 }),
        HistoryPageForTab: async () => ({
          messages: [
            { role: "user", content: "CI 又红了" },
            { role: "assistant", content: "我看下日志" },
          ],
          startTurn: 0,
          endTurn: 2,
          totalTurns: 2,
          hasOlder: false,
        }),
      },
    },
  };
}

function toolbarSend(): HTMLElement | null {
  return document.querySelector<HTMLElement>(".composer-toolbar-send");
}

function toolbarDefaultFoldClass(): string | null {
  const el = toolbarSend();
  return el?.className.includes("composer-toolbar-send--tools-default-fold") ? "on" : "off";
}

// 折叠入口（⋯ 工具）——Tooltip 包了一层 wrapper，查按钮本体。
function foldButton(): HTMLButtonElement | null {
  return document.querySelector<HTMLButtonElement>(".composer-tools-fold button[aria-haspopup=\"menu\"]");
}

// 二级菜单（AnchoredPopover portal 到 body）。
function toolsMenu(): HTMLElement | null {
  return document.querySelector<HTMLElement>(".composer-tools-menu");
}

function toolsMenuItems(): HTMLElement[] {
  return [...document.querySelectorAll<HTMLElement>(".composer-tools-menu [role=\"menuitem\"]")];
}

function clickControl(node: HTMLElement | null) {
  if (!node) return;
  node.dispatchEvent(new window.MouseEvent("click", { bubbles: true, cancelable: true, detail: 1 }));
}

// 四钮在 DOM 的查询器（显隐归 CSS，DOM 存在性归本测试）。
const flatButtonSelectors = {
  emoji: ".composer-emoji-picker button",
  screenshot: ".composer-screenshot-picker button",
  history: ".composer-history-picker button",
  rewrite: "button.composer__btn--rewrite",
} as const;

// 全量铺开四钮（历史键走 prop 直开——261 默认关；改写键走 lab 旗标）。
function fullToolbarProps(): ComposerProps {
  return { historyPickerEnabled: true };
}

// ── ① 开关关（默认）：类不挂，折叠行为仍只由宽度驱动 ──────────────────
{
  const dom = installDom();
  applyLabFlags({ composerToolsFold: false, draftRewrite: true });
  stubBridge();
  const { root } = await renderComposer(fullToolbarProps());
  eq(toolbarDefaultFoldClass(), "off", "flag off: the modifier class is absent (fold stays width-driven only)");
  ok(toolbarSend() !== null, "flag off: the toolbar renders");
  ok(foldButton() !== null, "flag off: the fold entry stays in the DOM (display ruled by CSS only, byte-for-byte as before)");
  for (const [name, selector] of Object.entries(flatButtonSelectors)) {
    ok(document.querySelector(selector) !== null, `flag off: the ${name} button still renders in the DOM (wide-window flat shape unchanged)`);
  }
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ② 开关开：类挂上；二级菜单见工具全集（同入口同菜单本体） ────────────
{
  const dom = installDom();
  applyLabFlags({ composerToolsFold: true, draftRewrite: true });
  stubBridge();
  const { root } = await renderComposer(fullToolbarProps());
  eq(toolbarDefaultFoldClass(), "on", "flag on: the modifier class is on the toolbar");
  ok(foldButton() !== null, "flag on: the fold entry renders (single-icon shape at any width, per the class rules)");
  // 菜单本体零改动：点开即见四项工具全集（emoji/截图/历史/改写）。
  await act(async () => { clickControl(foldButton()); await flushTimers(); });
  await act(async () => { await flushTimers(); });
  const menu = toolsMenu();
  ok(menu !== null, "flag on: clicking the fold entry opens the existing overflow menu");
  const items = toolsMenuItems().map((el) => el.textContent ?? "");
  eq(items.length, 4, "the menu lists the full tool set (4 items)");
  // LocaleProvider 在测试环境默认 en——断言双语兼容（zh：表情/截图/历史/改写）。
  ok(items.some((text) => text.includes("表情") || /emoji/i.test(text)), "the menu carries the emoji entry");
  ok(items.some((text) => text.includes("截图") || /screenshot/i.test(text)), "the menu carries the screenshot entry");
  ok(items.some((text) => text.includes("历史") || /history/i.test(text)), "the menu carries the history entry");
  ok(items.some((text) => text.includes("改写") || /rewr/i.test(text)), "the menu carries the rewrite entry");
  // 收尾关菜单，不留挂起弹层影响卸载。
  await act(async () => { clickControl(foldButton()); await flushTimers(); });
  await act(async () => { await flushTimers(); });
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ③ 开关切换：免重挂载即时换态（1067 单击换态契约同款） ───────────────
{
  const dom = installDom();
  applyLabFlags({ composerToolsFold: false });
  stubBridge();
  const { root } = await renderComposer();
  eq(toolbarDefaultFoldClass(), "off", "flip pre: class absent while off");
  await act(async () => {
    applyLabFlags({ composerToolsFold: true });
    await flushTimers();
  });
  eq(toolbarDefaultFoldClass(), "on", "flip: turning the lab flag ON applies the class immediately (no re-mount, no restart)");
  await act(async () => {
    applyLabFlags({ composerToolsFold: false });
    await flushTimers();
  });
  eq(toolbarDefaultFoldClass(), "off", "flip: turning OFF drops the class again (back to the pre-feature shape)");
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ④ 既有窄宽折叠零回归（源码级 CSS 契约） ─────────────────────────────
{
  const here = fileURLToPath(new URL(".", import.meta.url));
  const styles = readFileSync(`${here}../styles.css`, "utf8");
  const containerBlock = styles.match(/@container \(max-width: 559px\) \{[\s\S]*?\n\}/);
  ok(containerBlock !== null, "the narrow-width @container 559px fold block survives (existing behaviour untouched)");
  const block = containerBlock?.[0] ?? "";
  for (const selector of [".composer-emoji-picker", ".composer-screenshot-picker", ".composer-history-picker", ".composer__btn--rewrite"]) {
    ok(block.includes(`.composer-toolbar-send ${selector}`), `container block still hides ${selector} when narrow`);
  }
  ok(block.includes(".composer-toolbar-send .composer-tools-fold") && block.includes("display: inline-flex"),
    "container block still shows the fold entry when narrow (zero regression)");
  // 类门控块复用同一选择器组（同一折叠形态），且置于容器块之后
  // （同特异性后者胜——窄宽两态同类相遇终值一致）。
  const classBlockStart = styles.indexOf(".composer-toolbar-send--tools-default-fold .composer-emoji-picker");
  ok(classBlockStart > 0, "the class-gated fold block exists in styles.css");
  ok(classBlockStart > (styles.indexOf("@container (max-width: 559px)")), "the class-gated block sits after the container block (equal-specificity later-wins, both states agree when narrow)");
  for (const selector of [".composer-emoji-picker", ".composer-screenshot-picker", ".composer-history-picker", ".composer__btn--rewrite"]) {
    ok(styles.includes(`.composer-toolbar-send--tools-default-fold ${selector}`), `the class rules fold ${selector} at any width`);
  }
  ok(styles.includes(".composer-toolbar-send--tools-default-fold .composer-tools-fold"), "the class rules keep the fold entry visible");
}

// ── ⑤ config/数据面：设置行键/setter + 三语四键 ────────────────────────
{
  const here = fileURLToPath(new URL(".", import.meta.url));
  const srcRoot = `${here}..`;
  const labSection = readFileSync(`${srcRoot}/components/ExperimentalLabSection.tsx`, "utf8");
  const labLabelHits = [...labSection.matchAll(/labLabel\("composerToolsFold"/g)].length;
  eq(labLabelHits, 1, "the viewLayout card hosts exactly one composerToolsFold row (621 single-badge contract)");
  ok(labSection.includes("Boolean(s.experimentalComposerToolsFold)"), "the row reads its own key");
  ok(labSection.includes("app.SetExperimentalComposerToolsFold(on)"), "the row writes through its own setter");
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(`${srcRoot}/locales/${locale}.ts`, "utf8");
    const keys = ["settings.composerToolsFold", "settings.composerToolsFoldHint", "settings.composerToolsFold.on", "settings.composerToolsFold.off"];
    const missing = keys.filter((k) => !dict.includes(`"${k}"`));
    ok(missing.length === 0, `${locale} carries the tl1718 keys (missing: ${JSON.stringify(missing)})`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
