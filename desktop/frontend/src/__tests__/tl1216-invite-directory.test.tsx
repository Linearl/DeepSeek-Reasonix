// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1216-invite-directory.test.tsx
// tl1216 验收（前端件）：
//   ① 关键字检索修复断言：邀请框输入「fork」命中 fork开发-新10（大小写不
//      敏感；title/purpose/topicId/contactId 全字段）；
//   ② 层级逐步定位（预期方式②）：空查询=浏览模式，按 全局 → 项目 → 归档
//      分组渲染，组头带计数；有关键字=平铺命中；
//   ③ directory 加载韧性：单次失败不再把面板钉死成「没有可邀请的会话」——
//      失败态显式提示 + 退避重试成功后候选恢复（旧实现 [] 缓存为已加载）；
//   ④ topicId 兜底：title 缺失的会话按 topic_id 命中。
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
const { collabGroupRetryDelaysMs } = await import("../components/CollabGroupPanel");
type ChannelChatBindings = import("../components/ChannelChatPanel").ChannelChatBindings;
type CollabChannelView = import("../lib/bridge").CollabChannelView;
type CollabSessionDirectoryRow = import("../components/CollabInboxPanel").CollabSessionDirectoryRow;

// 重试梯拼到毫秒级（CollabInbox 面板测试同款做法），用完还原。
const realDelays = [...collabGroupRetryDelaysMs];
(collabGroupRetryDelaysMs as unknown as number[]).splice(0, collabGroupRetryDelaysMs.length, 5);

const chMine: CollabChannelView = {
  id: "ch_mine", name: "发布协调", createdAt: 1_699_000_000_000,
  hourlyLimit: 30, members: [HUMAN_LOCAL], messages: 0,
  ownerType: "human", ownerId: HUMAN_LOCAL,
  memberDetails: [{ contact: HUMAN_LOCAL, type: "human", joinedAt: 1_699_000_000_000 }],
};

type Call = { name: string; args: unknown[] };
const calls: Call[] = [];

/** 目录假数据：fork开发-新10 在列（与用户实测同形）；含项目/归档/无 title 行。 */
let directoryRows: CollabSessionDirectoryRow[] = [
  { contactId: "sc_fork10", title: "fork开发-新10", topicId: "topic_20261024-011346_6e93986f18ab3fc6", sessionPath: "p10", scope: "project", workspaceRoot: "D:\\1.workspace\\ai-passport", open: false },
  { contactId: "sc_merge", title: "合并线会话", sessionPath: "p2", scope: "global", open: false },
  { contactId: "sc_arch", title: "老调研存档", sessionPath: "p3", scope: "global", archived: true, open: false },
  { contactId: "", title: "尚未通信的新会话", topicId: "topic_20261027-000000_deadbeef", sessionPath: "p4", scope: "global", open: false },
];
let directoryFails = 0; // >0 时前 N 次 ListAddressableSessions 直接拒绝

const bindings: ChannelChatBindings = {
  async ListCollabChannels() { return [chMine]; },
  async ReadCollabChannel(ref, limit) { return { channel: { ...chMine, messages: 0 }, messages: [] as never[] }; },
  async ChannelMessagesForHuman(ref, sinceMs, limit) { return { channel: { ...chMine, messages: 0 }, messages: [] as never[] }; },
  async ChannelCreateForHuman(name, topic, hourlyLimit) { return { ...chMine }; },
  async ChannelLeaveHuman(ref) {},
  async ChannelSendFromHuman(ref, body) { return { id: "m1", sender: HUMAN_LOCAL, body, at: 1, fanout: [] }; },
  async ChannelInvite(ref, contactIDs) { return { ...chMine }; },
  async ChannelRemoveMember(ref, contactID) { return { ...chMine }; },
  async ChannelSetDeputy(ref, contactID) { return { ...chMine }; },
  async ChannelUnsetDeputy(ref) { return { ...chMine }; },
  async ChannelCancelFromHuman(ref, messageID) { return { messageId: messageID, channel: ref, cancelledAt: 1, cancelledQueued: 0, alreadyDelivered: 0, alreadyCancelled: false }; },
  async ListAddressableSessions() {
    calls.push({ name: "ListAddressableSessions", args: [] });
    if (directoryFails > 0) {
      directoryFails -= 1;
      throw new Error("bridge hiccup (simulated)");
    }
    return directoryRows.map((r) => ({ ...r }));
  },
};

const chat = () => document.querySelector(".channel-chat")!;

/** 面板挂载（可重复：卸掉旧 root 再建新 root——React 19 同 root 不可 render 复用）。 */
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

function setNativeValue(el: HTMLInputElement, value: string): void {
  // React 的 value tracker 会吞「直接设值再派发 input」——走原型原生 setter。
  const proto = dom.window.HTMLInputElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(proto, "value")!.set!;
  setter.call(el, value);
  el.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
}

/** 打开群 → 详情 → 邀请框，返回邀请盒。 */
async function openInviteBox(): Promise<HTMLElement> {
  await act(async () => { (chat().querySelector(".channel-chat__item") as HTMLElement).click(); });
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
  await act(async () => {
    Array.from(chat().querySelectorAll("button")).find((b) => b.textContent === "Details")!.click();
  });
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
  const detail = chat().querySelector(".channel-chat__detail")!;
  await act(async () => {
    Array.from(detail.querySelectorAll("button")).find((b) => b.textContent === "Add sessions")!.click();
  });
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
  return chat().querySelector(".channel-chat__invitebox")!;
}

async function typeQuery(value: string): Promise<void> {
  const box = chat().querySelector(".channel-chat__invitebox")!;
  await act(async () => {
    setNativeValue(box.querySelector(".channel-chat__invitequery") as HTMLInputElement, value);
  });
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
}

const candidateNames = (): string[] =>
  Array.from(chat().querySelectorAll(".channel-chat__invitecandidatename")).map((n) => n.textContent ?? "");
const groupHeads = (): string[] =>
  Array.from(chat().querySelectorAll(".channel-chat__invitegrouphead")).map((n) => n.textContent ?? "");

// ── ② 层级逐步定位：空查询=浏览模式，按 全局 → 项目 → 归档 分组 ──────────
const inviteBox = await openInviteBox();
assert.ok(inviteBox, "the invite box opens");
const heads = groupHeads();
assert.equal(heads.length, 3, `empty query renders three hierarchy sections, got: ${JSON.stringify(heads)}`);
assert.match(heads[0], /Global sessions/, "section 1 = global sessions");
assert.match(heads[1], /Project: ai-passport/, "section 2 = the project group, labeled by workspace short name");
assert.match(heads[2], /Archived/, "section 3 = archive");
const globalGroup = chat().querySelector(".channel-chat__invitegroup")!;
assert.equal(globalGroup.querySelectorAll(".channel-chat__invitecandidate").length, 2, "the global group holds its two rows (incl. the no-contact-id session)");
const projectGroup = chat().querySelectorAll(".channel-chat__invitegroup")[1]!;
assert.ok(projectGroup.querySelector(".channel-chat__invitecandidatename")!.textContent === "fork开发-新10", "fork开发-新10 lives in its project group — 层级逐步定位可达");

// ── ① 关键字检索：「fork」命中 fork开发-新10（用户实测场景断言）───────────
await typeQuery("fork");
assert.deepEqual(candidateNames(), ["fork开发-新10"], "query=fork matches fork开发-新10 (用户实测主断言)");
await typeQuery("FORK");
assert.deepEqual(candidateNames(), ["fork开发-新10"], "query is case-insensitive");
await typeQuery("存档");
assert.deepEqual(candidateNames(), ["老调研存档"], "CJK query matches titles");
await typeQuery("topic_20261027-000000_deadbeef");
assert.deepEqual(candidateNames(), ["尚未通信的新会话"], "topicId joins the haystack (title-missing sessions stay reachable)");
await typeQuery("不存在的关键字xyz");
assert.equal(chat().querySelector(".channel-chat__invitecandidates")!.textContent, "No sessions to invite", "a true no-match still shows the honest empty state");

// ── ③ directory 失败韧性：失败≠空态，重试后恢复 ─────────────────────────
// 重开面板：挂载即失败一次（directoryFails=1），退避 5ms 后重试成功。
directoryFails = 1;
calls.length = 0;
await mountPanel();
const failCalls = calls.filter((c) => c.name === "ListAddressableSessions").length;
assert.ok(failCalls >= 1, "the first directory load failed (simulated); the retry ladder may already have re-fetched");
await openInviteBox();
// 5ms 退避重试已把目录拉回（若仍显示「通讯录暂不可用」说明重试未接通）。
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 50)); });
const restored = candidateNames();
assert.ok(restored.length >= 4, `after the retry the candidates are back (got ${restored.length}); not stuck on the failure state`);
const restoredBoxText = chat().querySelector(".channel-chat__invitecandidates")!.textContent ?? "";
assert.ok(!restoredBoxText.includes("Session directory unavailable"), "the unavailable strip clears once the retry lands");

// 失败中（未重试成功前）的诚实态：连续失败时显示「暂不可用」而非「没有可邀请的会话」。
directoryFails = 99; // 持续失败：重试也失败
calls.length = 0;
await mountPanel();
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 80)); });
await openInviteBox();
const failBoxText = chat().querySelector(".channel-chat__invitecandidates")!.textContent ?? "";
assert.match(failBoxText, /Session directory unavailable/, "a persistently failing directory says so instead of the misleading empty state");
const retryCalls = calls.filter((c) => c.name === "ListAddressableSessions").length;
assert.ok(retryCalls >= 3, `the retry ladder keeps polling (got ${retryCalls} attempts)`);

if (currentRoot) {
  await act(async () => { currentRoot!.unmount(); });
}
(collabGroupRetryDelaysMs as unknown as number[]).splice(0, collabGroupRetryDelaysMs.length, ...realDelays);
console.log("tl1216-invite-directory: all assertions passed");
process.exit(0);
