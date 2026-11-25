// Run: tsx src/__tests__/collab-inbox-batch-dispose.test.tsx
// tl1768 acceptance on the panel side:
//   ① 批量消除（可恢复归档）：多选 → 全选 → 消除 → DismissCollabMail 收到全部
//      所选 id，无二次确认（可恢复路径，与逐行消除同款），退出多选；
//   ② 交集语义：已消除行不重复消除（混合选择只把未消除行交给后端）；「显示
//      已消除」视图下批量恢复只作用于已消除行（UndismissCollabMail）；
//   ③ 全选跨页：截断态「全选」先取一页全量（tl1230 的 markAllRead 同款），
//      消除收到全量 id——懒加载长列表不再要求滚完才能全选。
// The Go tests own the dispose contracts (dispose_test.go); this harness owns
// the batch wiring, the intersection semantics and the select-all full fetch.

import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { en } from "../locales/en";

const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost/", pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  Element: dom.window.Element,
  HTMLElement: dom.window.HTMLElement,
  Node: dom.window.Node,
  localStorage: dom.window.localStorage,
  IS_REACT_ACT_ENVIRONMENT: true,
  confirm: () => true,
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
dom.window.confirm = () => true;

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { CollabInboxPanel, setCollabInboxOpen, collabInboxMaxPageSize } = await import("../components/CollabInboxPanel");
const { LocaleProvider } = await import("../lib/i18n");
type CollabInboxBindings = import("../components/CollabInboxPanel").CollabInboxBindings;
type CollabMailSnapshot = import("../components/CollabInboxPanel").CollabMailSnapshot;
type CollabMailEntry = import("../components/CollabInboxPanel").CollabMailEntry;

const entryOf = (id: string, dismissed = false): CollabMailEntry => ({
  id,
  from: "sc_alice",
  to: "sc_main",
  at: 1_700_000_000_000,
  threadId: id,
  bucket: "mention",
  preview: `preview ${id}`,
  delivered: true,
  read: false,
  dismissed,
});

type Call = { name: string; args: unknown[] };
const calls: Call[] = [];

// Mutable fixture state; `fullEntries` answers the truncated select-all fetch.
let entries: CollabMailEntry[] = [];
let fullEntries: CollabMailEntry[] | null = null;

const snapshotNow = (truncated = false): CollabMailSnapshot => ({
  revision: `${entries.length}.0.0`,
  settings: { retention: "7d" },
  total: (fullEntries ?? entries).length,
  returned: entries.length,
  truncated,
  entries,
});

const bindings: CollabInboxBindings = {
  async ListCollabMail(_bucket: string, _from: string, _to: string, _state: string, limit: number, _dismissed: boolean, _order: string) {
    calls.push({ name: "ListCollabMail", args: [limit] });
    if (limit >= collabInboxMaxPageSize && fullEntries) {
      return { ...snapshotNow(false), total: fullEntries.length, returned: fullEntries.length, truncated: false, entries: fullEntries };
    }
    return snapshotNow(Boolean(fullEntries));
  },
  async ListCollabMailChains() {
    return { revision: "1.0.0", settings: { retention: "7d" }, total: 0, chains: [] };
  },
  async CountUnreadCollabMail() { return 0; },
  async DismissCollabMail(ids: string[]) {
    calls.push({ name: "DismissCollabMail", args: [[...ids]] });
    entries = entries.map((e) => (ids.includes(e.id) ? { ...e, dismissed: true } : e));
    return snapshotNow();
  },
  async UndismissCollabMail(ids: string[]) {
    calls.push({ name: "UndismissCollabMail", args: [[...ids]] });
    entries = entries.map((e) => (ids.includes(e.id) ? { ...e, dismissed: false } : e));
    return snapshotNow();
  },
  async MarkCollabMailRead() { return snapshotNow(); },
  async MarkCollabMailDecided() { return snapshotNow(); },
  async SetCollabMailRetention() { return snapshotNow(); },
  async SetCollabMailCleanupRule() { return snapshotNow(); },
  async CleanCollabMailNow() { return { removed: 0, snapshot: snapshotNow() }; },
  async DeleteCollabMail(ids: string[]) {
    calls.push({ name: "DeleteCollabMail", args: [[...ids]] });
    entries = entries.filter((e) => !ids.includes(e.id));
    return snapshotNow();
  },
  async ClearAllCollabMail() {
    entries = [];
    return { removed: 0, snapshot: snapshotNow() };
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
setCollabInboxOpen(true);
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });

const panel = document.querySelector(".collab-inbox-panel");
assert.ok(panel, "the panel renders");
const buttonIn = (scope: Element, text: string) =>
  Array.from(scope.querySelectorAll<HTMLButtonElement>("button")).find((b) => b.textContent?.trim() === text);
const batchbar = () => panel!.querySelector(".collab-inbox-panel__batchbar");
const checkboxes = () => panel!.querySelectorAll<HTMLInputElement>(".collab-inbox-panel__check input");
const settle = () => act(async () => { await new Promise((r) => setTimeout(r, 0)); });

async function enterMultiAndSelectAll() {
  const multiBtn = Array.from(panel!.querySelectorAll<HTMLButtonElement>("button")).find((b) => b.textContent === "Multi-select");
  assert.ok(multiBtn, "the toolbar offers the multi-select toggle");
  await act(async () => { multiBtn!.click(); });
  await settle();
  const selectAll = buttonIn(batchbar()!, "Select all");
  assert.ok(selectAll, "the batch bar offers select-all");
  await act(async () => { selectAll!.click(); });
  await settle();
}

// refetchByToggles drives a real refetch through a dependency change (the
// effect re-runs on order/showDismissed/view changes) so replaced fixtures
// actually reach the DOM.
async function refetchByToggles() {
  const oldest = Array.from(panel!.querySelectorAll<HTMLButtonElement>("button")).find((b) => b.textContent === "Oldest first");
  assert.ok(oldest, "the order toggle offers Oldest first");
  await act(async () => { oldest!.click(); });
  await settle();
  const newest = Array.from(panel!.querySelectorAll<HTMLButtonElement>("button")).find((b) => b.textContent === "Newest first");
  assert.ok(newest, "the order toggle offers Newest first");
  await act(async () => { newest!.click(); });
  await settle();
}

// ① 批量消除：无二次确认（可恢复路径），消除写口收到全部所选 id，退出多选。
calls.length = 0;
entries = [entryOf("m1"), entryOf("m2"), entryOf("m3")];
await refetchByToggles();
await enterMultiAndSelectAll();
assert.match(batchbar()!.textContent ?? "", /3 selected/, "select-all counts every row");
const dismissBatch = buttonIn(batchbar()!, en["collabInbox.dismiss"]);
assert.ok(dismissBatch, "the batch bar offers the recoverable batch dismiss");
await act(async () => { dismissBatch!.click(); });
await settle();
assert.ok(!document.querySelector(".reasonix-confirm-dialog"), "the recoverable dismiss asks NO confirmation (the destructive delete keeps its own)");
const dismissCall = calls.find((c) => c.name === "DismissCollabMail");
assert.ok(dismissCall, "the batch dismiss reaches the backend");
assert.deepEqual([...(dismissCall!.args[0] as string[])].sort(), ["m1", "m2", "m3"], "the dismiss carries exactly the selected ids");
assert.equal(checkboxes().length, 0, "a batch dismiss exits multi-select mode");

// ② 交集语义 + 批量恢复：混合选择只把未消除行交给消除；恢复只作用于已消除行。
calls.length = 0;
entries = [entryOf("k1", true), entryOf("k2", false), entryOf("k3", false)];
const showDismissed = panel!.querySelector<HTMLInputElement>(".collab-inbox-panel__showdismissed input");
assert.ok(showDismissed, "the show-dismissed toggle exists");
await act(async () => { showDismissed!.click(); });
await settle();
await enterMultiAndSelectAll();
assert.match(batchbar()!.textContent ?? "", /3 selected/, "the dismissed view selects all three rows");
const dismissMixed = buttonIn(batchbar()!, en["collabInbox.dismiss"]);
assert.ok(dismissMixed && !dismissMixed.disabled, "a mixed selection arms the dismiss button");
await act(async () => { dismissMixed!.click(); });
await settle();
const dismissMixedCall = calls.find((c) => c.name === "DismissCollabMail");
assert.ok(dismissMixedCall, "the mixed dismiss reaches the backend");
assert.deepEqual([...(dismissMixedCall!.args[0] as string[])].sort(), ["k2", "k3"], "the dismiss skips already-dismissed rows (intersection semantics)");

// 全部消除后回到已消除视图，批量恢复只把已消除行交给 UndismissCollabMail。
calls.length = 0;
await enterMultiAndSelectAll();
const dismissRest = buttonIn(batchbar()!, en["collabInbox.dismiss"]);
assert.ok(dismissRest && dismissRest.disabled, "with every selected row already dismissed the dismiss button is inert");
const restoreBatch = buttonIn(batchbar()!, "Restore");
assert.ok(restoreBatch, "the dismissed view offers the batch restore");
await act(async () => { restoreBatch!.click(); });
await settle();
const restoreCall = calls.find((c) => c.name === "UndismissCollabMail");
assert.ok(restoreCall, "the batch restore reaches the backend");
assert.deepEqual([...(restoreCall!.args[0] as string[])].sort(), ["k1", "k2", "k3"], "the restore carries the dismissed ids");

// ③ 全选跨页：截断态先取全量页再选——消除收到全量 id（懒加载不再锁死全选）。
calls.length = 0;
entries = [entryOf("p1"), entryOf("p2")]; // the loaded first page
fullEntries = ["p1", "p2", "p3", "p4", "p5"].map((id) => entryOf(id));
await act(async () => { showDismissed!.click(); }); // off → default view refetch (truncated)
await settle();
await enterMultiAndSelectAll();
assert.match(batchbar()!.textContent ?? "", /5 selected/, "select-all covers the FULL truncated set, not just the loaded page");
const fullFetch = calls.find((c) => c.name === "ListCollabMail" && (c.args[0] as number) >= collabInboxMaxPageSize);
assert.ok(fullFetch, "the truncated select-all fetches a full page first (markAllRead precedent)");
const dismissBatch2 = buttonIn(batchbar()!, en["collabInbox.dismiss"]);
assert.ok(dismissBatch2, "the cross-page dismiss button renders");
await act(async () => { dismissBatch2!.click(); });
await settle();
const dismissCall2 = calls.find((c) => c.name === "DismissCollabMail");
assert.ok(dismissCall2, "the cross-page dismiss reaches the backend");
assert.deepEqual([...(dismissCall2!.args[0] as string[])].sort(), ["p1", "p2", "p3", "p4", "p5"], "the dismiss carries every id across the lazy-load fold line");

setCollabInboxOpen(false);
await act(async () => { root.unmount(); });
console.log("collab-inbox-batch-dispose: all assertions passed");
