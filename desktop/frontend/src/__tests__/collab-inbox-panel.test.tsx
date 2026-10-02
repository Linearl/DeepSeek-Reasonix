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

const { default: React, act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { CollabInboxPanel, setCollabInboxOpen } = await import("../components/CollabInboxPanel");
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
};

const root = createRoot(document.getElementById("root")!);
setCollabInboxOpen(true);
await act(async () => {
  root.render(
    <LocaleProvider>
      <CollabInboxPanel bindings={bindings} />
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

// 发信方筛选 → 透传后端。
const [senderInput] = panel!.querySelectorAll<HTMLInputElement>(".collab-inbox-panel__filter");
await act(async () => {
  senderInput.focus();
});
await act(async () => {
  Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value")!.set!.call(senderInput, "sc_alice");
  senderInput.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
});
lastList = calls.filter((c) => c.name === "ListCollabMail").pop();
assert.deepEqual(lastList?.args[1], "sc_alice", "the sender filter reaches the backend query");

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

await act(async () => setCollabInboxOpen(false));
assert.equal(document.querySelector(".collab-inbox-panel"), null, "closing unmounts the panel");

await act(async () => root.unmount());
dom.window.close();
console.log("collab inbox panel checks passed");
