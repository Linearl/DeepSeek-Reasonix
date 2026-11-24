// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/composer-video-gate-toast.test.tsx
//
// tl1767r3 acceptance harness（门控关时视频附件静默丢弃无提示 返修）：
//  ① 门控关（videoInputEnabled=false）+ 粘贴视频文件 → 出「未开启视频输入」
//     warn toast（非静默），SavePastedVideo 零调用（附件不落盘）——返修前
//     该场景四路分发全不收、零提示（用户真机包113 现象）；
//  ② 门控关 + 粘贴 >50MB 视频文件 → 同样出「未开启视频输入」toast：
//     videoTooLarge 不再被门控静默跳过，且门控关时报「不支持」而非
//     「超 50MB」（文件根本不会被添加，尺寸文案会误导）；
//  ③ 门控关 + 粘贴普通文件（pdf）→ 不出视频 toast、走 SavePastedFile
//     既有链（门控修复不误伤普通文件路）；
//  ④ 门控开（videoInputEnabled=true）+ 粘贴视频文件 → SavePastedVideo
//     调用一次、无「未开启视频输入」toast（门控开行为与返修前一致：
//     附件落盘链原样）。
//
// 挂载/桥桩模式与 composer-draft-rewrite.test.tsx 同款（window.go.main.App
// 直挂 + ToastProvider 渲染 .toast--warn .toast__text 断言）。

import { JSDOM } from "jsdom";

import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { Composer } from "../components/Composer";
import { LocaleProvider, preloadLocale } from "../lib/i18n";
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
    process.exitCode = 1;
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

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  // locale 断言走 zh：LocaleProvider 的 readPref() 硬编码 ""（Task 694 后
  // 持久化移交 desktop config），detectLocale 回落 navigator.language——
  // jsdom 默认 "en-US"，此处覆写为 zh-CN 让 t() 出中文文案。
  Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "zh-CN" });
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

const savedVideos: { name: string; dataUrl: string }[] = [];
const savedFiles: { name: string; dataUrl: string }[] = [];

function stubBridge() {
  const w = window as unknown as { go?: { main?: { App?: unknown } } };
  w.go = {
    main: {
      App: {
        Commands: async () => [],
        Models: async () => [],
        ModelsForTab: async () => [],
        SlashArgs: async () => ({ items: [], from: 0 }),
        SavePastedVideo: async (name: string, dataUrl: string) => {
          savedVideos.push({ name, dataUrl });
          return `C:\\mock\\attachments\\${name}`;
        },
        SavePastedFile: async (name: string, dataUrl: string) => {
          savedFiles.push({ name, dataUrl });
          return `C:\\mock\\attachments\\${name}`;
        },
      },
    },
  };
}

function resetBridges() {
  savedVideos.length = 0;
  savedFiles.length = 0;
}

type ComposerProps = Partial<Parameters<typeof Composer>[0]>;

async function renderComposer(props: ComposerProps = {}) {
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  const currentProps: Parameters<typeof Composer>[0] = {
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
    ...props,
  };
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
  return { root };
}

function filePasteEvent(files: File[]): Event {
  const event = new window.Event("paste", { bubbles: true, cancelable: true });
  Object.defineProperty(event, "clipboardData", {
    configurable: true,
    value: { files, items: [], types: ["Files"], getData: () => "" },
  });
  return event;
}

async function pasteFiles(files: File[]) {
  const ta = document.querySelector("textarea");
  if (!ta) throw new Error("composer textarea not found");
  await act(async () => {
    ta.dispatchEvent(filePasteEvent(files));
    // 附件保存链（FileReader.readAsDataURL → bridge 落盘）是多跳异步，单次
    // flushTimers 不够——多轮短等确保链走完再断言。
    for (let i = 0; i < 6; i++) await new Promise((resolve) => setTimeout(resolve, 20));
  });
}

function warnToastTexts(): string[] {
  return [...document.querySelectorAll(".toast--warn .toast__text")].map((el) => el.textContent ?? "");
}

function videoFile(name: string, sizeOverride?: number): File {
  const file = new window.File([new Uint8Array(1024)], name, { type: "video/mp4" });
  if (sizeOverride !== undefined) {
    // jsdom 的 Blob.size 是原型 getter——实例覆写伪造超大文件，避免真分
    // 配 51MB buffer。
    Object.defineProperty(file, "size", { configurable: true, value: sizeOverride });
  }
  return file;
}

function pdfFile(): File {
  return new window.File([new Uint8Array(512)], "spec.pdf", { type: "application/pdf" });
}

const HINT = "未开启视频输入";
const HINT_EN = "Video input is not enabled";
const TOO_LARGE = "超过 50 MB";
const TOO_LARGE_EN = "50 MB inline limit";
const hasHintToast = (texts: string[]) => texts.some((t) => t.includes(HINT) || t.includes(HINT_EN));
const hasTooLargeToast = (texts: string[]) => texts.some((t) => t.includes(TOO_LARGE) || t.includes(TOO_LARGE_EN));

// ── ① 门控关 + 小视频 → 「未开启视频输入」toast、零落盘 ──────────────
{
  const dom = installDom();
  await preloadLocale("zh");
  stubBridge();
  resetBridges();
  const { root } = await renderComposer({ videoInputEnabled: false });
  await pasteFiles([videoFile("clip.mp4")]);
  const texts = warnToastTexts();
  ok(hasHintToast(texts), "gate off + small video: the videoInputDisabledHint warn toast appears (非静默)");
  ok(!hasTooLargeToast(texts), "gate off + small video: no videoTooLarge toast (尺寸检查不适用)");
  eq(savedVideos.length, 0, "gate off + small video: SavePastedVideo is never called (附件不落盘)");
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ② 门控关 + >50MB 视频 → 同样出「未开启」toast（超限不再静默跳过）──
{
  const dom = installDom();
  await preloadLocale("zh");
  stubBridge();
  resetBridges();
  const { root } = await renderComposer({ videoInputEnabled: false });
  await pasteFiles([videoFile("big.mp4", 51 * 1024 * 1024)]);
  const texts = warnToastTexts();
  ok(hasHintToast(texts), "gate off + oversized video: the videoInputDisabledHint toast still fires (videoTooLarge 路径不被静默跳过)");
  eq(savedVideos.length, 0, "gate off + oversized video: SavePastedVideo is never called");
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ③ 门控关 + 普通文件 → 不出视频 toast、SavePastedFile 既有链不受影响
{
  const dom = installDom();
  await preloadLocale("zh");
  stubBridge();
  resetBridges();
  const { root } = await renderComposer({ videoInputEnabled: false });
  await pasteFiles([pdfFile()]);
  const texts = warnToastTexts();
  ok(!hasHintToast(texts), "gate off + pdf: no video-gate toast for non-video files");
  eq(savedFiles.length, 1, "gate off + pdf: the ordinary-file chain still saves (attachOtherFiles 不受影响)");
  eq(savedVideos.length, 0, "gate off + pdf: no video bridge call");
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ④ 门控开 + 小视频 → SavePastedVideo 落盘一次、无「未开启」toast ──
{
  const dom = installDom();
  await preloadLocale("zh");
  stubBridge();
  resetBridges();
  const { root } = await renderComposer({ videoInputEnabled: true });
  await pasteFiles([videoFile("clip.mp4")]);
  const texts = warnToastTexts();
  ok(!hasHintToast(texts), "gate on + small video: no videoInputDisabledHint toast (门控开行为不变)");
  eq(savedVideos.length, 1, "gate on + small video: SavePastedVideo is called exactly once (附件落盘链原样)");
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ⑤ 三语文案齐备：三语 dict 都有 videoInputDisabledHint 键且带下一步指引
{
  const [{ zh }, { zhTW }, enMod] = await Promise.all([
    import("../locales/zh"),
    import("../locales/zh-TW"),
    import("../locales/en"),
  ]);
  const dicts: Record<string, string | undefined> = {
    zh: zh["composer.videoInputDisabledHint"],
    "zh-TW": zhTW["composer.videoInputDisabledHint"],
    en: (enMod as { en: Record<string, string> }).en["composer.videoInputDisabledHint"],
  };
  for (const k of Object.keys(dicts)) {
    const value = dicts[k];
    ok(typeof value === "string" && value.length > 0, `three-dialect copy: ${k} has composer.videoInputDisabledHint`);
    ok(typeof value === "string" && value.includes("model_overrides"), `three-dialect copy: ${k} hint names the model_overrides next step`);
    ok(typeof value === "string" && /video = true/.test(value ?? ""), `three-dialect copy: ${k} hint names video = true`);
  }
}

process.stdout.write(`\ncomposer-video-gate-toast: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exitCode = 1;
