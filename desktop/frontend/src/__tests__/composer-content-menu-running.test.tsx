// Run: tsx src/__tests__/composer-content-menu-running.test.tsx
//
// Task 594: the composer "+" menu must answer mid-turn. While a turn is
// running the pure text inserts (quick commands, @/#/ triggers) stay usable,
// the attachment item grays out with a three-language reason tooltip, and
// the turn-gated rows (plan/goal, subagent policy) carry the same hint —
// nothing in the menu reacts silently. When idle everything is available
// (no regression).

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
// Pin the zh dictionary so the three-language tooltip text is assertable
// (the key itself is compile-checked across zh/en/zh-TW); same trick as
// approval-modal-file-reference.test.tsx.
Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "zh-CN" });
Object.defineProperty(dom.window.navigator, "languages", { configurable: true, value: ["zh-CN"] });
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

const snippets = [{ title: "片段A", text: "片段A正文" }];

async function main() {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  const base: Parameters<typeof Composer>[0] = {
    running: true,
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
    onSetSubagentPolicy: () => {},
    subagentPolicy: "balanced",
    ready: true,
    quickCommands: snippets,
  };
  const paint = async (next: Partial<Parameters<typeof Composer>[0]> = {}) => {
    await act(async () => {
      root.render(
        <LocaleProvider>
          <ToastProvider>
            <Composer {...base} {...next} />
          </ToastProvider>
        </LocaleProvider>,
      );
      await flushTimers();
    });
  };
  await paint();

  console.log("\n任务 594 + 菜单 turn 进行中可用性拆分");

  const plusButton = () => document.querySelector<HTMLButtonElement>(".composer-content-trigger");
  const menuItems = () => [...document.querySelectorAll<HTMLButtonElement>(".composer-access-menu .composer-access-menu__item")];
  const itemWithTitle = (label: string) => menuItems().find((node) => (node.textContent ?? "").includes(label));

  // 1) running 中：+ 按钮可用，点击打开菜单。
  ok(plusButton() && !plusButton()!.disabled, "+ button stays enabled while running");
  await act(async () => { plusButton()!.click(); await flushTimers(); });
  ok(document.querySelectorAll(".composer-access-menu").length === 1, "popover opens mid-turn");
  process.stdout.write("DEBUG items=" + menuItems().length + " labels=" + menuItems().map((n) => n.textContent).join("|").slice(0, 300) + "\n");

  // 2) running 中：纯文本插入项全部可用。
  const referenceFiles = itemWithTitle("引用文件或文件夹");
  const referenceSessions = itemWithTitle("引用历史会话");
  const quickCommands = itemWithTitle("快捷指令");
  ok(referenceFiles && !referenceFiles.disabled, "reference-files insert available mid-turn");
  ok(referenceSessions && !referenceSessions.disabled, "reference-sessions insert available mid-turn");
  ok(quickCommands && !quickCommands.disabled, "quick-commands entry available mid-turn");

  // 3) running 中：快捷指令二级页可用并触发插入回调（choosing 行为不受伤；
  //    实际落输入框走 App 层 insertRequest，不在本测 scope）。
  const picked: string[] = [];
  await paint({ onInsertQuickCommand: (text: string) => picked.push(text) });
  await act(async () => { quickCommands!.click(); await flushTimers(); });
  const snippetRow = [...document.querySelectorAll<HTMLButtonElement>(".composer-access-menu [role=\"menuitem\"]")]
    .find((node) => (node.textContent ?? "").includes("片段A"));
  ok(Boolean(snippetRow), "quick-command picker page lists snippets mid-turn");
  if (snippetRow) {
    await act(async () => { snippetRow.click(); await flushTimers(); });
    eq(picked.join(","), "片段A正文", "picking a snippet fires the insert mid-turn");
  }

  // 4) running 中：附件置灰并给出三语 tooltip（zh 词典文案）。
  //    上一步选中片段后菜单自动关闭（choosing 契约），重开再断言。
  await act(async () => { plusButton()!.click(); await flushTimers(); });
  const attachment = itemWithTitle("添加文件或图片");
  ok(attachment && attachment.disabled, "attachment item disabled mid-turn");
  ok(attachment?.title === "会话运行中，结束后可用", "attachment item carries the running hint tooltip");

  // 5) running 中：执行方式/子代理委派置灰且同带提示（禁止无反应）。
  const plan = itemWithTitle("计划");
  ok(plan && plan.disabled, "plan task mode disabled mid-turn");
  ok(plan?.title === "会话运行中，结束后可用", "plan task mode carries the running hint tooltip");
  const light = itemWithTitle("轻量");
  ok(light && light.disabled, "subagent policy tier disabled mid-turn");
  ok(light?.title === "会话运行中，结束后可用", "subagent policy tier carries the running hint tooltip");

  // 6) 空闲回归：翻回 running=false，菜单若已关则重开，全部可用且提示消失。
  await paint({ running: false });
  if (document.querySelectorAll(".composer-access-menu").length === 0) {
    await act(async () => { plusButton()!.click(); await flushTimers(); });
  }
  const attachmentIdle = itemWithTitle("添加文件或图片");
  ok(attachmentIdle && !attachmentIdle.disabled, "attachment item available when idle");
  ok(!attachmentIdle?.title, "running hint tooltip gone when idle");
  const planIdle = itemWithTitle("计划");
  ok(planIdle && !planIdle.disabled, "plan task mode available when idle");
  const lightIdle = itemWithTitle("轻量");
  ok(lightIdle && !lightIdle.disabled, "subagent policy tier available when idle");
  ok(plusButton() && !plusButton()!.disabled, "+ button available when idle");

  await act(async () => {
    root.unmount();
    await flushTimers();
  });
  dom.window.close();

  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}

main();
