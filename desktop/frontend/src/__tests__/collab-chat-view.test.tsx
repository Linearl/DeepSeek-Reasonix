// Run: tsx src/__tests__/collab-chat-view.test.tsx
// 任务 856 ③（对话链通道 + ⑦右键跳转）acceptance on the view side:
//   ① 通道切换：对话链（单聊）/ 群聊 两 tab；群聊态渲染 ChannelChatPanel 原样；
//   ② 对话链列表：ListCollabDirectChains 读口驱动（会话对渲染，链名截断
//      带 title——用户 1138 hover 要求）；human 端点若出现读口里也不渲染名字
//      之外的任何跳转（v2 口径：human 不经协作中心）；
//   ③ 只读回放：点链 → ReadCollabDirectChain 双向时间线（左右分列+来源会话
//      标+收信方行），无输入框，只读提示条含「直接打开该会话即可」语义；
//   ④ ⑦右键菜单：消息右键两项「打开发信方/收信方会话」，点击经模块信号
//      onCollabChatOpenSession 送达（App 注册缝）；Esc/点击外部关闭。
// The Go tests own the aggregation contracts (direct_chain_test.go); this
// harness owns the channel wiring and the replay rendering.

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

const calls: { name: string; args: unknown[] }[] = [];
const pairs = [
  { pairId: "sc_a\u0000sc_b", a: "sc_a", b: "sc_b", count: 2, firstAt: 1_700_000_000_000, lastAt: 1_700_000_100_000, preview: "ack, slice committed" },
  { pairId: "sc_c\u0000sc_d", a: "sc_c", b: "sc_d", count: 1, firstAt: 1_700_000_050_000, lastAt: 1_700_000_050_000, preview: "branch kept, not merged" },
];
const replayEntries = [
  { id: "r1", from: "sc_a", to: "sc_b", at: 1_700_000_000_000, threadId: "r1", bucket: "mention", preview: "dispatch the slice", delivered: true, read: true },
  { id: "r2", from: "sc_b", to: "sc_a", at: 1_700_000_100_000, threadId: "r2", bucket: "mention", preview: "ack, slice committed", delivered: true, read: true },
];

(window as { go?: unknown }).go = {
  main: {
    App: {
      ListCollabDirectChains: (limit: number) => {
        calls.push({ name: "ListCollabDirectChains", args: [limit] });
        return Promise.resolve({ revision: "1.0.0", total: pairs.length, pairs });
      },
      ReadCollabDirectChain: (pairId: string, limit: number) => {
        calls.push({ name: "ReadCollabDirectChain", args: [pairId, limit] });
        return Promise.resolve({ pairId, a: "sc_a", b: "sc_b", entries: replayEntries });
      },
      ListAddressableSessions: () => {
        calls.push({ name: "ListAddressableSessions", args: [] });
        return Promise.resolve([
          { contactId: "sc_a", title: "主对话-开发线", sessionPath: "/w/a", topicId: "ta", scope: "project", open: true },
          { contactId: "sc_b", title: "fork开发-新6", sessionPath: "/w/b", topicId: "tb", scope: "project", open: true },
        ]);
      },
      ListCollabChannels: () => Promise.resolve([]),
      ReadCollabChannel: () => Promise.resolve({ channel: null as never, messages: [] }),
      ChannelMessagesForHuman: () => Promise.resolve({ channel: null as never, messages: [] }),
      ChannelCreateForHuman: () => Promise.reject(new Error("not wired")),
      ChannelJoinHuman: () => Promise.reject(new Error("not wired")),
      ChannelLeaveHuman: () => Promise.reject(new Error("not wired")),
      ChannelSendFromHuman: () => Promise.reject(new Error("not wired")),
      ChannelInvite: () => Promise.reject(new Error("not wired")),
      ChannelRemoveMember: () => Promise.reject(new Error("not wired")),
      ChannelCancelFromHuman: () => Promise.reject(new Error("not wired")),
    },
  },
};

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { CollabChatView, onCollabChatOpenSession } = await import("../components/CollabChatView");
const { LocaleProvider } = await import("../lib/i18n");

const jumps: string[] = [];
onCollabChatOpenSession((contactId) => { jumps.push(contactId); });

const root = createRoot(document.getElementById("root")!);
await act(async () => {
  root.render(
    <LocaleProvider>
      <CollabChatView />
    </LocaleProvider>,
  );
});
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });

const view = document.querySelector(".collab-chat-view");
assert.ok(view, "the chat view renders");
const tabs = () => Array.from(document.querySelectorAll<HTMLButtonElement>(".collab-chat-view__tab"));

// ① 默认通道=群聊（ChannelChatPanel 原样挂载）；对话链 tab 存在。
assert.ok(document.querySelector(".channel-chat"), "the groups channel mounts the 802 chat panel by default");
assert.equal(tabs().length, 2, "exactly two channel tabs (directs / groups)");

// ① 切到对话链：列表读口被拉起（limit=100），两条会话对渲染，链名带 title。
await act(async () => { tabs().find((b) => b.textContent === "Direct chains")!.click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.ok(calls.some((c) => c.name === "ListCollabDirectChains" && c.args[0] === 100), "opening the directs channel reads the aggregation port");
const dirItems = Array.from(document.querySelectorAll<HTMLElement>(".collab-chat-view__diritem"));
assert.equal(dirItems.length, 2, "two session-pair chains render");
const nameEl = dirItems[0].querySelector<HTMLElement>(".collab-chat-view__dirname")!;
assert.ok(nameEl.title.includes("↔"), "the truncated chain name carries a full hover title (用户 1138)");
assert.match(dirItems[0].textContent ?? "", /ack, slice committed/, "the newest pair's preview renders");

// ③ 打开一条链：只读回放（双向时间线、来源会话标、收信方行、无输入框）。
await act(async () => { dirItems[0].click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
const readCall = calls.find((c) => c.name === "ReadCollabDirectChain");
assert.ok(readCall && readCall.args[0] === "sc_a\u0000sc_b", "opening a chain reads its replay by pairId");
const msgs = Array.from(document.querySelectorAll<HTMLElement>(".collab-chat-view__msg"));
assert.equal(msgs.length, 2, "the replay renders both directions");
assert.ok(msgs[0].dataset.from === "sc_a" && msgs[0].dataset.to === "sc_b", "each replay line carries data-from/data-to (⑦ wiring)");
assert.equal(msgs[0].className.includes("collab-chat-view__msg--right"), false, "the pair-a side renders left");
assert.equal(msgs[1].className.includes("collab-chat-view__msg--right"), true, "the pair-b side renders right");
assert.match(msgs[1].textContent ?? "", /→/, "each line shows its recipient (来源会话标/收信方)");
assert.equal(document.querySelector(".collab-chat-view__stream input, .collab-chat-view__stream textarea"), null, "the replay has NO composer (read-only)");
assert.match(document.querySelector(".collab-chat-view__readonly")!.textContent ?? "", /open that session directly|直接打开该会话/i, "the read-only strip carries the「直接打开该会话即可」semantic");

// ④ ⑦ 右键菜单：两项跳转，点击发信方 → 模块信号送达。
const bubble = msgs[0].querySelector<HTMLElement>(".collab-chat-view__bubble")!;
bubble.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 20, clientY: 20 }));
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
const ctx = document.querySelector(".collab-chat-view__ctx");
assert.ok(ctx, "right-clicking a replay message opens the jump menu");
const ctxButtons = Array.from(ctx!.querySelectorAll<HTMLButtonElement>("button"));
assert.equal(ctxButtons.length, 2, "exactly two jump items (sender / recipient)");
assert.match(ctxButtons[0].textContent ?? "", /fork开发-新6|sc_b|主对话-开发线|sc_a/, "the menu items name their target sessions");
await act(async () => { ctxButtons[0].click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.equal(document.querySelector(".collab-chat-view__ctx"), null, "picking an item closes the menu");
assert.equal(jumps.length, 1, "the jump signal reached the App-registered seam");
assert.notEqual(jumps[0], "human:local", "a human endpoint never jumps through the hub (v2 口径)");

// ④ Esc 关闭。
bubble.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 20, clientY: 20 }));
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.ok(document.querySelector(".collab-chat-view__ctx"), "the menu reopens");
document.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.equal(document.querySelector(".collab-chat-view__ctx"), null, "Esc closes the menu");

// ① 切回群聊：对话链半边卸载（零成本）。
await act(async () => { tabs().find((b) => b.textContent === "Group chat")!.click(); });
assert.ok(document.querySelector(".channel-chat"), "switching back remounts the group channel");
assert.equal(document.querySelector(".collab-chat-view__dirlist"), null, "the directs half unmounts (zero cost)");

await act(async () => { root.unmount(); });
console.log("collab-chat-view: all assertions passed");
