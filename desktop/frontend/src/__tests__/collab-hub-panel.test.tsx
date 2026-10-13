// Run: tsx src/__tests__/collab-hub-panel.test.tsx
// 任务 856 ①（壳与融合）acceptance on the panel side:
//   ① 双视图切换 + localStorage 持久化（collabHub.view；重开恢复上次视图）；
//   ② 两图标合一后的容器接线：信件视图嵌 CollabInboxPanel（embedded，无自身
//      头部）、聊天视图嵌 ChannelChatPanel（802 复用面原样）；
//   ③ 外部打开信号重定向：setCollabInboxOpen(true)/setCollabGroupOpen(true)
//      打开 Hub 并切到对应视图（调用方零改动——856 计划风险面）；
//   ④ 409 观察窗降级：聊天视图「协作动态」次级按钮弹出 CollabGroupPanel
//      （embedded + sessionsOnly），观察窗行照常渲染（断言随 UI 演进迁移）；
//   ⑤ 关闭 Hub → 子面板模块 open 同步复位（未读徽标刷新时机保持）。
// The Go tests own the data contracts; this harness owns the shell wiring.

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
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { CollabHubPanel, setCollabHubOpen, isCollabHubOpen } = await import("../components/CollabHubPanel");
const { setCollabInboxOpen, isCollabInboxOpen } = await import("../components/CollabInboxPanel");
const { setCollabGroupOpen, isCollabGroupOpen } = await import("../components/CollabGroupPanel");
const { LocaleProvider } = await import("../lib/i18n");

const root = createRoot(document.getElementById("root")!);
await act(async () => {
  root.render(
    <LocaleProvider>
      <CollabHubPanel />
    </LocaleProvider>,
  );
});

const hub = () => document.querySelector(".collab-hub-panel");
const segButtons = () => Array.from(document.querySelectorAll<HTMLButtonElement>(".collab-hub-panel__view"));

// ① 默认视图 = 信件（首访无持久化值）；信件视图渲染 embedded 收件箱（无自身头部）。
setCollabHubOpen(true);
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.ok(hub(), "the hub panel renders once opened");
assert.ok(document.querySelector(".collab-inbox-panel"), "the mail view embeds the inbox panel");
assert.equal(document.querySelector(".collab-inbox-panel__head"), null, "the embedded inbox hides its own shell head (the hub owns the chrome)");
assert.equal(segButtons().length, 2, "the hub shell renders exactly two view tabs");

// ① 切到聊天：ChannelChatPanel 原样挂载（802 复用面）；持久化键落 localStorage。
await act(async () => { segButtons().find((b) => b.textContent === "Chat")!.click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.ok(document.querySelector(".channel-chat"), "the chat view mounts the WeChat-style chat panel (802 reuse face)");
assert.equal(window.localStorage.getItem("collabHub.view"), "chat", "the view choice persists to localStorage collabHub.view");
assert.equal(segButtons().find((b) => b.textContent === "Chat")!.getAttribute("aria-selected"), "true", "the chat tab is marked selected");

// ① 关闭再开：恢复上次视图（聊天）。
await act(async () => {
  (document.querySelector(".collab-hub-panel__close") as HTMLButtonElement).click();
});
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.equal(hub(), null, "closing unmounts the hub");
assert.equal(isCollabHubOpen(), false, "the module-level open flag is off");
assert.equal(isCollabInboxOpen(), false, "closing the hub resets the inbox module open (badge refresh timing)");
assert.equal(isCollabGroupOpen(), false, "closing the hub resets the group module open");
setCollabHubOpen(true);
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.ok(document.querySelector(".channel-chat"), "reopening restores the last view (chat)");

// ③ 外部信号重定向：既有调用方零改动——setCollabInboxOpen(true) 打开 Hub 并
// 切信件视图（即使 Hub 已开在聊天视图，同值信号也切）。
await act(async () => { setCollabInboxOpen(true); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.ok(document.querySelector(".collab-inbox-panel"), "the legacy inbox-open signal lands in the hub");
assert.equal(segButtons().find((b) => b.textContent === "Mail")!.getAttribute("aria-selected"), "true", "the legacy inbox-open signal switches to the mail view");

// ③ 群聊信号同理切聊天视图。
await act(async () => { setCollabGroupOpen(true); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.ok(document.querySelector(".channel-chat"), "the legacy group-open signal lands in the hub");
assert.equal(segButtons().find((b) => b.textContent === "Chat")!.getAttribute("aria-selected"), "true", "the legacy group-open signal switches to the chat view");

// ④ 409 观察窗降级：聊天视图「协作动态」按钮弹出 embedded 观察窗；bridge
// mock 是诚实的空目录（802 口径：空态 ≠ 错误），行级渲染由注入 bindings 的
// 独立挂载段断言（随 UI 演进迁移自 collab-group-panel.test.tsx）。
const activityButton = Array.from(document.querySelectorAll<HTMLButtonElement>(".collab-chat-view__tools .btn"))
  .find((b) => b.textContent === "Collab activity");
assert.ok(activityButton, "the chat view offers the degraded activity entry");
await act(async () => { activityButton!.click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 50)); });
const embedded = document.querySelector(".collab-chat-view__activity .collab-group-panel--embedded");
assert.ok(embedded, "the activity toggle renders the embedded observation window");
assert.match(embedded!.textContent ?? "", /No active collaboration sessions/, "the mock's honest empty directory renders the empty state, not an error");
assert.equal(document.querySelectorAll(".collab-group-panel__tab").length, 0, "the embedded window carries no channel tab (sessions-only downgrade)");
await act(async () => {
  (document.querySelector(".collab-chat-view__activityhead .btn") as HTMLButtonElement).click();
});
assert.equal(document.querySelector(".collab-chat-view__activity"), null, "collapsing the entry unmounts the window (zero cost when closed)");

// ① 切回信件视图再关闭：视图持久化跟随最后一次选择。
await act(async () => { segButtons().find((b) => b.textContent === "Mail")!.click(); });
assert.equal(window.localStorage.getItem("collabHub.view"), "mail", "switching back persists immediately");
setCollabHubOpen(false);
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.equal(hub(), null, "final close unmounts the hub");

// ④（行级迁移）embedded + sessionsOnly 形态在注入数据下照常渲染观察窗行
// （空态/失败态语义由 collab-group-panel.test.tsx 的独立模式断言继续覆盖）。
const { CollabGroupPanel } = await import("../components/CollabGroupPanel");
type CollabGroupBindings = import("../components/CollabGroupPanel").CollabGroupBindings;
type CollabViewSession = import("../components/CollabGroupPanel").CollabViewSession;
const sessionRow = (over: Partial<CollabViewSession> & { contactId: string }): CollabViewSession => ({
  title: over.contactId,
  state: "unknown",
  lastActivity: 1_700_000_000_000,
  unreadInbox: 0,
  ...over,
});
const observedBindings: CollabGroupBindings = {
  async GetCollabViewOverview() {
    return {
      sessions: [sessionRow({ contactId: "sc_main", title: "主对话", state: "running" })],
      generatedAt: 1_700_000_003_000,
    };
  },
  async ListCollabChannels() { return []; },
  async ReadCollabChannel() { return { channel: null as never, messages: [] }; },
  async ChannelMessagesForHuman() { return { channel: null as never, messages: [] }; },
  async ChannelCreateForHuman() { throw new Error("not wired in this harness"); },
  async ChannelJoinHuman() { throw new Error("not wired in this harness"); },
  async ChannelLeaveHuman() { throw new Error("not wired in this harness"); },
  async ChannelSendFromHuman() { throw new Error("not wired in this harness"); },
  async ChannelInvite() { throw new Error("not wired in this harness"); },
  async ChannelRemoveMember() { throw new Error("not wired in this harness"); },
  async ChannelCancelFromHuman() { throw new Error("not wired in this harness"); },
  async ListAddressableSessions() { return []; },
};
await act(async () => {
  root.render(
    <LocaleProvider>
      <CollabGroupPanel embedded sessionsOnly bindings={observedBindings} />
    </LocaleProvider>,
  );
});
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
const windowEl = document.querySelector(".collab-group-panel--embedded");
assert.ok(windowEl, "the embedded window mounts standalone for the row-level check");
assert.match(windowEl!.textContent ?? "", /主对话/, "the observation window renders session rows in embedded form");
assert.equal(document.querySelectorAll(".collab-group-panel__tab").length, 0, "sessions-only: no tabs even with data");

// 收尾卸载：观察窗的 30s 轮询 interval 随 unmount 清理，进程才能退出。
await act(async () => { root.unmount(); });

console.log("collab-hub-panel: all assertions passed");
