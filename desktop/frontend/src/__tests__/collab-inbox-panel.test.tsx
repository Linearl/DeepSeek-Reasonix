// Run: tsx src/__tests__/collab-inbox-panel.test.tsx
// Task 320 acceptance on the panel side: five buckets render, sender filter
// reaches the backend, the asc/desc date toggle re-queries with order=asc (a),
// dismiss/decide/retention return the new snapshot
// (contract ①), the chain view groups by thread (g), and the revision footer
// is visible. The Go tests own the storage contracts; this harness owns the
// rendering + binding wiring.
//
// Harness note: react/react-dom must be imported AFTER the JSDOM globals are
// in place (same order as ask-card-identity) — otherwise controlled-input
// onChange never fires in this environment.

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
const { CollabInboxPanel, setCollabInboxOpen, useCollabInboxUnreadCount } = await import("../components/CollabInboxPanel");
const { LocaleProvider } = await import("../lib/i18n");
type CollabInboxBindings = import("../components/CollabInboxPanel").CollabInboxBindings;
type CollabMailEntry = import("../components/CollabInboxPanel").CollabMailEntry;
type CollabMailSnapshot = import("../components/CollabInboxPanel").CollabMailSnapshot;

const entry = (over: Partial<CollabMailEntry> & { id: string }): CollabMailEntry => ({
  from: "sc_alice",
  to: "sc_main",
  at: 1_700_000_000_000,
  threadId: over.id,
  bucket: "mention",
  preview: "hello",
  delivered: true,
  read: false,
  ...over,
});

// One fixture per bucket + one approval pending on the viewer + one decided.
const fixtureEntries: CollabMailEntry[] = [
  entry({ id: "m_approval", bucket: "approval", approver: "sc_main", pendingMe: true, preview: "deploy request" }),
  entry({ id: "m_decided", bucket: "approval", approver: "sc_main", decidedBy: "human", preview: "already judged" }),
  entry({ id: "m_mention", preview: "please investigate" }),
  entry({ id: "m_group", channel: "dev", preview: "from the dev channel" }),
  entry({ id: "m_auto", bucket: "automation", from: "sc_heartbeat", preview: "cron report" }),
  entry({ id: "m_system", bucket: "system", preview: "read receipt" }),
];

type Call = { name: string; args: unknown[] };
const calls: Call[] = [];
let snapshot: CollabMailSnapshot = {
  revision: "1.5.1700000000000",
  settings: { retention: "7d" },
  total: fixtureEntries.length,
  returned: fixtureEntries.length,
  truncated: false,
  entries: fixtureEntries,
};

const bindings: CollabInboxBindings = {
  async ListCollabMail(bucket, from, to, state, limit, includeDismissed, order) {
    calls.push({ name: "ListCollabMail", args: [bucket, from, to, state, limit, includeDismissed, order] });
    return {
      ...snapshot,
      entries: snapshot.entries.filter((e) => (bucket === "all" ? true : e.bucket === bucket))
        .filter((e) => (from ? e.from === from : true))
        .filter((e) => (to ? e.to === to : true)),
    };
  },
  async ListCollabMailChains(bucket, limit) {
    calls.push({ name: "ListCollabMailChains", args: [bucket, limit] });
    return {
      revision: snapshot.revision,
      settings: snapshot.settings,
      total: 1,
      chains: [
        {
          threadId: "m_mention",
          participants: ["sc_alice", "sc_main"],
          count: 3,
          firstAt: 1_600_000_000_000,
          lastAt: 1_700_000_000_000,
          preview: "please investigate",
          entries: [entry({ id: "m_mention", preview: "please investigate", channel: "dev" }), entry({ id: "r1" }), entry({ id: "r2" })],
        },
      ],
    };
  },
  async DismissCollabMail(ids) {
    calls.push({ name: "DismissCollabMail", args: [ids] });
    const entries = snapshot.entries.filter((e) => !ids.includes(e.id));
    snapshot = { ...snapshot, revision: "2.0.0", entries, total: entries.length };
    return snapshot;
  },
  async MarkCollabMailRead(ids) {
    calls.push({ name: "MarkCollabMailRead", args: [ids] });
    const entries = snapshot.entries.map((e) => (ids.includes(e.id) ? { ...e, read: true } : e));
    snapshot = { ...snapshot, revision: "6.0.0", entries };
    return snapshot;
  },
  async UndismissCollabMail(ids) {
    calls.push({ name: "UndismissCollabMail", args: [ids] });
    return snapshot;
  },
  async MarkCollabMailDecided(messageID, by) {
    calls.push({ name: "MarkCollabMailDecided", args: [messageID, by] });
    const entries = snapshot.entries.map((e) => (e.id === messageID ? { ...e, decidedBy: by, pendingMe: false } : e));
    snapshot = { ...snapshot, revision: "3.0.0", entries };
    return snapshot;
  },
  async SetCollabMailRetention(retention) {
    calls.push({ name: "SetCollabMailRetention", args: [retention] });
    snapshot = { ...snapshot, revision: "4.0.0", settings: { retention } };
    return snapshot;
  },
  async SetCollabMailCleanupRule(rule) {
    calls.push({ name: "SetCollabMailCleanupRule", args: [rule] });
    snapshot = { ...snapshot, revision: "7.0.0", settings: { ...snapshot.settings, cleanupRule: rule } };
    return snapshot;
  },
  async CleanCollabMailNow() {
    calls.push({ name: "CleanCollabMailNow", args: [] });
    // 任务620：模拟「已删除会话的残留信」被清理——按当前规则移除后带回新快照。
    const entries = snapshot.entries.filter((e) => e.from !== "sc_gone");
    const removed = snapshot.entries.length - entries.length;
    snapshot = { ...snapshot, revision: "8.0.0", entries, total: entries.length };
    return { removed, snapshot };
  },
};

// 任务461-P4: an injectable addressable roster backs the from/to dropdowns —
// one project session (with group data seeded) + one global session.
const directory = {
  async ListAddressableSessions() {
    calls.push({ name: "ListAddressableSessions", args: [] });
    return [
      {
        contactId: "sc_alice",
        title: "Alice · 调研",
        sessionPath: "/tmp/projects/alpha/sessions/alice.session.jsonl",
        scope: "project",
        workspaceRoot: "/tmp/projects/alpha",
        open: true,
      },
      {
        contactId: "sc_main",
        title: "主对话",
        sessionPath: "/tmp/global/main.session.jsonl",
        scope: "global",
        open: true,
      },
    ];
  },
};

const root = createRoot(document.getElementById("root")!);
setCollabInboxOpen(true);
await act(async () => {
  root.render(
    <LocaleProvider>
      <CollabInboxPanel bindings={bindings} directory={directory} />
    </LocaleProvider>,
  );
});

const panel = document.querySelector(".collab-inbox-panel");
assert.ok(panel, "the panel renders into document.body once opened");
assert.equal(calls.some((c) => c.name === "ListCollabMail"), true, "opening loads the list snapshot");
assert.match(panel!.textContent ?? "", /Snapshot 1\.5\.1700000000000/, "the revision footer is visible (contract ①)");

// 任务 349n1 群标识：fan-out 来源频道以 #名 chip 标注在条目上；点对点信不出 chip。
const channelChips = Array.from(panel!.querySelectorAll(".collab-inbox-panel__channel"));
assert.equal(channelChips.length, 1, "exactly one group-stamped entry renders a channel chip");
assert.equal(channelChips[0].textContent, "#dev", "the channel chip shows #name");

// 五桶 tab 切换 → 后端按桶过滤。
const bucketButtons = Array.from(panel!.querySelectorAll<HTMLButtonElement>(".collab-inbox-panel__bucket"));
assert.equal(bucketButtons.length, 5, "five bucket tabs render");
await act(async () => {
  bucketButtons.find((b) => b.textContent === "Approvals")!.click();
});
let lastList = calls.filter((c) => c.name === "ListCollabMail").pop();
assert.deepEqual(lastList?.args[0], "approval", "clicking the approvals tab queries bucket=approval");

// 任务462: 列表行的路由行显示双方会话名（名单里查得到的 id 用名字），
// 完整 contact_id 降级为 hover（title 属性），不丢失。
// 任务649 ②：箭头两侧补「发信方/收信方」标签词（默认 en 语言包 → Sender/Recipient），
// 谁发谁收一眼可辨。
const routeSpans = Array.from(panel!.querySelectorAll(".collab-inbox-panel__row .collab-inbox-panel__route"));
assert.ok(routeSpans.length >= 1, "at least one list row renders a route line");
assert.match(
  routeSpans[0].textContent ?? "",
  /Sender Alice · 调研 → Recipient 主对话/,
  "the list route labels BOTH sides with sender/recipient tag words",
);
assert.equal(routeSpans[0].querySelectorAll(".collab-inbox-panel__routelabel").length, 2,
  "both tag words render as styled label spans");
assert.doesNotMatch(routeSpans[0].textContent ?? "", /sc_alice/, "the raw sender contact_id is replaced by the name in visible text");
assert.match(routeSpans[0].getAttribute("title") ?? "", /contact_id=sc_alice/, "hover keeps the full sender contact_id");
assert.match(routeSpans[0].getAttribute("title") ?? "", /contact_id=sc_main/, "hover keeps the full recipient contact_id");

// 任务649 ②：from/to 下拉带可见标签（label 包裹，文案同 sender/recipient 键）。
const filterWraps = Array.from(panel!.querySelectorAll(".collab-inbox-panel__filterwrap"));
assert.equal(filterWraps.length, 2, "both filter dropdowns carry a visible label");
assert.equal(filterWraps[0].querySelector("span")?.textContent, "Sender", "the sender dropdown's label word renders");
assert.equal(filterWraps[1].querySelector("span")?.textContent, "Recipient", "the recipient dropdown's label word renders");

// 任务649 ①+任务716 ③：排序/视图分段控件落位视图工具条（716 定稿第三位；
// 649 时曾在头行 actions），不再留在 filters 行。
assert.ok(
  panel!.querySelector(".collab-inbox-panel__toolbar .collab-inbox-panel__ordertoggle"),
  "the date-sort segmented control lives in the view toolbar row",
);
assert.ok(
  panel!.querySelector(".collab-inbox-panel__toolbar .collab-inbox-panel__viewtoggle"),
  "the view segmented control lives in the view toolbar row",
);
assert.ok(
  !panel!.querySelector(".collab-inbox-panel__filters .collab-inbox-panel__ordertoggle")
    && !panel!.querySelector(".collab-inbox-panel__filters .collab-inbox-panel__viewtoggle"),
  "the filters row no longer hosts the sort/view controls",
);

// 任务716（用户拖拽定稿）：面板四区块自上而下 = 头部 → 维护行 → 视图工具条
// → 桶过滤；头部只剩标题+关闭（无副标题），维护行收纳保留期/清理条件/立即清理。
{
  const suffixes = ["head", "maint", "toolbar", "buckets"];
  const indexes = suffixes.map((suffix) => {
    const el = panel!.querySelector(`.collab-inbox-panel__${suffix}`);
    return el ? Array.from(panel!.children).indexOf(el) : -1;
  });
  assert.ok(
    indexes.every((idx) => idx >= 0),
    "all four fixed blocks render",
  );
  assert.deepEqual(
    [...indexes].sort((a, b) => a - b),
    indexes,
    "the four blocks stack in the dragged-final order head → maint → toolbar → buckets",
  );
  assert.equal(
    panel!.querySelectorAll(".collab-inbox-panel__head span").length,
    1,
    "the head renders only the title span — no subtitle (716 ③)",
  );
  const maintSelects = panel!.querySelectorAll(".collab-inbox-panel__maint select");
  assert.equal(maintSelects.length, 2, "the maintenance row hosts both the retention and cleanup selects");
  assert.ok(
    Array.from(panel!.querySelectorAll<HTMLButtonElement>(".collab-inbox-panel__maint button"))
      .some((btn) => btn.textContent === "Clean now"),
    "the clean-now button lives in the maintenance row",
  );
}

// 任务461-P4: from/to 过滤是下拉 —— 选项=会话名（值=contact_id），hover 标题
// 是「项目 › 分组 › 会话名 › contact_id」，顶部「全部」= 不过滤；选中即透传后端。
const directoryCalls = calls.filter((c) => c.name === "ListAddressableSessions");
assert.equal(directoryCalls.length >= 1, true, "opening the panel loads the addressable roster");
const [senderSelect, recipientSelect] = panel!.querySelectorAll<HTMLSelectElement>(".collab-inbox-panel__filter");
assert.ok(senderSelect && recipientSelect, "the from/to filters render as dropdowns");
const senderOptions = Array.from(senderSelect.querySelectorAll("option"));
assert.equal(senderOptions[0].textContent, "All", "the top option is the 「全部」 catch-all");
assert.equal(senderOptions[0].value, "", "the catch-all filters nothing off");
const aliceOption = senderOptions.find((o) => o.value === "sc_alice");
assert.ok(aliceOption, "each roster session renders an option keyed by contact_id");
assert.equal(aliceOption!.textContent, "Alice · 调研", "the option label is the session name");
assert.match(aliceOption!.title, /alpha/, "hover title names the project");
assert.match(aliceOption!.title, /sc_alice/, "hover title ends with the contact_id");
const mainOption = senderOptions.find((o) => o.value === "sc_main");
assert.ok(mainOption, "the global session renders too");
assert.ok(mainOption!.title.startsWith("Global"), "a scope-less session's breadcrumb leads with the global label");
await act(async () => {
  Object.getOwnPropertyDescriptor(dom.window.HTMLSelectElement.prototype, "value")!.set!.call(senderSelect, "sc_alice");
  senderSelect.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
});
lastList = calls.filter((c) => c.name === "ListCollabMail").pop();
assert.deepEqual(lastList?.args[1], "sc_alice", "the sender filter reaches the backend query");
assert.deepEqual(lastList?.args[2], "", "the untouched recipient filter stays the catch-all");
await act(async () => {
  Object.getOwnPropertyDescriptor(dom.window.HTMLSelectElement.prototype, "value")!.set!.call(recipientSelect, "sc_main");
  recipientSelect.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
});
lastList = calls.filter((c) => c.name === "ListCollabMail").pop();
assert.deepEqual(lastList?.args[2], "sc_main", "the recipient filter reaches the backend query");
await act(async () => {
  Object.getOwnPropertyDescriptor(dom.window.HTMLSelectElement.prototype, "value")!.set!.call(senderSelect, "");
  senderSelect.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
});
lastList = calls.filter((c) => c.name === "ListCollabMail").pop();
assert.deepEqual(lastList?.args[1], "", "switching back to 「全部」 clears the sender filter");

// 日期排序切换（a）：默认最新在前（desc）；点「Oldest first」→ 透传 order=asc。
// 排序本身的正确性由 Go 侧 TestFilterBySenderRecipientAndDateOrder 承担，
// 这里的接缝职责是「控件状态一定到达后端查询」。
const orderButtons = Array.from(
  panel!.querySelectorAll<HTMLButtonElement>(".collab-inbox-panel__ordertoggle .collab-inbox-panel__state"),
);
assert.equal(orderButtons.length, 2, "the date sort toggle renders two options");
assert.equal(
  calls.filter((c) => c.name === "ListCollabMail").pop()?.args[6],
  "desc",
  "the date sort defaults to newest-first",
);
await act(async () => {
  orderButtons.find((b) => b.textContent === "Oldest first")!.click();
});
assert.equal(
  calls.filter((c) => c.name === "ListCollabMail").pop()?.args[6],
  "asc",
  "clicking oldest-first re-queries with order=asc",
);

// 待我审子态可见（审批桶激活后 state chips 出现）。
assert.ok(
  Array.from(panel!.querySelectorAll(".collab-inbox-panel__state")).some((b) => b.textContent === "Awaiting me"),
  "approval bucket exposes the 待我审 sub-state",
);

// 裁决：对未裁决审批点「标记已裁决」→ decidedBy=human 回显。
await act(async () => {
  Array.from(panel!.querySelectorAll<HTMLButtonElement>("button"))
    .find((b) => b.textContent === "Mark decided" && b.closest(".collab-inbox-panel__row"))
    ?.click();
});
const decideCall = calls.find((c) => c.name === "MarkCollabMailDecided");
assert.deepEqual(decideCall?.args, ["m_approval", "human"], "the decide action records the decider as human");
assert.match(document.body.textContent ?? "", /Decided by human/, "the recorded decider renders on the row");

// 消除 → 新快照行消失（e-①：调用即返回新 revision）。
await act(async () => {
  Array.from(panel!.querySelectorAll<HTMLButtonElement>("button"))
    .find((b) => b.textContent === "Dismiss" && b.closest(".collab-inbox-panel__row"))
    ?.click();
});
assert.equal(calls.some((c) => c.name === "DismissCollabMail"), true, "dismiss goes through the binding");
assert.match(document.body.textContent ?? "", /Snapshot 2\.0\.0/, "dismiss returns the NEW revision to the footer");

// 保留期切换 → SetCollabMailRetention(30d)。
const retentionSelect = panel!.querySelector<HTMLSelectElement>(".collab-inbox-panel__retention select")!;
await act(async () => {
  const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLSelectElement.prototype, "value")!.set!;
  setter.call(retentionSelect, "30d");
  retentionSelect.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
});
assert.deepEqual(calls.find((c) => c.name === "SetCollabMailRetention")?.args, ["30d"], "retention switch reaches the backend");

// 任务 464：会话删除清理规则四选一 → SetCollabMailCleanupRule(both)。
// 第一个 .collab-inbox-panel__retention 是保留期，第二个是清理规则。
const cleanupSelect = panel!.querySelectorAll<HTMLSelectElement>(".collab-inbox-panel__retention select")[1];
assert.ok(cleanupSelect, "the cleanup rule select renders next to retention");
assert.equal(cleanupSelect.value, "never", "the cleanup rule defaults to never (keep everything)");
await act(async () => {
  const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLSelectElement.prototype, "value")!.set!;
  setter.call(cleanupSelect, "both");
  cleanupSelect.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
});
assert.deepEqual(calls.find((c) => c.name === "SetCollabMailCleanupRule")?.args, ["both"], "cleanup rule switch reaches the backend");

// 任务620：立即清理——按钮触发 CleanCollabMailNow（与面板打开同一套写侧维护，
// 去节流），返回的移除数成为就地反馈，清理后的新快照直接接管列表。
{
  snapshot = {
    ...snapshot,
    entries: [...fixtureEntries, entry({ id: "m_gone", from: "sc_gone", preview: "stale mail from a deleted session" })],
    total: fixtureEntries.length + 1,
  };
  await act(async () => {
    Array.from(panel!.querySelectorAll<HTMLButtonElement>("button"))
      .find((b) => b.textContent === "Clean now")!
      .click();
  });
  assert.equal(calls.some((c) => c.name === "CleanCollabMailNow"), true, "the clean-now button goes through the binding");
  assert.match(document.body.textContent ?? "", /Cleaned 1/, "the removed count renders as the click feedback");
  assert.match(document.body.textContent ?? "", /Snapshot 8\.0\.0/, "clean-now returns the NEW snapshot to the footer");
  assert.equal(
    document.body.textContent?.includes("stale mail from a deleted session"),
    false,
    "the cleaned-up entry leaves the list",
  );
}

// 对话链视图（g）。
const chainToggle = Array.from(panel!.querySelectorAll<HTMLButtonElement>(".collab-inbox-panel__state"))
  .find((b) => b.textContent === "Chains");
assert.ok(chainToggle, "the chain view toggle renders");
await act(async () => {
  chainToggle!.click();
});
assert.equal(calls.some((c) => c.name === "ListCollabMailChains"), true, "chain view queries the grouped endpoint");
assert.match(document.body.textContent ?? "", /3 rounds/, "chain round count renders");
const chainHead = document.querySelector<HTMLButtonElement>(".collab-inbox-panel__chainhead");
assert.ok(chainHead, "a chain row renders");
await act(async () => {
  chainHead!.click();
});
assert.match(document.body.textContent ?? "", /please investigate/, "expanding a chain reveals its rounds");
assert.match(document.body.textContent ?? "", /#dev/, "the chain entry carries the channel chip too");

// 任务462: 会话链的参与者与条目发方都显示会话名，完整 id 在 hover。
const chainRoute = document.querySelector(".collab-inbox-panel__chainroute");
assert.ok(chainRoute, "the chain head renders its route line");
assert.match(chainRoute!.textContent ?? "", /Alice · 调研 ↔ 主对话/, "chain participants render as conversation names");
assert.match(chainRoute!.getAttribute("title") ?? "", /contact_id=sc_alice/, "chain hover keeps the sender contact_id");
const chainEntryRoute = document.querySelector(".collab-inbox-panel__chainentry .collab-inbox-panel__route");
assert.ok(chainEntryRoute, "a chain entry renders its sender route");
assert.equal(chainEntryRoute!.textContent, "Alice · 调研", "the chain entry's sender shows the conversation name");
assert.match(chainEntryRoute!.getAttribute("title") ?? "", /contact_id=sc_alice/, "the chain entry hover keeps the contact_id");

await act(async () => setCollabInboxOpen(false));
assert.equal(document.querySelector(".collab-inbox-panel"), null, "closing unmounts the panel");

// 任务 320 遗留 #1：图标行未读徽标 hook —— 挂载取一次数；面板开/合各刷新一次
// （打开路径可能触发保留期清理，关闭路径紧随已读/消除，两个时刻都可能变）；
// tl1226 补第三条（写操作成功广播，回归断言见下文 P8 节）。
// 徽标的按钮接线与 >99 折叠由 App.tsx 承担（integrity 锚点锁接缝）。
let unreadCount = 3;
const badgeBindings: CollabInboxBindings = {
  ...bindings,
  async CountUnreadCollabMail() {
    calls.push({ name: "CountUnreadCollabMail", args: [] });
    return unreadCount;
  },
};
function BadgeProbe() {
  const n = useCollabInboxUnreadCount(badgeBindings);
  return <span className="badge-probe">{n}</span>;
}
const badgeHost = document.createElement("div");
document.body.appendChild(badgeHost);
const badgeRoot = createRoot(badgeHost);
await act(async () => {
  badgeRoot.render(<BadgeProbe />);
});
assert.equal(document.querySelector(".badge-probe")!.textContent, "3", "mounting the badge hook fetches the unread count");
unreadCount = 1;
await act(async () => setCollabInboxOpen(true));
await act(async () => setCollabInboxOpen(false));
assert.equal(document.querySelector(".badge-probe")!.textContent, "1", "panel open/close transitions refresh the count");
assert.equal(
  calls.filter((c) => c.name === "CountUnreadCollabMail").length >= 3,
  true,
  "the badge count is fetched on mount and on every open/close transition",
);
await act(async () => badgeRoot.unmount());
badgeHost.remove();

// ── 任务 461 P8 ③：折叠计数徽标 + 一键批量已读 ─────────────────────────
// 完全同内容的 9 条折叠为 1 条并渲染 ×N 徽标（Go 侧 TestNineDuplicatesFold-
// ToOneAndBatchMarkRead 承担存储契约；这里的接缝职责是徽标渲染与按钮接线）。
const foldedFixture = entry({ id: "m_dup", preview: "heartbeat confirmation", duplicateCount: 9 });
const consumedFixture = entry({ id: "m_consumed", preview: "already consumed", read: true });
snapshot = { ...snapshot, revision: "5.0.0", entries: [foldedFixture, consumedFixture] };
// 徽标节的收尾把面板关了，先重开；多轮重渲染后早前捕获的节点引用已脱离
// 文档树（React 根上委托点击不再可达），一律现场重查。
await act(async () => setCollabInboxOpen(true));
const p8Panel = document.querySelector(".collab-inbox-panel");
assert.ok(p8Panel, "the panel reopens for the P8 checks");
// 早期测试把视图切到了 chains 且状态残留（reopen 后 refresh 走链视图查询，
// 链视图不渲染折叠徽标）——先切回列表视图再选 All 桶。
await act(async () => {
  Array.from(p8Panel!.querySelectorAll<HTMLButtonElement>(".collab-inbox-panel__viewtoggle button"))
    .find((b) => b.textContent === "List")!
    .click();
});
await act(async () => {
  Array.from(p8Panel!.querySelectorAll<HTMLButtonElement>(".collab-inbox-panel__bucket"))
    .find((b) => b.textContent === "All")!
    .click();
});
assert.match(p8Panel!.textContent ?? "", /×9/, "the folded cluster renders a ×N count chip");

// ── tl1226：全部已读后未读角标即时清零（写侧广播 → 角标重查）─────────────
// 徽标节（上文）钉了挂载 + 开/合两条刷新时机；这里钉第三条：面板内写操作
// 成功后角标 hook 即时重查 CountUnreadCollabMail——用户实测「全部已读后
// 左下角角标仍挂 27」的回归断言，面板不动、无开/合过渡。
const tl1226BadgeCount = () => snapshot.entries.filter((e) => !e.read).length;
let tl1226CountCalls = 0;
const tl1226BadgeBindings: CollabInboxBindings = {
  ...bindings,
  async CountUnreadCollabMail() {
    tl1226CountCalls += 1;
    return tl1226BadgeCount();
  },
};
function Tl1226BadgeProbe() {
  const n = useCollabInboxUnreadCount(tl1226BadgeBindings);
  return <span className="badge-probe-tl1226">{n}</span>;
}
const tl1226Host = document.createElement("div");
document.body.appendChild(tl1226Host);
const tl1226Root = createRoot(tl1226Host);
await act(async () => {
  tl1226Root.render(<Tl1226BadgeProbe />);
});
assert.equal(document.querySelector(".badge-probe-tl1226")!.textContent, "1", "tl1226: badge reads 1 unread on mount (folded cluster is unread)");
const tl1226CallsBeforeRead = tl1226CountCalls;

// 一键批量已读：把当前视图未读条目的 id 集合发到后端；折叠条目只带 primary
// id，整簇展开由 Go 侧 MarkRead 完成（此处钉「未读集合到达后端」这一接缝）。
// 任务716 ③：按钮落位视图工具条。
await act(async () => {
  Array.from(p8Panel!.querySelectorAll<HTMLButtonElement>(".collab-inbox-panel__toolbar button"))
    .find((b) => b.textContent === "Mark all read")!
    .click();
});
const readCall = calls.filter((c) => c.name === "MarkCollabMailRead").pop();
assert.deepEqual(readCall?.args[0], ["m_dup"], "mark-all-read sends exactly the unread entry ids");
assert.match(p8Panel!.textContent ?? "", /Snapshot 6\.0\.0/, "the mark-read snapshot refreshes the panel in place");
// tl1226：写成功 → 广播 → 角标重查（未读 1 → 0），全程无开/合过渡。
assert.equal(
  tl1226CountCalls > tl1226CallsBeforeRead,
  true,
  "tl1226: a successful mark-all-read notifies the badge hook to re-query the backend",
);
assert.equal(
  document.querySelector(".badge-probe-tl1226")!.textContent,
  "0",
  "tl1226: the unread badge clears right after mark-all-read without any open/close transition",
);
await act(async () => tl1226Root.unmount());
tl1226Host.remove();

// ── tl1226b ①：新信到达角标即涨（后端投递 CollabMailChanged 事件 → 角标重查）──
// tl1226 钉了挂载/开合/写广播三条刷新时机；这里钉第四条：后端投递观察者发的
// CollabMailChanged 运行时事件到达 → hook 即时重查 CountUnreadCollabMail——
// 新信不必等任何面板交互即涨角标（不引轮询；Go 侧契约见
// desktop/collab_mail_arrival_test.go）。harness 默认无 window.runtime，这里
// 装一个捕获 EventsOn 订阅的假运行时来驱动事件。
{
  let arrivedUnread = 2;
  let arrivedCountCalls = 0;
  const runtimeHandlers = new Map<string, Array<(payload?: unknown) => void>>();
  (dom.window as unknown as { runtime: unknown }).runtime = {
    EventsOn: (name: string, cb: (payload?: unknown) => void) => {
      const list = runtimeHandlers.get(name) ?? [];
      list.push(cb);
      runtimeHandlers.set(name, list);
      return () => {
        runtimeHandlers.set(name, (runtimeHandlers.get(name) ?? []).filter((fn) => fn !== cb));
      };
    },
  };
  (dom.window as unknown as { go: unknown }).go = { main: { App: {} } };
  const arrivedBindings: CollabInboxBindings = {
    ...bindings,
    async CountUnreadCollabMail() {
      arrivedCountCalls += 1;
      return arrivedUnread;
    },
  };
  function ArrivedBadgeProbe() {
    const n = useCollabInboxUnreadCount(arrivedBindings);
    return <span className="badge-probe-arrived">{n}</span>;
  }
  const arrivedHost = document.createElement("div");
  document.body.appendChild(arrivedHost);
  const arrivedRoot = createRoot(arrivedHost);
  await act(async () => {
    arrivedRoot.render(<ArrivedBadgeProbe />);
  });
  assert.equal(document.querySelector(".badge-probe-arrived")!.textContent, "2", "tl1226b: badge mounts at 2 unread");
  const callsBeforeArrival = arrivedCountCalls;
  arrivedUnread = 3;
  await act(async () => {
    for (const fn of runtimeHandlers.get("CollabMailChanged") ?? []) fn({ id: "m_new", from: "sc_a", to: "sc_b" });
  });
  assert.equal(
    arrivedCountCalls > callsBeforeArrival,
    true,
    "tl1226b: a CollabMailChanged runtime event re-queries the unread count",
  );
  assert.equal(
    document.querySelector(".badge-probe-arrived")!.textContent,
    "3",
    "tl1226b: the badge rises on mail arrival without any user interaction",
  );
  // 卸载即注销订阅：此后事件不再触发重查（不泄漏监听器）。
  await act(async () => {
    arrivedRoot.unmount();
  });
  const callsAfterUnmount = arrivedCountCalls;
  await act(async () => {
    for (const fn of runtimeHandlers.get("CollabMailChanged") ?? []) fn({ id: "m_new2" });
  });
  assert.equal(
    arrivedCountCalls,
    callsAfterUnmount,
    "tl1226b: after unmount the runtime-event subscription is gone (no re-query)",
  );
  arrivedHost.remove();
}

// ── tl1230：信件列表懒加载（首屏 20 条 + 滚动近底增量加载）─────────────────
// 用户钦定方案：首屏只加载 20 条（治「信件读取中…」长时间不出数据），滚动
// 接近底部再增量。后端 List 天然支持任意 limit（Go 侧零改动），这里的接缝职
// 责：①首屏请求 limit=20 且只渲染 20 行；②近底滚动逐页 +20；③Truncated 翻
// 假后滚动不再放页。jsdom 无布局度量，滚动用显式钉 scrollTop/clientHeight/
// scrollHeight 后派发冒泡 scroll 事件驱动（嵌入形态面板本体即滚动容器）。
{
  const pagedFixture = Array.from({ length: 45 }, (_, i) =>
    entry({ id: `m_page_${i}`, preview: `paged mail ${i}` }),
  );
  const pagedLimits: number[] = [];
  const pagedBindings: CollabInboxBindings = {
    ...bindings,
    async ListCollabMail(bucket, from, to, state, limit, includeDismissed, order) {
      pagedLimits.push(limit);
      const size = Math.min(limit, pagedFixture.length);
      return {
        revision: "10.0.0",
        settings: { retention: "7d" },
        total: pagedFixture.length,
        returned: size,
        truncated: size < pagedFixture.length,
        entries: pagedFixture.slice(0, size),
      };
    },
  };
  const pagedHost = document.createElement("div");
  document.body.appendChild(pagedHost);
  const pagedRoot = createRoot(pagedHost);
  await act(async () => {
    pagedRoot.render(
      <LocaleProvider>
        <CollabInboxPanel bindings={pagedBindings} directory={directory} embedded />
      </LocaleProvider>,
    );
  });
  const pagedPanel = pagedHost.querySelector(".collab-inbox-panel");
  assert.ok(pagedPanel, "tl1230: the paged panel renders");
  assert.deepEqual(pagedLimits, [20], "tl1230: the first screen fetches exactly limit=20");
  assert.equal(
    pagedPanel!.querySelectorAll(".collab-inbox-panel__row").length,
    20,
    "tl1230: only the first 20 entries render on the first screen",
  );

  // 滚动近底 → 增量 +20。jsdom 的度量全为 0（自动续载效应按 clientHeight=0
  // 恒惰性），这里显式钉出「距底 0px」的布局态再派发滚动（600+400 = 距底，
  // 过 64px 近底阈值）。
  const pinMetric = (el: HTMLElement, prop: string, value: number) =>
    Object.defineProperty(el, prop, { configurable: true, value });
  const scrollPanel = pagedPanel as HTMLElement;
  pinMetric(scrollPanel, "scrollTop", 600);
  pinMetric(scrollPanel, "clientHeight", 400);
  pinMetric(scrollPanel, "scrollHeight", 1000);
  await act(async () => {
    scrollPanel.dispatchEvent(new dom.window.Event("scroll", { bubbles: true }));
  });
  assert.deepEqual(pagedLimits, [20, 40], "tl1230: scrolling near the bottom fetches the next page");
  assert.equal(
    pagedPanel!.querySelectorAll(".collab-inbox-panel__row").length,
    40,
    "tl1230: 40 entries render after one page growth",
  );

  await act(async () => {
    scrollPanel.dispatchEvent(new dom.window.Event("scroll", { bubbles: true }));
  });
  assert.deepEqual(pagedLimits, [20, 40, 60], "tl1230: the second scroll loads the tail page");
  assert.equal(
    pagedPanel!.querySelectorAll(".collab-inbox-panel__row").length,
    45,
    "tl1230: all 45 entries render once fully loaded",
  );

  // Truncated 翻假：继续滚动不得再放页（后端已无更多，空拉 = 白读）。
  const callsWhenComplete = pagedLimits.length;
  await act(async () => {
    scrollPanel.dispatchEvent(new dom.window.Event("scroll", { bubbles: true }));
  });
  assert.equal(
    pagedLimits.length,
    callsWhenComplete,
    "tl1230: scrolling after truncation cleared stops fetching",
  );
  await act(async () => {
    pagedRoot.unmount();
  });
  pagedHost.remove();
}

// tl1230 × tl1226 交叉回归：懒加载后「全部已读」必须仍是全量口径——截断态
// （27 封未读只加载了 20 封）下点「全部已读」，先取一页全量再汇总未读 id，
// 不得只清已加载页（否则角标残留 7，tl1226 的用户实测场景复发）。
{
  const unreadMany = Array.from({ length: 27 }, (_, i) =>
    entry({ id: `m_unread_${i}`, preview: `unread mail ${i}` }),
  );
  let fullFetchLimit = 0;
  const truncatedBindings: CollabInboxBindings = {
    ...bindings,
    async ListCollabMail(bucket, from, to, state, limit, includeDismissed, order) {
      if (limit >= 500) {
        fullFetchLimit = limit;
        return {
          revision: "11.0.0",
          settings: { retention: "7d" },
          total: unreadMany.length,
          returned: unreadMany.length,
          truncated: false,
          entries: unreadMany,
        };
      }
      const size = Math.min(limit, unreadMany.length);
      return {
        revision: "11.0.0",
        settings: { retention: "7d" },
        total: unreadMany.length,
        returned: size,
        truncated: size < unreadMany.length,
        entries: unreadMany.slice(0, size),
      };
    },
    async MarkCollabMailRead(ids) {
      calls.push({ name: "MarkCollabMailRead", args: [ids] });
      const readIds = new Set(ids);
      const entries = unreadMany.map((e) => (readIds.has(e.id) ? { ...e, read: true } : e));
      return {
        revision: "12.0.0",
        settings: { retention: "7d" },
        total: entries.length,
        returned: entries.length,
        truncated: false,
        entries,
      };
    },
  };
  const truncHost = document.createElement("div");
  document.body.appendChild(truncHost);
  const truncRoot = createRoot(truncHost);
  await act(async () => {
    truncRoot.render(
      <LocaleProvider>
        <CollabInboxPanel bindings={truncatedBindings} directory={directory} embedded />
      </LocaleProvider>,
    );
  });
  const truncPanel = truncHost.querySelector(".collab-inbox-panel");
  assert.ok(truncPanel, "tl1230: the truncated 27-unread panel renders");
  assert.equal(
    truncPanel!.querySelectorAll(".collab-inbox-panel__row").length,
    20,
    "tl1230: the 27-unread list starts at the 20-entry first screen",
  );
  const markAllBtn = Array.from(truncPanel!.querySelectorAll<HTMLButtonElement>("button"))
    .find((b) => b.textContent === "Mark all read");
  assert.ok(markAllBtn, "tl1230: the mark-all-read button shows while truncated");
  await act(async () => {
    markAllBtn!.click();
  });
  assert.equal(fullFetchLimit, 500, "tl1230: mark-all-read fetches a full page while truncated");
  const truncReadCall = calls.filter((c) => c.name === "MarkCollabMailRead").pop();
  assert.equal(
    Array.isArray(truncReadCall?.args[0]) && (truncReadCall!.args[0] as string[]).length,
    27,
    "tl1230: mark-all-read covers ALL 27 unread entries, not just the loaded 20",
  );
  await act(async () => {
    truncRoot.unmount();
  });
  truncHost.remove();
}

// ── tl1238 ①：多选态「全选」入口在列表头部可见可达 ──────────────────────────
// 用户实测「多选模式没有全选按钮」：batchbar 全选按钮在 DOM 里但挂在滚动内容
// 最底部，懒加载长列表下沉到折叠线以下 = 可见性缺陷。修复 = 列表头部主控复选
// 框（sticky 常驻）+ batchbar sticky 底部；两处同一 selected 状态。这里的接缝
// 职责：①头部主控框随多选态渲染且文案同「全选」键；②勾=全选当前视图、再点
// =清空当前视图；③与行首复选框/batchbar 计数联动；④sticky 两条 CSS 落盘
// （980 ② 的 styles.css 断言先例）。
{
  const msEntries = [
    entry({ id: "ms_a", preview: "multi a" }),
    entry({ id: "ms_b", preview: "multi b" }),
    entry({ id: "ms_c", preview: "multi c" }),
  ];
  const msBindings: CollabInboxBindings = {
    ...bindings,
    async ListCollabMail() {
      return {
        revision: "13.0.0",
        settings: { retention: "7d" },
        total: msEntries.length,
        returned: msEntries.length,
        truncated: false,
        entries: msEntries,
      };
    },
  };
  const msHost = document.createElement("div");
  document.body.appendChild(msHost);
  const msRoot = createRoot(msHost);
  await act(async () => {
    msRoot.render(
      <LocaleProvider>
        <CollabInboxPanel bindings={msBindings} directory={directory} embedded />
      </LocaleProvider>,
    );
  });
  const msPanel = msHost.querySelector(".collab-inbox-panel");
  assert.ok(msPanel, "tl1238: the multi-select panel renders");
  assert.equal(
    msPanel!.querySelector(".collab-inbox-panel__checkall"),
    null,
    "tl1238: no header select-all renders before multi mode is on",
  );
  await act(async () => {
    Array.from(msPanel!.querySelectorAll<HTMLButtonElement>(".collab-inbox-panel__toolbar button"))
      .find((b) => b.textContent === "Multi-select")!
      .click();
  });
  const checkall = msPanel!.querySelector<HTMLElement>(".collab-inbox-panel__checkall");
  assert.ok(checkall, "tl1238: the list-header master select-all renders in multi mode");
  assert.equal(
    checkall!.querySelector("span")?.textContent,
    "Select all",
    "tl1238: the header control carries the same 全选 label as the batchbar button",
  );
  const msCheckboxes = () => msPanel!.querySelectorAll<HTMLInputElement>(".collab-inbox-panel__check input");
  assert.equal(msCheckboxes().length, 3, "tl1238: the three rows grow their checkboxes");
  const headerBox = () => checkall!.querySelector<HTMLInputElement>("input")!;
  assert.equal(headerBox().checked, false, "tl1238: the header checkbox starts unchecked at 0 selected");
  await act(async () => {
    headerBox().click();
  });
  assert.equal(
    Array.from(msCheckboxes()).every((c) => c.checked),
    true,
    "tl1238: ticking the header checkbox selects every row",
  );
  assert.match(
    msPanel!.querySelector(".collab-inbox-panel__batchbar")!.textContent ?? "",
    /3 selected/,
    "tl1238: the batchbar count follows the header select-all",
  );
  // 取消一行 → 头部主控框回到未勾（部分选中≠全选）；再勾头部 → 恢复全选。
  await act(async () => {
    msCheckboxes()[0].click();
  });
  assert.equal(headerBox().checked, false, "tl1238: unchecking one row unchecks the header master");
  assert.match(
    msPanel!.querySelector(".collab-inbox-panel__batchbar")!.textContent ?? "",
    /2 selected/,
    "tl1238: the batchbar count drops with the row uncheck",
  );
  await act(async () => {
    headerBox().click();
  });
  assert.equal(
    Array.from(msCheckboxes()).every((c) => c.checked),
    true,
    "tl1238: re-ticking the header master re-selects every row",
  );
  // sticky 两条 CSS 落盘（可见性修复的本体：头部框钉顶、batchbar 钉底）。
  {
    const testDir = dirname(fileURLToPath(import.meta.url));
    const styles = readFileSync(resolve(testDir, "../styles.css"), "utf8");
    const checkallRule = styles.slice(
      styles.indexOf(".collab-inbox-panel__checkall {"),
      styles.indexOf("}", styles.indexOf(".collab-inbox-panel__checkall {")),
    );
    assert.match(checkallRule, /position:\s*sticky/, "tl1238: the header master sticks to the visible top (was the invisible-select-all defect)");
    const batchbarRule = styles.slice(
      styles.indexOf(".collab-inbox-panel__batchbar {"),
      styles.indexOf("}", styles.indexOf(".collab-inbox-panel__batchbar {")),
    );
    assert.match(batchbarRule, /position:\s*sticky/, "tl1238: the batchbar sticks to the visible bottom");
  }
  await act(async () => {
    msRoot.unmount();
  });
  msHost.remove();
}

// ── tl1238 ②：点开查看即已读——角标随查看即时消除，不等面板关闭 ─────────────
// 用户实测「点开面板看完、关了角标仍挂 27 封（已读状态与角标计数不对账）」：
// 排查结论 = 角标计数源（CountUnreadCollabMail）与开/合/写/到达四条刷新接线
// 均健全（既有断言在上文），真正的缺口是查看路径从不推进已读游标——此前唯一
// 已读口是工具条「全部已读」。修复 = 详情弹窗打开瞬间按信结算（复用既有
// MarkCollabMailRead 写口 + mail-changed 广播）。接缝职责：①点开未读信 →
// 该信 id 到达写口、角标即时重查并回落，全程不开合面板；②行已读态翻转；
// ③点开已读信不再发写（零多余往返）。
{
  const rvEntries = [
    entry({ id: "rv_a", preview: "review a" }),
    entry({ id: "rv_b", preview: "review b" }),
    entry({ id: "rv_done", preview: "already read", read: true }),
  ];
  const rvReadCalls: string[][] = [];
  const rvSnapshot = (): CollabMailSnapshot => ({
    revision: "14.0.0",
    settings: { retention: "7d" },
    total: rvEntries.length,
    returned: rvEntries.length,
    truncated: false,
    entries: rvEntries,
  });
  const rvBindings: CollabInboxBindings = {
    ...bindings,
    async ListCollabMail() {
      return rvSnapshot();
    },
    async ReadCollabMail(id: string) {
      const e = rvEntries.find((x) => x.id === id)!;
      return { id, from: e.from, to: e.to, at: e.at, threadId: e.threadId, bucket: e.bucket, preview: e.preview, body: `full body of ${id}` };
    },
    async MarkCollabMailRead(ids) {
      rvReadCalls.push(ids);
      const hit = new Set(ids);
      for (const e of rvEntries) {
        if (hit.has(e.id)) e.read = true;
      }
      return rvSnapshot();
    },
  };
  let rvCountCalls = 0;
  const rvBadgeBindings: CollabInboxBindings = {
    ...rvBindings,
    async CountUnreadCollabMail() {
      rvCountCalls += 1;
      return rvEntries.filter((e) => !e.read).length;
    },
  };
  function RvBadgeProbe() {
    const n = useCollabInboxUnreadCount(rvBadgeBindings);
    return <span className="badge-probe-rv">{n}</span>;
  }
  const rvHost = document.createElement("div");
  document.body.appendChild(rvHost);
  const rvRoot = createRoot(rvHost);
  const rvBadgeHost = document.createElement("div");
  document.body.appendChild(rvBadgeHost);
  const rvBadgeRoot = createRoot(rvBadgeHost);
  await act(async () => {
    rvRoot.render(
      <LocaleProvider>
        <CollabInboxPanel bindings={rvBindings} directory={directory} embedded />
      </LocaleProvider>,
    );
    rvBadgeRoot.render(<RvBadgeProbe />);
  });
  const rvPanel = rvHost.querySelector(".collab-inbox-panel");
  assert.ok(rvPanel, "tl1238: the read-on-view panel renders");
  assert.equal(document.querySelector(".badge-probe-rv")!.textContent, "2", "tl1238: the badge mounts at 2 unread");
  const rvDetailBtn = (id: string) =>
    Array.from(rvPanel!.querySelectorAll<HTMLButtonElement>(".collab-inbox-panel__row"))
      .find((row) => row.querySelector(".collab-inbox-panel__preview")?.textContent === `review ${id.slice(3)}`)
      ?.querySelector<HTMLButtonElement>(".collab-inbox-panel__rowactions button");
  const rvRowReadChip = (id: string) =>
    Array.from(rvPanel!.querySelectorAll(".collab-inbox-panel__row"))
      .find((row) => row.querySelector(".collab-inbox-panel__preview")?.textContent === `review ${id.slice(3)}`)
      ?.querySelector(".collab-inbox-panel__read");

  // 点开第一封未读 → 写口收到 [rv_a]、角标即时重查回落 2→1，行翻已读，
  // 全程无面板开合（「查看即清，不等关面板」的本体断言）。
  const countBeforeA = rvCountCalls;
  await act(async () => {
    rvDetailBtn("rv_a")!.click();
  });
  assert.deepEqual(rvReadCalls, [["rv_a"]], "tl1238: opening an unread letter settles exactly that id as read");
  assert.equal(rvCountCalls > countBeforeA, true, "tl1238: the settlement notifies the badge hook to re-query");
  assert.equal(document.querySelector(".badge-probe-rv")!.textContent, "1", "tl1238: the badge drops 2→1 the moment the letter is viewed, no close needed");
  assert.equal(rvRowReadChip("rv_a")!.textContent, "Read", "tl1238: the viewed row flips to the read state in place");
  // 关掉弹窗再点第二封（弹窗复用同一 openDetail 口）。
  await act(async () => {
    document.querySelector(".collab-inbox-detail")!.querySelector<HTMLButtonElement>(".modal__actions button")!.click();
  });
  const countBeforeB = rvCountCalls;
  await act(async () => {
    rvDetailBtn("rv_b")!.click();
  });
  assert.deepEqual(rvReadCalls, [["rv_a"], ["rv_b"]], "tl1238: the second unread letter settles on its own open");
  assert.equal(document.querySelector(".badge-probe-rv")!.textContent, "0", "tl1238: the badge clears as the last unread letter is viewed");
  assert.equal(rvCountCalls > countBeforeB, true, "tl1238: each settlement re-queries the badge");
  // 点开已读信 → 不再发写（零多余往返，角标计数调用也不动）。
  const countBeforeDone = rvCountCalls;
  await act(async () => {
    document.querySelector(".collab-inbox-detail")!.querySelector<HTMLButtonElement>(".modal__actions button")!.click();
  });
  const doneBtn = Array.from(rvPanel!.querySelectorAll<HTMLButtonElement>(".collab-inbox-panel__rowactions button"))
    .find((b) => b.closest(".collab-inbox-panel__row")?.querySelector(".collab-inbox-panel__preview")?.textContent === "already read");
  await act(async () => {
    doneBtn!.click();
  });
  assert.deepEqual(rvReadCalls, [["rv_a"], ["rv_b"]], "tl1238: opening an already-read letter sends NO extra write");
  assert.equal(rvCountCalls, countBeforeDone, "tl1238: no write means no badge re-query either");
  await act(async () => {
    rvRoot.unmount();
    rvBadgeRoot.unmount();
  });
  rvHost.remove();
  rvBadgeHost.remove();
}

await act(async () => root.unmount());
dom.window.close();
console.log("collab inbox panel checks passed");
