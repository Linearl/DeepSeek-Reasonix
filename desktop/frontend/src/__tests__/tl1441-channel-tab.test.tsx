// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1441-channel-tab.test.tsx
// tl1441 ①（tab 形态，用户 20261106 00:35 升级口径）harness：
//   ①「打开标签页」：协作中心聊天视图（ChannelChatPanel）群聊 chathead 提供
//      「打开标签页」按钮 → channelTabStore 打开即激活；
//   ② 群聊 tab 同型实体：mergeChannelTabMetas 把注册表投影成 TabMeta 形状
//      追加在会话 tab 之后（id 前缀 channel: / scope=channel / topicTitle=群名）；
//   ③ tab 工作区复用（ChannelWorkspacePanel）：顶部栏（返回列表/群名/成员数/
//      详情管理）+ 消息流（channel 数据源）+ 输入区 + 发送桥接 channel_send
//      mock + 30s 轮询契约 + 详情抽屉（成员/退群）+ 右键撤回；
//   ④ store 语义：开/关/激活/激活回退/localStorage 持久化恢复；
//   ⑤ App 接线钉（源码级）：四个 handler 拦截 + 合并投影 + 面板 surface +
//      右侧栏门控（会话专属面在群聊态隐藏的接线点）；
//   ⑥ 文案三语（zh/zh-TW/en）。
// Go 侧契约（store/绑定）由 desktop 包测试持有；本 harness 持渲染与接线。

import * as React from "react"; // tsconfig exclude 了 __tests__，tsx 对本文件走 classic JSX——必须显式绑定 React
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";

const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost/", pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  Element: dom.window.Element,
  HTMLElement: dom.window.HTMLElement,
  Node: dom.window.Node,
  localStorage: dom.window.localStorage,
  IS_REACT_ACT_ENVIRONMENT: true,
  // tl1441 ②：工作区面板换装 Composer 后需要的 jsdom 缺失件（composer 族
  // harness 同款 stub 集——MutationObserver/ResizeObserver/matchMedia/rAF）。
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
const { LocaleProvider } = await import("../lib/i18n");
const {
  openChannelTab, closeChannelTabs, activateChannelTab,
  loadOpenChannelTabs, loadActiveChannelTabId, onChannelTabsChange,
  channelTabId, isChannelTabId, channelRefFromTabId,
  mergeChannelTabMetas, channelTabMeta, CHANNEL_TAB_ID_PREFIX,
} = await import("../lib/channelTabStore");
type TabMeta = import("../lib/types").TabMeta;
type ChannelWorkspaceBindings = import("../components/ChannelWorkspacePanel").ChannelWorkspaceBindings;
type ChannelChatBindings = import("../components/ChannelChatPanel").ChannelChatBindings;
const { ChannelWorkspacePanel } = await import("../components/ChannelWorkspacePanel");
const { ChannelChatPanel, HUMAN_LOCAL } = await import("../components/ChannelChatPanel");
const { ChannelMessageStream } = await import("../components/ChannelMessageStream");
const fs = await import("node:fs");
const path = await import("node:path");

// ── store 全局态先清干净（模块级单例，持久化恢复段依赖确定起点）─────────
window.localStorage.clear();
closeChannelTabs(loadOpenChannelTabs().map((e) => channelTabId(e.ref)));
activateChannelTab(null);

let passed = 0;
function ok(cond: unknown, label: string): void {
  if (!cond) throw new Error(label);
  passed += 1;
}

// ── ④ store 语义 ────────────────────────────────────────────────────────
ok(CHANNEL_TAB_ID_PREFIX === "channel:", "the channel id prefix is the single routing anchor");
ok(isChannelTabId("channel:ch_x") && !isChannelTabId("tab_1"), "isChannelTabId discriminates by prefix only");
ok(channelRefFromTabId("channel:ch_x") === "ch_x", "channelRefFromTabId strips the prefix");
ok(channelRefFromTabId("tab_1") === null, "session ids yield null ref");

openChannelTab({ ref: "ch_a", name: "发布协调" });
ok(loadOpenChannelTabs().length === 1 && loadOpenChannelTabs()[0].name === "发布协调", "open registers the entry with its name");
ok(loadActiveChannelTabId() === "channel:ch_a", "open activates by default (口径① 打开即激活)");
openChannelTab({ ref: "ch_a", name: "发布协调" }, { activate: false });
ok(loadOpenChannelTabs().length === 1, "re-opening the same ref does not duplicate");

openChannelTab({ ref: "ch_b", name: "合并线" }, { activate: false });
ok(loadOpenChannelTabs().length === 2, "a second ref registers without stealing activation");
ok(loadActiveChannelTabId() === "channel:ch_a", "activate:false keeps the current activation");

activateChannelTab(null);
ok(loadActiveChannelTabId() === null, "activate(null) returns the surface to the session workspace");

openChannelTab({ ref: "ch_b", name: "合并线" });
closeChannelTabs(["channel:ch_b"]);
ok(loadOpenChannelTabs().length === 1, "close removes the entry");
ok(loadActiveChannelTabId() === null, "closing the active tab falls back to the session surface");

// 持久化恢复：open 列表 + active id 落 localStorage；active 指向已关 tab = 视为无激活。
openChannelTab({ ref: "ch_restore", name: "恢复群" });
ok(window.localStorage.getItem("reasonix.channelTabs.active") === "channel:ch_restore", "the active id persists");
ok(JSON.parse(window.localStorage.getItem("reasonix.channelTabs.open") || "[]").length >= 1, "the open list persists");

// 订阅通知：外部（CollabChatPanel）调用 store 时镜像订阅者能收到。
let notified = 0;
const unsub = onChannelTabsChange(() => { notified += 1; });
openChannelTab({ ref: "ch_notify", name: "通知群" });
ok(notified >= 1, "store mutations notify subscribers (App 镜像依赖此缝)");
unsub();

// ── ② TabMeta 同型投影（mergeChannelTabMetas 纯函数）────────────────────
const sessionTab = {
  id: "tab_1", scope: "project", workspaceRoot: "/w", workspaceName: "W",
  topicId: "t1", topicTitle: "会话一", label: "", ready: true, running: false, mode: "normal",
} as unknown as TabMeta;
const mergedNoChannel = mergeChannelTabMetas([sessionTab], [], null);
ok(mergedNoChannel.length === 1 && mergedNoChannel[0] === sessionTab, "no channel tabs = the session list passes through untouched");

const entries = loadOpenChannelTabs();
const merged = mergeChannelTabMetas([sessionTab], entries, "channel:ch_notify");
ok(merged.length === entries.length + 1, "channel pseudo tabs append after session tabs");
const pseudo = merged[merged.length - 1];
ok(pseudo.id === "channel:ch_notify", "the pseudo tab id carries the channel anchor prefix");
ok((pseudo as { scope?: string }).scope === "channel", "scope=channel marks the same-shaped entity's data source");
ok(pseudo.topicTitle === "通知群", "topicTitle carries the group name (tabDisplayTitle 消费)");
ok(pseudo.active === true, "the active channel tab projects as active");
ok(pseudo.running === false && !pseudo.sessionPath && !pseudo.topicId, "no session machinery fields (预取/hydration/租约对伪实体 no-op)");
const mergedInactive = mergeChannelTabMetas([sessionTab], entries, null);
ok(mergedInactive[mergedInactive.length - 1].active === false, "no activation = pseudo tabs render inactive");
ok(channelTabMeta(entries[0], false).id.startsWith("channel:"), "channelTabMeta keeps the anchor id shape");

// ── ③ ChannelWorkspacePanel（tab 工作区复用 + 数据源换 channel）─────────
type Msg = import("../lib/bridge").CollabChannelMessageView;
const workspaceChannel = {
  id: "ch_ws", name: "工作区群", topic: "tab 形态", createdAt: 1, hourlyLimit: 30,
  members: [HUMAN_LOCAL, "sc_dev"], messages: 1, ownerType: "human", ownerId: HUMAN_LOCAL,
  memberDetails: [
    { contact: HUMAN_LOCAL, type: "human", joinedAt: 1 },
    { contact: "sc_dev", type: "session", joinedAt: 2 },
  ],
};
const wsMessages: Msg[] = [
  { id: "m1", sender: "sc_dev", body: "agent 汇报进度", at: 1_700_000_003_000 },
  { id: "m2", sender: HUMAN_LOCAL, body: "收到", at: 1_700_000_004_000 },
];
const wsTails: Array<{ channel: typeof workspaceChannel; messages: Msg[] }> = [
  { channel: workspaceChannel, messages: wsMessages },
  { channel: { ...workspaceChannel, messages: 2 }, messages: [...wsMessages, { id: "m3", sender: "sc_dev", body: "补一条", at: 1_700_000_005_000 }] },
];
let tailReads = 0;
let sentBodies: string[] = [];
const recalledIds = new Set<string>();
const wsBindings: ChannelWorkspaceBindings = {
  ChannelMessagesForHuman: async () => {
    tailReads += 1;
    const snap = wsTails[Math.min(tailReads, wsTails.length) - 1];
    // 撤回后的尾窗读数如实返回墓碑态（否则 refresh 对账会盖掉乐观墓碑）。
    return {
      channel: snap.channel,
      messages: snap.messages.map((m) => (recalledIds.has(m.id) ? { ...m, cancelledAt: 1 } : m)),
    };
  },
  ChannelSendFromHuman: async (_ref, body) => {
    sentBodies.push(body);
    return { id: `sent:${sentBodies.length}`, sender: HUMAN_LOCAL, body, at: Date.now() };
  },
  ChannelCancelFromHuman: async (_r, messageId) => { recalledIds.add(messageId); return {} as Awaited<ReturnType<ChannelChatBindings["ChannelCancelFromHuman"]>>; },
  ChannelInvite: async (_r, ids) => ({ ...workspaceChannel, members: [...workspaceChannel.members, ...ids] }),
  ChannelRemoveMember: async () => workspaceChannel,
  ChannelSetDeputy: async () => workspaceChannel,
  ChannelUnsetDeputy: async () => workspaceChannel,
  ChannelLeaveHuman: async () => undefined,
  ChannelDissolveForHuman: async () => undefined,
  ListAddressableSessions: async () => [{ contactId: "sc_dev", title: "开发会话", scope: "global", archived: false, sessionPath: "/s/sc_dev.jsonl" }],
  // tl1441 ③：群聊附件预览读回（工作区绑定面成员；缺省时流内缩略图不渲染）。
  ChannelAttachmentDataURL: async () => "data:image/png;base64,iVBORw0KGgo=",
};

const rootEl = dom.window.document.getElementById("root")!;
const root = createRoot(rootEl);
let backToListCalls = 0;
let openSessionCalls: string[] = [];
root.render(
  React.createElement(
    LocaleProvider,
    null,
    React.createElement(ChannelWorkspacePanel, {
      channelRef: "ch_ws",
      fallbackName: "工作区群",
      bindings: wsBindings,
      onBackToList: () => { backToListCalls += 1; },
      onOpenSession: (contactId: string) => { openSessionCalls.push(contactId); },
    }),
  ),
);
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); });

const ws = () => dom.window.document.querySelector(".channel-workspace");
ok(ws(), "the channel workspace mounts as the tab surface");
ok(ws()?.getAttribute("data-channel-ref") === "ch_ws", "the workspace surface is bound to the channel ref (数据源换 channel)");

// 顶部栏（会话区顶部栏位置承载群聊特有功能——升级口径③）。
const topbar = dom.window.document.querySelector(".channel-workspace__topbar")!;
ok(topbar.querySelector(".channel-workspace__backlist"), "the topbar carries the back-to-list affordance");
ok(topbar.querySelector(".channel-workspace__name")?.textContent === "工作区群", "the topbar shows the group name");
ok(topbar.querySelector(".channel-workspace__topic")?.textContent === "tab 形态", "the topbar shows the topic");
ok(topbar.textContent?.includes("2 members"), "the topbar shows the member count (harness 默认 en locale)");
const detailToggle = topbar.querySelector(".channel-workspace__detailtoggle");
ok(detailToggle, "the topbar carries the detail/members toggle (详情/成员管理入口)");

// 消息流（channel 数据源渲染，共用件同契约）。
const stream = dom.window.document.querySelector(".channel-workspace__stream")!;
ok(stream.querySelector(".channel-chat__bubble"), "the workspace stream renders channel bubbles");
ok(stream.querySelectorAll(".channel-chat__row").length === 2, "the tail window renders both messages");
ok(stream.textContent?.includes("agent 汇报进度"), "agent messages render with their body");

// 发送桥接 channel_send（mock 绑定收口）——tl1441 ② 换装 Composer 群聊模式
// 后，输入面是 Composer（channelMode），发送经 onSend 桥接。
const composerHost = dom.window.document.querySelector(".channel-workspace__composer")!;
ok(composerHost.getAttribute("data-tl1441-composer") === "channel-mode", "the composer slot is the Composer channel-mode mount (②换装)");
const input = composerHost.querySelector(".composer__input")! as HTMLTextAreaElement;
const sendBtn = composerHost.querySelector(".composer__btn--send")! as HTMLButtonElement;
ok(input && sendBtn, "the Composer channel-mode input area is mounted");
// React 受控组件：focus + 原生 setter + 选区 + InputEvent（composer 族 harness 同款）。
const typeIntoComposer = async (value: string): Promise<void> => {
  await act(async () => {
    input.focus();
    const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLTextAreaElement.prototype, "value")!.set!;
    setter.call(input, value);
    input.setSelectionRange(value.length, value.length);
    input.dispatchEvent(new dom.window.InputEvent("input", { bubbles: true, data: value, inputType: "insertText", isComposing: false }));
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
};
await typeIntoComposer("你好群");
await act(async () => { sendBtn.click(); await new Promise((resolve) => setTimeout(resolve, 10)); });
ok(sentBodies.includes("你好群"), "sending bridges to ChannelSendFromHuman (发送桥接 channel_send)");
ok(ws()?.querySelectorAll(".channel-chat__row").length === 3, "the sent message lands optimistically then reconciles");

// 详情抽屉（共用件 ChannelDetailDrawer）。
await act(async () => { (detailToggle as HTMLButtonElement).click(); await new Promise((resolve) => setTimeout(resolve, 10)); });
const drawer = dom.window.document.querySelector(".channel-chat__detail");
ok(drawer, "the detail drawer opens from the workspace topbar");
ok(drawer?.textContent?.includes("开发会话"), "the drawer resolves member display names from the directory");
ok(drawer?.textContent?.includes("Leave group"), "the drawer offers leave");
await act(async () => { dom.window.document.body.click(); await new Promise((resolve) => setTimeout(resolve, 10)); });
ok(!dom.window.document.querySelector(".channel-chat__detail"), "clicking outside dismisses the drawer (house 形态同款)");

// 右键撤回（own 消息）。
const ownBubble = Array.from(dom.window.document.querySelectorAll(".channel-chat__row--own .channel-chat__bubble"))[0] as HTMLElement;
ok(ownBubble, "an own bubble is present for the recall path");
await act(async () => { ownBubble.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 5, clientY: 5 })); await new Promise((resolve) => setTimeout(resolve, 10)); });
const ctxMenu = dom.window.document.querySelector(".channel-chat__menu");
ok(ctxMenu, "right-clicking an own bubble opens the recall menu");
await act(async () => {
  Array.from(dom.window.document.querySelectorAll(".channel-chat__menu button")).find((b) => b.textContent === "Recall")?.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
  await new Promise((resolve) => setTimeout(resolve, 10));
});
ok(dom.window.document.querySelector(".channel-chat__tombstone"), "recalling renders the tombstone (349 Cancel 语义)");

// 返回列表（口径③：返回列表入口）。
await act(async () => {
  (dom.window.document.querySelector(".channel-workspace__backlist") as HTMLButtonElement).click();
  await new Promise((resolve) => setTimeout(resolve, 5));
});
ok(backToListCalls === 1, "back-to-list routes through the injected callback (App 拦截切回会话工作区)");

// ── ①「打开标签页」按钮（ChannelChatPanel chathead）─────────────────────
activateChannelTab(null);
const before = loadOpenChannelTabs().length;
const panelBindings: Partial<ChannelChatBindings> = {
  ListCollabChannels: async () => [{
    id: "ch_ws", name: "工作区群", createdAt: 1, hourlyLimit: 30,
    members: [HUMAN_LOCAL, "sc_dev"], messages: 1, ownerType: "human", ownerId: HUMAN_LOCAL,
    memberDetails: [
      { contact: HUMAN_LOCAL, type: "human" as const, joinedAt: 1 },
      { contact: "sc_dev", type: "session" as const, joinedAt: 2 },
    ],
  }],
  ReadCollabChannel: async () => ({ channel: workspaceChannel, messages: wsMessages }),
  ChannelMessagesForHuman: async () => ({ channel: workspaceChannel, messages: wsMessages }),
  ChannelSendFromHuman: async () => ({ id: "sx", sender: HUMAN_LOCAL, body: "x", at: 1 }),
  ChannelCreateForHuman: async () => workspaceChannel,
  ChannelLeaveHuman: async () => undefined,
  ChannelDissolveForHuman: async () => undefined,
  ChannelInvite: async () => workspaceChannel,
  ChannelRemoveMember: async () => workspaceChannel,
  ChannelSetDeputy: async () => workspaceChannel,
  ChannelUnsetDeputy: async () => workspaceChannel,
  ChannelCancelFromHuman: async () => ({} as Awaited<ReturnType<ChannelChatBindings["ChannelCancelFromHuman"]>>),
  ListAddressableSessions: async () => [],
};
root.render(React.createElement("div"));
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
root.render(
  React.createElement(
    LocaleProvider,
    null,
    React.createElement(ChannelChatPanel, { bindings: panelBindings as ChannelChatBindings }),
  ),
);
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); });
const chatRoot = () => dom.window.document.querySelector(".channel-chat")!;
await act(async () => {
  (chatRoot().querySelector(".channel-chat__item") as HTMLButtonElement).click();
  await new Promise((resolve) => setTimeout(resolve, 10));
});
const openTabBtn = chatRoot().querySelector(".channel-chat__opentab") as HTMLButtonElement;
ok(openTabBtn && openTabBtn.textContent === "Open in tab", "the group chathead offers the open-in-tab button (口径①；harness 默认 en，三语见文件级断言)");
await act(async () => { openTabBtn.click(); await new Promise((resolve) => setTimeout(resolve, 10)); });
ok(loadOpenChannelTabs().length === before + 1, "clicking open-in-tab registers the channel tab");
ok(loadActiveChannelTabId() === "channel:ch_ws", "open-in-tab activates the tab (App 层据此切工作区面)");

// 消息流共用件直测：@名字 快捷入口（tab 工作区无对话链通道=纯文本渲染）。
{
  const el = dom.window.document.createElement("div");
  const mentionRoot = createRoot(el);
  let mentionClicks = 0;
  await act(async () => {
    mentionRoot.render(React.createElement(
      LocaleProvider,
      null,
      React.createElement(ChannelMessageStream, {
        messages: [{ id: "mm", sender: "sc_dev", body: "hello @You", at: 1 }],
        memberDetails: [{ contact: HUMAN_LOCAL, type: "human", joinedAt: 1 }],
        directoryTitle: () => "You",
        onMentionClick: () => { mentionClicks += 1; },
      }),
    ));
    await new Promise((resolve) => setTimeout(resolve, 10));
  });
  const mention = el.querySelector(".channel-chat__mention") as HTMLElement;
  ok(mention, "mention needles render as the quick-entry span");
  await act(async () => { mention.click(); });
  ok(mentionClicks === 1, "mention clicks route through the injected handler");
  mentionRoot.render(React.createElement(
    LocaleProvider,
    null,
    React.createElement(ChannelMessageStream, {
      messages: [{ id: "mm", sender: "sc_dev", body: "hello @You", at: 1 }],
      memberDetails: [{ contact: HUMAN_LOCAL, type: "human", joinedAt: 1 }],
      directoryTitle: () => "You",
    }),
  ));
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 10)); });
  // 与原 ChannelChatPanel 语义同价：@名字 span 恒渲染（handler 缺省=点击 no-op，
  // 同 onViewDirectChains 缺省行为）——tab 工作区不传 handler 即此形态。
  ok(el.querySelectorAll(".channel-chat__mention").length === 1, "the mention span still renders without a handler (原语义同价)");
  await act(async () => { (el.querySelector(".channel-chat__mention") as HTMLElement).click(); });
  ok(mentionClicks === 1, "clicks stay no-op without the injected handler");
}

root.render(React.createElement("div"));
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });

// ── ⑤ App 接线钉（源码级断言——App 级挂载不进组件 harness）──────────────
const appSrc = fs.readFileSync(path.join(import.meta.dirname, "..", "App.tsx"), "utf-8");
for (const needle of [
  "mergeChannelTabMetas(visibleTabs, channelTabEntries, activeChannelTabIdState)",
  "if (isChannelTabId(id)) {",
  "closeChannelTabs([id]);",
  "activateChannelTab(nextActiveTabId);",
  "const sessionIds = ids.filter((id) => !isChannelTabId(id));",
  "if (isChannelTabId(tabId)) return;",
  "<ChannelWorkspacePanel",
  "activateChannelTab(null); setCollabGroupOpen(true);",
  "automationView || channelSurfaceActive ? false : workspacePanelRenderable",
]) {
  ok(appSrc.includes(needle), `App wiring pin: ${needle.slice(0, 48)}…`);
}
const workspaceSrc = fs.readFileSync(path.join(import.meta.dirname, "..", "components", "ChannelWorkspacePanel.tsx"), "utf-8");
ok(workspaceSrc.includes('data-tl1441-composer="channel-mode"'), "the composer slot stays on its test anchor (②已换装 Composer 群聊模式)");

// ── ⑥ 三语文案 ──────────────────────────────────────────────────────────
const zh = fs.readFileSync(path.join(import.meta.dirname, "..", "locales", "zh.ts"), "utf-8");
const en = fs.readFileSync(path.join(import.meta.dirname, "..", "locales", "en.ts"), "utf-8");
const zhTw = fs.readFileSync(path.join(import.meta.dirname, "..", "locales", "zh-TW.ts"), "utf-8");
ok(zh.includes('"collabGroup.chat.openTab": "打开标签页"'), "zh: 打开标签页");
ok(en.includes('"collabGroup.chat.openTab": "Open in tab"'), "en: Open in tab");
ok(zhTw.includes('"collabGroup.chat.openTab": "開啟分頁"'), "zh-TW: 開啟分頁");
ok(zh.includes('"channelWorkspace.title"') && en.includes('"channelWorkspace.title"') && zhTw.includes('"channelWorkspace.title"'), "channelWorkspace.title exists in all three locales");

console.log(`tl1441-channel-tab: ${passed} assertions passed`);
