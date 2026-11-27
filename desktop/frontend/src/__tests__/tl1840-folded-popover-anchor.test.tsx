// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1840-folded-popover-anchor.test.tsx
//
// tl1840 acceptance harness (折叠态弹层锚点失效):
//  根因：输入栏工具组折叠是纯 CSS display:none（@container ≤559px 断点或
//  --tools-default-fold 修饰类，styles.css 46134/46152）；四钮（emoji/截图/
//  历史/改写）的容器被藏起后元素仍在 DOM，getBoundingClientRect() 返回全零
//  DOMRect；AnchoredPopover.updatePosition 不判零 rect，calculatePosition 用
//  (0,0,0,0) 锚算出 clamp 后 (8,8)——弹层落到窗口左上角。
//  修法（用户预期定稿）：锚定对象动态化——AnchoredPopover 新增可选
//  fallbackAnchorRef；主锚 rect 不可测（全零）时换备用锚，同一套定位逻辑
//  只换锚。未折叠主锚可见=行为零变化；折叠态锚定工具组折叠按钮（⋯）。
//
//  ① 单元钉（rect 桩，anchored-popover-scroll 同款）：
//     a. 折叠态：主锚全零 rect + 备用锚真实 rect → 弹层位置由备用锚 rect 推出
//        （top=fallback.bottom+offset、left=fallback.left），绝非 (8,8)；
//     b. 未折叠：主锚真实 rect（备用锚也在场）→ 位置由主锚推出——fallback
//        不劫持，行为零变化（行为钉）；
//     c. 无备用锚 + 主锚全零 rect → 修法前同款 (8,8)（未传 fallback 的既有
//        弹层逐字节保持原行为）。
//  ② Composer 级 e2e（composer-tools-default-fold 同款宿主）：开关开折叠态，
//     点「⋯」→ 菜单锚定折叠按钮；点「历史提示词」/「表情」菜单项 → 弹层
//     位置仍由折叠按钮 rect 推出（align end：left=fold.right-menuWidth、
//     auto：top=fold.top-menuHeight-offset），而非 (8,8)。
//  ③ 源码钉：emoji/历史两弹层挂 fallbackAnchorRef={toolsMenuAnchorRef}；
//     toolsMenu/content-intent 弹层不挂（锚恒可见/不在折叠面）。

import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";
import { createElement, useRef } from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { AnchoredPopover } from "../components/AnchoredPopover";

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

type RectParts = Pick<DOMRect, "left" | "top" | "right" | "bottom" | "width" | "height">;

function rect({ left, top, right, bottom, width, height }: RectParts): DOMRect {
  return { left, top, right, bottom, width, height, x: left, y: top, toJSON: () => ({}) } as DOMRect;
}

function installDom(): JSDOM {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  Object.defineProperty(dom.window, "matchMedia", {
    configurable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent: () => false,
    }),
  });
  Object.defineProperty(window, "innerWidth", { configurable: true, value: 800 });
  Object.defineProperty(window, "innerHeight", { configurable: true, value: 600 });
  return dom;
}

// rect 桩：data-testid 命中即返回桩值，未命中（弹层本体除外）走原型默认。
function stubRects(
  dom: JSDOM,
  rules: { selector: string; parts: RectParts | null }[],
  popoverParts: RectParts,
) {
  const original = dom.window.HTMLElement.prototype.getBoundingClientRect;
  dom.window.HTMLElement.prototype.getBoundingClientRect = function getBoundingClientRect() {
    if (this instanceof dom.window.HTMLElement && this.dataset.anchoredPopover === "active") {
      return rect(popoverParts);
    }
    for (const rule of rules) {
      if (this instanceof dom.window.Element && this.matches(rule.selector)) {
        return rule.parts ? rect(rule.parts) : original.call(this);
      }
    }
    return original.call(this);
  };
  return () => {
    dom.window.HTMLElement.prototype.getBoundingClientRect = original;
  };
}

async function nextFrame() {
  await act(async () => {
    await new Promise((resolve) => requestAnimationFrame(() => resolve(undefined)));
  });
}

// 单元探针宿主：主锚+可选备用锚+一个 bottom/start 弹层（refs 属组件内部）。
function UnitHarness({ showFallback = true }: { showFallback?: boolean }) {
  const primaryRef = useRef<HTMLButtonElement>(null);
  const fallbackRef = useRef<HTMLButtonElement>(null);
  return createElement(
    "div",
    null,
    createElement("button", { "data-testid": "primary", ref: primaryRef }, "primary"),
    showFallback ? createElement("button", { "data-testid": "fallback", ref: fallbackRef }, "fallback") : null,
    createElement(
      AnchoredPopover,
      {
        open: true,
        anchorRef: primaryRef,
        fallbackAnchorRef: fallbackRef,
        onClose: () => {},
        className: "test-popover",
        placement: "bottom",
        align: "start",
      },
      createElement("div", null, "Menu"),
    ),
  );
}

async function renderUnitHarness(dom: JSDOM, showFallback: boolean) {
  const rootEl = dom.window.document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(createElement(UnitHarness, { showFallback }));
  });
  await nextFrame();
  return root;
}

const POPOVER_RECT: RectParts = { left: 0, top: 0, right: 240, bottom: 120, width: 240, height: 120 };

console.log("\ntl1840 folded popover anchor");

// ── ①a 折叠态：主锚全零 rect，位置由备用锚推出 ──────────────────────────
{
  const dom = installDom();
  stubRects(
    dom,
    [
      // display:none 的主锚：全零 DOMRect（真实浏览器行为）。
      { selector: "[data-testid='primary']", parts: { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 } },
      // 折叠按钮（备用锚）可见：真实 rect。
      { selector: "[data-testid='fallback']", parts: { left: 300, top: 300, right: 340, bottom: 330, width: 40, height: 30 } },
    ],
    POPOVER_RECT,
  );
  const root = await renderUnitHarness(dom, true);
  const popover = dom.window.document.querySelector<HTMLElement>("[data-anchored-popover='active']");
  if (!popover) throw new Error("popover did not render");
  eq(popover.style.left, "300px", "folded: popover left derives from the fallback (fold button) rect");
  eq(popover.style.top, "338px", "folded: popover top = fallback.bottom + offset (bottom placement)");
  eq(popover.dataset.ready, "true", "folded: popover measured against the visible fallback anchor");
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ①b 未折叠：主锚可见 → 主锚胜，fallback 不劫持（行为钉） ─────────────
{
  const dom = installDom();
  stubRects(
    dom,
    [
      { selector: "[data-testid='primary']", parts: { left: 60, top: 100, right: 260, bottom: 130, width: 200, height: 30 } },
      { selector: "[data-testid='fallback']", parts: { left: 300, top: 300, right: 340, bottom: 330, width: 40, height: 30 } },
    ],
    POPOVER_RECT,
  );
  const root = await renderUnitHarness(dom, true);
  const popover = dom.window.document.querySelector<HTMLElement>("[data-anchored-popover='active']");
  if (!popover) throw new Error("popover did not render");
  eq(popover.style.left, "60px", "unfolded: popover left derives from the PRIMARY anchor (fallback must not hijack)");
  eq(popover.style.top, "138px", "unfolded: popover top = primary.bottom + offset (behaviour unchanged)");
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ①c 无备用锚 + 主锚全零 rect：修法前同款 (8,8)（既有弹层零变化） ─────
{
  const dom = installDom();
  stubRects(
    dom,
    [{ selector: "[data-testid='primary']", parts: { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 } }],
    POPOVER_RECT,
  );
  const root = await renderUnitHarness(dom, false);
  const popover = dom.window.document.querySelector<HTMLElement>("[data-anchored-popover='active']");
  if (!popover) throw new Error("popover did not render");
  eq(popover.style.left, "8px", "no fallback: zero-rect anchor keeps the legacy clamp to the viewport gap");
  eq(popover.style.top, "8px", "no fallback: legacy behaviour for popovers without fallbackAnchorRef is preserved");
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ② Composer 级 e2e：折叠态经「⋯」菜单打开的弹层锚定折叠按钮 ──────────
{
  const { Composer } = await import("../components/Composer");
  const { LocaleProvider } = await import("../lib/i18n");
  const { ToastProvider } = await import("../lib/toast");
  const { applyLabFlags } = await import("../lib/labFlags");
  type ComposerProps = Partial<Parameters<typeof Composer>[0]>;

  class TestResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  }

  const dom = installDom();
  globalThis.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.localStorage = dom.window.localStorage;
  globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  globalThis.InputEvent = dom.window.InputEvent;
  globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.File = dom.window.File;
  globalThis.FileReader = dom.window.FileReader;
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
  // matchMedia matches:true → prefers-reduced-motion，关闭动画瞬时完成（收尾干净）。
  Object.defineProperty(dom.window, "matchMedia", {
    configurable: true,
    value: (query: string) => ({
      matches: true,
      media: query,
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent: () => false,
    }),
  });

  const flushTimers = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 0));

  // 折叠形态 rect 桩：⋯ 折叠按钮可见（700,500,730,530），四钮全零（display:none）。
  stubRects(
    dom,
    [
      { selector: ".composer-tools-fold button", parts: { left: 700, top: 500, right: 730, bottom: 530, width: 30, height: 30 } },
      { selector: ".composer-emoji-picker button", parts: { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 } },
      { selector: ".composer-screenshot-picker button", parts: { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 } },
      { selector: ".composer-history-picker button", parts: { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 } },
      { selector: "button.composer__btn--rewrite", parts: { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 } },
    ],
    // 两个菜单本体同尺寸 200×100。
    { left: 0, top: 0, right: 200, bottom: 100, width: 200, height: 100 },
  );

  const w = window as unknown as { go?: { main?: { App?: unknown } } };
  w.go = {
    main: {
      App: {
        Commands: async () => [],
        Models: async () => [],
        ModelsForTab: async () => [],
        SlashArgs: async () => ({ items: [], from: 0 }),
        HistoryPageForTab: async () => ({
          messages: [],
          startTurn: 0,
          endTurn: 0,
          totalTurns: 0,
          hasOlder: false,
        }),
        HistoryPage: async () => ({ entries: [], hasMore: false, nextFrom: 0 }),
      },
    },
  };

  applyLabFlags({ composerToolsFold: true });

  const rootEl = dom.window.document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  const composerProps: ComposerProps = {
    running: false,
    collaborationMode: "normal",
    toolApprovalMode: "ask",
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
    historyPickerEnabled: true,
  };
  await act(async () => {
    root.render(
      createElement(LocaleProvider, { locale: "en" },
        createElement(ToastProvider, null, createElement(Composer, composerProps as never))),
    );
    await flushTimers();
  });

  const foldButton = dom.window.document.querySelector<HTMLButtonElement>(".composer-tools-fold button[aria-haspopup='menu']");
  ok(foldButton !== null, "composer: the fold entry renders under the default-fold flag");

  const clickControl = (node: HTMLElement | null) => {
    if (!node) return;
    node.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true, cancelable: true, detail: 1 }));
  };

  // 期望值全部由折叠按钮 rect 推出（菜单 200×100，align end + auto placement）：
  // left = fold.right - 200 = 530；top = fold.top - 100 - 8 = 392（上方有位）。
  const FOLD_DERIVED_LEFT = "530px";
  const FOLD_DERIVED_TOP = "392px";

  // 点「⋯」：tools 菜单锚=折叠按钮本身。
  await act(async () => { clickControl(foldButton); await flushTimers(); });
  await act(async () => { await flushTimers(); });
  const toolsMenu = dom.window.document.querySelector<HTMLElement>(".composer-tools-menu");
  ok(toolsMenu !== null, "composer: clicking the fold entry opens the tools menu");
  eq(toolsMenu?.style.left, FOLD_DERIVED_LEFT, "composer: the tools menu is anchored to the fold button (left)");
  eq(toolsMenu?.style.top, FOLD_DERIVED_TOP, "composer: the tools menu is anchored to the fold button (top)");

  // 点「历史提示词」菜单项 → 弹层须锚定折叠按钮而非 (8,8)（tl1840 主诉）。
  const historyItem = [...dom.window.document.querySelectorAll<HTMLElement>(".composer-tools-menu [role='menuitem']")]
    .find((el) => /history/i.test(el.textContent ?? ""));
  ok(historyItem !== undefined, "composer: the history entry is present in the tools menu");
  await act(async () => { clickControl(historyItem ?? null); await flushTimers(); });
  await act(async () => { await flushTimers(); });
  const historyMenu = dom.window.document.querySelector<HTMLElement>(".composer-history-menu");
  ok(historyMenu !== null, "composer: the history picker opens from the folded tools menu");
  eq(historyMenu?.style.left, FOLD_DERIVED_LEFT, "composer: the history picker is anchored to the FOLD BUTTON (left) — not the (8,8) bug");
  eq(historyMenu?.style.top, FOLD_DERIVED_TOP, "composer: the history picker is anchored to the FOLD BUTTON (top) — not the (8,8) bug");

  // 同机制：emoji 菜单项 → 弹层同样锚定折叠按钮。
  await act(async () => { clickControl(foldButton); await flushTimers(); });
  await act(async () => { await flushTimers(); });
  const emojiItem = [...dom.window.document.querySelectorAll<HTMLElement>(".composer-tools-menu [role='menuitem']")]
    .find((el) => /emoji/i.test(el.textContent ?? ""));
  ok(emojiItem !== undefined, "composer: the emoji entry is present in the tools menu");
  await act(async () => { clickControl(emojiItem ?? null); await flushTimers(); });
  await act(async () => { await flushTimers(); });
  const emojiPopover = dom.window.document.querySelector<HTMLElement>(".composer-emoji-popover");
  ok(emojiPopover !== null, "composer: the emoji picker opens from the folded tools menu");
  eq(emojiPopover?.style.left, FOLD_DERIVED_LEFT, "composer: the emoji picker is anchored to the FOLD BUTTON (left)");
  eq(emojiPopover?.style.top, FOLD_DERIVED_TOP, "composer: the emoji picker is anchored to the FOLD BUTTON (top)");

  // 收尾：清挂起弹层后卸载（reduced-motion 下关闭瞬时完成）。
  await act(async () => {
    dom.window.document.body.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flushTimers();
    await flushTimers();
  });
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ③ 源码钉：fallbackAnchorRef 挂对位置 ────────────────────────────────
{
  const here = new URL(".", import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, "$1");
  const composer = readFileSync(`${here}../components/Composer.tsx`, "utf8");
  const anchored = readFileSync(`${here}../components/AnchoredPopover.tsx`, "utf8");
  ok(anchored.includes("fallbackAnchorRef?:"), "source: AnchoredPopover declares the optional fallbackAnchorRef prop");
  // 折叠面内两个弹层（emoji/历史）挂备用锚=折叠按钮 ref（props 在 className 前，
  // 向前取块）。
  const emojiIdx = composer.indexOf("composer-emoji-popover composer-menu-surface");
  const emojiBlock = composer.slice(Math.max(0, emojiIdx - 400), emojiIdx + 100);
  ok(/fallbackAnchorRef=\{toolsMenuAnchorRef\}/.test(emojiBlock), "source: the emoji popover falls back to the fold button anchor");
  const historyIdx = composer.indexOf("composer-history-menu composer-menu-surface");
  const historyBlock = composer.slice(Math.max(0, historyIdx - 400), historyIdx + 100);
  ok(/fallbackAnchorRef=\{toolsMenuAnchorRef\}/.test(historyBlock), "source: the history popover falls back to the fold button anchor");
  // tools 菜单自身（锚恒可见）与 content/intent 菜单（不在折叠面）不挂——保持既有行为。
  const toolsBlock = composer.slice(composer.indexOf("composer-tools-menu composer-menu-surface"), composer.indexOf("composer-tools-menu composer-menu-surface") + 300);
  ok(!toolsBlock.includes("fallbackAnchorRef"), "source: the tools menu keeps its always-visible anchor (no fallback prop)");
  const contentBlock = composer.slice(composer.indexOf("composer-content-menu composer-intent-menu"), composer.indexOf("composer-content-menu composer-intent-menu") + 300);
  ok(!contentBlock.includes("fallbackAnchorRef"), "source: the content/intent menu (not in the fold face) takes no fallback prop");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
