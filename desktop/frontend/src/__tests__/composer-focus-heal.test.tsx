// Run: npx tsx src/__tests__/composer-focus-heal.test.tsx
//
// 任务 276 修复 b（composer 焦点自愈）行为验收：
//  1) 用户主动失焦（blur 事件在案）后，禁用翻转不归还焦点——自愈绝不和
//     用户抢焦点；
//  2) 禁用→恢复可用循环后焦点保持在输入框（jsdom 不模拟「禁用静默夺焦」，
//     浏览器真实路径由探针 composer-focus 在 desktop.log 验证）；
//  3) 三探针与修复点在源码落位（412 同款源码锚点检查，desktop.log 走
//     reportFrontendLog 的 4MB 滚动通道）。

import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
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

async function frames(n: number): Promise<void> {
  for (let i = 0; i < n; i += 1) {
    await new Promise((resolve) => requestAnimationFrame(() => resolve(undefined)));
    await flushTimers();
  }
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

type ComposerProps = Parameters<typeof Composer>[0];

async function renderComposer(initial?: Partial<ComposerProps>) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  let currentProps: ComposerProps = {
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
    ...initial,
  };
  const rerender = async (next?: Partial<ComposerProps>) => {
    currentProps = { ...currentProps, ...next };
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
  await rerender();
  return { root, rerender };
}

function composerInput(): HTMLTextAreaElement {
  const ta = document.getElementById("composer-input");
  if (!(ta instanceof HTMLTextAreaElement)) throw new Error("composer textarea missing");
  return ta;
}

console.log("\n任务 276 composer 焦点自愈（修复 b）");

// 1) 用户主动 blur 后，禁用循环不抢焦点。
{
  const dom = installDom();
  const { rerender } = await renderComposer();
  const ta = composerInput();
  ta.focus();
  ok(document.activeElement === ta, "focus lands on the composer textarea");
  ta.blur(); // 用户离开：blur 事件在案，wasFocused=false
  await rerender({ disabled: true });
  await rerender({ disabled: false });
  await frames(4);
  ok(document.activeElement !== ta, "no focus steal after a user-initiated blur + disable cycle");
  dom.window.close();
}

// 2) 禁用→恢复可用循环：焦点保持在输入框（jsdom 不产生禁用静默夺焦）。
{
  const dom = installDom();
  const { rerender } = await renderComposer();
  const ta = composerInput();
  ta.focus();
  await rerender({ disabled: true });
  ok(composerInput().disabled, "disable cycle takes effect on the textarea");
  await rerender({ disabled: false });
  await frames(4);
  ok(document.activeElement === composerInput(), "focus stays on the composer across a disable cycle");
  dom.window.close();
}

// 3) 源码锚点：三探针 + 兜底重试 + 禁用自愈全部落位（grep composer-focus 即可对链）。
{
  const here = fileURLToPath(new URL(".", import.meta.url));
  const read = (p: string) => readFileSync(here + "../" + p, "utf8").replace(/\r\n/g, "\n");
  const composerSrc = read("components/Composer.tsx");
  const guardSrc = read("lib/useComposerImeGuard.ts");
  const has = (src: string, needle: string) => src.includes(needle);

  ok(has(composerSrc, "\"composer-focus\""), "probe channel feature=composer-focus (desktop.log)");
  ok(has(composerSrc, "draft epoch bumped"), "probe 1: epoch bump logged with from -> to keys");
  ok(has(composerSrc, "focus restore cancelled by draft epoch"), "probe 3: restore-cancelled logged");
  ok(has(guardSrc, "ime composition interrupted by focus loss"), "probe 2a: composition broken by focus loss");
  ok(has(guardSrc, "ime composition interrupted by unmount"), "probe 2b: composition broken by unmount");
  ok(has(composerSrc, "focus restore exhausted retries"), "fix b: bounded retry exhaust probe");
  ok(has(composerSrc, "focus restored after disable flip"), "fix b: disable-flip heal probe");
  ok(has(composerSrc, "reportFrontendLog"), "probes route through reportFrontendLog (4MB rolling channel)");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
