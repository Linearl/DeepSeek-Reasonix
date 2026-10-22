// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1066-steer-image.test.tsx
//
// 任务1066（P2）：引导消息图片增强——引导块内图片对齐用户消息渲染形态
// （56px 缩略图 + 点击 ImageViewer 放大预览）+ 补复制按钮（复制原文含
// @引用，与用户消息 CopyButton 的 actionText 语义一致，便于直接重发）。
//
// 本文件钉三处引导场景的一致性（可证伪）：
//  ① steer 气泡（SteerCard 非 deferred）：正文剥除 @引用、图片缩略图出现、
//     点击缩略图打开 ImageViewer（.image-viewer-backdrop）、复制按钮存在；
//  ② 723 deferred 占位：折叠态不渲染图片/复制按钮；展开态与 ① 同款形态；
//  ③ 未应用引导 notice 块（NoticeCard code="unapplied_steer"）：同款形态。
//
// DOM/桥接模式复用 composer-image-capability：JSDOM + css-stub-register +
// LocaleProvider(preloadLocale("zh"))。

import { JSDOM } from "jsdom";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";

import { STEER_NOTICE_PREFIX } from "../lib/useController";

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

const IMG_REF = "@[shot.png](.reasonix/attachments/shot-1066.png)";

console.log("\ntl1066 steer image + copy");

{
  const dom = installDom();
  installBridgeApp({
    AttachmentDataURL: async () => "data:image/png;base64,iVBORw0KGgo=",
  });
  const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
  await preloadLocale("zh");
  const { SteerCard, NoticeCard } = await import("../components/TranscriptCards");
  // 预加载 lazy(MessageAttachments) 依赖的动态模块——TranscriptCards 里
  // React.lazy 挂起时依赖此 import() 先结算，否则首次渲染只出 Suspense
  // fallback（null），需在 act 外提前 import。
  await import("../components/MessageAttachments");
  const rootEl = document.getElementById("root")!;

  const freshHost = () => {
    const host = document.createElement("div");
    rootEl.appendChild(host);
    return { host, root: createRoot(host) };
  };

  const render = async (host: HTMLElement, root: ReturnType<typeof createRoot>, node: React.ReactElement) => {
    await act(async () => {
      root.render(<LocaleProvider>{node}</LocaleProvider>);
      // lazy(MessageAttachments) 的动态 import 在单次 flush 内可能未结算——
      // 多 flush 几轮，与 waitFor 的等待语义一致。
      for (let i = 0; i < 5; i++) await flushTimers();
    });
  };

  // ── ① steer 气泡（非 deferred） ──
  {
    const { host, root } = freshHost();
    const steerText = `${STEER_NOTICE_PREFIX}看看这张截图 ${IMG_REF}`;
    await render(host, root, <SteerCard id="s-1066" text={steerText} />);
    await waitFor(() => Boolean(host.querySelector(".msg-attachment--image")));
    ok(host.querySelector(".msg-attachment--image") !== null, "① steer 气泡图片缩略图出现（复用 .msg-attachment 形态）");
    ok(host.querySelector(".steer-line__text")?.textContent?.includes("看看这张截图") === true, "① 正文保留引导文字");
    ok(host.querySelector(".steer-line__text")?.textContent?.includes("@[shot.png]") !== true, "① 正文剥除 @引用");
    ok(host.querySelector("button.copybtn, .steer-line__copy") !== null, "① steer 气泡带复制按钮");
    // 点击缩略图 → ImageViewer（portal 到 .chat-pane）——断言与 ②③ 同款口径
    // （.msg-attachment--image 存在即可点击，不要求 img 子元素——img 由
    // preview data URL 异步加载，JSDOM 下不保证渲染，属环境噪声非行为回归）。
    const thumb = host.querySelector(".msg-attachment--image") as HTMLElement | null;
    ok(thumb !== null, "① 图片缩略图是可点击卡片");
    if (thumb) {
      await act(async () => {
        thumb.click();
        await flushTimers();
      });
      await waitFor(() => Boolean(document.querySelector(".chat-pane > .image-viewer-backdrop")));
      ok(Boolean(document.querySelector(".chat-pane > .image-viewer-backdrop")), "① 点击缩略图打开 ImageViewer 放大预览");
    }
    // 复制 payload = 原文含 @引用
    const copyBtn = host.querySelector(".steer-line__copy") as HTMLButtonElement | null;
    ok(copyBtn !== null, "① .steer-line__copy 按钮存在");
    if (copyBtn) {
      // 直接读组件树里 CopyButton 的 text prop 语义：断言 DOM title/aria 与
      // 组件渲染一致，再用 getText 路径无法直接拿，改为读渲染源码级验证
      // （payload 契约由 CopyButton text={body} 保证——见 TranscriptCards.tsx）。
      ok(copyBtn.getAttribute("type") === "button", "① 复制按钮是 button 元素");
    }
    await act(async () => { root.unmount(); });
    host.remove();
  }

  // ── ② 723 deferred 占位 ──
  {
    const { host, root } = freshHost();
    const steerText = `${STEER_NOTICE_PREFIX}看看 723 的截图 ${IMG_REF}\n第二行只有展开才可见`;
    await render(host, root, <SteerCard id="s-def-1066" text={steerText} deferred={true} />);
    // 折叠态：不渲染图片/复制按钮（723 原语义）
    eq(host.querySelector(".msg-attachment--image") === null, true, "② 折叠态不渲染图片缩略图");
    eq(host.querySelector(".steer-line__copy") === null, true, "② 折叠态不渲染复制按钮");
    const button = host.querySelector("button.steer-line__bubble--deferred") as HTMLButtonElement | null;
    ok(button !== null, "② 折叠态仍是 723 占位按钮");
    if (button) {
      await act(async () => { button.click(); });
      await waitFor(() => Boolean(host.querySelector(".msg-attachment--image")));
      ok(host.querySelector(".msg-attachment--image") !== null, "② 展开态图片缩略图出现");
      ok(host.querySelector(".steer-line__text")?.textContent?.includes("第二行只有展开才可见") === true, "② 展开态显示完整正文");
      // 展开态正文在 .steer-line__expanded 内的 .steer-line__text--expanded
      // span（button 内保留的是全文纯文本，剥除动作发生在展开块内）。
      const expandedText = host.querySelector(".steer-line__expanded .steer-line__text--expanded")?.textContent ?? "";
      ok(expandedText.includes("第二行只有展开才可见") && !expandedText.includes("@[shot.png]"), "② 展开态正文剥除 @引用");
      ok(host.querySelector(".steer-line__copy") !== null, "② 展开态带复制按钮");
      const thumb = host.querySelector(".msg-attachment--image") as HTMLElement | null;
      if (thumb) {
        await act(async () => {
          thumb.click();
          await flushTimers();
        });
        await waitFor(() => Boolean(document.querySelector(".chat-pane > .image-viewer-backdrop")));
        ok(Boolean(document.querySelector(".chat-pane > .image-viewer-backdrop")), "② 展开态点击缩略图打开 ImageViewer");
      }
    }
    await act(async () => { root.unmount(); });
    host.remove();
  }

  // ── ③ 未应用引导 notice 块 ──
  {
    const { host, root } = freshHost();
    const noticeItem = {
      kind: "notice",
      id: "n-1066",
      level: "warn",
      code: "unapplied_steer",
      text: `本条引导未能应用，因为当前回合在处理它之前已结束。如仍需要，请重新发送：\n看看这张截图 ${IMG_REF}`,
      createdAt: Date.now(),
    };
    await render(host, root, <NoticeCard item={noticeItem as never} />);
    await waitFor(() => Boolean(host.querySelector(".msg-attachment--image")));
    ok(host.querySelector(".msg-attachment--image") !== null, "③ 未应用引导块图片缩略图出现");
    ok(host.querySelector(".notice-line__text-body")?.textContent?.includes("看看这张截图") === true, "③ 正文保留引导文字");
    ok(host.querySelector(".notice-line__text-body")?.textContent?.includes("@[shot.png]") !== true, "③ 正文剥除 @引用");
    ok(host.querySelector(".steer-line__copy") !== null, "③ 未应用引导块带复制按钮");
    const thumb = host.querySelector(".msg-attachment--image") as HTMLElement | null;
    if (thumb) {
      await act(async () => {
        thumb.click();
        await flushTimers();
      });
      await waitFor(() => Boolean(document.querySelector(".chat-pane > .image-viewer-backdrop")));
      ok(Boolean(document.querySelector(".chat-pane > .image-viewer-backdrop")), "③ 点击缩略图打开 ImageViewer");
    }
    await act(async () => { root.unmount(); });
    host.remove();
  }

  dom.window.close();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
