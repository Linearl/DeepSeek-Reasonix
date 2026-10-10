// Run: tsx src/__tests__/collab-inbox-failure.test.tsx
// 任务587 验收钉缝：后端读取失败时面板必须显示**失败态**而非「暂无信件」；
// 失败路径必须留痕（console + ring buffer，网关断连时 desktop.log 通道自身
// 静默也不丢证据）；失败后按退避计划自动恢复刷新（网关重连无需用户重开面板）；
// 读取进行中不得冒充空态；失败时保留的旧数据必须如实声明「不是最新结果」。
// Go 侧契约由 collabinbox/desktop 包测试承担，这里钉「失败→展示位→恢复」这条
// 前端断裂段的渲染接缝。
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

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { CollabInboxPanel, setCollabInboxOpen, collabInboxRetryDelaysMs } = await import("../components/CollabInboxPanel");
const { peekFrontendLogRing, drainFrontendLogRing } = await import("../lib/frontendLog");
const { LocaleProvider } = await import("../lib/i18n");
type CollabInboxBindings = import("../components/CollabInboxPanel").CollabInboxBindings;
type CollabMailSnapshot = import("../components/CollabInboxPanel").CollabMailSnapshot;

const okSnapshot: CollabMailSnapshot = {
  revision: "9.0.1700000000000",
  settings: { retention: "7d" },
  total: 1,
  returned: 1,
  truncated: false,
  entries: [
    {
      id: "m_recovered",
      from: "sc_alice",
      to: "sc_main",
      at: 1_700_000_000_000,
      threadId: "m_recovered",
      bucket: "mention",
      preview: "recovered mail",
      delivered: true,
      read: false,
    },
  ],
};

type ListMode = "reject" | "hang" | "ok";
let listMode: ListMode = "ok";
const listCalls: number[] = [];
const bindings: CollabInboxBindings = {
  async ListCollabMail() {
    listCalls.push(listCalls.length + 1);
    if (listMode === "reject") throw new Error("gateway closed (task587 fixture)");
    if (listMode === "hang") await new Promise(() => {}); // never settles
    return okSnapshot;
  },
  async ListCollabMailChains() {
    return { revision: okSnapshot.revision, settings: okSnapshot.settings, total: 0, chains: [] };
  },
  async CountUnreadCollabMail() {
    return 0;
  },
  async DismissCollabMail() {
    return okSnapshot;
  },
  async MarkCollabMailRead() {
    return okSnapshot;
  },
  async UndismissCollabMail() {
    return okSnapshot;
  },
  async MarkCollabMailDecided() {
    return okSnapshot;
  },
  async SetCollabMailRetention() {
    return okSnapshot;
  },
  async SetCollabMailCleanupRule() {
    return okSnapshot;
  },
};

// 测试提速：退避计划压到毫秒级（生产默认 [2s,4s,8s,15s] 不变）。
collabInboxRetryDelaysMs.splice(0, collabInboxRetryDelaysMs.length, 30, 60, 120, 200);

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

const root = createRoot(document.getElementById("root")!);
await act(async () => {
  root.render(
    <LocaleProvider>
      <CollabInboxPanel bindings={bindings} />
    </LocaleProvider>,
  );
});

const bodyText = () => document.body.textContent ?? "";
const assertOnce = (needle: string, present: boolean, what: string) =>
  assert.equal(bodyText().includes(needle), present, `${what}: "${needle}" must ${present ? "render" : "not render"}`);
const query = <T extends Element>(selector: string) => document.querySelector<T>(selector);
const bucketButton = (label: string) =>
  Array.from(document.querySelectorAll<HTMLButtonElement>(".collab-inbox-panel__bucket"))
    .find((b) => b.textContent === label);

// ── ① 读取失败 → 失败态，绝不冒充「暂无信件」；失败路径有留痕 ─────────────
drainFrontendLogRing();
listMode = "reject";
await act(async () => setCollabInboxOpen(true));
let panel = query(".collab-inbox-panel");
assert.ok(panel, "the panel renders");
assertOnce("No mail yet", false, "a failed read must NOT render the honest-empty text");
assertOnce("暂无信件", false, "a failed read must NOT render the empty state in any locale");
assertOnce("Couldn't load the Mail Center", true, "the failure state renders an explicit error label");
assert.ok(
  query(".collab-inbox-panel__error"),
  "the failure state carries its own block class (distinct from __empty)",
);
assert.match(
  query(".collab-inbox-panel__error-detail")?.getAttribute("title") ?? "",
  /task587 fixture/,
  "the raw error travels in the detail title for diagnosis",
);
assert.ok(
  query(".collab-inbox-panel__error button"),
  "the failure state offers a retry button",
);
const ring = peekFrontendLogRing();
assert.ok(
  ring.some((l) => l.includes("[frontend:collab-inbox]") && l.includes("list failed") && l.includes("task587 fixture")),
  "the failed read leaves a trace in the frontend log ring (survives a dead gateway)",
);

// ── ② 读取进行中不冒充空态（无数据 + 新一轮尝试开始时旧失败横幅退役） ──────
listMode = "hang";
await act(async () => setCollabInboxOpen(false));
await act(async () => setCollabInboxOpen(true));
assertOnce("Loading mail…", true, "an in-flight read renders the loading state");
assertOnce("No mail yet", false, "an in-flight read must NOT render the empty state");
assertOnce("Couldn't load the Mail Center", false, "the previous attempt's banner retires when a new attempt starts");
// 挂起的读取没有 catch 路径；直接关面板收尾（cancel 清计时器，hang 的 promise 无害）。
await act(async () => setCollabInboxOpen(false));

// ── ③ 手动重试：恢复取数后失败态消失、信件渲染 ────────────────────────────
listMode = "reject";
await act(async () => setCollabInboxOpen(true));
assert.ok(query(".collab-inbox-panel__error"), "the failing open shows the failure block again (no data yet)");
listMode = "ok";
await act(async () => {
  query<HTMLButtonElement>(".collab-inbox-panel__error button")!.click();
});
assertOnce("Couldn't load the Mail Center", false, "a successful retry clears the failure state");
assertOnce("recovered mail", true, "the recovered mail renders after the retry");
assertOnce("No mail yet", false, "no empty-state text while rows render");

// ── ④ 自动恢复：失败后按退避计划自动刷新，无需用户交互 ────────────────────
// （③ 的成功快照仍在 —— 失败不清数据，此处失败形态是「旧数据+细条」，
//   无数据失败块已由 ①③ 钉住。）
drainFrontendLogRing();
listMode = "reject";
await act(async () => setCollabInboxOpen(false));
await act(async () => setCollabInboxOpen(true));
assert.ok(query(".collab-inbox-panel__errorstrip"), "the failing open shows the stale-data strip (rows kept)");
listMode = "ok"; // 网关在此刻恢复；等待退避计时器自动取数
const callsBeforeAuto = listCalls.length;
await act(async () => {
  await sleep(80); // 首档退避 30ms + 余量
});
assert.ok(
  listCalls.length > callsBeforeAuto,
  "the recovery loop re-queries automatically after the gateway comes back",
);
assertOnce("recovered mail", true, "the panel shows the recovered mail WITHOUT any user interaction");
assertOnce("Last refresh failed", false, "the failure strip clears itself after auto-recovery");

// ── ⑤ 关面板停表：退避计时器不得在面板关闭后继续打后端 ────────────────────
listMode = "reject";
await act(async () => setCollabInboxOpen(false));
await act(async () => setCollabInboxOpen(true));
await act(async () => setCollabInboxOpen(false)); // 失败状态下直接关面板
const callsAtClose = listCalls.length;
await act(async () => {
  await sleep(200); // 覆盖两档退避窗
});
assert.equal(
  listCalls.length,
  callsAtClose,
  "closing the panel cancels the pending retry (no background querying)",
);

// ── ⑥ 旧数据 + 刷新失败 → 旧数据照常渲染 + 细条如实声明「不是最新」 ────────
listMode = "ok";
await act(async () => setCollabInboxOpen(true));
assertOnce("recovered mail", true, "the rows render before the failure drill");
listMode = "reject";
await act(async () => {
  // 切桶触发刷新失败：失败不清空已展示的旧快照（可用性>新鲜度），
  // 但顶部细条必须如实声明当前显示的不是最新结果。
  bucketButton("Approvals")!.click();
});
assertOnce("recovered mail", true, "a failed refresh keeps the previously fetched rows visible");
assertOnce("Last refresh failed", true, "the stale-data strip declares the rows are not fresh");
assert.ok(query(".collab-inbox-panel__errorstrip"), "the stale strip carries its own class");
assertOnce("Couldn't load the Mail Center", false, "no full failure block while stale rows render");
// 自动恢复（同 ④ 的机制）清掉细条。
listMode = "ok";
await act(async () => {
  await sleep(80);
});
assertOnce("Last refresh failed", false, "auto-recovery clears the stale strip");
assertOnce("recovered mail", true, "the recovered rows render after the strip clears");

await act(async () => setCollabInboxOpen(false));
await act(async () => root.unmount());
dom.window.close();
console.log("collab inbox failure-state checks passed");
