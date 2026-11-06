// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1439-collab-chat-memory.test.tsx
//
// tl1439 验收（协作中心聊天面板两小修）：
//   修②「打开面板自动恢复上次会话」：
//   ① 首访无记录 = 现状默认视图（群聊 tab + 占位，不代开）；
//   ② 打开协作中心→聊天 tab：自动进入上次打开的群/对话链（重开面板=重挂即
//      恢复，等价跨重启路径——记忆走 localStorage，同 collabHub.view 模式）；
//   ③ 删除/归档了上次记忆的群/链 → 优雅回落默认视图（不报错不空白）；
//   ④ 显式回到列表（群返回键/对话链 tab 重击）即清对应记忆，重开不再代开；
//   ⑤ 损坏存储值降级默认（不炸）；
//   ⑥ 信件 tab 零涉及：记忆键全部在 collabChat.* 独立命名空间（本文件断言键
//      名），collabHub.view 不被读写。
//   修①「消息贴边内移」（styles.css 静态断言）：
//   ⑦ .channel-chat__stream 水平内边距与对话链回放流 .collab-chat-view__stream
//      同值（12px 16px），两视图留白一致、不再贴边。
// Go 侧无涉及（纯前端记忆）；渲染接线由本 harness 持有。

import * as React from "react"; // tsconfig exclude 了 __tests__，tsx 对本文件走 classic JSX——必须显式绑定 React
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
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
const { LocaleProvider } = await import("../lib/i18n");
const { CollabChatView } = await import("../components/CollabChatView");
const {
  readCollabChatTab,
  readCollabChatLastPairId,
  readCollabChatLastChannelId,
} = await import("../lib/collabChatMemory");
type CollabChannelView = import("../lib/bridge").CollabChannelView;
type CollabChannelMessageView = import("../lib/bridge").CollabChannelMessageView;

// ── 键名契约：独立命名空间，信件视图（collabHub.view）零涉及 ──────────────
assert.equal(readCollabChatTab(), "groups", "fresh storage falls back to the groups tab (current default)");
assert.equal(readCollabChatLastPairId(), null, "fresh storage has no directs pair memory");
assert.equal(readCollabChatLastChannelId(), null, "fresh storage has no groups channel memory");

// ── 内存 bridge（群 ×2 + 对话链 ×2 + 回放）───────────────────────────────
const calls: { name: string; args: unknown[] }[] = [];
const chA: CollabChannelView = {
  id: "ch_a", name: "发布协调", createdAt: 1_699_000_000_000,
  hourlyLimit: 30, members: ["human:local", "sc_dev"], messages: 1,
  ownerType: "human", ownerId: "human:local",
  memberDetails: [
    { contact: "human:local", type: "human", joinedAt: 1_699_000_000_000 },
    { contact: "sc_dev", type: "session", joinedAt: 1_699_000_001_000 },
  ],
};
const chB: CollabChannelView = {
  id: "ch_b", name: "合并线", createdAt: 1_699_000_003_000,
  hourlyLimit: 30, members: ["sc_owner", "human:local"], messages: 0,
  ownerType: "session", ownerId: "sc_owner",
};
const pairP1 = "sc_a\u0000sc_b";
const pairs = [
  { pairId: pairP1, a: "sc_a", b: "sc_b", count: 2, firstAt: 1_700_000_000_000, lastAt: 1_700_000_100_000, preview: "ack, slice committed" },
  { pairId: "sc_c\u0000sc_d", a: "sc_c", b: "sc_d", count: 1, firstAt: 1_700_000_050_000, lastAt: 1_700_000_050_000, preview: "branch kept, not merged" },
];
// 对话链回放条目（CollabMailEntryView 形状）与群聊尾窗消息（CollabChannelMessageView
// 形状）是两种读口——分开建模，不混用。
type CollabMailEntryView = import("../lib/bridge").CollabMailEntryView;
const chainEntries: CollabMailEntryView[] = [
  { id: "r1", from: "sc_a", to: "sc_b", at: 1_700_000_000_000, threadId: "r1", bucket: "mention", preview: "dispatch the slice" },
  { id: "r2", from: "sc_b", to: "sc_a", at: 1_700_000_100_000, threadId: "r2", bucket: "mention", preview: "ack, slice committed" },
];
const channelTail: CollabChannelMessageView[] = [
  { id: "m1", sender: "sc_dev", body: "sliding window landed", at: 1_700_000_020_000, fanout: [] },
];

(window as { go?: unknown }).go = {
  main: {
    App: {
      ListCollabChannels: () => {
        calls.push({ name: "ListCollabChannels", args: [] });
        return Promise.resolve([chA, chB]);
      },
      ReadCollabChannel: (ref: string, limit: number) => {
        calls.push({ name: "ReadCollabChannel", args: [ref, limit] });
        return Promise.resolve({ channel: ref === "ch_a" ? chA : chB, messages: [] });
      },
      ChannelMessagesForHuman: (ref: string, sinceMs: number, limit: number) => {
        calls.push({ name: "ChannelMessagesForHuman", args: [ref, sinceMs, limit] });
        const channel = ref === "ch_a" ? chA : chB;
        return Promise.resolve({ channel, messages: ref === "ch_a" ? channelTail : [] });
      },
      ListCollabDirectChains: (limit: number) => {
        calls.push({ name: "ListCollabDirectChains", args: [limit] });
        return Promise.resolve({ revision: "1.0.0", total: pairs.length, pairs });
      },
      ReadCollabDirectChain: (pairId: string, limit: number) => {
        calls.push({ name: "ReadCollabDirectChain", args: [pairId, limit] });
        return Promise.resolve({ pairId, a: "sc_a", b: "sc_b", entries: chainEntries });
      },
      ListAddressableSessions: () => Promise.resolve([
        { contactId: "sc_a", title: "主对话-开发线", sessionPath: "/w/a", topicId: "ta", scope: "project", open: true },
        { contactId: "sc_b", title: "fork开发-新6", sessionPath: "/w/b", topicId: "tb", scope: "project", open: true },
      ]),
      GetCollabViewOverview: () => Promise.resolve({ sessions: [], generatedAt: 0 }),
      ChannelCreateForHuman: () => Promise.reject(new Error("not wired")),
      ChannelJoinHuman: () => Promise.reject(new Error("not wired")),
      ChannelLeaveHuman: () => Promise.reject(new Error("not wired")),
      ChannelDissolveForHuman: () => Promise.reject(new Error("not wired")),
      ChannelSendFromHuman: () => Promise.reject(new Error("not wired")),
      ChannelInvite: () => Promise.reject(new Error("not wired")),
      ChannelRemoveMember: () => Promise.reject(new Error("not wired")),
      ChannelSetDeputy: () => Promise.reject(new Error("not wired")),
      ChannelUnsetDeputy: () => Promise.reject(new Error("not wired")),
      ChannelCancelFromHuman: () => Promise.reject(new Error("not wired")),
    },
  },
};

const rootEl = document.getElementById("root")!;
const flush = () => act(async () => { await new Promise((r) => setTimeout(r, 0)); });
const mount = async (): Promise<{ unmount: () => void }> => {
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(<LocaleProvider><CollabChatView /></LocaleProvider>);
  });
  await flush();
  return { unmount: () => act(async () => { root.unmount(); }) };
};
const tabButtons = () => Array.from(document.querySelectorAll<HTMLButtonElement>(".collab-chat-view__tab"));
const tabSelected = (label: string) =>
  tabButtons().find((b) => b.textContent === label)?.getAttribute("aria-selected") === "true";

// ── ① 首访无记录：默认群聊 tab + 占位视图（不代开）────────────────────────
let handle = await mount();
assert.ok(document.querySelector(".channel-chat"), "first visit mounts the groups panel (current default)");
assert.equal(tabSelected("Direct chains"), false, "first visit does not force the directs tab");
assert.equal(document.querySelector(".channel-chat__chathead"), null, "first visit opens no group on the user's behalf");
assert.ok(document.querySelector(".channel-chat__placeholder"), "first visit shows the honest placeholder");
await handle.unmount();

// ── ② 群聊记忆恢复：上次打开的群自动进入（重开面板=重挂恢复路径）──────────
window.localStorage.setItem("collabChat.lastGroupsChannel", "ch_a");
window.localStorage.setItem("collabChat.lastTab", "groups");
handle = await mount();
const chathead = document.querySelector(".channel-chat__chathead");
assert.ok(chathead, "the remembered group auto-opens on mount");
assert.match(chathead!.textContent ?? "", /发布协调/, "the auto-opened group is the remembered one");
assert.equal(document.querySelector(".channel-chat__item--on")?.textContent?.includes("发布协调"), true, "the remembered group is marked active in the list");
assert.ok(
  calls.some((c) => c.name === "ChannelMessagesForHuman" && c.args[0] === "ch_a"),
  "the restored group fetches its message tail in the same load",
);
await handle.unmount();

// ── ③ 已删除/解散的群 → 优雅回落默认视图（不报错不空白）───────────────────
window.localStorage.setItem("collabChat.lastGroupsChannel", "ch_gone");
handle = await mount();
assert.equal(document.querySelector(".channel-chat__chathead"), null, "a deleted remembered group opens nothing");
assert.ok(document.querySelector(".channel-chat__placeholder"), "a deleted remembered group falls back to the placeholder (not blank)");
assert.equal(document.querySelector(".channel-chat__actionerror"), null, "the deleted-group fallback surfaces no error strip");
assert.equal(document.querySelectorAll(".channel-chat__item").length, 2, "the group list still renders for manual picking");
await handle.unmount();

// ── ④ 显式返回群列表即清记忆：重开不再代开 ────────────────────────────────
window.localStorage.setItem("collabChat.lastGroupsChannel", "ch_a");
handle = await mount();
await act(async () => {
  (document.querySelector(".channel-chat__back") as HTMLButtonElement).click();
});
await flush();
assert.equal(window.localStorage.getItem("collabChat.lastGroupsChannel"), null, "explicit back to the list clears the groups memory");
assert.equal(readCollabChatLastChannelId(), null, "the memory read-side agrees the key is cleared");
await handle.unmount();
handle = await mount();
assert.equal(document.querySelector(".channel-chat__chathead"), null, "reopening after explicit back shows the list, not the old group");
await handle.unmount();

// ── ④续 群打开即落盘（点击任一群写入记忆）────────────────────────────────
handle = await mount();
await act(async () => {
  (Array.from(document.querySelectorAll<HTMLElement>(".channel-chat__item"))
    .find((el) => el.textContent?.includes("合并线")) as HTMLElement).click();
});
await flush();
assert.equal(window.localStorage.getItem("collabChat.lastGroupsChannel"), "ch_b", "opening a group persists the groups memory");
await handle.unmount();

// ── ②续 tab+对话链记忆恢复：上次停在对话链且开着 P1 → 重开直达 ─────────────
window.localStorage.setItem("collabChat.lastTab", "directs");
window.localStorage.setItem("collabChat.lastDirectsPair", pairP1);
handle = await mount();
assert.equal(tabSelected("Direct chains"), true, "the remembered directs tab is restored");
await flush();
const replayMsgs = document.querySelectorAll(".collab-chat-view__msg");
assert.equal(replayMsgs.length, 2, "the remembered pair auto-opens its replay");
assert.ok(
  calls.some((c) => c.name === "ReadCollabDirectChain" && c.args[0] === pairP1),
  "the remembered pair is read by pairId",
);
assert.equal(window.localStorage.getItem("collabChat.lastDirectsPair"), pairP1, "restore does not disturb the stored pair memory");
await handle.unmount();

// ── ③续 已删除的对话链 → 优雅回落占位（链列表照常渲染，不报错）─────────────
window.localStorage.setItem("collabChat.lastDirectsPair", "sc_x\u0000sc_y");
handle = await mount();
await flush();
assert.equal(document.querySelectorAll(".collab-chat-view__msg").length, 0, "a deleted remembered pair opens no replay");
assert.ok(document.querySelector(".collab-chat-view__placeholder"), "a deleted remembered pair falls back to the placeholder (not blank)");
assert.equal(document.querySelector(".collab-chat-view__direrror"), null, "the deleted-pair fallback surfaces no error strip");
assert.equal(document.querySelectorAll(".collab-chat-view__diritem").length, 2, "the pair list still renders for manual picking");
await handle.unmount();

// ── ④续 链打开落盘 / 对话链 tab 重击（回列表）清链记忆 ─────────────────────
handle = await mount();
await flush();
await act(async () => {
  (document.querySelector(".collab-chat-view__diritem") as HTMLElement).click();
});
await flush();
assert.equal(window.localStorage.getItem("collabChat.lastDirectsPair"), pairP1, "opening a chain persists the pair memory");
await act(async () => {
  tabButtons().find((b) => b.textContent === "Direct chains")!.click();
});
await flush();
assert.equal(window.localStorage.getItem("collabChat.lastDirectsPair"), null, "re-clicking the directs tab (back to list) clears the pair memory");
assert.equal(window.localStorage.getItem("collabChat.lastTab"), "directs", "the tab memory follows the last visit");
assert.ok(document.querySelector(".collab-chat-view__placeholder"), "closing the chain falls back to the pick-a-chain placeholder");
await handle.unmount();

// ── ⑤ 损坏存储值降级默认（不炸）───────────────────────────────────────────
window.localStorage.setItem("collabChat.lastTab", "bogus");
window.localStorage.setItem("collabChat.lastDirectsPair", "");
assert.equal(readCollabChatTab(), "groups", "a corrupted tab value degrades to the groups default");
assert.equal(readCollabChatLastPairId(), null, "an empty pair memory reads as no memory");
handle = await mount();
assert.ok(document.querySelector(".channel-chat"), "a corrupted tab value still mounts the groups panel");
await handle.unmount();

// ── ⑥ 信件零涉及：collabHub.view 全程未被本机制触碰 ────────────────────────
assert.equal(window.localStorage.getItem("collabHub.view"), null, "the mail/chat hub view key is never touched by the chat memory");

// ── ⑦ 修① CSS 断言：群聊消息流内边距与对话链回放流同值（不再贴边）─────────
const cssSource = readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), "../styles.css"), "utf8");
const ruleBody = (selector: string): string => {
  const match = cssSource.match(new RegExp(`${selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{([^}]*)\\}`));
  assert.ok(match, `styles.css keeps a ${selector} rule`);
  return match[1];
};
const groupsPadding = ruleBody(".channel-chat__stream").match(/padding:\s*([^;]+);/)?.[1]?.trim();
const directsPadding = ruleBody(".collab-chat-view__stream").match(/padding:\s*([^;]+);/)?.[1]?.trim();
assert.equal(groupsPadding, "12px 16px", "the groups stream carries the inset padding (no longer edge-hugging)");
assert.equal(groupsPadding, directsPadding, "both chat views share the same stream inset (consistency)");

console.log("tl1439-collab-chat-memory: all assertions passed");
