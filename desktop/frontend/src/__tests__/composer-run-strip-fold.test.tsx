// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/composer-run-strip-fold.test.tsx
//
// tl1508b 修①：状态行「⋯ N」折叠/展开的记忆 + 同排一行实测。包 87 复验两问题
// 之一（展开另起一行 + 选择不保持）。
//   AC1 记忆持久化：点击「⋯ N」→ localStorage 单键落 "expanded"；重置 store +
//       预置存储的新挂载（= 刷新/重开的惰性水合路径）aria-expanded 仍为 true
//       ——首版 useState(false) 的选择丢失问题即此钉；
//   AC2 折回与损坏回退：再点折回 → 存储值 "folded"；存储值损坏（任意非枚举串）
//       → 默认折叠；
//   AC3 存储不可用：getItem/setItem 抛错（加固 webview）→ 本次运行内存生效、
//       不崩溃、不落盘；
//   AC4 空间够保持一行（实测驱动预留）：detail 的 flex-basis 不再强制 100%；
//       strip 实测 offsetHeight ≤ 40（单行）→ 展开态只预留 30px（不再多让
//       26px 空档）；实测 > 40（折到第二行）→ 预留抬升 56px。jsdom 无布局：
//       按元素 mock offsetHeight + 可触发的 ResizeObserver 假件断言「实测接线」
//       （与 composer-meta-wrap 套件同法；scrollWidth≤clientWidth 的行为断言在
//       jsdom 恒真值 0，故布局行为由 composer-narrow-overflow 套件做 CSS 契约钉）；
//   AC5 状态行不在场不再强制折回（首版 auto-reset effect 已随记忆语义撤销）：
//       展开态下轮次结束→新轮次开始，展开选择保持。

import { JSDOM } from "jsdom";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { Composer } from "../components/Composer";
import { LocaleProvider } from "../lib/i18n";
import { ToastProvider } from "../lib/toast";
import {
  __resetComposerRunStripPreferenceForTests,
  getComposerRunStripExpanded,
} from "../lib/composerRunStripPreference";
import type { CollaborationMode, ToolApprovalMode } from "../lib/types";

const STORAGE_KEY = "reasonix-composer-run-strip-expanded";

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

// 共用重测路径把测量包在 rAF 里；jsdom 的 rAF 是 ~16ms 定时器，setTimeout(0)
// 等不到——与 meta-wrap 套件同法，直接泵两帧。
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
    running: true,
    turnStartAt: Date.now(),
    turnTokens: 150,
    currentToolLabel: "write_file",
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

function queryStrip() {
  return {
    strip: document.querySelector(".composer-run-strip") as HTMLElement | null,
    more: document.querySelector(".composer-run-strip__more") as HTMLButtonElement | null,
    detail: document.querySelector(".composer-run-strip__detail") as HTMLElement | null,
    card: document.querySelector(".composer-card") as HTMLElement | null,
  };
}

function reserved(card: HTMLElement | null): string {
  return card?.style.getPropertyValue("--composer-run-strip-reserved") ?? "";
}

function mockOffsetHeight(el: HTMLElement, value: number) {
  Object.defineProperty(el, "offsetHeight", { configurable: true, get: () => value });
}

console.log("\ncomposer run strip fold memory + inline measure (tl1508b 修①)");

// AC1：默认折叠 → 展开落盘 → 折回落盘（同块内两向写路径）。
{
  const dom = installDom();
  __resetComposerRunStripPreferenceForTests();
  const { root } = await renderComposer();

  const { strip, more, detail, card } = queryStrip();
  ok(strip !== null && more !== null && detail !== null, "running strip with readings renders");
  eq(more!.getAttribute("aria-expanded"), "false", "AC1: fresh session starts folded (default)");
  eq(localStorage.getItem(STORAGE_KEY), null, "AC1: no stored choice before the first toggle");
  eq(reserved(card), "30px", "AC1: folded strip reserves the single-row 30px");

  await act(async () => {
    more!.click();
    await flushTimers();
  });
  eq(more!.getAttribute("aria-expanded"), "true", "AC1: toggle expands");
  eq(localStorage.getItem(STORAGE_KEY), "expanded", "AC1: expansion persists to the localStorage key");
  eq(reserved(card), "56px", "AC4: jsdom constant height keeps the pessimistic wrapped reservation (56px)");

  await act(async () => {
    more!.click();
    await flushTimers();
  });
  eq(more!.getAttribute("aria-expanded"), "false", "AC2: second toggle folds again");
  eq(localStorage.getItem(STORAGE_KEY), "folded", "AC2: folding persists to the localStorage key");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

// AC1 续（刷新/重开模拟）：重置 store + 存储值 "expanded" → 新挂载即展开。
// 真实重开 = 进程重启后从 localStorage 惰性水合；套件内以 __reset + 预置存储
// 复刻同一条读路径（写路径已在上一块断言）。
{
  const dom = installDom();
  __resetComposerRunStripPreferenceForTests();
  localStorage.setItem(STORAGE_KEY, "expanded");
  const { root } = await renderComposer();
  const { more, detail } = queryStrip();
  eq(more!.getAttribute("aria-expanded"), "true", "AC1: a fresh mount hydrates the stored expanded choice (refresh/reopen keeps it)");
  eq(getComposerRunStripExpanded(), true, "AC1: the store reports expanded after hydration");
  eq(detail!.hidden, false, "AC1: detail layer visible on the hydrated expanded session");
  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

// AC5（状态行不在场不强制折回）：存储值 "expanded" → 先挂无状态行轮（轮次
// 结束），再转运行轮——展开选择保持，不被首版的 auto-reset 折回。
{
  const dom = installDom();
  __resetComposerRunStripPreferenceForTests();
  localStorage.setItem(STORAGE_KEY, "expanded");
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  const baseProps: ComposerProps = {
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
  };
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ToastProvider>
          <Composer {...baseProps} />
        </ToastProvider>
      </LocaleProvider>,
    );
    await flushTimers();
  });
  eq(document.querySelector(".composer-run-strip"), null, "AC5: idle turn renders no strip (absence phase)");
  eq(getComposerRunStripExpanded(), true, "AC5: strip absence does not flip the stored choice");
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ToastProvider>
          <Composer {...baseProps} running={true} turnStartAt={Date.now()} turnTokens={150} currentToolLabel="write_file" />
        </ToastProvider>
      </LocaleProvider>,
    );
    await flushTimers();
  });
  const { more } = queryStrip();
  eq(more!.getAttribute("aria-expanded"), "true", "AC5: the next running turn restores the user's expanded choice (no forced fold-back)");
  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

// AC2 续：存储值损坏 → 回默认（折叠）。
{
  const dom = installDom();
  __resetComposerRunStripPreferenceForTests();
  localStorage.setItem(STORAGE_KEY, "junk-value");
  const { root } = await renderComposer();
  const { more } = queryStrip();
  eq(more!.getAttribute("aria-expanded"), "false", "AC2: corrupt stored value falls back to the folded default");
  eq(getComposerRunStripExpanded(), false, "AC2: store reports the default for a corrupt value");
  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

// AC3：存储不可用（加固 webview）→ 仅内存生效，不崩溃、不落盘。
{
  const dom = installDom();
  __resetComposerRunStripPreferenceForTests();
  const throwingStorage = {
    getItem: () => { throw new Error("denied"); },
    setItem: () => { throw new Error("denied"); },
    removeItem: () => { throw new Error("denied"); },
    clear: () => { throw new Error("denied"); },
    key: () => null,
    length: 0,
  };
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: throwingStorage });
  const { root } = await renderComposer();
  let { more } = queryStrip();
  eq(more!.getAttribute("aria-expanded"), "false", "AC3: unavailable storage renders the folded default without crashing");
  await act(async () => {
    more!.click();
    await flushTimers();
  });
  ({ more } = queryStrip());
  eq(more!.getAttribute("aria-expanded"), "true", "AC3: in-memory choice still applies for the run when storage is denied");
  eq(getComposerRunStripExpanded(), true, "AC3: memory-only preference flips after the toggle");
  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

// AC4：实测同排/折行 → 预留 30/56 跟随（mock offsetHeight + RO 触发）。
{
  const dom = installDom();
  __resetComposerRunStripPreferenceForTests();
  const { root } = await renderComposer();
  const { strip, more, card } = queryStrip();
  ok(strip !== null, "measure block: strip renders");

  // 展开态（jsdom 常量高 → 悲观预留 56）。
  await act(async () => {
    more!.click();
    await flushTimers();
  });
  eq(reserved(card), "56px", "AC4: expanded strip starts at the pessimistic wrapped reservation");

  // 实测单行（宽态，同排一行）→ 预留回落 30。
  mockOffsetHeight(strip!, 30);
  await act(async () => {
    fireResize(strip!);
    await flushTimers();
  });
  eq(reserved(card), "30px", "AC4: measured one-line strip (offsetHeight 30) drops the reservation to 30px (no dead gap)");

  // 实测折行（detail 整块到第二行）→ 预留抬升 56。
  mockOffsetHeight(strip!, 56);
  await act(async () => {
    fireResize(strip!);
    await flushTimers();
  });
  eq(reserved(card), "56px", "AC4: measured wrapped strip (offsetHeight 56) raises the reservation to 56px");

  // 宽度突变路径（窗口 resize → 共用 rAF update）同样重测。
  mockOffsetHeight(strip!, 30);
  await act(async () => {
    window.dispatchEvent(new dom.window.Event("resize"));
    await flushRaf();
  });
  eq(reserved(card), "30px", "AC4: window resize re-measures and restores the one-line reservation");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

// AC4 续：CSS 契约——detail 不再强制独占第二行（flex-basis 非 100%）。
{
  const styles = readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), "../styles.css"), "utf8")
    .replace(/\/\*[\s\S]*?\*\//g, "");
  const rule = /([^{}]+)\{([^{}]*)\}/g;
  let detailDecls = "";
  let match: RegExpExecArray | null;
  while ((match = rule.exec(styles)) !== null) {
    if (match[1].split(",").map((p) => p.trim()).includes(".composer-run-strip__detail")) {
      detailDecls = match[2];
    }
  }
  ok(!/\bflex\s*:\s*1\s+1\s+100%/.test(detailDecls), "AC4: detail layer no longer forces a full-width second row (flex-basis 100% removed)");
  ok(/flex\s*:\s*0\s+1\s+auto/.test(detailDecls), "AC4: detail layer is an inline-capable flex item (0 1 auto)");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
