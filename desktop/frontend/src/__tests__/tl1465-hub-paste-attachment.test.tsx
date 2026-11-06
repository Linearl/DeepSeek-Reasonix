// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1465-hub-paste-attachment.test.tsx
// tl1465 D（用户 20261106 18:01 补充）harness：
//   D1 粘图即时反馈：协作中心-群聊输入框粘贴图片 → 占位 chip 即时出现
//      （落盘中态），落盘成功换缩略图（SaveChannelAttachment 全局工作区
//      锚点 + ChannelAttachmentDataURL 读回）——此前无任何反馈（纯 textarea
//      无粘贴链，图片静默消失）。
//   D2 跨面接力：粘贴后的 chip+文本写入持久化草稿（composerDraftPersistence
//      同一存储，键 channel:<ref> 与群聊 tab Composer 同一份）→ 切到群聊
//      tab（ChannelWorkspacePanel 挂 Composer channelMode）→ 输入框文本
//      与粘图缩略图都在（缩略图经 channelMode 回填效果补 previewUrl）。
//   D3 发送组装：发送体=文本+`@<绝对路径>`（escapeRefPath 转义空格存活
//      @-token 语法），发送后 chip 清空。
//   D4 原生剪贴板回退：无文件载荷的 image-hint 粘贴走 SaveChannelClipboardImage
//      （此前该链锚活动会话工作区=群外文件，接收方永远读不到）。
//   D5 读桥收裸路径（@ 不进读桥；剥 @ 断点在 ChannelMessageStream A/③ 件，
//      此处钉预览读回参数无 @）。
// Go 侧契约（SaveChannelClipboardImage 全局锚点/读桥 @ 规范化）由 desktop 包
// 测试持有。

import { JSDOM } from "jsdom";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";

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

const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost/", pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  Element: dom.window.Element,
  HTMLElement: dom.window.HTMLElement,
  Node: dom.window.Node,
  localStorage: dom.window.localStorage,
  IS_REACT_ACT_ENVIRONMENT: true,
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
  requestAnimationFrame: dom.window.requestAnimationFrame.bind(dom.window),
  cancelAnimationFrame: dom.window.cancelAnimationFrame.bind(dom.window),
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
// react-dom 输入事件 polyfill 在 jsdom 下探 IE 专有 API——composer 族 harness 同款垫片。
Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
window.matchMedia = (() => ({
  matches: true,
  media: "(prefers-reduced-motion: reduce)",
  onchange: null,
  addEventListener() {},
  removeEventListener() {},
  addListener() {},
  removeEventListener() {},
  dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;

const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
await preloadLocale("zh");
// 草稿持久化默认关闭（App 启动时开启）——harness 手动开（与生产同链）。
const { setComposerDraftPersistenceEnabled } = await import("../lib/composerDraftPersistence");
setComposerDraftPersistenceEnabled(true);
const { ChannelChatPanel, HUMAN_LOCAL } = await import("../components/ChannelChatPanel");
const { ChannelWorkspacePanel } = await import("../components/ChannelWorkspacePanel");
type ChannelChatBindings = import("../components/ChannelChatPanel").ChannelChatBindings;
type ChannelWorkspaceBindings = import("../components/ChannelWorkspacePanel").ChannelWorkspaceBindings;
type CollabChannelMessageView = import("../lib/bridge").CollabChannelMessageView;

// 全新 localStorage 起点（composer:drafts:v1 是全局单例存储）。
window.localStorage.clear();

const flush = async (): Promise<void> => {
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
};

const workspaceChannel = {
  id: "ch_hub", name: "常用对话组", topic: "", createdAt: 1, hourlyLimit: 30,
  members: [HUMAN_LOCAL, "sc_dev"], messages: 0, ownerType: "human", ownerId: HUMAN_LOCAL,
  memberDetails: [
    { contact: HUMAN_LOCAL, type: "human", joinedAt: 1 },
    { contact: "sc_dev", type: "session", joinedAt: 2 },
  ],
};

const sentBodies: string[] = [];
let savedDataUrls: string[] = [];
let nativeClipboardSaves = 0;
let previewPaths: string[] = [];

const panelBindings: ChannelChatBindings = {
  async ListCollabChannels() { return [{ ...workspaceChannel, members: [...workspaceChannel.members], memberDetails: workspaceChannel.memberDetails?.map((m) => ({ ...m })) }]; },
  async ReadCollabChannel(_ref, limit) { return { channel: workspaceChannel, messages: [] as CollabChannelMessageView[] }; void limit; },
  async ChannelMessagesForHuman() { return { channel: workspaceChannel, messages: [] as CollabChannelMessageView[] }; },
  async ChannelCreateForHuman() { return workspaceChannel; },
  async ChannelLeaveHuman() { return undefined; },
  async ChannelDissolveForHuman() { return undefined; },
  async ChannelSendFromHuman(_ref, body) {
    sentBodies.push(body);
    return { id: "hub-sent", sender: HUMAN_LOCAL, body, at: Date.now(), fanout: [] };
  },
  async ChannelInvite() { return workspaceChannel; },
  async ChannelRemoveMember() { return workspaceChannel; },
  async ChannelSetDeputy() { return workspaceChannel; },
  async ChannelUnsetDeputy() { return workspaceChannel; },
  async ChannelCancelFromHuman() { return {} as import("../lib/bridge").CollabChannelCancelResultView; },
  async ListAddressableSessions() { return [{ contactId: "sc_dev", title: "开发会话", scope: "global", archived: false, sessionPath: "/s/sc_dev.jsonl" }]; },
  async ChannelAttachmentDataURL(path) {
    previewPaths.push(path);
    return "data:image/png;base64,iVBORw0KGgo=";
  },
  async SaveChannelAttachment(dataUrl) {
    savedDataUrls.push(dataUrl);
    return "C:\\mock-global\\.reasonix\\attachments\\clipboard-hub-1.png";
  },
  async SaveChannelClipboardImage() {
    nativeClipboardSaves += 1;
    return "C:\\mock-global\\.reasonix\\attachments\\clipboard-native-1.png";
  },
};

const rootEl = document.getElementById("root")!;
const root = createRoot(rootEl);
await act(async () => {
  root.render(
    <LocaleProvider>
      <ChannelChatPanel bindings={panelBindings} />
    </LocaleProvider>,
  );
});
await flush();

const chat = () => document.querySelector(".channel-chat")!;
// 打开群（草稿从持久化层播种——此处为空起点）。
await act(async () => { (chat().querySelector(".channel-chat__item") as HTMLElement).click(); });
await flush();

const input = () => chat().querySelector(".channel-chat__input") as HTMLTextAreaElement;
ok(input(), "the group chat input mounts");

function pasteEvent(clipboardData: unknown): Event {
  const event = new dom.window.Event("paste", { bubbles: true, cancelable: true });
  Object.defineProperty(event, "clipboardData", { value: clipboardData });
  return event;
}

// ── D1 粘图即时反馈：文件载荷 → 占位 chip 即时出现 → 落盘成功换缩略图 ──
const pngFile = new dom.window.File(["png-bytes"], "shot.png", { type: "image/png" });
await act(async () => {
  input().dispatchEvent(pasteEvent({ files: [pngFile], items: [], getData: () => "" }));
  await new Promise((resolve) => setTimeout(resolve, 0));
});
ok(chat().querySelector(".channel-chat__chip--pending"), "a pending placeholder chip appears immediately (粘图即时反馈)");
ok(chat().querySelector(".channel-chat__chippending"), "the placeholder carries the saving label (落盘中态可见)");
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 10)); });
const thumb = chat().querySelector("img.channel-chat__chipthumb") as HTMLImageElement | null;
ok(thumb, "the chip turns into a thumbnail after the save lands (缩略图)");
eq(savedDataUrls.length, 1, "the image went through the channel save bridge (全局工作区锚点)");
eq(chat().querySelectorAll(".channel-chat__chip--pending").length, 0, "no pending chip remains");

// ── D5 预览读回收裸路径 ──
eq(previewPaths.some((p) => p.startsWith("@")), false, "the preview bridge receives the bare path (no @)");

// ── D3 发送组装：文本+@引用；发送后 chip 清空 ──────────────────────────
{
  // 文本经粘贴链入草稿（同一 setDraft 通路，jsdom 下合成 input 事件对本
  // 受控 textarea 不触发 onChange——纯 harness 机制问题；键盘输入路径由
  // channel-chat-panel.test.tsx 既有断言持有）。
  const value = "看这张图";
  await act(async () => {
    input().dispatchEvent(pasteEvent({ files: [], items: [], getData: () => value }));
  });
  await flush();
  eq(input().value, value, "the pasted text lands in the hub textarea draft");
  const sendBtn2 = chat().querySelector(".channel-chat__send") as HTMLButtonElement | null;
  if (!sendBtn2) process.stdout.write(`  DEBUG buttons: ${Array.from(chat().querySelectorAll("button")).map((b) => JSON.stringify(b.textContent)).join(",")}\n`);
  await act(async () => {
    sendBtn2?.click();
    await new Promise((resolve) => setTimeout(resolve, 5));
  });
  eq(sentBodies.length, 1, "the send goes through the human send port");
  const body = sentBodies[0];
  if (!body.startsWith("看这张图 ")) process.stdout.write(`  DEBUG body=${JSON.stringify(body)}\n`);
  ok(body.startsWith("看这张图 "), "the body starts with the text");
  ok(body.includes("@C:\\mock-global\\.reasonix\\attachments\\clipboard-hub-1.png"), "the body embeds the @absolute-path ref (与 tab Composer 提交链同组装)");
  eq(chat().querySelectorAll(".channel-chat__chip").length, 0, "the chips clear after the send");
}

// ── D2 跨面接力：新粘一张（不发送）→ 持久化草稿可查 → tab Composer 恢复 ──
const png2 = new dom.window.File(["png2"], "second.png", { type: "image/png" });
await act(async () => {
  input().dispatchEvent(pasteEvent({ files: [png2], items: [], getData: () => "" }));
  await new Promise((resolve) => setTimeout(resolve, 10));
});
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 200)); }); // 持久化 debounce 150ms
{
  const persisted = JSON.parse(window.localStorage.getItem("composer:drafts:v1") ?? "{}")["channel:ch_hub"];
  ok(persisted, "the channel draft persists under the shared composer key");
  eq(persisted.attachments?.length, 1, "the unsent chip persists as a draft attachment");
  eq(persisted.attachments?.[0]?.path, "C:\\mock-global\\.reasonix\\attachments\\clipboard-hub-1.png", "the persisted path is the absolute channel path");
}
root.unmount();

// 切到群聊 tab：ChannelWorkspacePanel 挂 Composer channelMode —— 同一份草稿
// 恢复（文本+chip），缩略图经 channelMode 回填效果补齐。
let tabSentBodies: string[] = [];
const wsBindings: ChannelWorkspaceBindings = {
  ...({} as ChannelWorkspaceBindings),
  ChannelMessagesForHuman: async () => ({ channel: workspaceChannel, messages: [] as CollabChannelMessageView[] }),
  ChannelSendFromHuman: async (_ref, body) => { tabSentBodies.push(body); return { id: "tab-sent", sender: HUMAN_LOCAL, body, at: Date.now(), fanout: [] }; },
  ChannelCancelFromHuman: async () => ({}) as Awaited<ReturnType<ChannelWorkspaceBindings["ChannelCancelFromHuman"]>>,
  ChannelInvite: async () => workspaceChannel,
  ChannelRemoveMember: async () => workspaceChannel,
  ChannelSetDeputy: async () => workspaceChannel,
  ChannelUnsetDeputy: async () => workspaceChannel,
  ChannelLeaveHuman: async () => undefined,
  ChannelDissolveForHuman: async () => undefined,
  ListAddressableSessions: async () => [{ contactId: "sc_dev", title: "开发会话", scope: "global", archived: false, sessionPath: "/s/sc_dev.jsonl" }],
  ChannelAttachmentDataURL: async () => "data:image/png;base64,iVBORw0KGgo=",
};
{
  const { loadPersistedComposerDraft } = await import("../lib/composerDraftPersistence");
  const probe = loadPersistedComposerDraft("channel:ch_hub");
  process.stdout.write(`  DEBUG pre-tab persisted=${JSON.stringify(probe)}\n`);
  const root2 = createRoot(document.getElementById("root")!);
  await act(async () => {
    root2.render(
      <LocaleProvider>
        <ChannelWorkspacePanel channelRef="ch_hub" fallbackName="常用对话组" bindings={wsBindings} />
      </LocaleProvider>,
    );
  });
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); });
  const composerHost = document.querySelector(".channel-workspace__composer")!;
  const tabInput = composerHost.querySelector(".composer__input") as HTMLTextAreaElement;
  ok(tabInput, "the tab Composer mounts");
  eq(tabInput.value, "", "the tab draft text starts from the shared persisted draft (空文本一致)");
  // chip 恢复：channelMode 附件 chip 挂上（缩略图由回填效果补齐）。
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 10)); });
  // chip 形态：回填效果补了 previewUrl → `--image` 变体（缩略图）；回填前=
  // `--attachment` 文件名 chip。两者任一在场即「粘图切 tab 仍在」。
  const chipEl = document.querySelector(".composer-context__item--attachment, .composer-context__item--image");
  const chipThumb = document.querySelector(".composer-context__thumb img");
  if (!chipEl) {
    process.stdout.write(`  DEBUG context html=${(document.querySelector(".composer-context")?.innerHTML ?? "").slice(0, 200)}\n`);
  }
  ok(chipEl, "the pasted chip is present in the tab Composer (切 tab 后仍在)");
  ok(chipThumb, "the tab chip carries the backfilled thumbnail");
  root2.unmount();
}

// ── D4 原生剪贴板回退：无文件载荷的 image-hint 粘贴 ─────────────────────
{
  const root3 = createRoot(document.getElementById("root")!);
  await act(async () => {
    root3.render(
      <LocaleProvider>
        <ChannelChatPanel bindings={panelBindings} />
      </LocaleProvider>,
    );
  });
  await flush();
  await act(async () => { (chat().querySelector(".channel-chat__item") as HTMLElement).click(); });
  await flush();
  await act(async () => {
    input().dispatchEvent(pasteEvent({ files: [], items: [{ type: "image/png" }], getData: () => "" }));
    await new Promise((resolve) => setTimeout(resolve, 10));
  });
  eq(nativeClipboardSaves, 1, "an image-hint paste without file payload goes through the native clipboard bridge");
  ok(chat().querySelector("img.channel-chat__chipthumb"), "the native clipboard chip renders a thumbnail too");
  // 面板文本粘贴不被图片分支吞掉：纯文本照常上屏。
  await act(async () => {
    input().dispatchEvent(pasteEvent({ files: [], items: [], getData: () => "纯文本" }));
  });
  eq(input().value, "纯文本", "a plain text paste still lands in the textarea");
  root3.unmount();
}

process.stdout.write(`\ntl1465-hub-paste-attachment: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
process.exit(0);
