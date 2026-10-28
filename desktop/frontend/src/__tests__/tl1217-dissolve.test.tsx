// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1217-dissolve.test.tsx
// tl1217 验收（前端件）：群主死锁出口——详情面板「解散群聊」。
//   ① 群主可见解散入口；两击确认：第一击只武装（换确认文案+危险色），不发口；
//   ② 第二击发 ChannelDissolveForHuman 口（带群 id），成功后回群列表（群消失）；
//   ③ 后端拒绝如实透出错误条（不静默）；
//   ④ 非群主无解散入口（仅退群）；
//   ⑤ 武装态切群/关详情即解除（防跨群误带）。
import * as React from "react"; // tsconfig exclude 了 __tests__，tsx 对本文件走 classic JSX
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
const { LocaleProvider } = await import("../lib/i18n");
const { ChannelChatPanel, HUMAN_LOCAL } = await import("../components/ChannelChatPanel");
type ChannelChatBindings = import("../components/ChannelChatPanel").ChannelChatBindings;
type CollabChannelView = import("../lib/bridge").CollabChannelView;

type Call = { name: string; args: unknown[] };
const calls: Call[] = [];

const state = {
  channels: [
    {
      id: "ch_mine", name: "发布协调", createdAt: 1_699_000_000_000, hourlyLimit: 30,
      members: [HUMAN_LOCAL, "sc_a"], messages: 1, ownerType: "human", ownerId: HUMAN_LOCAL,
      memberDetails: [
        { contact: HUMAN_LOCAL, type: "human", joinedAt: 1_699_000_000_000 },
        { contact: "sc_a", type: "session", joinedAt: 1_699_000_001_000 },
      ],
    },
    {
      id: "ch_mine2", name: "协调二群", createdAt: 1_699_000_002_000, hourlyLimit: 30,
      members: [HUMAN_LOCAL], messages: 0, ownerType: "human", ownerId: HUMAN_LOCAL,
      memberDetails: [{ contact: HUMAN_LOCAL, type: "human", joinedAt: 1_699_000_002_000 }],
    },
    {
      id: "ch_other", name: "别人的群", createdAt: 1_699_000_003_000, hourlyLimit: 30,
      members: ["sc_owner", HUMAN_LOCAL], messages: 1, ownerType: "session", ownerId: "sc_owner",
      memberDetails: [
        { contact: "sc_owner", type: "session", joinedAt: 1_699_000_003_000 },
        { contact: HUMAN_LOCAL, type: "human", joinedAt: 1_699_000_004_000 },
      ],
    },
  ] as CollabChannelView[],
  messages: {
    ch_mine: [{ id: "m1", sender: HUMAN_LOCAL, body: "开工", at: 1_700_000_001_000, fanout: [] }],
    ch_mine2: [],
    ch_other: [{ id: "o1", sender: "sc_owner", body: "收到", at: 1_700_000_005_000, fanout: [] }],
  } as Record<string, unknown[]>,
  refuseDissolve: false,
  dissolved: [] as string[],
};

const view = (id: string): CollabChannelView => JSON.parse(JSON.stringify(state.channels.find((c) => c.id === id)));

const bindings: ChannelChatBindings = {
  async ListCollabChannels() { return state.channels.map((c) => JSON.parse(JSON.stringify(c))); },
  async ReadCollabChannel(ref, limit) {
    const ch = state.channels.find((c) => c.id === ref || c.name === ref);
    if (!ch) throw new Error(`no channel ${ref}`);
    return { channel: view(ch.id), messages: (state.messages[ch.id] as never[]).slice(-limit) };
  },
  async ChannelMessagesForHuman(ref, sinceMs, limit) {
    const ch = state.channels.find((c) => c.id === ref || c.name === ref);
    if (!ch) throw new Error(`no channel ${ref}`);
    return { channel: view(ch.id), messages: (state.messages[ch.id] as never[]).slice(-limit) };
  },
  async ChannelCreateForHuman(name, topic, hourlyLimit) { return view("ch_mine"); },
  async ChannelLeaveHuman(ref) {
    calls.push({ name: "ChannelLeaveHuman", args: [ref] });
    const ch = state.channels.find((c) => c.id === ref || c.name === ref);
    if (ch?.ownerId === HUMAN_LOCAL) throw new Error("collabchannel: the channel owner cannot leave (no successor rule yet)");
  },
  async ChannelDissolveForHuman(ref) {
    calls.push({ name: "ChannelDissolveForHuman", args: [ref] });
    if (state.refuseDissolve) throw new Error("collabchannel: only the channel owner may do that");
    state.dissolved.push(String(ref));
    state.channels = state.channels.filter((c) => c.id !== ref);
  },
  async ChannelSendFromHuman(ref, body) { return { id: "mx", sender: HUMAN_LOCAL, body, at: 1, fanout: [] }; },
  async ChannelInvite(ref, contactIDs) { return view(ref); },
  async ChannelRemoveMember(ref, contactID) { return view(ref); },
  async ChannelSetDeputy(ref, contactID) { return view(ref); },
  async ChannelUnsetDeputy(ref) { return view(ref); },
  async ChannelCancelFromHuman(ref, messageID) { return { messageId: messageID, channel: ref, cancelledAt: 1, cancelledQueued: 0, alreadyDelivered: 0, alreadyCancelled: false }; },
  async ListAddressableSessions() { return []; },
};

let currentRoot: Awaited<ReturnType<typeof createRoot>> | null = null;
async function mountPanel(): Promise<void> {
  if (currentRoot) {
    await act(async () => { currentRoot!.unmount(); });
    currentRoot = null;
  }
  currentRoot = createRoot(document.getElementById("root")!);
  await act(async () => {
    currentRoot!.render(
      <LocaleProvider>
        <ChannelChatPanel bindings={bindings} />
      </LocaleProvider>,
    );
  });
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
}
await mountPanel();

const chat = () => document.querySelector(".channel-chat")!;
const detailButton = (label: string): HTMLElement | undefined =>
  Array.from(chat().querySelector(".channel-chat__detail")!.querySelectorAll("button")).find((b) => b.textContent === label);

async function openDetailOfFirst(): Promise<void> {
  await act(async () => { (chat().querySelector(".channel-chat__item") as HTMLElement).click(); });
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
  await act(async () => {
    Array.from(chat().querySelectorAll("button")).find((b) => b.textContent === "Details")!.click();
  });
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
}

// ── ① 群主可见解散入口；第一击只武装不发口 ─────────────────────────────
await openDetailOfFirst();
const dissolveBtn = detailButton("Dissolve group");
assert.ok(dissolveBtn, "the owner sees the dissolve entry in the detail drawer");
calls.length = 0;
await act(async () => { dissolveBtn!.click(); });
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
assert.equal(calls.filter((c) => c.name === "ChannelDissolveForHuman").length, 0, "the first click only arms — no port call");
const armedBtn = Array.from(chat().querySelector(".channel-chat__detail")!.querySelectorAll("button"))
  .find((b) => b.textContent!.startsWith("Click again to dissolve"));
assert.ok(armedBtn, "the armed state shows the confirm copy");
assert.ok(armedBtn!.className.includes("btn--danger"), "the armed state renders as the danger button");

// ── ⑤ 武装态关详情即解除（防跨群误带）─────────────────────────────────
await act(async () => {
  Array.from(chat().querySelectorAll("button")).find((b) => b.textContent === "Close")!.click();
});
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
await openDetailOfFirst();
assert.ok(detailButton("Dissolve group"), "reopening the detail starts disarmed");
assert.equal(Array.from(chat().querySelector(".channel-chat__detail")!.querySelectorAll("button"))
  .filter((b) => b.textContent!.startsWith("Click again")).length, 0, "no stale armed confirm copy");

// ── ② 第二击发口；成功后回群列表，群消失 ─────────────────────────────
const btn2 = detailButton("Dissolve group")!;
calls.length = 0;
await act(async () => { btn2.click(); }); // 武装
await act(async () => { btn2.click(); }); // 确认执行
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
const dissolveCalls = calls.filter((c) => c.name === "ChannelDissolveForHuman");
assert.equal(dissolveCalls.length, 1, "exactly one dissolve call");
assert.equal(dissolveCalls[0].args[0], "ch_mine", "the port receives the channel ref");
assert.deepEqual(state.dissolved, ["ch_mine"], "the backend fake recorded the dissolve");
assert.ok(!chat().textContent!.includes("发布协调"), "the dissolved group leaves the list");
assert.equal(chat().querySelector(".channel-chat__detail"), null, "the drawer closed back to the list");

// ── ③ 后端拒绝如实透出 ────────────────────────────────────────────────
state.refuseDissolve = true;
await act(async () => {
  const item = Array.from(chat().querySelectorAll(".channel-chat__item")).find((el) => el.textContent!.includes("协调二群")) as HTMLElement;
  item.click();
});
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
await act(async () => {
  Array.from(chat().querySelectorAll("button")).find((b) => b.textContent === "Details")!.click();
});
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
const btn3 = Array.from(chat().querySelector(".channel-chat__detail")!.querySelectorAll("button"))
  .find((b) => b.textContent === "Dissolve group")!;
await act(async () => { btn3.click(); });
await act(async () => { btn3.click(); });
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
const errStrip = chat().querySelector(".channel-chat__actionerror");
assert.ok(errStrip, "a refusal surfaces the error strip");
assert.match(errStrip!.textContent ?? "", /only the channel owner may do that/, "the refusal carries the backend reason");
// 武装态在失败后复位（下一击重新从武装开始，避免连点误触）。
assert.ok(Array.from(chat().querySelector(".channel-chat__detail")!.querySelectorAll("button"))
  .some((b) => b.textContent === "Dissolve group"), "the button returns to its disarmed label");

// ── ④ 非群主无解散入口 ────────────────────────────────────────────────
await act(async () => {
  Array.from(chat().querySelectorAll("button")).find((b) => b.textContent === "Close")!.click();
});
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
const otherItem = Array.from(chat().querySelectorAll(".channel-chat__item"))
  .find((el) => el.textContent!.includes("别人的群"));
await act(async () => { (otherItem as HTMLElement).click(); });
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
await act(async () => {
  Array.from(chat().querySelectorAll("button")).find((b) => b.textContent === "Details")!.click();
});
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
const otherDetail = chat().querySelector(".channel-chat__detail")!;
assert.ok(Array.from(otherDetail.querySelectorAll("button")).some((b) => b.textContent === "Leave group"), "a non-owner still sees Leave group");
assert.equal(Array.from(otherDetail.querySelectorAll("button")).filter((b) => b.textContent === "Dissolve group").length, 0, "a non-owner sees no dissolve entry");

if (currentRoot) {
  await act(async () => { currentRoot!.unmount(); });
}
console.log("tl1217-dissolve: all assertions passed");
process.exit(0);
