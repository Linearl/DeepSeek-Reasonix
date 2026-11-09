// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/composer-meta-wrap.test.tsx
//
// tl1508 修①：按钮行（.composer-meta）折两行的实测判定 + 分割线不压钮的高度
// 约束联动。jsdom 无真实布局：用可触发的 ResizeObserver 假件 + 按元素 mock
// offsetHeight / scrollHeight，只断言「触发器接线 + 数值契约」这一纯逻辑部分。
//   AC1 meta 观察者接线：composerMetaRef 的元素挂了 ResizeObserver（宽度突变
//       ——右侧边栏自动展开——后立即重测重排的联动面）；
//   AC2 折行判定=实测 offsetHeight 形态（661px 教训：不设宽度阈值）：
//       81px（两行）→ .composer-meta--tworow 挂上；43px（单行）→ 摘除；
//   AC3 reserved 回退：jsdom offsetHeight 恒 0 → reservedHeight =
//       COMPOSER_AUTO_RESERVED_HEIGHT(58)，既有测量读数逐字节不变（与
//       composer-width-remeasure AC1 同读数 108px）；
//   AC4 折两行收紧输入区：meta 实测 81px → reserved 58+(81-43)=96 → 内容
//       300px 的输入高度被约束到 307-96=211px（回退路径应为 249px，读数可辨）。

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { Composer } from "../components/Composer";
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

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

// update() 把重测包在 rAF 里；jsdom 的 rAF 是 ~16ms 定时器，setTimeout(0)
// 等不到——直接泵两帧。
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

async function renderComposer(extraProps: Partial<Parameters<typeof Composer>[0]> = {}) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  const currentProps: Parameters<typeof Composer>[0] = {
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
    ...extraProps,
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

function mockCardWidth(card: HTMLElement, width: number) {
  Object.defineProperty(card, "getBoundingClientRect", {
    configurable: true,
    value: () => ({ width, height: 0, top: 0, left: 0, right: width, bottom: 0, x: 0, y: 0, toJSON: () => ({}) }),
  });
}

function mockOffsetHeight(el: HTMLElement, value: number) {
  Object.defineProperty(el, "offsetHeight", { configurable: true, get: () => value });
}

console.log("\ncomposer meta wrap (tl1508 修①)");

{
  const dom = installDom();
  const { root } = await renderComposer();

  const card = document.querySelector(".composer-card") as HTMLElement;
  const meta = document.querySelector(".composer-meta") as HTMLElement;
  const textarea = document.querySelector(".composer__input") as HTMLTextAreaElement;
  const measureTextarea = document.querySelector(".composer__input--measure") as HTMLTextAreaElement;
  ok(card !== null && meta !== null, "composer card and meta row render");
  ok(
    TestResizeObserver.instances.some((i) => i.targets.has(meta)),
    "AC1: meta row observer is wired on mount (width-mutation re-measure link)",
  );

  let measuredHeight = 108;
  Object.defineProperty(measureTextarea, "scrollHeight", {
    configurable: true,
    get: () => measuredHeight,
  });
  mockCardWidth(card, 800);
  await act(async () => {
    fireResize(card);
    await flushRaf();
  });
  eq(textarea.style.height, "108px", "AC3: jsdom fallback keeps the legacy measurement (reserved 58, height 108px)");
  ok(!meta.classList.contains("composer-meta--tworow"), "AC2: collapsed meta offsetHeight (0) is not two-row");

  mockOffsetHeight(meta, 81);
  await act(async () => {
    fireResize(meta);
    await flushRaf();
  });
  ok(meta.classList.contains("composer-meta--tworow"), "AC2: measured 81px (two rows) mounts composer-meta--tworow");

  // AC4：两行 meta 的预留高度差进入输入区约束——300px 内容被夹到 307-96=211px。
  // 触发走 meta 观察者（卡片观察者对同宽回调去抖，是任务939 的防回环语义，
  // meta 高度变化正是它不该吞的事件面）。
  measuredHeight = 300;
  await act(async () => {
    fireResize(meta);
    await flushRaf();
  });
  eq(textarea.style.height, "211px", "AC4: wrapped meta shrinks the reserved budget and clamps the input (211px)");
  eq(textarea.style.overflowY, "auto", "AC4: clamped input flips overflow-y to auto (divider no longer pressed)");

  mockOffsetHeight(meta, 43);
  await act(async () => {
    fireResize(meta);
    await flushRaf();
  });
  ok(!meta.classList.contains("composer-meta--tworow"), "AC2: measured 43px (single row) unmounts composer-meta--tworow");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
