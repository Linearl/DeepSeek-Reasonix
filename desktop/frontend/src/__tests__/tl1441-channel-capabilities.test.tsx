// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1441-channel-capabilities.test.tsx
// tl1441 ③（能力对齐六件）harness——群聊模式与会话同交互的验收面：
//   ① 粘贴剪贴板图片 → channel 桥落盘（SaveChannelAttachment，非会话链）→
//      附件 chip 进输入框（验收 1 前半）；会话桥（SavePastedImage/
//      AttachmentDataURL）零调用=双链不串。
//   ② 截图按钮在群聊模式在场（attachmentInputEnabled 门控复用；端到端截屏
//      流由 ScreenshotOverlay 既有族持有，本件钉按钮在场+路由源码钉）。
//   ③ 改写草稿：lab 开→按钮在群聊模式在场（②步的隐藏已解除）；点按走
//      档位弹窗→RewriteDraftForChannel（draft+群尾窗上下文+所选档），回填
//      输入框不自动发送；rewriteContextProvider 提供群尾窗。
//   ④ 消息流附件缩略图（验收 1 后半「成员侧可见图」）：@…/.reasonix/
//      attachments/<img> 引用渲染内嵌图；非图片/含 .. /无读桥=不渲染。
//   ⑤ 快捷指令/表情/历史提示词在场（② harness 已深验，此处兜底在场态）。
//   ⑥ 源码钉：Composer 路由锚（savePastedImageRouted/attachmentPreviewRouted/
//      RewriteDraftForChannel 分支）+ Go 桥声明钉。
// Go 侧契约由 desktop/channel_attachment_app_test.go 持有（落盘锚点/逃出拒绝/
// 借道降级）；本 harness 持渲染与路由面。

import { JSDOM } from "jsdom";
import * as React from "react";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  Node: dom.window.Node,
  HTMLElement: dom.window.HTMLElement,
  HTMLTextAreaElement: dom.window.HTMLTextAreaElement,
  HTMLInputElement: dom.window.HTMLInputElement,
  Event: dom.window.Event,
  CustomEvent: dom.window.CustomEvent,
  KeyboardEvent: dom.window.KeyboardEvent,
  InputEvent: dom.window.InputEvent,
  MouseEvent: dom.window.MouseEvent,
  PointerEvent: dom.window.MouseEvent,
  MutationObserver: dom.window.MutationObserver,
  File: dom.window.File,
  FileReader: dom.window.FileReader,
  localStorage: dom.window.localStorage,
  requestAnimationFrame: dom.window.requestAnimationFrame.bind(dom.window),
  cancelAnimationFrame: dom.window.cancelAnimationFrame.bind(dom.window),
  IS_REACT_ACT_ENVIRONMENT: true,
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
function menuRect(height: number): DOMRect {
  return { width: 320, height, top: 400, bottom: 400 + height, left: 10, right: 330, x: 10, y: 400, toJSON: () => ({}) } as DOMRect;
}
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
  get(this: HTMLElement) {
    if (this.classList.contains("slashmenu")) return 320;
    if (this.classList.contains("slashmenu__row")) return 300;
    return 0;
  },
});
Object.defineProperty(dom.window.HTMLElement.prototype, "offsetHeight", {
  configurable: true,
  get(this: HTMLElement) {
    if (this.classList.contains("slashmenu")) return 360;
    if (this.classList.contains("slashmenu__row")) return 34;
    return 0;
  },
});
window.matchMedia = (() => ({
  matches: true,
  media: "(prefers-reduced-motion: reduce)",
  onchange: null,
  addEventListener() {},
  removeEventListener() {},
  addListener() {},
  removeListener() {},
  dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { Composer } = await import("../components/Composer");
const { LocaleProvider } = await import("../lib/i18n");
const { ToastProvider } = await import("../lib/toast");
const { applyLabFlags } = await import("../lib/labFlags");
const { ChannelMessageStream } = await import("../components/ChannelMessageStream");
const { HUMAN_LOCAL } = await import("../components/ChannelChatPanel");
type ComposerProps = Parameters<typeof Composer>[0];
type CollaborationMode = import("../lib/types").CollaborationMode;
type ToolApprovalMode = import("../lib/types").ToolApprovalMode;

window.localStorage.clear();

let passed = 0;
function ok(cond: unknown, label: string): void {
  if (!cond) throw new Error(label);
  passed += 1;
}
const flush = () => new Promise((resolve) => setTimeout(resolve, 0));
async function waitFor(predicate: () => boolean, label: string): Promise<void> {
  for (let i = 0; i < 40; i += 1) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  throw new Error(`waitFor timeout: ${label}`);
}

const rootEl = dom.window.document.getElementById("root")!;
const root = createRoot(rootEl);
const noop = () => undefined;
function baseProps(overrides: Partial<ComposerProps> = {}): ComposerProps {
  return {
    running: false,
    collaborationMode: "normal" as CollaborationMode,
    toolApprovalMode: "ask" as ToolApprovalMode,
    modelLabel: "",
    onSend: noop,
    onCancel: () => Promise.resolve({ receipt: { id: "", status: "rejected", reason: "x" }, restoredText: "", restoredQueuedIDs: [] } as never),
    onCycleMode: noop,
    onSetMode: noop,
    onSetCollaborationMode: noop,
    onSetToolApprovalMode: noop,
    onToggleYoloApprovalMode: () => Promise.resolve(true),
    onClearGoal: noop,
    onPauseGoal: noop,
    onResumeGoal: noop,
    onSwitchModel: () => true,
    onSetEffort: noop,
    ready: true,
    channelMode: true,
    ...overrides,
  };
}
async function paint(props: ComposerProps): Promise<void> {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ToastProvider>
          <Composer {...props} />
        </ToastProvider>
      </LocaleProvider>,
    );
    await flush();
  });
}
const ta = () => dom.window.document.querySelector(".composer__input")! as HTMLTextAreaElement;
async function typeText(value: string): Promise<void> {
  await act(async () => {
    const el = ta();
    el.focus();
    const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLTextAreaElement.prototype, "value")!.set!;
    setter.call(el, value);
    el.setSelectionRange(value.length, value.length);
    el.dispatchEvent(new dom.window.InputEvent("input", { bubbles: true, data: value, inputType: "insertText", isComposing: false }));
    await flush();
  });
}
function imagePasteEvent(file: File): Event {
  const event = new dom.window.Event("paste", { bubbles: true, cancelable: true });
  Object.defineProperty(event, "clipboardData", {
    configurable: true,
    value: {
      files: [file],
      items: [],
      types: [file.type],
      getData: () => "",
    },
  });
  return event;
}
/** bridge.ts 的 app Proxy 每次调用都实时读 window.go.main.App——桩挂这里。 */
function installGoApp(methods: Record<string, unknown>): void {
  (dom.window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
    main: { App: { Commands: async () => [], Models: async () => [], ModelsForTab: async () => [], ...methods } },
  };
}

const TINY_PNG_URL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";

// ── ① 粘图链：channel 桥落盘 + chip 进输入框 + 会话桥零调用 ─────────────
{
  const calls: Record<string, number> = {
    SaveChannelAttachment: 0,
    SavePastedImage: 0,
    AttachmentDataURL: 0,
    ChannelAttachmentDataURL: 0,
  };
  installGoApp({
    SaveChannelAttachment: async () => { calls.SaveChannelAttachment += 1; return "/mock-global/.reasonix/attachments/c1.png"; },
    SavePastedImage: async () => { calls.SavePastedImage += 1; return ".reasonix/attachments/session-1.png"; },
    AttachmentDataURL: async () => { calls.AttachmentDataURL += 1; return TINY_PNG_URL; },
    ChannelAttachmentDataURL: async () => { calls.ChannelAttachmentDataURL += 1; return TINY_PNG_URL; },
    SaveScreenshotAttachment: async () => { calls.SavePastedImage += 1000; return ".reasonix/attachments/shot.png"; },
    SavePastedFile: async () => { calls.SavePastedImage += 1000; return ".reasonix/attachments/file.bin"; },
    SavePastedAudio: async () => { calls.SavePastedImage += 1000; return ".reasonix/attachments/a.ogg"; },
    Commands: async () => [],
    Models: async () => [],
    ModelsForTab: async () => [],
    ScanPromptHistory: async () => [],
  });
  await paint(baseProps({ sessionKey: "channel:ch_cap", attachmentInputEnabled: true, imageInputEnabled: true }));
  const file = new dom.window.File(["img"], "photo.png", { type: "image/png", lastModified: 1 });
  let pasteSeen = 0;
  const probe = () => { pasteSeen += 1; };
  dom.window.document.addEventListener("paste", probe, true);
  await act(async () => {
    ta().dispatchEvent(imagePasteEvent(file as unknown as File));
    await flush();
    await flush();
  });
  dom.window.document.removeEventListener("paste", probe, true);
  await waitFor(() => calls.SaveChannelAttachment === 1, "channel save call");
  await waitFor(() => calls.ChannelAttachmentDataURL === 1, "channel preview call");
  ok(calls.SaveChannelAttachment === 1, "pasted image saves through the channel bridge (粘图链路由 SaveChannelAttachment)");
  ok(calls.SavePastedImage === 0 && calls.AttachmentDataURL === 0, "the session attachment chain stays untouched (双链不串)");
  ok(calls.ChannelAttachmentDataURL === 1, "the chip preview reads back through the channel bridge");
  const chipCount = dom.window.document.querySelectorAll(".composer-context__item").length;
  ok(chipCount === 1, "the pasted image lands in the attachment chip row (验收 1 附件位)");

  // 音频/普通文件在群聊态跳过（六件清单外，且会话链锚错家）。
  const audioFile = new dom.window.File(["a"], "voice.ogg", { type: "audio/ogg", lastModified: 1 });
  const binFile = new dom.window.File(["b"], "doc.pdf", { type: "application/pdf", lastModified: 1 });
  await act(async () => {
    ta().dispatchEvent(imagePasteEvent(audioFile as unknown as File));
    ta().dispatchEvent(imagePasteEvent(binFile as unknown as File));
    await flush();
    await flush();
  });
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 60)); });
  ok(calls.SavePastedImage === 0 && calls.SaveChannelAttachment === 1, "audio/generic pastes are skipped in channel mode (六件清单外)");
}

// ── ② 截图按钮在场 + ⑤ 通用能力兜底 ────────────────────────────────────
{
  installGoApp({ ScanPromptHistory: async () => [], Commands: async () => [], Models: async () => [], ModelsForTab: async () => [] });
  await paint(baseProps({ sessionKey: "channel:ch_cap", attachmentInputEnabled: true, imageInputEnabled: true, historyPickerEnabled: true, quickCommands: [{ title: "日报", text: "请生成今日日报", enabled: true }], onInsertQuickCommand: noop }));
  ok(dom.window.document.querySelector(".composer-screenshot-trigger"), "the screenshot button renders in channel mode (截图，attachmentInputEnabled 门控复用)");
  ok(dom.window.document.querySelector(".composer-emoji-picker"), "emoji entry present (表情)");
  ok(dom.window.document.querySelector(".composer-history-picker"), "history picker present (历史提示词)");
}

// ── ③ 改写草稿：群聊链（RewriteDraftForChannel + 群尾窗上下文）──────────
{
  applyLabFlags({ draftRewrite: true });
  const rewriteCalls: Array<{ draft: string; tail: string; budget: number }> = [];
  installGoApp({
    RewriteDraftForChannel: async (draft: string, tail: string, budget: number) => {
      rewriteCalls.push({ draft, tail, budget });
      return "改写后的群聊草稿";
    },
    RewriteDraftForTab: async () => { throw new Error("channel mode must not call the tab rewrite bridge"); },
    ScanPromptHistory: async () => [],
    Commands: async () => [],
    Models: async () => [],
    ModelsForTab: async () => [],
  });
  await paint(baseProps({
    sessionKey: "channel:ch_rewrite",
    attachmentInputEnabled: true,
    imageInputEnabled: true,
    rewriteContextProvider: async () => "我：帮我看看",
  }));
  await typeText("帮我看看那个 CI 报错");
  const rewriteBtn = dom.window.document.querySelector(".composer__btn--rewrite") as HTMLButtonElement | null;
  ok(rewriteBtn, "the rewrite button renders in channel mode when the lab flag is on (改写草稿解禁)");
  await act(async () => { rewriteBtn?.click(); await flush(); });
  const dialog = dom.window.document.querySelector("div.lab-pick-dialog[role='dialog']");
  ok(dialog, "clicking the rewrite button opens the budget dialog (与会话同交互)");
  const tierBtns = Array.from(dom.window.document.querySelectorAll("div.lab-pick-dialog[role='dialog'] button[aria-pressed]")) as HTMLButtonElement[];
  await act(async () => { tierBtns[0]?.click(); await flush(); await flush(); });
  ok(rewriteCalls.length === 1, "picking a tier calls RewriteDraftForChannel once");
  ok(rewriteCalls[0]?.draft === "帮我看看那个 CI 报错", "the rewrite call carries the current draft");
  ok(rewriteCalls[0]?.tail === "我：帮我看看", "the rewrite context comes from the channel tail provider (rewriteContextProvider)");
  ok(rewriteCalls[0]?.budget > 0, "the budget tier reaches the channel bridge");
  await act(async () => { await flush(); });
  ok(ta().value === "改写后的群聊草稿", "the rewritten draft refills the input without auto-send (回填不发送)");
  applyLabFlags({ draftRewrite: false });
}

root.render(React.createElement("div"));
await act(async () => { await flush(); });

// ── ④ 消息流附件缩略图（成员侧可见图）───────────────────────────────────
{
  const previews: Record<string, string> = {
    // tl1465：读桥收裸路径（@ 前缀在 attachmentRefsOf 剥除——事故修复后契约；
    // 断言意图不变：成员侧可见图）。
    "/mock-global/.reasonix/attachments/p1.png": TINY_PNG_URL,
    "D:\\w\\.reasonix\\attachments\\p2.png": TINY_PNG_URL,
  };
  const preview = async (path: string): Promise<string> => {
    const hit = previews[path];
    if (!hit) throw new Error("outside");
    return hit;
  };
  const streamEl = dom.window.document.createElement("div");
  dom.window.document.body.appendChild(streamEl);
  const streamRoot = createRoot(streamEl);
  const paintStream = async (props: Record<string, unknown>): Promise<void> => {
    await act(async () => {
      streamRoot.render(React.createElement(
        LocaleProvider,
        null,
        React.createElement(ChannelMessageStream, {
          messages: [],
          memberDetails: [],
          directoryTitle: (id: string) => id,
          ...props,
        } as never),
      ));
      await flush();
    });
  };
  await paintStream({
    messages: [
      { id: "m1", sender: HUMAN_LOCAL, body: "看这张 @/mock-global/.reasonix/attachments/p1.png", at: 1 },
      { id: "m2", sender: "sc_dev", body: "win 路径 @D:\\w\\.reasonix\\attachments\\p2.png 也认", at: 2 },
      { id: "m3", sender: "sc_dev", body: "普通 @会话 提及不渲染图", at: 3 },
      { id: "m4", sender: "sc_dev", body: "逃出路径 @/etc/passwd/../.reasonix/attachments/evil.png 不渲染", at: 4 },
      { id: "m5", sender: "sc_dev", body: "非图片 @/mock-global/.reasonix/attachments/note.txt 不渲染", at: 5 },
    ],
    attachmentPreview: preview,
  });
  const pics = Array.from(streamEl.querySelectorAll(".channel-chat__attpic")) as HTMLImageElement[];
  ok(pics.length === 2, "attachment image refs render as inline thumbnails (验收 1 成员侧可见图，多图逐张)");
  ok(pics.every((pic) => pic.getAttribute("data-attachment-path")), "thumbnails carry their source path marker");
  // 无读桥（bindings 缺省）= 纯文本，不渲染。
  await paintStream({
    messages: [{ id: "m1", sender: HUMAN_LOCAL, body: "看这张 @/mock-global/.reasonix/attachments/p1.png", at: 1 }],
  });
  ok(streamEl.querySelectorAll(".channel-chat__attpic").length === 0, "without the preview bridge the stream stays plain text");
  streamRoot.render(React.createElement("div"));
  await act(async () => { await flush(); });
  streamEl.remove();
}

// ── ⑥ 源码钉 + 桥声明钉 ────────────────────────────────────────────────
const composerSrc = fs.readFileSync(path.join(import.meta.dirname, "..", "components", "Composer.tsx"), "utf-8");
for (const needle of [
  "channelMode ? app.SaveChannelAttachment(dataUrl) : app.SavePastedImage(dataUrl)",
  "channelMode ? app.ChannelAttachmentDataURL(path) : app.AttachmentDataURL(path)",
  "await app.SaveChannelAttachment(dataUrl)",
  "await app.RewriteDraftForChannel(current, recent, contextBudgetKB)",
  "if (channelMode) return;",
]) {
  ok(composerSrc.includes(needle), `Composer routing pin: ${needle.slice(0, 52)}…`);
}
const workspaceSrc = fs.readFileSync(path.join(import.meta.dirname, "..", "components", "ChannelWorkspacePanel.tsx"), "utf-8");
ok(workspaceSrc.includes("rewriteContextProvider={buildRewriteTail}"), "workspace pins the rewrite tail provider");
ok(workspaceSrc.includes("attachmentPreview={channelAttachmentPreview}"), "workspace pins the stream preview bridge (tl1465：引用稳定化后的 useCallback 形态)");
const bridgeSrc = fs.readFileSync(path.join(import.meta.dirname, "..", "lib", "bridge.ts"), "utf-8");
for (const needle of ["SaveChannelAttachment(dataUrl: string): Promise<string>", "ChannelAttachmentDataURL(path: string): Promise<string>", "RewriteDraftForChannel(draft: string, contextTail: string, contextBudgetKB: number): Promise<string>"]) {
  ok(bridgeSrc.includes(needle), `bridge declaration pin: ${needle.slice(0, 44)}…`);
}
const goBridge = fs.readFileSync(path.join(process.cwd(), "..", "channel_attachment_app.go"), "utf-8");
ok(goBridge.includes("func (a *App) SaveChannelAttachment"), "Go bridge pin: SaveChannelAttachment");
ok(goBridge.includes("func (a *App) ChannelAttachmentDataURL"), "Go bridge pin: ChannelAttachmentDataURL");

console.log(`tl1441-channel-capabilities: ${passed} assertions passed`);
