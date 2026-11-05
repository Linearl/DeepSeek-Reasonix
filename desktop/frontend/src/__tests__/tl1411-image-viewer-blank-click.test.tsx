// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1411-image-viewer-blank-click.test.tsx
//
// tl1411（P2 desktop）：图片查看器交互增强——点1「点击空白处关闭」。
//
// 可证伪断言（与任务书验收一一对应）：
//  ① 空白点击关闭：查看器打开后，点击遮罩（backdrop 本体）关闭；
//  ② 死区补强：点击不可见的 .image-viewer__content 包裹层（图片与文件名之间
//     的 gap/边距带，真实浏览器里命中包裹层本体）同样关闭——这是用户
//     「点空白没反应」感知的机制根因；
//  ③ 图片本体点击不关闭（区分断言，验收原文）；
//  ④ 文件名说明条点击不关闭（同为可见内容）；
//  ⑤ Esc 关闭保留（回归）。
//
// DOM/桥接模式复用 tl1289-image-viewer-navigation：JSDOM + css-stub-register +
// LocaleProvider(preloadLocale("zh")) + MessageAttachments 直渲。

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

function viewerOpen(): boolean {
  return Boolean(document.querySelector(".image-viewer-backdrop"));
}

function backdropEl(): HTMLElement {
  return document.querySelector<HTMLElement>(".image-viewer-backdrop")!;
}

async function openViewer(thumb: HTMLElement): Promise<void> {
  await act(async () => {
    thumb.click();
    await flushTimers();
  });
  await waitFor(() => viewerOpen());
}

/** 在指定元素上派发冒泡 click，模拟真实浏览器里命中该元素的点击。 */
async function clickElement(el: Element): Promise<void> {
  await act(async () => {
    el.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await flushTimers();
  });
}

console.log("\ntl1411 点1：图片查看器点击空白处关闭");

installDom();
installBridgeApp({
  AttachmentDataURL: async () => "data:image/png;base64,AAAA",
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

await render(
  <MessageAttachments
    attachments={[{ path: ".reasonix/attachments/blank.png", name: "blank.png", kind: "image", source: "attachment", ext: "PNG" }]}
    tabId="tab-1411"
  />,
);
const thumb = host.querySelector<HTMLElement>(".msg-attachment--image")!;
ok(Boolean(thumb), "缩略卡渲染");

// ── ① backdrop 本体点击关闭 ──
await openViewer(thumb);
ok(viewerOpen(), "打开 ImageViewer");
await clickElement(backdropEl());
ok(!viewerOpen(), "点击遮罩空白区（backdrop 本体）关闭（验收①）");

// ── ② content 包裹层点击关闭（死区补强）──
await openViewer(thumb);
const content = document.querySelector<HTMLElement>(".image-viewer__content")!;
ok(Boolean(content), "content 包裹层存在");
await clickElement(content);
ok(!viewerOpen(), "点击 content 包裹层（gap/边距带）同样关闭（验收②）");

// ── ③ 图片本体点击不关闭 ──
await openViewer(thumb);
const img = document.querySelector<HTMLElement>(".image-viewer__image")!;
await clickElement(img);
ok(viewerOpen(), "点击图片本体不关闭（验收③）");

// ── ④ 文件名说明条点击不关闭 ──
const name = document.querySelector<HTMLElement>(".image-viewer__name")!;
ok(Boolean(name), "文件名说明条渲染");
await clickElement(name);
ok(viewerOpen(), "点击文件名说明条不关闭（验收④）");

// ── ⑤ Esc 关闭保留 ──
await act(async () => {
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  await flushTimers();
});
ok(!viewerOpen(), "Esc 关闭保留（验收⑤）");

// ── 补充：关闭按钮不因冒泡双重关闭报错 ──
await openViewer(thumb);
const closeBtn = document.querySelector<HTMLButtonElement>(".image-viewer__close")!;
await clickElement(closeBtn);
ok(!viewerOpen(), "X 关闭按钮路径不受冒泡改动影响");

process.stdout.write(`\ntl1411 blank-click: ${passed} passed, ${failed} failed\n`);
process.exit(failed > 0 ? 1 : 0);
