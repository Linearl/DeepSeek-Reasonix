// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1411-image-viewer-save-local.test.tsx
//
// tl1411（P2 desktop）：图片查看器交互增强——点2「保存到本地」按钮。
//
// 可证伪断言（与任务书验收一一对应）：
//  ① 按钮触发：点击「保存到本地」按钮 → 保存对话框桥（PickExportFile）以
//     原格式默认文件名+mime 被调用，写入桥（SaveExportFile）收到原始
//     base64 载荷（mock 下载通道，验收原文「保存按钮触发断言」）；
//  ② 原格式：data URL mime=image/png → 默认文件名 *.png；jpeg → *.jpg，
//     载荷为原字节不转码；
//  ③ 取消：对话框返回空路径 → 不落写入桥、无成功 toast；
//  ④ 失败：写入桥 reject → 错误 toast、按钮恢复可用；
//  ⑤ 忙守卫：对话框悬而未决时按钮 disabled、二次点击不再触发对话框；
//  ⑥ 多图：切换到第 N 张后保存的是当前图（载荷与文件名跟随）；
//  ⑦ 非 data URL（不可存源）→ 错误 toast、不触碰对话框桥。
//
// DOM/桥接模式复用 tl1289/tl1411-blank-click harness。

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

interface SaveCalls {
  pick: { filename: string; mime: string }[];
  write: { path: string; payload: string; base64: boolean }[];
}

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

function viewerOpen(): boolean {
  return Boolean(document.querySelector(".image-viewer-backdrop"));
}

function saveButton(): HTMLButtonElement | null {
  return document.querySelector<HTMLButtonElement>(".image-viewer__save");
}

function toastTexts(level: string): string[] {
  return [...document.querySelectorAll<HTMLElement>(`.toast--${level}`)].map((el) => (el.textContent ?? "").trim());
}

interface Harness {
  host: HTMLElement;
  cleanup: () => void;
  saveCalls: SaveCalls;
  render: (attachments: DisplayAttachment[]) => Promise<void>;
  openViewer: (index?: number) => Promise<void>;
}

async function setupHarness(saveOverrides: {
  pick?: (filename: string, mime: string) => Promise<string>;
  write?: (path: string, payload: string, base64: boolean) => Promise<void>;
  urls?: Record<string, string>;
}): Promise<Harness> {
  const host = document.createElement("div");
  document.getElementById("root")!.appendChild(host);
  const root = createRoot(host);
  const saveCalls: SaveCalls = { pick: [], write: [] };

  installBridgeApp({
    AttachmentDataURL: async (path: string) => saveOverrides.urls?.[path] ?? "data:image/png;base64,AAAA",
    PickExportFile: async (filename: string, mime: string) => {
      saveCalls.pick.push({ filename, mime });
      return saveOverrides.pick ? saveOverrides.pick(filename, mime) : `C:\\Users\\test\\Downloads\\${filename}`;
    },
    SaveExportFile: async (path: string, payload: string, base64: boolean) => {
      saveCalls.write.push({ path, payload, base64 });
      if (saveOverrides.write) await saveOverrides.write(path, payload, base64);
    },
  });

  const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
  const { ToastProvider } = await import("../lib/toast");
  await preloadLocale("zh");
  const { MessageAttachments } = await import("../components/MessageAttachments");

  const render = async (attachments: DisplayAttachment[]) => {
    await act(async () => {
      root.render(
        <ToastProvider>
          <LocaleProvider>
            <MessageAttachments attachments={attachments} tabId="tab-1411-save" />
          </LocaleProvider>
        </ToastProvider>,
      );
      for (let i = 0; i < 5; i++) await flushTimers();
    });
  };

  const openViewer = async (index = 0) => {
    const thumbs = host.querySelectorAll<HTMLElement>(".msg-attachment--image");
    await act(async () => {
      thumbs[index]!.click();
      await flushTimers();
    });
    await waitFor(() => viewerOpen());
  };

  return {
    host,
    saveCalls,
    render,
    openViewer,
    cleanup: () => {
      act(() => {
        root.unmount();
      });
      host.remove();
    },
  };
}

console.log("\ntl1411 点2：图片查看器保存到本地");

// ── ①② 按钮触发 + 原格式 ──
{
  const dom = installDom();
  const h = await setupHarness({
    urls: { ".reasonix/attachments/photo.png": "data:image/png;base64,AAAA" },
  });
  await h.render([imageAttachment(".reasonix/attachments/photo.png", "photo.png")]);
  await h.openViewer();

  const btn = saveButton();
  ok(Boolean(btn), "保存按钮渲染");
  eq(btn?.getAttribute("aria-label"), "保存到本地", "按钮 aria-label 三语（zh）");

  await act(async () => {
    btn?.click();
    await flushTimers();
  });
  await waitFor(() => h.saveCalls.write.length > 0);

  eq(h.saveCalls.pick.length, 1, "保存对话框桥恰好调用一次");
  eq(h.saveCalls.pick[0]?.filename, "photo.png", "默认文件名保留原扩展名（png）");
  eq(h.saveCalls.pick[0]?.mime, "image/png", "mime 传给对话框桥");
  eq(h.saveCalls.write[0]?.path, "C:\\Users\\test\\Downloads\\photo.png", "写入桥收到对话框选定路径");
  eq(h.saveCalls.write[0]?.payload, "AAAA", "写入桥收到原始 base64 载荷（不转码）");
  eq(h.saveCalls.write[0]?.base64, true, "写入桥 base64 通道标记");
  await waitFor(() => toastTexts("info").length > 0);
  ok(toastTexts("info")[0]?.includes("photo.png") === true, "成功 toast 含保存路径（复用 externalOpener.saved）");
  h.cleanup();
  dom.window.close();
}

// ── ②续 jpg 原格式 ──
{
  const dom = installDom();
  const h = await setupHarness({
    urls: { ".reasonix/attachments/shot.jpg": "data:image/jpeg;base64,BBBB" },
  });
  await h.render([{ path: ".reasonix/attachments/shot.jpg", name: "shot.jpg", kind: "image", source: "attachment", ext: "JPG" }]);
  await h.openViewer();
  await act(async () => {
    saveButton()?.click();
    await flushTimers();
  });
  await waitFor(() => h.saveCalls.write.length > 0);
  eq(h.saveCalls.pick[0]?.filename, "shot.jpg", "jpg 原格式默认文件名");
  eq(h.saveCalls.pick[0]?.mime, "image/jpeg", "jpeg mime 透传");
  eq(h.saveCalls.write[0]?.payload, "BBBB", "jpg 载荷原字节");
  h.cleanup();
  dom.window.close();
}

// ── ③ 取消路径 ──
{
  const dom = installDom();
  const h = await setupHarness({ pick: async () => "" });
  await h.render([imageAttachment(".reasonix/attachments/a.png", "a.png")]);
  await h.openViewer();
  await act(async () => {
    saveButton()?.click();
    await flushTimers();
  });
  await act(async () => { await flushTimers(); });
  eq(h.saveCalls.write.length, 0, "取消对话框后不落写入桥");
  eq(toastTexts("info").length, 0, "取消后无成功 toast");
  h.cleanup();
  dom.window.close();
}

// ── ④ 失败路径：错误 toast + 按钮恢复 ──
{
  const dom = installDom();
  const h = await setupHarness({ write: async () => { throw new Error("disk full"); } });
  await h.render([imageAttachment(".reasonix/attachments/a.png", "a.png")]);
  await h.openViewer();
  await act(async () => {
    saveButton()?.click();
    await flushTimers();
  });
  await waitFor(() => toastTexts("error").length > 0);
  ok(toastTexts("error")[0]?.includes("保存图片失败") === true, "失败 toast 出现（三语文案）");
  ok(toastTexts("error")[0]?.includes("disk full") === true, "失败 toast 携带错误详情");
  await waitFor(() => saveButton()?.disabled === false);
  ok(saveButton()?.disabled === false, "失败后按钮恢复可用");
  h.cleanup();
  dom.window.close();
}

// ── ⑤ 忙守卫：悬而未决时 disabled 且二次点击不重复触发 ──
{
  const dom = installDom();
  let releasePick: (() => void) | null = null;
  const gate = new Promise<string>((resolve) => { releasePick = () => resolve("C:\\out.png"); });
  const h = await setupHarness({ pick: () => gate });
  await h.render([imageAttachment(".reasonix/attachments/a.png", "a.png")]);
  await h.openViewer();
  await act(async () => {
    saveButton()?.click();
    await flushTimers();
  });
  eq(saveButton()?.disabled, true, "对话框悬而未决时按钮 disabled");
  await act(async () => {
    saveButton()?.click();
    await flushTimers();
  });
  eq(h.saveCalls.pick.length, 1, "忙守卫：二次点击不再触发对话框");
  await act(async () => {
    releasePick?.();
    await flushTimers();
  });
  await waitFor(() => h.saveCalls.write.length > 0);
  eq(h.saveCalls.write.length, 1, "放行后完成一次写入");
  h.cleanup();
  dom.window.close();
}

// ── ⑥ 多图：保存当前图 ──
{
  const dom = installDom();
  const h = await setupHarness({
    urls: {
      ".reasonix/attachments/a.png": "data:image/png;base64,AAAA",
      ".reasonix/attachments/b.png": "data:image/png;base64,BBBB",
    },
  });
  await h.render([
    imageAttachment(".reasonix/attachments/a.png", "a.png"),
    imageAttachment(".reasonix/attachments/b.png", "b.png"),
  ]);
  await h.openViewer(0);
  const next = document.querySelector<HTMLButtonElement>(".image-viewer__nav--next")!;
  await act(async () => {
    next.click();
    await flushTimers();
  });
  const src = document.querySelector<HTMLImageElement>(".image-viewer__image")?.getAttribute("src");
  ok(src?.includes("BBBB") === true, "已切到第二张");
  await act(async () => {
    saveButton()?.click();
    await flushTimers();
  });
  await waitFor(() => h.saveCalls.write.length > 0);
  eq(h.saveCalls.pick[0]?.filename, "b.png", "多图保存跟随当前图文件名");
  eq(h.saveCalls.write[0]?.payload, "BBBB", "多图保存当前图载荷");
  h.cleanup();
  dom.window.close();
}

// ── ⑦ 非 data URL：错误 toast、不触碰对话框桥 ──
{
  const dom = installDom();
  const h = await setupHarness({
    urls: { ".reasonix/attachments/weird.png": "https://example.com/remote.png" },
  });
  await h.render([imageAttachment(".reasonix/attachments/weird.png", "weird.png")]);
  await h.openViewer();
  await act(async () => {
    saveButton()?.click();
    await flushTimers();
  });
  await waitFor(() => toastTexts("error").length > 0);
  eq(h.saveCalls.pick.length, 0, "不可存源不触发对话框桥");
  eq(h.saveCalls.write.length, 0, "不可存源不落写入桥");
  ok(toastTexts("error")[0]?.includes("保存图片失败") === true, "不可存源给出错误 toast");
  h.cleanup();
  dom.window.close();
}

process.stdout.write(`\ntl1411 save-local: ${passed} passed, ${failed} failed\n`);
process.exit(failed > 0 ? 1 : 0);
