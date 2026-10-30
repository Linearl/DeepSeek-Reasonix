// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/composer-shell-mode-bang-bang.test.tsx
//
// tl1274: the composer's shell mode (composer--shell class + "$" caret) must
// mirror what the backend actually executes. A single leading "!" runs shell,
// so terminal styling is correct — including with leading whitespace (the
// backend gate trims too). A leading "!!" is the quick-command picker trigger
// (task 593); the backend routes it as a normal turn, so the composer must
// NOT flip into terminal mode for it.
//
// jsdom contract: same harness as composer-quick-command-bang.test.tsx — one
// composer instance, sequential scenarios, full-text reset before each assert.

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

function composerTextarea(): HTMLTextAreaElement {
  const ta = document.querySelector<HTMLTextAreaElement>("#composer-input");
  if (!ta) throw new Error("plain composer textarea did not render");
  return ta;
}

function composerShellMode(): boolean {
  const box = document.querySelector<HTMLElement>(".composer");
  if (!box) throw new Error("composer element did not render");
  return box.classList.contains("composer--shell");
}

function caretText(): string {
  const caret = document.querySelector<HTMLElement>(".composer__caret");
  if (!caret) throw new Error("composer caret did not render");
  return caret.textContent ?? "";
}

const snippets = [
  { title: "发布检查", text: "跑发布检查单" },
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

  console.log("\ntl1274 终端模式样式与后端 shell 门槛语义对齐");

  // 1) 单 !：终端态样式在（后端会真执行 shell）。
  await typeText(ta, "!ls");
  ok(composerShellMode(), "single ! shows shell mode");
  ok(caretText() === "$", "single ! swaps the caret to $");

  // 2) 前导空白 + 单 !：后端 trim 后仍执行 shell，样式保持一致。
  await typeText(ta, "  !ls");
  ok(composerShellMode(), "leading whitespace + single ! still shows shell mode");

  // 3) 行首 !!：快捷指令触发符，不是 shell——样式必须退回普通输入态。
  await typeText(ta, "!!ls");
  ok(!composerShellMode(), "leading !! does not show shell mode");
  ok(caretText() !== "$", "leading !! keeps the normal caret");

  // 4) 前导空白 + !!：后端 trim 后同样按普通 turn 送模型，样式一致不进终端态。
  await typeText(ta, "  !!ls");
  ok(!composerShellMode(), "leading whitespace + !! does not show shell mode");

  // 5) !! 行首同时弹快捷指令面板——终端态退出与选择面板共存互不干扰。
  await typeText(ta, "!!");
  ok(!composerShellMode(), "!! picker scenario stays out of shell mode");
  const pickerRows = [...document.querySelectorAll<HTMLElement>(".slashmenu [role=\"option\"]")];
  ok(pickerRows.some((row) => (row.textContent ?? "").includes("发布检查")),
    "quick-command picker still opens for !! while shell mode is off");

  // 6) 普通文本基线。
  await typeText(ta, "explain the bang bang operator");
  ok(!composerShellMode(), "plain text never shows shell mode");

  await act(async () => {
    root.unmount();
    await flushTimers();
  });
  dom.window.close();

  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}

main();
