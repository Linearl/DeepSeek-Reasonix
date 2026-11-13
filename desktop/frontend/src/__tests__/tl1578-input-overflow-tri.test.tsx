// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1578-input-overflow-tri.test.tsx
//
// tl1578 输入区溢出三形态合并修复——验收钉：
//   形态① 侧边栏（自动 hover 展开/收起）弹出后输入区与界面重叠：
//     根因=自动路径与手动 toggleSidebar 的布局路径分歧——手动路径独有
//     anchorAppScrollToChat（.app overflow-x:auto 在 0.16s 列宽过渡期的
//     scrollLeft 漂移三次回钉），自动路径只有 set+save。用户定调「路径归一
//     化」：把同一步接进 hover expand/collapse（pulse/浮层清扫两步仍刻意
//     不复制，见 App.tsx 注释）。本套件以源码契约钉死两侧同函数。
//   形态② 内容多时输入区向下溢出、盖到输入栏下方状态栏：
//     状态栏零背景声明（透明），footer（flex:0 0 auto 不可收缩）溢出中栏
//     网格行时内容直接叠印状态栏文字。修法对照 tl1415 概要表头重叠修复
//     （f3425e3c5，.rc-maintable__head 背景从不透明化+--z-inline-sticky）：
//     .statusbar 不透明背景（var(--bg)=.layout 自身底色，视觉零变化）+
//     position/z 提层；上间距 margin→padding（几何逐字节不变，空带入盒）。
//   形态③ 手动减少输入框高度时溢出加重：
//     预留高度两把尺——输入测量（tl1508 修① 已 meta 感知：58+实测超额）与
//     卡片高度写手（漏接，仍裸 58）。meta 折两行（≈81px>名义 43px）时卡比
//     内容矮、卡底内容下溢；卡调得越矮差值占比越大=「减高加重」。修=
//     composerReservedHeight SSOT，两消费方同式同值。本套件以 jsdom 实测钉
//     卡高随 meta 增量生长（修前 104px 不生长=红）。

import { JSDOM } from "jsdom";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { Composer } from "../components/Composer";
import { composerReservedHeight, resolveComposerContentSizing } from "../lib/composerSizing";
import { LocaleProvider } from "../lib/i18n";
import { ToastProvider } from "../lib/toast";
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

function countMatches(haystack: string, needle: string): number {
  return haystack.split(needle).length - 1;
}

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

// 共用重测路径把测量包在 rAF 里；jsdom 的 rAF 是 ~16ms 定时器，setTimeout(0)
// 等不到——与 composer-run-strip-fold 套件同法，直接泵两帧。
function flushRaf(): Promise<void> {
  return new Promise((resolve) => {
    window.requestAnimationFrame(() => window.requestAnimationFrame(() => resolve()));
  });
}

type ROCallback = (entries: unknown[], observer: unknown) => void;

class TestResizeObserver {
  static instances: TestResizeObserver[] = [];
  callback: ROCallback;
  targets = new Set<Element>();
  constructor(callback: ROCallback) {
    this.callback = callback;
    TestResizeObserver.instances.push(this);
  }
  observe(target: Element) { this.targets.add(target); }
  unobserve(target: Element) { this.targets.delete(target); }
  disconnect() { this.targets.clear(); TestResizeObserver.instances = TestResizeObserver.instances.filter((i) => i !== this); }
}

function fireResize(target: Element) {
  for (const instance of TestResizeObserver.instances) {
    if (instance.targets.has(target)) {
      instance.callback([{ target }], instance);
    }
  }
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
  TestResizeObserver.instances = [];
  globalThis.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver;
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: true,
      media: "(prefers-reduced-motion: reduce)",
      onchange: null,
      addEventListener() {},
      removeListener() {},
      addListener() {},
      removeEventListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

type ComposerProps = Parameters<typeof Composer>[0];

async function renderComposer(props: Partial<ComposerProps> = {}) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  const currentProps: ComposerProps = {
    running: false,
    collaborationMode: "normal" as CollaborationMode,
    toolApprovalMode: "ask" as ToolApprovalMode,
    goal: "",
    cwd: "/repo",
    modelLabel: "DeepSeek-R1",
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
  return { root };
}

function mockOffsetHeight(el: HTMLElement, value: number) {
  Object.defineProperty(el, "offsetHeight", { configurable: true, get: () => value });
}

// ── 形态③：卡高写手与输入测量共用 meta 感知预留（SSOT）──────────────────
{
  const dom = installDom();
  // resized 模式入口：手定高度经 layoutPreferences.v1 落盘，挂载即水合。
  localStorage.setItem("reasonix.layoutPreferences.v1", JSON.stringify({ sizes: { composerHeight: 104 } }));
  const { root } = await renderComposer();

  const card = document.querySelector(".composer-card") as HTMLElement | null;
  const meta = document.querySelector(".composer-meta") as HTMLElement | null;
  const textarea = document.querySelector(".composer__input") as HTMLElement | null;
  ok(card !== null && meta !== null && textarea !== null, "form3: resized composer mounts with card/meta/textarea");

  // 修前基线形态：meta 未实测（offsetHeight 0）→ 预留 58 → 卡=104。
  eq(card!.style.getPropertyValue("--composer-height"), "104px", "form3: baseline single-row reservation keeps the saved 104px card");
  eq(card!.style.getPropertyValue("--composer-run-strip-reserved"), "0px", "form3: idle turn reserves no run strip (isolation)");

  // meta 折两行实测 ≈81px（名义 43px → 超额 38px）：与 run-strip 套件同法，
  // mock offsetHeight + 触发 meta 行 ResizeObserver 走共用重测路径。
  mockOffsetHeight(meta!, 81);
  await act(async () => {
    fireResize(meta!);
    await flushRaf();
    await flushTimers();
  });

  // 卡高写手（此前漏接的裸 58 → 本修后 58+38=96）：32(输入下限)+96=128，
  // 卡随 meta 增量生长——修前此值停在 104px（卡比内容矮=下溢面）。
  eq(card!.style.getPropertyValue("--composer-height"), "128px", "form3: card height grows by the measured meta extra (writer is reservation-aware)");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

// 形态③ 输入测量侧语义（纯函数钉）：jsdom scrollHeight 恒 0，textarea inline
// 高度走 measureTextareaAutoHeight 的早退路径无法在组件级观测；测量共用的
// resolveComposerContentSizing+composerReservedHeight 组合在此按真值钉死——
// meta 折行（预留 96）下输入下限收紧 46→32（tl1508 修① 语义）、卡高逻辑值
// 128 与组件级实测一致。
{
  const shared = resolveComposerContentSizing({
    contentHeight: 0,
    manualLogicalHeight: 104,
    maxLogicalHeight: 307,
    reservedHeight: composerReservedHeight(58, 38),
  });
  eq(shared.inputHeight, 32, "form3 (pure): meta-aware reservation shrinks the input floor 46→32 (tl1508 semantics)");
  eq(shared.logicalHeight, 128, "form3 (pure): logical card height = input floor + shared reservation (matches the mounted card)");
  const legacy = resolveComposerContentSizing({
    contentHeight: 0,
    manualLogicalHeight: 104,
    maxLogicalHeight: 307,
    reservedHeight: composerReservedHeight(58, 0),
  });
  eq(composerReservedHeight(58, 0), 58, "form3 (pure): zero extra degrades byte-identically to the bare base reservation");
  eq(legacy.logicalHeight, 104, "form3 (pure): zero extra keeps the saved 104px card (jsdom/unmeasured baseline)");
}

// ── 形态①：自动/手动共用同一条布局路径（源码契约）──────────────────────
{
  const here = dirname(fileURLToPath(import.meta.url));
  const app = readFileSync(resolve(here, "../App.tsx"), "utf8");

  const hoverStart = app.indexOf("const leftSidebarHover = useSidebarHoverBehavior({");
  const hoverEnd = app.indexOf("const toggleSidebar", hoverStart);
  ok(hoverStart >= 0 && hoverEnd > hoverStart, "form1: left hover block located in App.tsx");
  const hoverBlock = app.slice(hoverStart, hoverEnd);

  // 同路径证据：hover expand 与 collapse 各调一次 anchorAppScrollToChat，
  // 与手动 toggleSidebar 调的是同一函数（同一布局收口）。
  eq(countMatches(hoverBlock, "anchorAppScrollToChat()"), 2, "form1: hover expand AND collapse both anchor app scroll (same step as manual toggle)");
  const toggleStart = app.indexOf("const toggleSidebar = useCallback");
  const toggleEnd = app.indexOf("const sidebarWidthClamp", toggleStart);
  const toggleBlock = app.slice(toggleStart, toggleEnd);
  eq(countMatches(toggleBlock, "anchorAppScrollToChat()"), 1, "form1: manual toggleSidebar anchors app scroll (the path being shared)");
  ok(!/closeTransientOverlays/.test(hoverBlock), "form1: hover path deliberately does not dismiss transient overlays (passive semantics, documented)");
  ok(!/pulseSidebarToggle/.test(hoverBlock), "form1: hover path deliberately does not pulse the toggle button (cosmetic, documented)");

  // 锚点定义本体：三次回钉覆盖 0.16s 列宽过渡（立即/rAF/+300ms）。
  const anchorStart = app.indexOf("const anchorAppScrollToChat = useCallback");
  const anchorBlock = app.slice(anchorStart, anchorStart + 600);
  ok(anchorBlock.includes("el.scrollLeft = 0") && anchorBlock.includes("requestAnimationFrame(pin)") && anchorBlock.includes("setTimeout(pin, 300)"),
    "form1: anchor pins scrollLeft at 0/rAF/+300ms covering the grid transition");

  // 右栏 dock 本就同路径（归一化的既有样板）：hover 展开/收起走的就是手动
  // toggleWorkspacePanel 调用的 openWorkspacePanel/closeWorkspacePanel。
  ok(app.includes("expand: openWorkspacePanel") && app.includes("collapse: () => closeWorkspacePanelRef.current()"),
    "form1: right dock hover already routes through the manual open/close functions");
  const toggleWsStart = app.indexOf("const toggleWorkspacePanel = useCallback");
  const toggleWsBlock = app.slice(toggleWsStart, toggleWsStart + 400);
  ok(toggleWsBlock.includes("closeWorkspacePanel();") && toggleWsBlock.includes("openWorkspacePanel();"),
    "form1: manual workspace toggle calls the same open/close functions as hover");
}

// ── 形态②：状态栏不透明遮罩（对照表头先例，CSS 契约）──────────────────
{
  const here = dirname(fileURLToPath(import.meta.url));
  // Strip comments so declaration parsing never matches prose inside them.
  const styles = readFileSync(resolve(here, "../styles.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

  function matchingBlocks(selector: string): string[] {
    const blocks: string[] = [];
    const rule = /([^{}]+)\{([^{}]*)\}/g;
    let match: RegExpExecArray | null;
    while ((match = rule.exec(styles)) !== null) {
      const selectors = match[1].split(",").map((part) => part.trim());
      if (selectors.includes(selector)) blocks.push(match[2]);
    }
    return blocks;
  }

  function finalDeclaration(selector: string, property: string): string | undefined {
    let value: string | undefined;
    for (const block of matchingBlocks(selector)) {
      const declaration = new RegExp(`(?:^|;)\\s*${property}\\s*:\\s*([^;]+)`, "g");
      let match: RegExpExecArray | null;
      while ((match = declaration.exec(block)) !== null) {
        value = match[1].trim();
      }
    }
    return value;
  }

  eq(finalDeclaration(".statusbar", "background"), "var(--bg)", "form2: statusbar paints an opaque mask (layout's own background, visually identical)");
  eq(finalDeclaration(".statusbar", "position"), "relative", "form2: statusbar is a positioned mask");
  eq(finalDeclaration(".statusbar", "z-index"), "var(--z-inline-sticky)", "form2: statusbar elevated with the same token as the tl1415 sticky header precedent (below local-backdrop)");
  eq(finalDeclaration(".statusbar", "padding-top"), "6px", "form2: top gap lives inside the mask box (workbench refresh keeps its 6px as padding)");
  ok(matchingBlocks(".statusbar").every((block) => !/(^|;)\s*margin-top\s*:/.test(block)), "form2: no .statusbar rule stacks a margin-top on top of the padding band");
  eq(finalDeclaration(".statusbar", "margin"), "0 auto", "form2: horizontal auto-centering kept");

  // 表头先例本体健在（同 token 同策略的对照锚点）：.rc-maintable__head th
  // sticky + 不透明背景 + --z-inline-sticky（tl1415b 定稿形态）。
  const headBlock = matchingBlocks(".rc-maintable__head th").join("\n");
  ok(headBlock.includes("position: sticky") && headBlock.includes("background: var(--bg-elev)") && headBlock.includes("z-index: var(--z-inline-sticky)"),
    "form2: the header precedent rule (sticky+opaque+inline-sticky) is present as the referenced prior art");

  // theme-style 档自带不透明背景与定高 padding（本修基础块不回退它）。
  const themedBlock = matchingBlocks(":root[data-theme-style] .statusbar").join("\n");
  ok(themedBlock.includes("background: var(--panel)") && themedBlock.includes("padding: 0 16px"),
    "form2: theme-style variant keeps its own opaque panel background and fixed padding");
}

console.log(`\ntl1578 input overflow tri-fix: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
