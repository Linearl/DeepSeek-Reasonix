// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1564-dispatch-card.test.tsx
// tl1564（P2 群聊派活过程可视化最小档）harness：
//
//   派活判定镜像 Go routeRecipients（internal/collabchannel/collabchannel.go
//   tl1406 四分支）的「确定性定向投递」面，按 ChannelMessageStream 现有数据
//   结构落地（判定依据逐条见组件 dispatchRecipientsOf 注释）。本 harness 钉：
//
//   ① 渲染形态：派活消息在气泡同位换装任务分配卡（徽标「任务分配」+
//      谁→谁 行 + 任务摘要体）；data-dispatch-to 锚点带执行者显示名。
//   ② 判定分支：routedTo 发布期注记 > body @提及（needle=@显示名/@contactId，
//      历史读回兜底——Go 不落库 routedTo） > human-owned + 在册 deputy 默认
//      收件人（发送者≠deputy）。
//   ③ 零回归锚：deputyless 零投递（Go 分支3）、session-owned fan-out-all
//      （Go 分支4）、仅 @ 自己（Go 发送者自排除=零投递）、无任何路由线索的
//      普通消息 → 一律普通气泡原样（非派活渲染零回归）；已读/投递失败标注、
//      撤回墓碑形态不变。
//
// Go 侧路由语义（分支序/发送者自排除/离册副管理员降级）由 internal/collabchannel
// routing_test.go 持有；本 harness 只钉前端渲染与判定接线。

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
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

const { LocaleProvider } = await import("../lib/i18n");

const { ChannelMessageStream } = await import("../components/ChannelMessageStream");
const { HUMAN_LOCAL } = await import("../components/ChannelChatPanel");

type CollabChannelMemberView = import("../lib/bridge").CollabChannelMemberView;
type CollabChannelMessageView = import("../lib/bridge").CollabChannelMessageView;

const rootEl = document.getElementById("root")!;
const root = createRoot(rootEl);

const memberDetails: CollabChannelMemberView[] = [
  { contact: HUMAN_LOCAL, type: "human", joinedAt: 1 },
  { contact: "sc_dev6", type: "session", joinedAt: 2 },
  { contact: "sc_merge", type: "session", joinedAt: 3 },
];
// 名册显示名镜像应用口径：human 端点=「You」（en，与 collabGroup.chat.you
// 对齐——composer 插入的 @You needle 即由此而来），会话=title。
const directoryTitle = (id: string) =>
  id === HUMAN_LOCAL ? "You" : id === "sc_dev6" ? "开发会话" : id === "sc_merge" ? "合并会话" : id;

const flush = async (): Promise<void> => {
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
};

async function renderStream(
  messages: CollabChannelMessageView[],
  extra: { channelOwnerType?: string; deputyId?: string } = {},
): Promise<void> {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ChannelMessageStream
          messages={messages}
          memberDetails={memberDetails}
          directoryTitle={directoryTitle}
          channelOwnerType={extra.channelOwnerType}
          deputyId={extra.deputyId}
        />
      </LocaleProvider>,
    );
  });
  await flush();
}

const cards = () => Array.from(document.querySelectorAll(".channel-chat__dispatch"));
const bubbles = () => Array.from(document.querySelectorAll(".channel-chat__bubble"));
const cardText = () => cards().map((c) => c.textContent ?? "").join("\n");

// ══ ① 渲染形态：@提及历史消息 → 任务分配卡（谁→谁 + 任务摘要）═══════════
{
  await renderStream([
    // 历史读回形态（routedTo 恒缺——Go 只在 Publish 返回值携带）：@显示名 needle。
    { id: "d1", sender: HUMAN_LOCAL, body: "@开发会话 修一下登录页的报错", at: 1, fanout: [] },
  ]);
  eq(cards().length, 1, "① mention body renders one dispatch card (气泡同位换装)");
  eq(bubbles().length, 0, "① the dispatch message keeps no plain bubble");
  const card = cards()[0];
  eq(card.getAttribute("data-dispatch-to"), "开发会话", "① data-dispatch-to anchor carries the executor display name");
  // JSDOM navigator 默认 en-US → en 字典（与 channel-chat-panel.test.tsx 同一
  // 断言口径）；执行者显示名来自注入的 directoryTitle，locale 无关。
  ok(cardText().includes("Task assignment"), "① card carries the Task assignment badge (locale en)");
  ok(cardText().includes("You → 开发会话"), "① card header states 谁→谁 (sender → executor)");
  ok(cardText().includes("修一下登录页的报错"), "① card body carries the task summary");
  // @contactId needle（Go mentionedMembers 的另一 needle 家族，composer 直打 id 形态）。
  await renderStream([
    { id: "d2", sender: "sc_merge", body: "@sc_dev6 请对齐接口契约", at: 2, fanout: [] },
  ]);
  eq(cards().length, 1, "① @contactId mention also dispatches (needle 家族与 Go 同口径)");
  eq(cards()[0].getAttribute("data-dispatch-to"), "开发会话", "① contact-id mention resolves to the executor display name");
  ok(cardText().includes("合并会话 → 开发会话"), "① session sender → executor assignment line");
}

// ══ ② routedTo 发布期注记优先（无 @ body 也成卡）═════════════════════════
{
  await renderStream([
    { id: "r1", sender: HUMAN_LOCAL, body: "整理周报初稿", at: 3, fanout: [], routedTo: ["sc_dev6", "sc_merge"] },
  ]);
  eq(cards().length, 1, "② routedTo publish annotation renders the card (发布期权威注记)");
  eq(cards()[0].getAttribute("data-dispatch-to"), "开发会话, 合并会话", "② both routed executors listed");
  ok(cardText().includes("You → 开发会话, 合并会话"), "② assignment line covers multi-executor");
}

// ══ ③ deputy 默认收件人分支（human-owned + 在册 deputy + 无提及）══════════
{
  await renderStream(
    [{ id: "p1", sender: HUMAN_LOCAL, body: "把发布清单核一遍", at: 4, fanout: [] }],
    { channelOwnerType: "human", deputyId: "sc_dev6" },
  );
  eq(cards().length, 1, "③ un-mentioned human line in a deputy-equipped human group dispatches to the deputy");
  eq(cards()[0].getAttribute("data-dispatch-to"), "开发会话", "③ the deputy alone is the executor");
  // 发送者=deputy 的无提及发言：自派自收被 Go 发送者排除=零投递=非派活。
  await renderStream(
    [{ id: "p2", sender: "sc_dev6", body: "清单核完了", at: 5, fanout: [] }],
    { channelOwnerType: "human", deputyId: "sc_dev6" },
  );
  eq(cards().length, 0, "③ deputy's own un-mentioned line is not a card (发送者自排除=零投递)");
  eq(bubbles().length, 1, "③ deputy's own line keeps the plain bubble");
}

// ══ ④ 零回归锚：非派活消息一律普通气泡原样 ═══════════════════════════════
{
  await renderStream([
    // Go 分支3：human-owned 无 deputy 无提及 = deputyless 零投递（提示条承载）。
    { id: "z1", sender: HUMAN_LOCAL, body: "大家好，我是人类", at: 6, fanout: [] },
    // Go 分支4：session-owned fan-out-all 广播语义非派活。
    { id: "z2", sender: "sc_owner", body: "合并线开工", at: 7, fanout: [] },
    // 无任何路由线索的普通消息（宿主未传新 props 的旧调用形态）。
    { id: "z3", sender: "sc_dev6", body: "收到", at: 8, fanout: [] },
  ]);
  eq(cards().length, 0, "④ deputyless / fan-out-all / plain lines render zero cards");
  eq(bubbles().length, 3, "④ all three non-dispatch lines keep the plain bubble");
  // session-owned 群即使设了 deputy，无提及发言也是广播（Go 分支4 优先）。
  await renderStream(
    [{ id: "z4", sender: HUMAN_LOCAL, body: "广播一下", at: 9, fanout: [] }],
    { channelOwnerType: "session", deputyId: "sc_dev6" },
  );
  eq(cards().length, 0, "④ session-owned group never dispatches via the deputy branch");
  // 仅 @ 自己：提及分支命中但发送者自排除后为空 = 零投递（Go 同款，不落
  // deputy 分支，也不渲染空卡）。
  await renderStream(
    [{ id: "z5", sender: HUMAN_LOCAL, body: "@You 记一下", at: 10, fanout: [] }],
    { channelOwnerType: "human", deputyId: "sc_dev6" },
  );
  eq(cards().length, 0, "④ self-only mention is zero-delivery, not a card (no empty card)");
  eq(bubbles().length, 1, "④ self-only mention keeps the plain bubble");
}

// ══ ⑤ 既有形态不变：回执标注/撤回墓碑在派活卡消息上原样 ══════════════════
{
  await renderStream([
    {
      id: "q1", sender: HUMAN_LOCAL, body: "@开发会话 出包", at: 11,
      fanout: [
        { messageId: "q1", member: "sc_dev6", fanoutId: "f1", state: "delivered", deliveredAt: 1, readAt: 2 },
        { messageId: "q1", member: "sc_merge", fanoutId: "f2", state: "failed", error: "mailbox closed" },
      ],
    },
  ]);
  eq(cards().length, 1, "⑤ dispatch card keeps the receipts pipeline");
  const rowText = document.querySelector(".channel-chat__row")?.textContent ?? "";
  ok(rowText.includes("开发会话 read"), "⑤ read receipt renders on the dispatch row (钉钉口径不变)");
  ok(rowText.includes("delivery to 合并会话 failed"), "⑤ failed receipt renders on the dispatch row (诚实错误保留)");
  await renderStream([
    { id: "q2", sender: HUMAN_LOCAL, body: "@开发会话 撤回演练", at: 12, fanout: [], cancelledAt: 13 },
  ]);
  eq(document.querySelectorAll(".channel-chat__tombstone").length, 1, "⑤ cancelled dispatch stays a tombstone (349 语义优先)");
  eq(cards().length, 0, "⑤ cancelled dispatch renders no card");
}

process.stdout.write(`\ntl1564 dispatch-card harness: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exitCode = 1;
