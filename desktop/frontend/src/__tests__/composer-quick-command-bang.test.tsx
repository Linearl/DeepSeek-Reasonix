// Run: tsx src/__tests__/composer-quick-command-bang.test.tsx
//
// Task 593: "!!" at the very head of the composer input opens the
// quick-command picker (same dropdown interaction as "/"), picking a snippet
// consumes the "!!" token and inserts the snippet text. Line-head only — any
// "!!" elsewhere in the text never triggers — and the "/" / "@" / "#"
// triggers stay untouched.
//
// jsdom note: these suites install the DOM after react-dom has loaded, so
// React boots with canUseDOM=false and falls back to its keyup-driven
// change-detection polyfill (same contract as composer-ime.test.tsx). The
// polyfill only delivers reliably for the first mount of a document, so the
// scenarios below share one composer instance and run sequentially — each
// scenario restarts from a known text before asserting.

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

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

const menuRect = (height: number) => ({
  top: 0, left: 0, bottom: height, right: 360, width: 360, height, x: 0, y: 0, toJSON: () => {},
});

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
Object.defineProperty(dom.window.HTMLElement.prototype, "getBoundingClientRect", {
  configurable: true,
  value(this: HTMLElement) {
    if (this.classList.contains("slashmenu")) return menuRect(360);
    if (this.classList.contains("slashmenu__row")) return menuRect(34);
    return menuRect(0);
  },
});
Object.defineProperty(dom.window.HTMLElement.prototype, "offsetWidth", {
  configurable: true,
  get: () => 600,
});
Object.defineProperty(dom.window.HTMLElement.prototype, "offsetHeight", {
  configurable: true,
  get(this: HTMLElement) {
    if (this.classList.contains("slashmenu")) return 360;
    if (this.classList.contains("slashmenu__row")) return 34;
    return 0;
  },
});
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

// React's value tracker only reports a change (and fires onChange) when the
// DOM value moved outside its patched setter — same helper as the IME suite.
function setNativeValue(ta: HTMLTextAreaElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value")?.set;
  if (!setter) throw new Error("native textarea value setter unavailable");
  setter.call(ta, value);
}

async function typeText(ta: HTMLTextAreaElement, value: string) {
  await act(async () => {
    ta.focus();
    setNativeValue(ta, value);
    ta.setSelectionRange(value.length, value.length);
    ta.dispatchEvent(new window.InputEvent("input", { bubbles: true, data: value, inputType: "insertText", isComposing: false }));
    ta.dispatchEvent(new window.KeyboardEvent("keyup", { key: "!", bubbles: true }));
    await flushTimers();
  });
}

async function pressKey(ta: HTMLTextAreaElement, key: string) {
  await act(async () => {
    ta.dispatchEvent(new window.KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
    await flushTimers();
  });
}

function composerTextarea(): HTMLTextAreaElement {
  const ta = document.querySelector<HTMLTextAreaElement>("#composer-input");
  if (!ta) throw new Error("plain composer textarea did not render");
  return ta;
}

function quickCommandRows(): string[] {
  return [...document.querySelectorAll<HTMLElement>(".slashmenu [role=\"option\"]")]
    .map((node) => node.textContent ?? "");
}

function pickerCount(): number {
  return document.querySelectorAll(".slashmenu").length;
}

const snippets = [
  { title: "pre-release关键动作", text: "跑发布检查单" },
  { title: "转线", text: "切换到另一条线" },
  { title: "停用片段", text: "禁用的片段不应出现", enabled: false },
];

async function main() {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  const props: Parameters<typeof Composer>[0] = {
    running: false,
    collaborationMode: "normal" as CollaborationMode,
    toolApprovalMode: "ask" as ToolApprovalMode,
    goal: "",
    cwd: "/repo",
    modelLabel: "reasonix",
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
    quickCommands: snippets,
  };
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ToastProvider>
          <Composer {...props} />
        </ToastProvider>
      </LocaleProvider>,
    );
    await flushTimers();
  });

  const ta = composerTextarea();

  console.log("\n任务 593 输入框行首 !! 唤起快捷指令选择列表");

  // 1) 行首 !! 打开列表：禁用片段不出现，hint 在列。
  await typeText(ta, "!");
  eq(pickerCount(), 0, "single ! does not open the picker");
  await typeText(ta, "!!");
  const rows1 = quickCommandRows();
  eq(pickerCount(), 1, "line-head !! opens the picker");
  ok(rows1.some((row) => row.includes("pre-release关键动作")), "enabled snippet 1 listed");
  ok(rows1.some((row) => row.includes("转线")), "enabled snippet 2 listed");
  ok(!rows1.some((row) => row.includes("停用片段")), "disabled snippet never listed");
  eq(rows1.length, 2, "only enabled snippets listed");

  // 2) 键盘选择：方向键移动高亮，回车选中替换 !! 并插入片段文本。
  eq(document.querySelector<HTMLElement>(".slashmenu [aria-selected=\"true\"]")?.textContent,
    "pre-release关键动作跑发布检查单", "first snippet highlighted initially");
  await pressKey(ta, "ArrowDown");
  eq(document.querySelector<HTMLElement>(".slashmenu [aria-selected=\"true\"]")?.textContent,
    "转线切换到另一条线", "ArrowDown moves the highlight to the second snippet");
  await pressKey(ta, "Enter");
  eq(ta.value, "切换到另一条线", "picking consumes the !! token and inserts the snippet text");
  eq(pickerCount(), 0, "picker closes after the pick");

  // 3) 过滤：!! 后继续输入按标题/正文过滤；Esc 关闭。
  await typeText(ta, "!!转");
  const rows3 = quickCommandRows();
  ok(rows3.length === 1 && rows3[0].includes("转线"), "typing after !! filters the list");
  await pressKey(ta, "Escape");
  eq(pickerCount(), 0, "Escape dismisses the picker");

  // 4) 行头硬约束：正文中任何位置的 !! 不触发；光标离开行首 token 面板关闭。
  await typeText(ta, "前面有正文!!");
  eq(pickerCount(), 0, "!! mid-text never triggers");
  await typeText(ta, "!!");
  eq(pickerCount(), 1, "line-head !! triggers again after plain text");
  await act(async () => {
    setNativeValue(ta, "!!\nsecond line");
    ta.setSelectionRange(ta.value.length, ta.value.length);
    ta.dispatchEvent(new window.InputEvent("input", { bubbles: true, inputType: "insertText", isComposing: false }));
    ta.dispatchEvent(new window.KeyboardEvent("keyup", { key: "!", bubbles: true }));
    await flushTimers();
  });
  eq(pickerCount(), 0, "caret outside the line-head token closes the picker");

  // 5) 既有触发符互斥面：@ 文本接管面板（@ 流自己的文件列表），片段行消失。
  await typeText(ta, "!!");
  eq(pickerCount(), 1, "sanity: !! picker open");
  await typeText(ta, "@");
  ok(!quickCommandRows().some((row) => row.includes("pre-release关键动作")),
    "replacing text with @ hands the panel to the @ flow (no snippet rows)");

  await act(async () => {
    root.unmount();
    await flushTimers();
  });
  dom.window.close();

  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}

main();
