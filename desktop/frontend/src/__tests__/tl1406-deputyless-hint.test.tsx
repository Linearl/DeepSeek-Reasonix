// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1406-deputyless-hint.test.tsx
// tl1406 验收（前端件）：human 群未设副管理员时未 @ 发言 = 零投递——发送成功
// 但没人被推送，后端回执带 deputyless=true，前端如实提示（Q3）；下一条正常
// 路由的消息（deputyless=false）把提示清掉。文案键 collabGroup.chat.deputylessHint。
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

const { act } = await import("react"); // tsconfig jsx=react-jsx：JSX 不引 React 命名空间
const { createRoot } = await import("react-dom/client");
const { LocaleProvider } = await import("../lib/i18n");
const { ChannelChatPanel, HUMAN_LOCAL } = await import("../components/ChannelChatPanel");
type ChannelChatBindings = import("../components/ChannelChatPanel").ChannelChatBindings;
type CollabChannelView = import("../lib/bridge").CollabChannelView;

const chMine: CollabChannelView = {
  id: "ch_dl", name: "无副群", createdAt: 1_699_000_000_000,
  hourlyLimit: 30, members: [HUMAN_LOCAL], messages: 0,
  ownerType: "human", ownerId: HUMAN_LOCAL,
  memberDetails: [{ contact: HUMAN_LOCAL, type: "human", joinedAt: 1_699_000_000_000 }],
};

// 发送回执可编排：每次 send 弹出下一份（默认 deputyless=false）。
let sendReplies: { deputyless?: boolean }[] = [];
const sentBodies: string[] = [];

const bindings: ChannelChatBindings = {
  async ListCollabChannels() { return [chMine]; },
  async ReadCollabChannel(_ref, _limit) { return { channel: { ...chMine, messages: 0 }, messages: [] as never[] }; },
  async ChannelMessagesForHuman(_ref, _sinceMs, _limit) { return { channel: { ...chMine, messages: 0 }, messages: [] as never[] }; },
  async ChannelCreateForHuman(_name, _topic, _hourlyLimit) { return { ...chMine }; },
  async ChannelLeaveHuman(_ref) {},
  async ChannelSendFromHuman(_ref, body) {
    sentBodies.push(body);
    const reply = sendReplies.shift() ?? { deputyless: false };
    return { id: `m${sentBodies.length}`, sender: HUMAN_LOCAL, body, at: 1, fanout: [], deputyless: reply.deputyless };
  },
  async ChannelInvite(_ref, _contactIDs) { return { ...chMine }; },
  async ChannelRemoveMember(_ref, _contactID) { return { ...chMine }; },
  async ChannelSetDeputy(_ref, _contactID) { return { ...chMine }; },
  async ChannelUnsetDeputy(_ref) { return { ...chMine }; },
  async ChannelDissolveForHuman(_ref) {},
  async ChannelCancelFromHuman(_ref, messageID) { return { messageId: messageID, channel: _ref, cancelledAt: 1, cancelledQueued: 0, alreadyDelivered: 0, alreadyCancelled: false }; },
  async ListAddressableSessions() { return []; },
};

const chat = () => document.querySelector(".channel-chat")!;

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

function setNativeValue(el: HTMLTextAreaElement, value: string): void {
  const proto = dom.window.HTMLTextAreaElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(proto, "value")!.set!;
  setter.call(el, value);
  el.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
}

async function send(body: string): Promise<void> {
  await act(async () => { (chat().querySelector(".channel-chat__item") as HTMLElement).click(); });
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
  await act(async () => { setNativeValue(chat().querySelector(".channel-chat__input") as HTMLTextAreaElement, body); });
  await act(async () => {
    (Array.from(chat().querySelectorAll("button")).find((b) => b.classList.contains("channel-chat__send")) as HTMLElement).click();
  });
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
}
const hint = () => chat().querySelector(".channel-chat__deputyless");

await mountPanel();

// ① deputyless=true：提示出现，文案键渲染为英文默认 locale 文本。
sendReplies = [{ deputyless: true }];
await send("今天先做 A 项");
assert.ok(hint(), "zero-delivery send must surface the deputyless hint");
assert.match(hint()!.textContent!, /No deputy in this group/, `hint copy: ${hint()!.textContent}`);

// ② 关闭按钮可手动清提示。
await act(async () => {
  (Array.from(hint()!.querySelectorAll("button")).find((b) => b.textContent === "Close") as HTMLElement).click();
});
await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
assert.equal(hint(), null, "the dismiss button clears the hint");

// ③ 正常路由（deputyless=false）不提示；下一条把旧提示清掉。
sendReplies = [];
await send("普通消息");
assert.equal(hint(), null, "a routed send leaves no hint");

sendReplies = [{ deputyless: true }];
await send("没人收到的那条");
assert.ok(hint(), "hint re-arms for a deputyless send");
sendReplies = [];
await send("又一条正常消息");
assert.equal(hint(), null, "the next routed send clears the stale hint");

console.log("tl1406-deputyless-hint: all assertions passed");
process.exit(0); // 面板轮询 timer 挂住事件循环，断言全过后显式退出（tl1216 同款）
