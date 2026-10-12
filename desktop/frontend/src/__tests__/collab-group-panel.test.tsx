// Run: tsx src/__tests__/collab-group-panel.test.tsx
// Task 409 acceptance on the panel side:
//   ① 一次呈现 ≥3 个协作会话的状态+当前任务（数据来自既有接口绑定，无新协议）；
//   ② 打开即拉取 + 30s 轮询上界（验收「派单后 1 分钟内刷新」的实现位）+ 手动刷新重新拉取；
//   ③ 空态/单会话不劣化（空态 ≠ 错误；单会话一行，无多余装饰）；
//   ④ 任务587 同款：读取失败显式失败块 + 重试，绝不冒充「暂无协作」。
// The Go tests own the aggregation contracts (collab_view_app_test.go); this
// harness owns the rendering + binding wiring.
//
// Harness note: react/react-dom must be imported AFTER the JSDOM globals are
// in place (same order as collab-inbox-panel) — otherwise controlled-input
// onChange never fires in this environment.

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
const { CollabGroupPanel, setCollabGroupOpen, isCollabGroupOpen, COLLAB_GROUP_POLL_MS, collabGroupRetryDelaysMs } = await import("../components/CollabGroupPanel");
const { LocaleProvider } = await import("../lib/i18n");
type CollabGroupBindings = import("../components/CollabGroupPanel").CollabGroupBindings;
type CollabViewSession = import("../components/CollabGroupPanel").CollabViewSession;
type CollabViewOverview = import("../components/CollabGroupPanel").CollabViewOverview;

// 验收 ② 的实现位：轮询周期 30s 严格小于 1 分钟上界，退避序列存在且递增。
assert.equal(COLLAB_GROUP_POLL_MS, 30_000, "the open-panel poll must stay at 30s (under the 1-minute acceptance bound)");
assert.deepEqual(collabGroupRetryDelaysMs, [2_000, 4_000, 8_000, 15_000], "the failure backoff ladder is pinned");

const session = (over: Partial<CollabViewSession> & { contactId: string }): CollabViewSession => ({
  title: over.contactId,
  state: "unknown",
  lastActivity: 1_700_000_000_000,
  unreadInbox: 0,
  ...over,
});

// 一次呈现 ≥3 个协作会话（验收 ①）的状态+当前任务+最近往来。
const threeSessions: CollabViewSession[] = [
  session({ contactId: "sc_main", title: "主对话", state: "running", purpose: "协调今晚出包", identityType: "main", card: { id: "c1", title: "409 群聊视图", status: "running", updatedAt: 1_700_000_000_000 }, lastExchange: { from: "sc_main", to: "sc_sub", preview: "派单：先做后端聚合", at: 1_700_000_000_001 } }),
  session({ contactId: "sc_sub", title: "子对话·前端", state: "queued", identityType: "sub", unreadInbox: 2, card: { id: "c1", title: "409 群聊视图", status: "pending", updatedAt: 1_700_000_000_002 } }),
  session({ contactId: "sc_heartbeat", title: "心跳会话", state: "idle", identityType: "heartbeat" }),
  session({ contactId: "sc_remote", title: "另一进程的会话", state: "unknown" }),
];

type Call = { name: string; args: unknown[] };
const calls: Call[] = [];
let overview: CollabViewOverview = { sessions: threeSessions, generatedAt: 1_700_000_003_000 };
let overviewShouldFail = false;

const bindings: CollabGroupBindings = {
  async GetCollabViewOverview() {
    calls.push({ name: "GetCollabViewOverview", args: [] });
    if (overviewShouldFail) throw new Error("gateway closed");
    return overview;
  },
  async ListCollabChannels() {
    calls.push({ name: "ListCollabChannels", args: [] });
    return [
      { id: "ch_dev", name: "dev", createdAt: 1_699_000_000_000, hourlyLimit: 12, members: ["sc_main", "sc_sub"], messages: 2 },
    ];
  },
  async ReadCollabChannel(ref, limit) {
    calls.push({ name: "ReadCollabChannel", args: [ref, limit] });
    return {
      channel: { id: ref, name: "dev", createdAt: 1_699_000_000_000, hourlyLimit: 12, members: ["sc_main", "sc_sub"], messages: 2 },
      messages: [
        { id: "m1", sender: "sc_main", body: "第一行群发", at: 1_700_000_001_000, fanout: [
          { messageId: "m1", member: "sc_main", fanoutId: "f1", state: "delivered", deliveredAt: 1_700_000_001_100, readAt: 1_700_000_001_200 },
          { messageId: "m1", member: "sc_sub", fanoutId: "f2", state: "delivered", deliveredAt: 1_700_000_001_100 },
        ] },
        { id: "m2", sender: "sc_sub", body: "收到，回执随后", at: 1_700_000_002_000 },
      ],
    };
  },
  // 802：channels tab 由 ChannelChatPanel 承载，其聊天写路径在本 harness
  // 不断言（由 channel-chat-panel.test.tsx 深测）——类型面补齐为诚实空实现。
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

const root = createRoot(document.getElementById("root")!);
setCollabGroupOpen(true);
await act(async () => {
  root.render(
    <LocaleProvider>
      <CollabGroupPanel bindings={bindings} />
    </LocaleProvider>,
  );
});

assert.equal(isCollabGroupOpen(), true, "the module-level open flag is on");
const panel = document.querySelector(".collab-group-panel");
assert.ok(panel, "the panel renders into document.body once opened");
assert.equal(calls.filter((c) => c.name === "GetCollabViewOverview").length >= 1, true, "opening loads the overview");

// 验收 ①：四个会话行同屏；状态 chip 用共享判定的词汇；任务卡与最近往来同行呈现。
// （harness 的 navigator.language 是 en-US，标签按 en 字典断言；zh/zh-TW 键集
// 由 tsc 的 Record<DictKey, string> 契约保证一致。）
const rows = Array.from(panel!.querySelectorAll(".collab-group-panel__session"));
assert.ok(rows.length >= 3, `the roster shows ${rows.length} sessions in ONE view (acceptance: ≥3)`);
const text = panel!.textContent ?? "";
for (const [needle, what] of [
  ["主对话", "session title"], ["Running", "running state chip"], ["409 群聊视图", "current task card"],
  ["派单：先做后端聚合", "latest exchange preview"],
  ["子对话·前端", "second session"], ["心跳会话", "third session"], ["Idle", "idle chip"],
  ["另一进程的会话", "fourth session"], ["Unknown", "unknown chip"],
  ["Read-only observation", "the observation-window note (never invents AI chat)"],
] as const) {
  assert.match(text, new RegExp(needle), `the view shows ${what}`);
}
assert.match(text, /2 unread/, "the queued session's unread badge renders");

// 频道 tab（802 起）：微信式真群聊由 ChannelChatPanel 承载——面板级只断言
// 接线（挂载/名册/预览走 409 读口）；聊天交互与钉钉口径细节由
// channel-chat-panel.test.tsx 深测。旧「名册展开+fanout 标注」断言随 UI
// 演进迁移：delivered 回执不再显示（802 补充口径①）。
const tabButtons = Array.from(panel!.querySelectorAll<HTMLButtonElement>(".collab-group-panel__tab"));
assert.equal(tabButtons.length, 2, "two tabs render (sessions / channels)");
await act(async () => {
  tabButtons.find((b) => b.textContent === "Channels")!.click();
});
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
const chatPanel = panel!.querySelector(".channel-chat");
assert.ok(chatPanel, "the channels tab mounts the WeChat-style chat view");
const chatItems = Array.from(panel!.querySelectorAll(".channel-chat__item"));
assert.equal(chatItems.length, 1, "the 349 channel roster renders inside the chat view");
assert.match(chatItems[0].textContent ?? "", /dev/, "the group line shows its name");
assert.ok(calls.some((c) => c.name === "ListCollabChannels"), "the chat view reads the channel roster");
const previewCalls = calls.filter((c) => c.name === "ReadCollabChannel");
assert.ok(previewCalls.length >= 1 && previewCalls[previewCalls.length - 1].args[1] === 1, "list previews read via the 409 port with limit=1 (unread semantics preserved)");
assert.doesNotMatch(panel!.textContent ?? "", /Delivered/, "no delivered-receipt wording anywhere (DingTalk rule)");
// 切回会话 tab：聊天面板随 tab 卸载（关面板/切 tab 零成本）。
await act(async () => {
  tabButtons.find((b) => b.textContent === "Sessions")!.click();
});
assert.equal(panel!.querySelector(".channel-chat"), null, "switching tabs unmounts the chat view");

// 验收 ③（空态）：空目录渲染「暂无活跃协作会话」，不是错误块。
overview = { sessions: [], generatedAt: 1_700_000_004_000 };
await act(async () => {
  tabButtons.find((b) => b.textContent === "Sessions")!.click();
});
await act(async () => {
  const refreshButton = panel!.querySelector<HTMLButtonElement>(".collab-group-panel__actions .btn");
  refreshButton!.click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});
assert.ok(!document.querySelector(".collab-group-panel__error"), "a healthy empty directory is not an error");
assert.match(document.querySelector(".collab-group-panel")!.textContent ?? "", /No active collaboration sessions/, "the empty state renders its own message");

// 验收 ③（单会话）：一行会话正常呈现，无多余装饰。
overview = { sessions: [threeSessions[0]], generatedAt: 1_700_000_005_000 };
await act(async () => {
  const refreshButton = document.querySelector<HTMLButtonElement>(".collab-group-panel__actions .btn");
  refreshButton!.click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});
const singleRows = document.querySelectorAll(".collab-group-panel__session");
assert.equal(singleRows.length, 1, "a single session renders exactly one row");
assert.match(document.querySelector(".collab-group-panel")!.textContent ?? "", /主对话/, "the single session keeps its full row content");

// 验收 ④（失败≠空态）：网关关闭 → 显式失败块 + 重试按钮，绝不渲染「暂无」；
// 重试成功后回到数据面，退避计数复位。先做一次成功的空刷新（面板手里真正
// 无数据），失败才走整块错误；有旧数据时走 errorstrip（587 语义：旧数据优先）。
overview = { sessions: [], generatedAt: 1_700_000_006_000 };
await act(async () => {
  const refreshButton = document.querySelector<HTMLButtonElement>(".collab-group-panel__actions .btn");
  refreshButton!.click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});
assert.match(document.querySelector(".collab-group-panel")!.textContent ?? "", /No active collaboration sessions/, "the panel holds an empty roster before the failure");
overviewShouldFail = true;
await act(async () => {
  const refreshButton = document.querySelector<HTMLButtonElement>(".collab-group-panel__actions .btn");
  refreshButton!.click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});
const errorBlock = document.querySelector(".collab-group-panel__error");
assert.ok(errorBlock, "a failed read with no data renders the explicit error block");
assert.match(errorBlock!.textContent ?? "", /Failed to load the collaboration view/, "the error block names the failure");
assert.match(errorBlock!.textContent ?? "", /gateway closed/, "the error detail carries the raw cause");
assert.ok(errorBlock!.querySelector("button"), "the error block offers a retry button");
assert.doesNotMatch(document.body.textContent ?? "", /No active collaboration sessions/, "a failure must never render as the healthy empty state");

overviewShouldFail = false;
overview = { sessions: [threeSessions[0]], generatedAt: 1_700_000_007_000 };
await act(async () => {
  const retry = document.querySelector<HTMLButtonElement>(".collab-group-panel__error button");
  retry!.click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});
assert.ok(!document.querySelector(".collab-group-panel__error"), "a successful retry clears the error block");
assert.equal(document.querySelectorAll(".collab-group-panel__session").length, 1, "the retried view shows the data again");

setCollabGroupOpen(false);
console.log("collab-group-panel: all assertions passed");
