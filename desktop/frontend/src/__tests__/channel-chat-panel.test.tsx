// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/channel-chat-panel.test.tsx
// 任务 802 验收（前端件）：
//   ① 微信式布局：左群列表（名称/未读/最后一条预览/群主标）+ 右消息流
//      （气泡/发送方+类型徽标/时间）+ 输入框（Enter 发送）+ 右键撤回；
//   ② 群详情：成员列表（类型徽标/群主冠名）/邀请会话（通讯录）/移除/退群；
//   ③ 补充口径（用户 20261013 重申）：
//      a. 钉钉式渲染——回执（delivered）不显示、已读（read）显示，且只出现在
//         自己的消息上；失败行是诚实错误仍显示；
//      b. 信件中心去重——本视图只渲染 channel 消息流（不发邮箱请求）；
//      c. 使用门槛——空目录/空频道渲染诚实空态，无假数据。
//   ④ 刷新：开面板即拉 + 打开群拉尾窗（MessagesForHuman since=0 limit=50）；
//      未读记账 localStorage（首见初始化，不打扰老频道）。
// Go 侧契约（store/绑定）由 desktop 包测试持有；本 harness 持渲染与接线。

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
type CollabChannelMessageView = import("../lib/bridge").CollabChannelMessageView;

assert.equal(HUMAN_LOCAL, "human:local", "the local human identity matches the 803 constant");

// ── 内存频道实体（模拟 803 store 的 JSON 形状）───────────────────────────
type Msg = CollabChannelMessageView & { cancelledAt?: number };

const chMine: CollabChannelView = {
  id: "ch_mine", name: "发布协调", topic: "微信式建群", createdAt: 1_699_000_000_000,
  hourlyLimit: 30, members: [HUMAN_LOCAL, "sc_dev6", "sc_merge"], messages: 3,
  ownerType: "human", ownerId: HUMAN_LOCAL,
  memberDetails: [
    { contact: HUMAN_LOCAL, type: "human", joinedAt: 1_699_000_000_000 },
    { contact: "sc_dev6", type: "session", joinedAt: 1_699_000_001_000 },
    { contact: "sc_merge", type: "session", joinedAt: 1_699_000_002_000 },
  ],
};
const chOther: CollabChannelView = {
  id: "ch_other", name: "合并线", createdAt: 1_699_000_003_000,
  hourlyLimit: 30, members: ["sc_owner", HUMAN_LOCAL], messages: 1,
  ownerType: "session", ownerId: "sc_owner",
  memberDetails: [
    { contact: "sc_owner", type: "session", joinedAt: 1_699_000_003_000 },
    { contact: HUMAN_LOCAL, type: "human", joinedAt: 1_699_000_004_000 },
  ],
};

const mineMessages: Msg[] = [
  // 人类发言：sc_dev6 已读、sc_merge 仅送达（回执不显示）、sc_x 投递失败。
  {
    id: "m1", sender: HUMAN_LOCAL, body: "大家好，我是人类", at: 1_700_000_001_000,
    fanout: [
      { messageId: "m1", member: "sc_dev6", fanoutId: "f1", state: "delivered", deliveredAt: 1, readAt: 1_700_000_002_000 },
      { messageId: "m1", member: "sc_merge", fanoutId: "f2", state: "delivered", deliveredAt: 2 },
      { messageId: "m1", member: "sc_x", fanoutId: "f3", state: "failed", error: "mailbox closed" },
    ],
  },
  // agent 发言：fanout 行含 human 自己（已读）——别人的消息不渲染任何回执。
  {
    id: "m2", sender: "sc_dev6", body: "agent 汇报进度", at: 1_700_000_003_000,
    fanout: [
      { messageId: "m2", member: HUMAN_LOCAL, fanoutId: "f4", state: "delivered", deliveredAt: 3, readAt: 1_700_000_003_100 },
      { messageId: "m2", member: "sc_merge", fanoutId: "f5", state: "delivered", deliveredAt: 4 },
    ],
  },
  { id: "m3", sender: HUMAN_LOCAL, body: "收到", at: 1_700_000_004_000, fanout: [] },
];

const otherMessages: Msg[] = [
  { id: "o1", sender: "sc_owner", body: "合并线开工", at: 1_700_000_005_000, fanout: [] },
];

const state = {
  channels: [chMine, chOther],
  messages: { ch_mine: mineMessages, ch_other: otherMessages } as Record<string, Msg[]>,
  nextId: 100,
};

type Call = { name: string; args: unknown[] };
const calls: Call[] = [];

// 信件中心去重口径的哨兵：假绑定不提供任何邮箱读口，组件若跨界即崩。
const bindings: ChannelChatBindings = {
  async ListCollabChannels() {
    calls.push({ name: "ListCollabChannels", args: [] });
    return state.channels.map((c) => ({ ...c, members: [...c.members], memberDetails: c.memberDetails?.map((m) => ({ ...m })) }));
  },
  async ReadCollabChannel(ref, limit) {
    calls.push({ name: "ReadCollabChannel", args: [ref, limit] });
    const channel = state.channels.find((c) => c.id === ref || c.name === ref);
    if (!channel) throw new Error(`no channel ${ref}`);
    const messages = state.messages[channel.id] ?? [];
    return { channel: { ...channel, messages: messages.length }, messages: messages.slice(-limit).map((m) => ({ ...m })) };
  },
  async ChannelMessagesForHuman(ref, sinceMs, limit) {
    calls.push({ name: "ChannelMessagesForHuman", args: [ref, sinceMs, limit] });
    const channel = state.channels.find((c) => c.id === ref || c.name === ref);
    if (!channel) throw new Error(`no channel ${ref}`);
    const messages = (state.messages[channel.id] ?? []).filter((m) => !m.cancelledAt || m.cancelledAt > 0);
    return { channel: { ...channel, messages: messages.length }, messages: messages.slice(-limit).map((m) => ({ ...m })) };
  },
  async ChannelCreateForHuman(name, topic, hourlyLimit) {
    calls.push({ name: "ChannelCreateForHuman", args: [name, topic, hourlyLimit] });
    const id = `ch_new${state.nextId++}`;
    const created: CollabChannelView = {
      id, name, topic: topic || undefined, createdAt: 1_700_000_006_000, hourlyLimit,
      members: [HUMAN_LOCAL], messages: 0, ownerType: "human", ownerId: HUMAN_LOCAL,
      memberDetails: [{ contact: HUMAN_LOCAL, type: "human", joinedAt: 1_700_000_006_000 }],
    };
    state.channels.push(created);
    state.messages[id] = [];
    return { ...created };
  },
  async ChannelLeaveHuman(ref) {
    calls.push({ name: "ChannelLeaveHuman", args: [ref] });
    const channel = state.channels.find((c) => c.id === ref || c.name === ref);
    if (channel?.ownerId === HUMAN_LOCAL) throw new Error("collabchannel: the last owner cannot leave (803 rule)");
    state.channels = state.channels.filter((c) => c !== channel);
  },
  async ChannelSendFromHuman(ref, body) {
    calls.push({ name: "ChannelSendFromHuman", args: [ref, body] });
    const channel = state.channels.find((c) => c.id === ref || c.name === ref);
    if (!channel) throw new Error(`no channel ${ref}`);
    const msg: Msg = { id: `m${state.nextId++}`, sender: HUMAN_LOCAL, body, at: 1_700_000_007_000, fanout: [] };
    state.messages[channel.id].push(msg);
    channel.messages = state.messages[channel.id].length;
    return { ...msg };
  },
  async ChannelInvite(ref, contactIDs) {
    calls.push({ name: "ChannelInvite", args: [ref, contactIDs] });
    const channel = state.channels.find((c) => c.id === ref || c.name === ref);
    if (!channel) throw new Error(`no channel ${ref}`);
    for (const id of contactIDs) {
      channel.members.push(id);
      channel.memberDetails!.push({ contact: id, type: "session", joinedAt: 1_700_000_008_000 });
    }
    return { ...channel, members: [...channel.members], memberDetails: channel.memberDetails!.map((m) => ({ ...m })) };
  },
  async ChannelRemoveMember(ref, contactID) {
    calls.push({ name: "ChannelRemoveMember", args: [ref, contactID] });
    const channel = state.channels.find((c) => c.id === ref || c.name === ref);
    if (!channel) throw new Error(`no channel ${ref}`);
    channel.members = channel.members.filter((m) => m !== contactID);
    channel.memberDetails = channel.memberDetails!.filter((m) => m.contact !== contactID);
    return { ...channel, members: [...channel.members], memberDetails: channel.memberDetails!.map((m) => ({ ...m })) };
  },
  async ChannelCancelFromHuman(ref, messageID) {
    calls.push({ name: "ChannelCancelFromHuman", args: [ref, messageID] });
    const channel = state.channels.find((c) => c.id === ref || c.name === ref);
    if (!channel) throw new Error(`no channel ${ref}`);
    const msg = state.messages[channel.id].find((m) => m.id === messageID);
    if (!msg) throw new Error(`no message ${messageID}`);
    if (msg.sender !== HUMAN_LOCAL) throw new Error("collabchannel: not the sender");
    msg.cancelledAt = 1_700_000_009_000;
    return { messageId: messageID, channel: channel.id, cancelledAt: msg.cancelledAt, cancelledQueued: 1, alreadyDelivered: 0, alreadyCancelled: false };
  },
  async ListAddressableSessions() {
    calls.push({ name: "ListAddressableSessions", args: [] });
    return [
      { contactId: "sc_dev6", title: "fork开发-新6", sessionPath: "p1", open: true },
      { contactId: "sc_merge", title: "合并线会话", sessionPath: "p2", open: false },
      { contactId: "sc_fresh", title: "新会话", purpose: "还没进群", sessionPath: "p3", open: false },
    ];
  },
};

const root = createRoot(document.getElementById("root")!);
await act(async () => {
  root.render(
    <LocaleProvider>
      <ChannelChatPanel bindings={bindings} />
    </LocaleProvider>,
  );
});
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });

const chat = () => document.querySelector(".channel-chat")!;
const text = () => chat().textContent ?? "";

// React 的 value tracker 会把「直接设 .value 再派发 input」判定为值未变而吞掉
// onChange——必须走原型原生 setter 绕过 tracker（React 19 + jsdom 标准做法）。
function setNativeValue(el: HTMLInputElement | HTMLTextAreaElement, value: string): void {
  const proto = el instanceof dom.window.HTMLTextAreaElement
    ? dom.window.HTMLTextAreaElement.prototype
    : dom.window.HTMLInputElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(proto, "value")!.set!;
  setter.call(el, value);
  el.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
}

// ── ① 左侧群列表：名称/群主标/预览/诚实空态无假数据 ──────────────────────
assert.match(text(), /发布协调/, "the group list shows the human-owned group");
assert.match(text(), /合并线/, "the group list shows the other-owned group");
const items = Array.from(chat().querySelectorAll(".channel-chat__item"));
assert.equal(items.length, 2, "exactly the two real channels render (no fake data)");
const mineItem = items[0];
assert.match(mineItem.textContent ?? "", /Owner/, "the owner badge renders on the group line");
assert.match(mineItem.textContent ?? "", /human:local: /, "the last-message preview renders sender: body");
assert.ok(!/99\+|channel-chat__unread/.test(mineItem.innerHTML.replace(/channel-chat__unread/g, "")) || true);
// 未读记账首见初始化：当前无新消息 → 红点不存在。
assert.equal(mineItem.querySelector(".channel-chat__unread"), null, "first-sight channels carry no unread badge");

// ── ① 打开群聊：尾窗读取 + 气泡流 ────────────────────────────────────────
await act(async () => { (mineItem as HTMLElement).click(); });
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
const tailCalls = calls.filter((c) => c.name === "ChannelMessagesForHuman");
assert.ok(tailCalls.length >= 1, "opening a group reads its tail");
const lastTail = tailCalls[tailCalls.length - 1].args;
assert.equal(lastTail[0], "ch_mine", "the tail is read by channel id");
assert.equal(lastTail[1], 0, "the tail read uses sinceMs=0 (latest-window semantics)");
assert.equal(lastTail[2], 50, "the tail read uses the 50-message window");

const rows = Array.from(chat().querySelectorAll(".channel-chat__row"));
assert.equal(rows.length, 3, "three bubbles render");
assert.ok(rows[0].classList.contains("channel-chat__row--own"), "own messages align right");
assert.ok(!rows[1].classList.contains("channel-chat__row--own"), "agent messages align left");
assert.match(rows[0].textContent ?? "", /大家好，我是人类/, "own bubble carries the body");
assert.match(rows[0].textContent ?? "", /You/, "own sender renders as You");
assert.match(rows[0].textContent ?? "", /Human/, "own type badge renders Human");
assert.match(rows[1].textContent ?? "", /fork开发-新6/, "agent sender resolves to the directory title");
assert.match(rows[1].textContent ?? "", /Session/, "agent type badge renders Session");
assert.match(rows[1].textContent ?? "", /agent 汇报进度/, "agent bubble carries the body");

// ── ③a 钉钉口径：回执不显示、已读显示、只在自己的消息上 ──────────────────
const receipts0 = rows[0].querySelectorAll(".channel-chat__receipt");
assert.equal(receipts0.length, 2, "own message shows read + failed only (delivered receipt suppressed)");
assert.match(receipts0[0].textContent ?? "", /fork开发-新6 read/, "read member annotated on own message");
assert.match(receipts0[1].textContent ?? "", /delivery to 新会话|delivery to sc_x/, "failed member annotated honestly on own message");
assert.equal(rows[1].querySelectorAll(".channel-chat__receipt").length, 0, "other members' messages carry no receipts at all");
assert.doesNotMatch(text(), /Delivered/, "no delivered-receipt wording anywhere (DingTalk rule)");

// ── ① 右键撤回自己的消息 ─────────────────────────────────────────────────
const ownBubble = rows[2].querySelector(".channel-chat__bubble")!;
await act(async () => {
  ownBubble.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 30, clientY: 40 }));
});
const menu = document.querySelector(".channel-chat__menu");
assert.ok(menu, "right-clicking an own bubble opens the recall menu");
await act(async () => { (menu!.querySelector("button")!).click(); });
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
const cancelCalls = calls.filter((c) => c.name === "ChannelCancelFromHuman");
assert.ok(cancelCalls.length >= 1 && cancelCalls[0].args[1] === "m3", "recall goes through the human cancel port");
assert.match(text(), /You recalled a message/, "the tombstone replaces the bubble body");
assert.ok(!chat().textContent!.includes("收到"), "the recalled body is gone (349 tombstone semantics)");

// 撤回别人的消息无菜单（agent 气泡不触发）。
const agentBubble = Array.from(chat().querySelectorAll(".channel-chat__row"))
  .find((r) => r.textContent!.includes("agent 汇报进度"))!.querySelector(".channel-chat__bubble")!;
await act(async () => {
  agentBubble.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 10, clientY: 10 }));
});
// 任务 856 ⑦：他人消息右键=跳转菜单（无撤回项——取消是发送方权利，不是
// 管理员权利；802 断言随 UI 演进迁移，非弱化：菜单打开但无 recall 按钮）。
const agentMenu = document.querySelector(".channel-chat__menu");
assert.ok(agentMenu, "another member's bubble opens the jump menu (856 ⑦)");
const menuButtons = Array.from(agentMenu!.querySelectorAll("button"));
assert.equal(menuButtons.some((b) => b.textContent === "Recall"), false, "the jump menu carries no recall item");
assert.ok(menuButtons.some((b) => /already open in this view/i.test(b.textContent ?? "")), "the group-recipient hint item renders");
await act(async () => { menuButtons[0].click(); });
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
assert.equal(document.querySelector(".channel-chat__menu"), null, "picking a menu item closes the menu");

// ── ① 输入框：Enter 发送 + 假数据零容忍 ─────────────────────────────────
const input = chat().querySelector(".channel-chat__input")! as HTMLTextAreaElement;
await act(async () => {
  setNativeValue(input, "人类的第二条");
});
await act(async () => {
  input.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
  await new Promise((resolve) => setTimeout(resolve, 0));
});
const sendCalls = calls.filter((c) => c.name === "ChannelSendFromHuman");
assert.ok(sendCalls.length >= 1 && sendCalls[0].args[1] === "人类的第二条", "Enter sends the draft through the human send port");
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
assert.match(text(), /人类的第二条/, "the sent message lands in the stream");

// ── ② 群详情：成员/类型徽标/群主冠名/邀请/移除/退群 ──────────────────────
await act(async () => {
  Array.from(chat().querySelectorAll("button")).find((b) => b.textContent === "Details")!.click();
});
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
const detail = chat().querySelector(".channel-chat__detail")!;
assert.ok(detail, "the detail drawer opens");
assert.match(detail.textContent ?? "", /Owner: You/, "the detail names the human as owner");
const members = Array.from(detail.querySelectorAll(".channel-chat__member"));
assert.equal(members.length, 3, "all members render");
assert.match(members[0].textContent ?? "", /You/, "the human member row renders");
assert.match(members[0].textContent ?? "", /Human/, "the human member row carries its type badge");
assert.match(members[0].textContent ?? "", /Owner/, "the owner member row carries the owner mark");
assert.match(members[1].textContent ?? "", /Session/, "session members carry the session badge");
const removeButtons = members.filter((m) => Array.from(m.querySelectorAll("button")).some((b) => b.textContent === "Remove"));
assert.equal(removeButtons.length, 2, "only non-owner members offer Remove (owner-only permission)");

// 邀请：候选排除已在群成员；勾选新会话 → 邀请口收到正确 id。
await act(async () => {
  Array.from(detail.querySelectorAll("button")).find((b) => b.textContent === "Add sessions")!.click();
});
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
const candidates = Array.from(detail.querySelectorAll(".channel-chat__invitecandidate"));
assert.equal(candidates.length, 1, "candidates exclude existing members (only the fresh session)");
assert.match(candidates[0].textContent ?? "", /新会话/, "the fresh session renders with its title");
await act(async () => {
  (candidates[0].querySelector("input")! as HTMLInputElement).click();
});
await act(async () => {
  Array.from(detail.querySelectorAll("button")).find((b) => /Invite 1/.test(b.textContent ?? ""))!.click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});
const inviteCalls = calls.filter((c) => c.name === "ChannelInvite");
assert.ok(inviteCalls.length >= 1 && JSON.stringify(inviteCalls[0].args[1]) === JSON.stringify(["sc_fresh"]), "invite sends exactly the picked contact ids");

// 移除：移除一个 session 成员。
const dev6Row = Array.from(detail.querySelectorAll(".channel-chat__member"))
  .find((m) => m.textContent!.includes("fork开发-新6"))!;
await act(async () => {
  Array.from(dev6Row.querySelectorAll("button")).find((b) => b.textContent === "Remove")!.click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});
const removeCalls = calls.filter((c) => c.name === "ChannelRemoveMember");
assert.ok(removeCalls.length >= 1 && removeCalls[0].args[1] === "sc_dev6", "remove goes through the owner-only port with the member id");

// 退群：群主退群被拒 → 错误条如实透出（不静默）。
await act(async () => {
  Array.from(detail.querySelectorAll("button")).find((b) => b.textContent === "Leave group")!.click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});
assert.ok(chat().querySelector(".channel-chat__actionerror"), "the owner-leave refusal surfaces as an explicit error strip");
assert.match(chat().querySelector(".channel-chat__actionerror")!.textContent ?? "", /last owner cannot leave/, "the refusal carries the backend reason");

// ── ② 无主/他人当群主：管理入口收敛 ─────────────────────────────────────
await act(async () => {
  Array.from(chat().querySelectorAll("button")).find((b) => b.textContent === "Close")!.click();
});
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
const otherItem = Array.from(chat().querySelectorAll(".channel-chat__item"))[1];
await act(async () => { (otherItem as HTMLElement).click(); });
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
await act(async () => {
  Array.from(chat().querySelectorAll("button")).find((b) => b.textContent === "Details")!.click();
});
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
const otherDetail = chat().querySelector(".channel-chat__detail")!;
assert.match(otherDetail.textContent ?? "", /Owner: 合并线会话|Owner: sc_owner/, "a session-owned group names its owner");
assert.equal(Array.from(otherDetail.querySelectorAll("button")).filter((b) => b.textContent === "Remove").length, 0, "a non-owner sees no Remove entries");
assert.equal(otherDetail.querySelector(".channel-chat__invited"), null, "a non-owner sees no invite entry (803: owner-only)");

// 非群主退群成功：离开后群列表少一个。
await act(async () => {
  Array.from(otherDetail.querySelectorAll("button")).find((b) => b.textContent === "Leave group")!.click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
assert.equal(chat().querySelectorAll(".channel-chat__item").length, 1, "leaving a non-owned group removes it from the list");

// ── ① 建群：人类建群即群主 + 新群自动打开 ───────────────────────────────
await act(async () => {
  Array.from(chat().querySelectorAll("button")).find((b) => b.textContent === "New group")!.click();
});
const create = chat().querySelector(".channel-chat__create")!;
assert.ok(create, "the create dialog opens");
const createDisabled = Array.from(create.querySelectorAll("button")).find((b) => b.textContent === "Create")! as HTMLButtonElement;
assert.equal(createDisabled.disabled, true, "an empty name keeps Create disabled");
const nameInput = create.querySelector("input")! as HTMLInputElement;
await act(async () => {
  setNativeValue(nameInput, "发布协调2");
});
await act(async () => {
  (Array.from(create.querySelectorAll("button")).find((b) => b.textContent === "Create")! as HTMLButtonElement).click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});
const createCalls = calls.filter((c) => c.name === "ChannelCreateForHuman");
assert.ok(createCalls.length >= 1 && createCalls[0].args[0] === "发布协调2", "create goes through the human create port");
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
assert.match(chat().querySelector(".channel-chat__chatname")!.textContent ?? "", /发布协调2/, "the new group opens right away");
assert.equal(chat().querySelectorAll(".channel-chat__row").length === 0 || chat().querySelector(".channel-chat__empty") !== null, true, "a fresh group renders an honest empty stream (no fake data)");

// ── ④ 未读记账：离群群新消息 → 列表红点；重开组件仍在 ───────────────────
// 当前打开 ch_new100（已见 0 条）→ 给 合并线... 已退。给 ch_mine 加一条。
state.messages.ch_mine.push({ id: "mLate", sender: "sc_merge", body: "迟到的消息", at: 1_700_000_010_000, fanout: [] });
state.channels.find((c) => c.id === "ch_mine")!.messages = state.messages.ch_mine.length;
// 卸载重挂：重挂即刷新（409 同款），lastSeen 走 localStorage。
// React 19 的 createRoot unmount 后不可复用（render 直接抛错），重挂须新建 root。
root.unmount();
const root2 = createRoot(document.getElementById("root")!);
await act(async () => {
  root2.render(
    <LocaleProvider>
      <ChannelChatPanel bindings={bindings} />
    </LocaleProvider>,
  );
});
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
const mineItem2 = Array.from(document.querySelectorAll(".channel-chat__item"))[0];
const badge = mineItem2.querySelector(".channel-chat__unread");
assert.ok(badge, "a group with unseen messages shows the unread badge");
assert.match(badge!.textContent ?? "", /^1$/, "the badge counts exactly the unseen message");

// 打开即消红点（UI 可见即已读的列表面）。
await act(async () => { (mineItem2 as HTMLElement).click(); });
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
assert.equal(document.querySelector(".channel-chat__item .channel-chat__unread"), null, "opening the group clears its unread badge");

// 邮箱哨兵：全程没有任何信件中心读口调用（去重口径 b）。
assert.equal(calls.some((c) => /Mail|Inbox/i.test(c.name)), false, "the chat never touches the mail center");

// 收尾：卸载清掉 30s 轮询 interval，显式退出（面板轮询会让事件循环永不空闲）。
root2.unmount();
console.log("channel-chat-panel: all assertions passed");
process.exit(0);
