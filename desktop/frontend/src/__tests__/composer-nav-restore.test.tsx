// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/composer-nav-restore.test.tsx
//
// 任务 913 焦点回归：输入框偶发失焦（打字中断/需重新点击）。
// 根因链（desktop.log 276 三探针全静默 = 失焦走的是探针盲区）：
//  ① 导航遮罩（runtimeTransitioning）把 composer 宿主 hidden+inert+disabled，
//     浏览器静默收走焦点，settle 后无人归还；
//  ② 276 禁用翻转自愈的放弃路径（耗尽/epoch 取消）零痕迹；
//  ③ App 级 decision 释放恢复是单发 focus()，静默失败无重试无留痕。
// 本套件钉修复后契约：
//  1) 遮罩期焦点被收走 → settle 后归还 + 落 navigation-release 日志；
//  2) 遮罩落下时焦点不在 composer → 不归还、零日志（不无谓抢焦点）；
//  3) settle 时用户已点走 → 让位，绝不抢；
//  4) 决策面接管 → 由 decision 释放路径负责，本路径不插手；
//  5) restoreComposerFocus 共享器：落位即中 / 缺失耗尽留痕 / 用户点走让位；
//  6) 源码锚点（App 接线、Composer 放弃留痕、探针文案）。

import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { useRuntimeTransitionFocusRestore } from "../app-runtime/useLocalUiLifecycles";
import { restoreComposerFocus } from "../lib/composerFocus";

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
  globalThis.CustomEvent = dom.window.CustomEvent;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  // React-DOM 的 IE 输入事件 polyfill 会走 attachEvent/detachEvent；补桩避免
  // 手动派发 focus 事件时刷 TypeError 噪音（composer-focus-heal 同款）。
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
  // 截获 reportFrontendLog：window.go 挂最小 stub，其余 bridge 路径不触达。
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

function navRestoreLogs(): string[] {
  return captured.filter((l) => l.feature === "composer-focus").map((l) => l.message);
}

function composerInput(): HTMLTextAreaElement {
  const ta = document.getElementById("composer-input");
  if (!(ta instanceof HTMLTextAreaElement)) throw new Error("composer input missing");
  return ta;
}

// jsdom 的 .focus()/.blur() 不保证派发 focusin/focusout；钩子的追踪依赖这两
// 条事件，测试里显式补发，语义与浏览器一致（focusin 在 activeElement 落位
// 后触发）。
function focusComposer(ta: HTMLTextAreaElement) {
  ta.focus();
  document.dispatchEvent(new FocusEvent("focusin", { bubbles: true }));
}

function blurComposer() {
  composerInput().blur();
  document.dispatchEvent(new FocusEvent("focusout", { bubbles: true }));
}

function focusOther(el: HTMLElement) {
  el.focus();
  document.dispatchEvent(new FocusEvent("focusin", { bubbles: true }));
}

function Harness(props: { transitioning: boolean; decisionActive: boolean }): React.ReactElement {
  useRuntimeTransitionFocusRestore({
    runtimeTransitioning: props.transitioning,
    decisionSurfaceActive: props.decisionActive,
  });
  return (
    <div>
      <textarea id="composer-input" />
      <button type="button" id="elsewhere">elsewhere</button>
    </div>
  );
}

async function renderHarness(initial: { transitioning: boolean; decisionActive: boolean }) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root: Root = createRoot(rootEl);
  let props = initial;
  const rerender = async (next: Partial<typeof initial>) => {
    props = { ...props, ...next };
    await act(async () => {
      root.render(<Harness {...props} />);
      await flushTimers();
    });
  };
  await rerender({});
  return { root, rerender };
}

function elsewhereButton(): HTMLElement {
  const el = document.getElementById("elsewhere");
  if (!(el instanceof HTMLElement)) throw new Error("elsewhere button missing");
  return el;
}

console.log("\n任务 913 导航遮罩焦点归还 + 共享恢复器");

// 1) 遮罩期焦点被收走 → settle 后归还 + 正向留痕。
{
  captured.length = 0;
  const dom = installDom();
  const { rerender } = await renderHarness({ transitioning: false, decisionActive: false });
  const ta = composerInput();
  focusComposer(ta);
  ok(document.activeElement === ta, "focus lands on the composer input");
  await rerender({ transitioning: true });
  // 浏览器在遮罩 commit 时把焦点落到 body（display:none/inert 静默收走）。
  blurComposer();
  await rerender({ transitioning: false });
  ok(document.activeElement === ta, "focus is restored to the composer after the navigation settles");
  ok(navRestoreLogs().includes("focus restore after navigation release"),
    "navigation-release restore leaves a positive composer-focus log");
  dom.window.close();
}

// 2) 遮罩落下时焦点不在 composer → 不归还、零日志。
{
  captured.length = 0;
  const dom = installDom();
  const { rerender } = await renderHarness({ transitioning: false, decisionActive: false });
  await rerender({ transitioning: true });
  await rerender({ transitioning: false });
  ok(document.activeElement !== composerInput(), "no focus restore when the composer did not hold focus");
  ok(navRestoreLogs().length === 0, "no navigation-release log when nothing was taken");
  dom.window.close();
}

// 3) settle 时用户已点走 → 让位，绝不抢。
{
  captured.length = 0;
  const dom = installDom();
  const { rerender } = await renderHarness({ transitioning: false, decisionActive: false });
  const ta = composerInput();
  focusComposer(ta);
  await rerender({ transitioning: true });
  blurComposer();
  focusOther(elsewhereButton());
  await rerender({ transitioning: false });
  ok(document.activeElement === elsewhereButton(), "yields to a user click that landed during the mask");
  ok(navRestoreLogs().length === 0, "no restore attempt logged when the user already took focus");
  dom.window.close();
}

// 4) 决策面接管 → 由 decision 释放路径负责，本路径不插手。
{
  captured.length = 0;
  const dom = installDom();
  const { rerender } = await renderHarness({ transitioning: false, decisionActive: false });
  const ta = composerInput();
  focusComposer(ta);
  await rerender({ transitioning: true });
  blurComposer();
  await rerender({ transitioning: false, decisionActive: true });
  ok(document.activeElement !== ta, "defers to the decision-release path when a decision took over");
  ok(navRestoreLogs().length === 0, "no navigation-release log while a decision is active");
  dom.window.close();
}

// 5a) 共享器：目标在位 → 即刻落位，无耗尽日志。
{
  captured.length = 0;
  const dom = installDom();
  const input = document.createElement("textarea");
  input.id = "composer-input";
  document.body.appendChild(input);
  restoreComposerFocus({ site: "unit" });
  ok(document.activeElement === input, "restoreComposerFocus lands when the target is focusable");
  await frames(4);
  ok(!navRestoreLogs().includes("focus restore exhausted retries"), "no exhaustion log on a clean landing");
  dom.window.close();
}

// 5b) 共享器：目标缺失 → 有界重试耗尽必须留痕（盲区③修复）。
{
  captured.length = 0;
  const dom = installDom();
  restoreComposerFocus({ site: "unit-missing" });
  await frames(5);
  const exhaust = captured.find((l) => l.feature === "composer-focus" && l.message === "focus restore exhausted retries");
  ok(Boolean(exhaust), "exhausted retries are logged instead of failing silently");
  ok(exhaust?.detail.includes("site=unit-missing"), "exhaustion log carries the site tag");
  dom.window.close();
}

// 5c) 共享器：重试间隙用户点走 → 让位且不留耗尽日志。
{
  captured.length = 0;
  const dom = installDom();
  const input = document.createElement("textarea");
  input.id = "composer-input";
  // 模拟目标该帧不可聚焦（hidden/inert 下 focus() 静默失败）。
  input.focus = () => {};
  document.body.appendChild(input);
  const button = document.createElement("button");
  button.type = "button";
  document.body.appendChild(button);
  restoreComposerFocus({ site: "unit-yield" });
  focusOther(button);
  await frames(5);
  ok(document.activeElement === button, "keeps the user's new focus target during retries");
  ok(!navRestoreLogs().includes("focus restore exhausted retries"), "no exhaustion log when yielding to the user");
  dom.window.close();
}

// 6) 源码锚点：App 接线、Composer 放弃留痕、探针文案全部落位。
{
  const here = fileURLToPath(new URL(".", import.meta.url));
  const read = (p: string) => readFileSync(here + "../" + p, "utf8").replace(/\r\n/g, "\n");
  const appSrc = read("App.tsx");
  const hookSrc = read("app-runtime/useLocalUiLifecycles.ts");
  const sharedSrc = read("lib/composerFocus.ts");
  const composerSrc = read("components/Composer.tsx");
  const has = (src: string, needle: string) => src.includes(needle);

  ok(has(appSrc, "useRuntimeTransitionFocusRestore({ runtimeTransitioning"), "App wires the navigation-release restore");
  ok(has(appSrc, "site: \"decision-release\""), "decision release routes through the shared restore");
  ok(has(hookSrc, "focus restore after navigation release"), "navigation-release positive probe");
  ok(has(sharedSrc, "focus restore exhausted retries"), "shared restore logs exhausted retries");
  ok(has(composerSrc, "disable-flip heal exhausted retries"), "heal exhaustion is logged (blind spot 2)");
  ok(has(composerSrc, "disable-flip heal cancelled by draft epoch"), "heal epoch-cancel is logged (blind spot 2)");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
