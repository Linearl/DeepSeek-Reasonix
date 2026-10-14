// Run: tsx src/__tests__/composer-content-menu-running.test.tsx
//
// Task 594: the composer "+" menu must answer mid-turn. While a turn is
// running the pure text inserts (quick commands, @/#/ triggers) stay usable,
// and the turn-gated rows (plan/goal, subagent policy) carry a reason
// tooltip — nothing in the menu reacts silently. When idle everything is
// available (no regression).
// Task 870: attaching is draft editing (local staging only), so the
// attachment item is available mid-turn too — the 594 gray-out is gone.

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

  // 4) 任务 870：running 中附件项可用（附件=编辑草稿，纯本地暂存），
  //    594 的置灰与 tooltip 移除。上一步选中片段后菜单自动关闭
  //    （choosing 契约），重开再断言。
  await act(async () => { plusButton()!.click(); await flushTimers(); });
  const attachment = itemWithTitle("添加文件或图片");
  ok(attachment && !attachment.disabled, "attachment item available mid-turn (870)");
  ok(!attachment?.title, "attachment item has no running gate tooltip");

  // 4.5) 任务 870 行为钉：running 中带附件发送走引导队列——附件以
  //      @.reasonix/attachments 引用随 submit 文本入队（与纯文本 steer 同路），
  //      发送后草稿附件清空；绑定 go 桥模拟内核返回。
  {
    const calls: Array<{ name: string; args: unknown[] }> = [];
    // tabId 挂载会牵起一堆与本案无关的内核读（模型目录、上下文用量等），
    // 用 Proxy 兜底成空数组返回，未覆盖的绑定不再以 unhandled rejection 炸测。
    const appStub: Record<string, unknown> = {
      SavePastedImage: async () => ".reasonix/attachments/shot-870.png",
      AttachmentDataURL: async () => "data:image/png;base64,iVBORw0KGgo=",
      ListTabs: async () => [{ id: "tab-870", turnId: "turn-870" }],
      EnqueueInboxSteerForTurn: async (tabId: string, turnId: string, display: string, submit: string) => {
        calls.push({ name: "EnqueueInboxSteerForTurn", args: [tabId, turnId, display, submit] });
        return { itemId: "item-870", disposition: "steer_accepted", position: 1, paused: false };
      },
      InboxSnapshot: async () => ({ revision: 0, paused: false, recovered: false, items: [], itemsCount: 0, bytes: 0, maxItems: 0, maxBytes: 0 }),
    };
    const appProxy = new Proxy(appStub, {
      get(target, prop) {
        if (prop in target) return target[prop];
        return async () => [];
      },
    });
    (dom.window as unknown as { go: unknown }).go = { main: { App: appProxy } };
    await paint({ tabId: "tab-870", turnId: "turn-870" });
    await act(async () => { plusButton()!.click(); await flushTimers(); });
    const attachItem = itemWithTitle("添加文件或图片");
    ok(Boolean(attachItem), "attachment item reachable for the send-path pin");
    const fileInput = document.querySelector<HTMLInputElement>(".composer-content-file-input");
    ok(Boolean(fileInput), "hidden attachment input present");
    if (attachItem && fileInput) {
      await act(async () => {
        attachItem.click();
        await flushTimers();
        const png = new dom.window.File([new Uint8Array([137, 80, 78, 71])], "shot.png", { type: "image/png" });
        Object.defineProperty(fileInput, "files", { value: [png], configurable: true });
        fileInput.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
        // 等待异步 attach（sha256 + SavePastedImage + AttachmentDataURL）落定。
        for (let i = 0; i < 10 && !document.querySelector(".composer-context__item"); i++) {
          await flushTimers();
        }
      });
      const chip = () => document.querySelector(".composer-context__item");
      ok(Boolean(chip()), "attachment chip lands in the draft mid-turn (870)");
      const setValue = Object.getOwnPropertyDescriptor(dom.window.HTMLTextAreaElement.prototype, "value")!.set!;
      await act(async () => {
        const input = document.querySelector<HTMLTextAreaElement>("#composer-input");
        // 附件单独构成可发送输入（composer-context 条目），文本为空也允许
        // 提交；本钉只断言 870 特有的附件路由。纯文本引导提交的 submit 文本
        // 保真由 turn-running-degrade.test.tsx 覆盖（args[3] 携带原文）。
        if (input) {
          setValue.call(input, "补充说明");
          input.setSelectionRange(4, 4);
          input.dispatchEvent(new dom.window.InputEvent("input", { bubbles: true, data: "补充说明", inputType: "insertText", isComposing: false }));
        }
        await flushTimers();
        document.querySelector<HTMLButtonElement>(".composer__btn--send")?.click();
        for (let i = 0; i < 10 && calls.length === 0; i++) {
          await flushTimers();
        }
        await flushTimers();
      });
      const steer = calls.find((c) => c.name === "EnqueueInboxSteerForTurn");
      ok(Boolean(steer), "running send with attachment routes to the exact-turn guidance enqueue");
      const submitArg = typeof steer?.args[3] === "string" ? steer.args[3] : "";
      ok(submitArg.includes("@.reasonix/attachments/shot-870.png"), "guidance submit text carries the attachment @ref");
      eq(steer?.args[1], "turn-870", "guidance enqueue is fenced to the running turn id");
      ok(!chip(), "attachment chip cleared after the guidance send");
    }
    await paint({ tabId: undefined, turnId: undefined });
    delete (dom.window as unknown as { go?: unknown }).go;
    // 断言 5/6 沿用「菜单处于打开态」的前置：这里重开它。
    await act(async () => { plusButton()!.click(); await flushTimers(); });
  }

  // 5) running 中：执行方式/子代理委派置灰且同带提示（禁止无反应）。
  const plan = itemWithTitle("计划");
  ok(plan && plan.disabled, "plan task mode disabled mid-turn");
  ok(plan?.title === "会话运行中，结束后可用", "plan task mode carries the running hint tooltip");
  const light = itemWithTitle("经济");
  ok(light && light.disabled, "subagent policy tier disabled mid-turn");
  ok(light?.title === "会话运行中，结束后可用", "subagent policy tier carries the running hint tooltip");

  // 6) 空闲回归：翻回 running=false，菜单若已关则重开，全部可用且提示消失。
  await paint({ running: false });
  if (document.querySelectorAll(".composer-access-menu").length === 0) {
    await act(async () => { plusButton()!.click(); await flushTimers(); });
  }
  const attachmentIdle = itemWithTitle("添加文件或图片");
  ok(attachmentIdle && !attachmentIdle.disabled, "attachment item available when idle");
  const planIdle = itemWithTitle("计划");
  ok(planIdle && !planIdle.disabled, "plan task mode available when idle");
  const lightIdle = itemWithTitle("经济");
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
