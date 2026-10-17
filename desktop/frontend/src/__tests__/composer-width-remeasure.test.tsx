// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/composer-width-remeasure.test.tsx
//
// 任务 939：侧边栏展开/收起只改 layout 元素的 --sidebar-expanded-width，不触发
// window resize 也不动 documentElement 属性——输入区宽度变了但 Composer 的自动
// 高度测量不重跑，inline 高度停留在旧值（偏低），resized 固定高度卡片里输入框
// 下方出现错排空白；拖动改高度「恢复」是因为 composerHeight 变化强制重测。
// 修复 = 对 composer 卡片宽度挂 ResizeObserver：
//   AC1 宽度变化 → 重测 textarea 高度（新 scrollHeight 生效）；
//   AC2 只有高度变化（自己写出来的测量结果）→ 不重测，防「测量→写高度→再测量」回环；
//   AC3 宽度回到原值后高度不被误改。
// jsdom 无真实布局：用可触发的 ResizeObserver 假件 + 按元素 mock
// getBoundingClientRect / scrollHeight 来断言「触发器接线」这一纯逻辑部分。

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

function fireCardResize(card: HTMLElement, width: number) {
  for (const instance of TestResizeObserver.instances) {
    if (instance.targets.has(card)) {
      instance.callback([{ target: card }], instance);
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
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

async function renderComposer() {
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

console.log("\ncomposer width re-measure (task 939)");

{
  const dom = installDom();
  const { root } = await renderComposer();

  const card = document.querySelector(".composer-card") as HTMLElement;
  const textarea = document.querySelector(".composer__input") as HTMLTextAreaElement;
  const measureTextarea = document.querySelector(".composer__input--measure") as HTMLTextAreaElement;
  ok(card !== null, "composer card renders");
  ok(TestResizeObserver.instances.some((i) => i.targets.has(card)), "card width observer is wired on mount");

  let measuredHeight = 108;
  Object.defineProperty(measureTextarea, "scrollHeight", {
    configurable: true,
    get: () => measuredHeight,
  });

  // jsdom 里 getBoundingClientRect 恒为 0；初始 lastCardWidth=0，先给卡片一个
  // 真实宽度基线，再通过「宽度变化」触发重测。注意默认手动高度 140px 会把
  // input 下限抬到 82px（140-58），断言值都取在该下限之上。
  mockCardWidth(card, 800);
  await act(async () => {
    fireCardResize(card, 800);
    await flushRaf();
  });
  eq(textarea.style.height, "108px", "AC1: width change re-measures and applies the current scrollHeight");

  // 模拟侧边栏展开：输入区变窄 → 文本换行增多 → scrollHeight 变大。
  measuredHeight = 150;
  mockCardWidth(card, 520);
  await act(async () => {
    fireCardResize(card, 520);
    await flushRaf();
  });
  eq(textarea.style.height, "150px", "AC1: narrower card after sidebar expand picks up the taller wrap");

  // AC2：宽度不变的 ResizeObserver 回调（高度变化触发的自家写入）不重测。
  measuredHeight = 22;
  await act(async () => {
    fireCardResize(card, 520);
    await flushRaf();
  });
  eq(textarea.style.height, "150px", "AC2: same-width callback does not re-measure (no feedback loop)");

  // AC3：宽度回到原值时按当前内容重测，不保留旧值（内容短则回落到手动下限）。
  measuredHeight = 108;
  mockCardWidth(card, 800);
  await act(async () => {
    fireCardResize(card, 800);
    await flushRaf();
  });
  eq(textarea.style.height, "108px", "AC3: width restored re-measures against the current content");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
