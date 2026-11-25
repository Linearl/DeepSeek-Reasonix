// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/composer-audio-gate-toast.test.tsx
//
// tl1803 acceptance harness（audio 门控关静默丢弃无提示，照 tl1767r3 视频套件
// 同款返修）：① 门控关（audioInputEnabled=false）+ 粘贴音频文件 → 出「未开启
// 音频输入」warn toast（非静默），SavePastedAudio 零调用（附件不落盘）——
// 返修前该场景四路分发全不收、零提示（tl1767r3 报告申报的同类）；
// ② 门控关 + 粘贴 >50MB 音频 → 同样出「未开启音频输入」toast：audioTooLarge
// 不再被门控静默跳过，且门控关时报「不支持」而非「超 50MB」（文件根本不会
// 被添加，尺寸文案会误导）；
// ③ 门控关 + 粘贴普通文件（pdf）→ 不出音频 toast、走 SavePastedFile 既有链
// （门控修复不误伤普通文件路）；
// ④ 门控开（audioInputEnabled=true）+ 粘贴音频 → SavePastedAudio 调用一次、
// 无「未开启音频输入」toast（门控开行为与返修前逐字节一致：附件落盘链原样）；
// ⑤ 门控关 + channelMode（群聊）+ 音频 → 零 toast：tl1441 ③ 群聊六件能力
// 清单无音频，静默跳过是有意设计（提示去开 audio 不可兑现），本钉防提示
// 泄漏进群聊；同场景粘图片作为阳性对照（图片走群聊附件链 SaveChannel-
// Attachment），证明粘贴路由确实到达 attach 层——「零 toast」不是路由断裂
// 的假绿；
// ⑥ 三语文案齐备：三语 dict 都有 audioInputDisabledHint 键且带下一步指引。
//
// 挂载/桥桩模式与 composer-video-gate-toast.test.tsx 同款（window.go.main.App
// 直挂 + ToastProvider 渲染 .toast--warn .toast__text 断言）；⑤ 的群聊场景
// 桥面用 Proxy 兜底（未显式桩的 App 方法回 async no-op），防 channelMode
// 挂载路径的其余桥调用炸场。

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

const savedAudios: { name: string; dataUrl: string }[] = [];
const savedFiles: { name: string; dataUrl: string }[] = [];
const savedChannelImages: { name: string; dataUrl: string }[] = [];

function stubBridge(options: { channelFallback?: boolean } = {}) {
  const w = window as unknown as { go?: { main?: { App?: unknown } } };
  const base: Record<string, unknown> = {
    Commands: async () => [],
    Models: async () => [],
    ModelsForTab: async () => [],
    SlashArgs: async () => ({ items: [], from: 0 }),
    SavePastedAudio: async (name: string, dataUrl: string) => {
      savedAudios.push({ name, dataUrl });
      return `C:\\mock\\attachments\\${name}`;
    },
    SavePastedFile: async (name: string, dataUrl: string) => {
      savedFiles.push({ name, dataUrl });
      return `C:\\mock\\attachments\\${name}`;
    },
    SaveChannelAttachment: async (dataUrl: string) => {
      savedChannelImages.push({ name: "channel-image", dataUrl });
      return "C:\\mock\\channel\\image.png";
    },
  };
  // channelMode 挂载路径还有零散桥调用（群读回链 ChannelAttachmentDataURL、
  // 原生剪贴板 SaveChannelClipboardImage 等）——Proxy 兜底回 async no-op，
  // 断言只落在显式桩的三个保存链上。
  const app = options.channelFallback
    ? new Proxy(base, { get: (target, key) => (typeof key === "string" && key in target ? target[key] : async () => ({})) })
    : base;
  w.go = { main: { App: app } };
}

function resetBridges() {
  savedAudios.length = 0;
  savedFiles.length = 0;
  savedChannelImages.length = 0;
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

function audioFile(name: string, sizeOverride?: number): File {
  const file = new window.File([new Uint8Array(1024)], name, { type: "audio/mpeg" });
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

function pngFile(): File {
  return new window.File([new Uint8Array(256)], "shot.png", { type: "image/png" });
}

const HINT = "未开启音频输入";
const HINT_EN = "Audio input is not enabled";
const TOO_LARGE = "超过 50 MB";
const TOO_LARGE_EN = "50 MB inline limit";
const hasHintToast = (texts: string[]) => texts.some((t) => t.includes(HINT) || t.includes(HINT_EN));
const hasTooLargeToast = (texts: string[]) => texts.some((t) => t.includes(TOO_LARGE) || t.includes(TOO_LARGE_EN));

// ── ① 门控关 + 小音频 → 「未开启音频输入」toast、零落盘 ──────────────
{
  const dom = installDom();
  await preloadLocale("zh");
  stubBridge();
  resetBridges();
  const { root } = await renderComposer({ audioInputEnabled: false });
  await pasteFiles([audioFile("voice.mp3")]);
  const texts = warnToastTexts();
  ok(hasHintToast(texts), "gate off + small audio: the audioInputDisabledHint warn toast appears (非静默)");
  ok(!hasTooLargeToast(texts), "gate off + small audio: no audioTooLarge toast (尺寸检查不适用)");
  eq(savedAudios.length, 0, "gate off + small audio: SavePastedAudio is never called (附件不落盘)");
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ② 门控关 + >50MB 音频 → 同样出「未开启」toast（超限不再静默跳过）──
{
  const dom = installDom();
  await preloadLocale("zh");
  stubBridge();
  resetBridges();
  const { root } = await renderComposer({ audioInputEnabled: false });
  await pasteFiles([audioFile("big.mp3", 51 * 1024 * 1024)]);
  const texts = warnToastTexts();
  ok(hasHintToast(texts), "gate off + oversized audio: the audioInputDisabledHint toast still fires (audioTooLarge 路径不被静默跳过)");
  ok(!hasTooLargeToast(texts), "gate off + oversized audio: no audioTooLarge toast (门控关时报不支持而非超限)");
  eq(savedAudios.length, 0, "gate off + oversized audio: SavePastedAudio is never called");
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ③ 门控关 + 普通文件 → 不出音频 toast、SavePastedFile 既有链不受影响
{
  const dom = installDom();
  await preloadLocale("zh");
  stubBridge();
  resetBridges();
  const { root } = await renderComposer({ audioInputEnabled: false });
  await pasteFiles([pdfFile()]);
  const texts = warnToastTexts();
  ok(!hasHintToast(texts), "gate off + pdf: no audio-gate toast for non-audio files");
  eq(savedFiles.length, 1, "gate off + pdf: the ordinary-file chain still saves (attachOtherFiles 不受影响)");
  eq(savedAudios.length, 0, "gate off + pdf: no audio bridge call");
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ④ 门控开 + 小音频 → SavePastedAudio 落盘一次、无「未开启」toast ──
{
  const dom = installDom();
  await preloadLocale("zh");
  stubBridge();
  resetBridges();
  const { root } = await renderComposer({ audioInputEnabled: true });
  await pasteFiles([audioFile("voice.mp3")]);
  const texts = warnToastTexts();
  ok(!hasHintToast(texts), "gate on + small audio: no audioInputDisabledHint toast (门控开行为不变)");
  eq(savedAudios.length, 1, "gate on + small audio: SavePastedAudio is called exactly once (附件落盘链原样)");
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ⑤ 门控关 + 群聊态 + 音频 → 零 toast（tl1441 ③ 静默跳过保持）──────
// 阳性对照：同场景粘图片须走群聊附件链（SaveChannelAttachment 被调），证明
// 粘贴路由到达 attach 层——「零 toast」不是路由断裂的假绿。
{
  const dom = installDom();
  await preloadLocale("zh");
  stubBridge({ channelFallback: true });
  resetBridges();
  const { root } = await renderComposer({ audioInputEnabled: false, channelMode: true });
  await pasteFiles([audioFile("voice.mp3")]);
  ok(warnToastTexts().length === 0, "channel + gate off + audio: zero warn toasts (群聊静默跳过不泄漏提示)");
  eq(savedAudios.length, 0, "channel + gate off + audio: no audio save call");
  await pasteFiles([pngFile()]);
  await act(async () => {
    for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 20));
  });
  ok(savedChannelImages.length >= 1, "channel + image paste: SaveChannelAttachment called (阳性对照：粘贴路由可达 attach 层)");
  await act(async () => root.unmount());
  dom.window.close();
}

// ── ⑥ 三语文案齐备：三语 dict 都有 audioInputDisabledHint 键且带指引 ──
{
  const [{ zh }, { zhTW }, enMod] = await Promise.all([
    import("../locales/zh"),
    import("../locales/zh-TW"),
    import("../locales/en"),
  ]);
  const dicts: Record<string, string | undefined> = {
    zh: zh["composer.audioInputDisabledHint"],
    "zh-TW": zhTW["composer.audioInputDisabledHint"],
    en: (enMod as { en: Record<string, string> }).en["composer.audioInputDisabledHint"],
  };
  for (const k of Object.keys(dicts)) {
    const value = dicts[k];
    ok(typeof value === "string" && value.length > 0, `three-dialect copy: ${k} has composer.audioInputDisabledHint`);
    ok(typeof value === "string" && value.includes("model_overrides"), `three-dialect copy: ${k} hint names the model_overrides next step`);
    ok(typeof value === "string" && /audio = true/.test(value ?? ""), `three-dialect copy: ${k} hint names audio = true`);
  }
}

process.stdout.write(`\ncomposer-audio-gate-toast: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exitCode = 1;
