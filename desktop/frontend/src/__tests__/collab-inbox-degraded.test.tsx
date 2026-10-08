// Run: tsx src/__tests__/collab-inbox-degraded.test.tsx
// 任务511 验收②：锁繁忙时后端返回 degraded 空快照，面板必须显示降级提示
// 「收件箱暂时不可用」而不是诚实的「暂无信件」——排查报告 §4 缺口 1 的展示位
//（Go 侧 TestMailLockBusyMarksSnapshotDegraded 承担 Degraded 标记契约；
// 这里钉「degraded 快照 → 降级文案」这一渲染接缝，含零回归：健康空态仍显示
// 「暂无信件」，降级但有数据时数据优先）。
//
// Harness note: react/react-dom must be imported AFTER the JSDOM globals are
// in place (same order as collab-inbox-panel).

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
type CollabMailSnapshot = import("../components/CollabInboxPanel").CollabMailSnapshot;

const emptySnapshot: CollabMailSnapshot = {
  revision: "1.5.1700000000000",
  settings: { retention: "7d" },
  total: 0,
  returned: 0,
  truncated: false,
  entries: [],
};

let next: CollabMailSnapshot = emptySnapshot;
const bindings: CollabInboxBindings = {
  async ListCollabMail() {
    return next;
  },
  async ListCollabMailChains() {
    return { revision: next.revision, settings: next.settings, total: 0, chains: [] };
  },
  async CountUnreadCollabMail() {
    return 0;
  },
  async DismissCollabMail() {
    return next;
  },
  async MarkCollabMailRead() {
    return next;
  },
  async UndismissCollabMail() {
    return next;
  },
  async MarkCollabMailDecided() {
    return next;
  },
  async SetCollabMailRetention() {
    return next;
  },
  async SetCollabMailCleanupRule() {
    return next;
  },
};

const root = createRoot(document.getElementById("root")!);
await act(async () => {
  root.render(
    <LocaleProvider>
      <CollabInboxPanel bindings={bindings} />
    </LocaleProvider>,
  );
});

const openWith = async (snap: CollabMailSnapshot) => {
  next = snap;
  await act(async () => setCollabInboxOpen(true));
};
const close = async () => {
  await act(async () => setCollabInboxOpen(false));
};
const assertOnce = (needle: string, present: boolean, what: string) => {
  assert.equal(
    (document.body.textContent ?? "").includes(needle),
    present,
    `${what}: "${needle}" must ${present ? "render" : "not render"}`,
  );
};

// ① 锁繁忙 → degraded 空快照 → 降级提示，而非「暂无信件」。
await openWith({ ...emptySnapshot, degraded: true });
let panel = document.querySelector(".collab-inbox-panel");
assert.ok(panel, "the panel renders");
assertOnce("Mail Center temporarily unavailable (lock busy)", true, "degraded snapshot shows the lock-busy notice");
assertOnce("No mail yet", false, "a degraded empty panel must NOT say 暂无信件");
assert.ok(
  panel!.querySelector(".collab-inbox-panel__empty--degraded"),
  "the degraded empty state carries its modifier class",
);
await close();
assertOnce("Mail Center temporarily unavailable (lock busy)", false, "closing unmounts the notice");

// ② 零回归：健康空快照（无 degraded 字段）仍显示「暂无信件」。
await openWith({ ...emptySnapshot });
assertOnce("No mail yet", true, "a healthy empty snapshot keeps the honest empty state");
assertOnce("Mail Center temporarily unavailable (lock busy)", false, "a healthy snapshot shows no degraded notice");
await close();

// ③ 降级但有数据 → 数据优先渲染，不出空态行。
await openWith({
  ...emptySnapshot,
  degraded: true,
  total: 1,
  returned: 1,
  entries: [
    {
      id: "m1",
      from: "sc_alice",
      to: "sc_main",
      at: 1_700_000_000_000,
      threadId: "m1",
      bucket: "mention",
      preview: "real mail",
      delivered: true,
      read: false,
    },
  ],
});
assertOnce("real mail", true, "a degraded snapshot with data still renders its rows");
assertOnce("暂无信件", false, "no empty state line while rows render");
assertOnce("No mail yet", false, "no healthy empty text while rows render");

await act(async () => root.unmount());
dom.window.close();
console.log("collab inbox degraded-banner checks passed");
