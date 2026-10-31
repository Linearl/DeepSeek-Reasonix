// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1289-image-viewer-navigation.test.tsx
//
// 任务1289（P3 desktop）：多图消息查看大图——方向键按钮+键盘快捷键切换图片。
//
// 可证伪断言（与任务书验收一一对应）：
//  ① 切换：多图消息点开大图后，next/prev 按钮点击可切换，计数徽标（n / 总数）
//     与 img src 同步前进/后退；
//  ② 边界：第一张 prev 禁用且键盘 ← 为 no-op；最后一张 next 禁用且键盘 → 为
//     no-op（钳制不回绕）；
//  ③ 键盘：document 级 ←/→ 键盘事件驱动切换、Esc 关闭；关闭交互（X 按钮）
//     与切换零冲突；
//  ④ 单图：不渲染切换按钮与计数徽标（验收②），Esc 关闭不受影响。
//
// DOM/桥接模式复用 tl1066-steer-image：JSDOM + css-stub-register +
// LocaleProvider(preloadLocale("zh")) + 预 import lazy(MessageAttachments)。

import { JSDOM } from "jsdom";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";

import type { DisplayAttachment } from "../lib/attachmentDisplay";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label}${actual === expected ? "" : `: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`}`);
}

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div><div class=\"chat-pane\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "zh-CN" });
  globalThis.Node = dom.window.Node;
  globalThis.Element = dom.window.Element;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.CustomEvent = dom.window.CustomEvent;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.ResizeObserver = TestResizeObserver;
  Object.defineProperty(dom.window, "matchMedia", {
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

// stub bridge: MessageAttachments loadPreview falls back to AttachmentDataURL
function installBridgeApp(methods: Record<string, unknown>) {
  (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
    main: {
      App: {
        Commands: async () => [],
        Models: async () => [],
        ModelsForTab: async () => [],
        ...methods,
      },
    },
  };
}

async function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

async function waitFor(check: () => boolean, attempts = 20): Promise<void> {
  for (let i = 0; i < attempts; i++) {
    if (check()) return;
    await act(async () => {
      await flushTimers();
    });
  }
}

function imageAttachment(path: string, name: string): DisplayAttachment {
  return { path, name, kind: "image", source: "attachment", ext: "PNG" };
}

function viewerImage(): Element | null {
  return document.querySelector(".image-viewer-backdrop .image-viewer__image");
}

function counterText(): string {
  return (document.querySelector(".image-viewer-backdrop .image-viewer__counter")?.textContent ?? "").trim();
}

function prevButton(): HTMLButtonElement | null {
  return document.querySelector<HTMLButtonElement>(".image-viewer__nav--prev");
}

function nextButton(): HTMLButtonElement | null {
  return document.querySelector<HTMLButtonElement>(".image-viewer__nav--next");
}

async function clickInAct(button: HTMLButtonElement | null): Promise<void> {
  await act(async () => {
    button?.click();
    await flushTimers();
  });
}

function pressKey(key: string): Promise<void> {
  return act(async () => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true }));
    await flushTimers();
  });
}

console.log("\ntl1289 image viewer multi-image navigation");

const IMAGES = [
  imageAttachment(".reasonix/attachments/a.png", "a.png"),
  imageAttachment(".reasonix/attachments/b.png", "b.png"),
  imageAttachment(".reasonix/attachments/c.png", "c.png"),
];

{
  const dom = installDom();
  installBridgeApp({
    // 每张图一个可区分的 data URL（a/b/c）——切换断言直接读 img src。
    AttachmentDataURL: async (path: string) => `data:image/png;base64,${path.slice(-5, -4)}`,
  });
  const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
  await preloadLocale("zh");
  const { MessageAttachments } = await import("../components/MessageAttachments");
  const rootEl = document.getElementById("root")!;
  const host = document.createElement("div");
  rootEl.appendChild(host);
  const root = createRoot(host);

  const render = async (node: React.ReactElement) => {
    await act(async () => {
      root.render(<LocaleProvider>{node}</LocaleProvider>);
      for (let i = 0; i < 5; i++) await flushTimers();
    });
  };

  // ── 多图：点第一张开大图 ──
  await render(<MessageAttachments attachments={IMAGES} tabId="tab-1289" />);
  const thumbs = host.querySelectorAll<HTMLElement>(".msg-attachment--image");
  eq(thumbs.length, 3, "三条图片缩略卡渲染");
  await act(async () => {
    thumbs[0]!.click();
    await flushTimers();
  });
  await waitFor(() => Boolean(document.querySelector(".chat-pane > .image-viewer-backdrop")));
  ok(Boolean(document.querySelector(".chat-pane > .image-viewer-backdrop")), "点击缩略卡打开 ImageViewer");
  eq(counterText(), "1 / 3", "计数徽标显示 1 / 3");
  eq(viewerImage()?.getAttribute("src"), "data:image/png;base64,a", "首图 src 为 a");

  // ── ① 切换：next 按钮点击前进 ──
  eq(nextButton()?.disabled, false, "第二位置 next 可用");
  await clickInAct(nextButton());
  eq(counterText(), "2 / 3", "点 next 后计数 2 / 3");
  eq(viewerImage()?.getAttribute("src"), "data:image/png;base64,b", "next 后 src 切到 b");
  eq(prevButton()?.disabled, false, "中段 prev 可用");
  await clickInAct(prevButton());
  eq(counterText(), "1 / 3", "点 prev 后回到 1 / 3");

  // ── ③ 键盘：→ 前进、← 后退 ──
  await pressKey("ArrowRight");
  eq(counterText(), "2 / 3", "键盘 → 切到 2 / 3");
  eq(viewerImage()?.getAttribute("src"), "data:image/png;base64,b", "键盘 → 后 src 为 b");
  await pressKey("ArrowLeft");
  eq(counterText(), "1 / 3", "键盘 ← 回到 1 / 3");

  // ── ② 边界：第一张 prev 禁用 + ← no-op ──
  eq(prevButton()?.disabled, true, "第一张 prev 禁用");
  await pressKey("ArrowLeft");
  eq(counterText(), "1 / 3", "第一张按 ← 保持 1 / 3（钳制不回绕）");
  await clickInAct(prevButton());
  eq(counterText(), "1 / 3", "点禁用 prev 仍是 1 / 3");

  // ── ② 边界：最后一张 next 禁用 + → no-op ──
  await pressKey("ArrowRight");
  await pressKey("ArrowRight");
  eq(counterText(), "3 / 3", "连按两次 → 到 3 / 3");
  eq(nextButton()?.disabled, true, "最后一张 next 禁用");
  await pressKey("ArrowRight");
  eq(counterText(), "3 / 3", "最后一张按 → 保持 3 / 3（钳制不回绕）");

  // ── ③ 零冲突：Esc 与 X 关闭仍工作 ──
  await pressKey("Escape");
  await waitFor(() => !document.querySelector(".image-viewer-backdrop"));
  ok(!document.querySelector(".image-viewer-backdrop"), "Esc 关闭大图");

  await act(async () => {
    thumbs[1]!.click();
    await flushTimers();
  });
  await waitFor(() => Boolean(document.querySelector(".chat-pane > .image-viewer-backdrop")));
  eq(counterText(), "2 / 3", "从第二张点开时起始计数 2 / 3");
  const closeBtn = document.querySelector<HTMLButtonElement>(".image-viewer__close");
  await clickInAct(closeBtn);
  await waitFor(() => !document.querySelector(".image-viewer-backdrop"));
  ok(!document.querySelector(".image-viewer-backdrop"), "X 按钮关闭不受切换改动影响");

  // ── ④ 单图：无切换按钮/计数徽标 ──
  await act(async () => {
    root.render(<LocaleProvider><MessageAttachments attachments={[IMAGES[0]!]} tabId="tab-1289" /></LocaleProvider>);
    for (let i = 0; i < 5; i++) await flushTimers();
  });
  await waitFor(() => Boolean(host.querySelector(".msg-attachment--image")));
  const singleThumb = host.querySelector<HTMLElement>(".msg-attachment--image");
  await act(async () => {
    singleThumb!.click();
    await flushTimers();
  });
  await waitFor(() => Boolean(document.querySelector(".chat-pane > .image-viewer-backdrop")));
  ok(Boolean(document.querySelector(".chat-pane > .image-viewer-backdrop")), "单图消息也能打开大图");
  ok(document.querySelector(".image-viewer__nav") === null, "单图不渲染切换按钮（验收②）");
  ok(document.querySelector(".image-viewer__counter") === null, "单图不渲染计数徽标");
  await pressKey("ArrowRight");
  ok(Boolean(document.querySelector(".chat-pane > .image-viewer-backdrop")), "单图按 → 不误关也不报错");
  await pressKey("Escape");
  await waitFor(() => !document.querySelector(".image-viewer-backdrop"));
  ok(!document.querySelector(".image-viewer-backdrop"), "单图 Esc 关闭正常");

  await act(async () => { root.unmount(); });
  host.remove();
}

console.log(`\ntl1289: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
