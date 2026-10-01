// Run: npx tsx src/__tests__/composer-ime-probe.test.tsx
//
// 任务 276 探针 2 运行时验收：IME 组合被打断的两条路径必须各落一条
// composer-focus 日志（desktop.log 走 reportFrontendLog 通道）——
//  2a) 组合进行中可编辑框失焦（focusout，relatedTarget 非自身）；
//  2b) 组合进行中组件树卸载（invocation token 换入 rich input 等）。
// 探针 1/3 在 Composer 内部，由 composer-focus-heal.test.tsx 的源码锚点覆盖。

import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { useComposerImeGuard } from "../lib/useComposerImeGuard";

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

interface CapturedLog {
  feature: string;
  level: string;
  message: string;
  detail: string;
}

const captured: CapturedLog[] = [];

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
  globalThis.FocusEvent = dom.window.FocusEvent;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = TestResizeObserver;
  // 截获 reportFrontendLog：window.go 挂最小 stub（本测试的组件树不调其他
  // bridge 方法），其余路径仍是内置 mock。
  (dom.window as unknown as { go: unknown }).go = {
    main: {
      App: {
        ReportFrontendLog: (feature: string, level: string, message: string, detail: string) => {
          captured.push({ feature, level, message, detail });
          return Promise.resolve();
        },
      },
    },
  };
  return dom;
}

function Harness(): null {
  const taRef = React.useRef<HTMLTextAreaElement | null>(null);
  const textRef = React.useRef("");
  const lastSelectionRef = React.useRef({ start: 0, end: 0 });
  useComposerImeGuard({
    taRef,
    text: "",
    invocationCount: 0,
    textRef,
    lastSelectionRef,
    setText: () => {},
    setPlainSelection: () => {},
  });
  return <textarea ref={taRef} />;
}

function compositionLogs(): string[] {
  return captured.filter((l) => l.feature === "composer-focus").map((l) => l.message);
}

console.log("\n任务 276 探针 2：IME 组合打断落日志");

{
  captured.length = 0;
  const dom = installDom();
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(<Harness />);
    await flushTimers();
  });
  const ta = document.querySelector("textarea");
  if (!ta) throw new Error("harness textarea missing");

  // 2a) 组合开始 → 焦点离开（relatedTarget 为空）→ 必须落「focus loss」探针。
  await act(async () => {
    ta.dispatchEvent(new dom.window.Event("compositionstart"));
    ta.dispatchEvent(new dom.window.FocusEvent("focusout", { relatedTarget: null }));
    await flushTimers();
  });
  let messages = compositionLogs();
  ok(messages.includes("ime composition interrupted by focus loss"), "probe 2a: focus loss mid-composition logged");

  // 组合正常结束不再落 2a：compositionend 后 focusout 不触发探针。
  await act(async () => {
    ta.dispatchEvent(new dom.window.Event("compositionend"));
    ta.dispatchEvent(new dom.window.FocusEvent("focusout", { relatedTarget: null }));
    await flushTimers();
  });
  messages = compositionLogs();
  ok(messages.filter((m) => m === "ime composition interrupted by focus loss").length === 1,
    "probe 2a: ended composition is not reported again");

  // 2b) 组合进行中卸载 → 清理路径必须落「unmount」探针。
  await act(async () => {
    ta.dispatchEvent(new dom.window.Event("compositionstart"));
    root.unmount();
    await flushTimers();
  });
  messages = compositionLogs();
  ok(messages.includes("ime composition interrupted by unmount"), "probe 2b: unmount mid-composition logged");

  dom.window.close();
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
