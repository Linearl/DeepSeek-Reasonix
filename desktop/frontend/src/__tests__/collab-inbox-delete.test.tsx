// Run: tsx src/__tests__/collab-inbox-delete.test.tsx
// 任务 856 ② acceptance on the panel side:
//   ① 多选模式：工具条「多选」开关 → 行首复选框 → 全选 → 浮动条计数；
//   ② 批量删除：确认对话框带「对话留档不影响已注入内容」语义句（铁律文案），
//      确认后 DeleteCollabMail 收到所选 id、面板收敛到新快照、退出多选；
//   ③ 对话链视图：按链选择，删除时展开为链内全部信件 id；
//   ④ 清空全部：维护行入口 + 二次确认 + ClearAllCollabMail（620 形状）。
// The Go tests own the physical-delete contracts (delete_test.go); this
// harness owns the multi-select wiring and the confirm copy.

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
  confirm: () => true,
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
dom.window.confirm = () => true;

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { CollabInboxPanel, setCollabInboxOpen } = await import("../components/CollabInboxPanel");
const { LocaleProvider } = await import("../lib/i18n");
type CollabInboxBindings = import("../components/CollabInboxPanel").CollabInboxBindings;
type CollabMailSnapshot = import("../components/CollabInboxPanel").CollabMailSnapshot;

const emptySnapshot: CollabMailSnapshot = {
  revision: "0.0.0",
  settings: { retention: "7d" },
  total: 0,
  returned: 0,
  truncated: false,
  entries: [],
};

const entryOf = (id: string) => ({
  id,
  from: "sc_alice",
  to: "sc_main",
  at: 1_700_000_000_000,
  threadId: id,
  bucket: "mention",
  preview: `preview ${id}`,
  delivered: true,
  read: false,
});

let entries = [entryOf("m1"), entryOf("m2"), entryOf("m3")];
type Call = { name: string; args: unknown[] };
const calls: Call[] = [];

const snapshotNow = (): CollabMailSnapshot => ({
  revision: `${entries.length}.0.0`,
  settings: { retention: "7d" },
  total: entries.length,
  returned: entries.length,
  truncated: false,
  entries,
});

const bindings: CollabInboxBindings = {
  async ListCollabMail() { return snapshotNow(); },
  async ListCollabMailChains() {
    return {
      revision: "1.0.0",
      settings: { retention: "7d" },
      total: 1,
      chains: [{
        threadId: "t1",
        participants: ["sc_alice", "sc_main"],
        count: entries.length,
        firstAt: 1,
        lastAt: 2,
        preview: "chain preview",
        entries,
      }],
    };
  },
  async CountUnreadCollabMail() { return entries.filter((e) => !e.read).length; },
  async DismissCollabMail(ids) {
    calls.push({ name: "DismissCollabMail", args: [ids] });
    entries = entries.filter((e) => !ids.includes(e.id));
    return snapshotNow();
  },
  async MarkCollabMailRead(ids) {
    calls.push({ name: "MarkCollabMailRead", args: [ids] });
    entries = entries.filter((e) => !ids.includes(e.id));
    return snapshotNow();
  },
  async UndismissCollabMail(ids) { return snapshotNow(); },
  async MarkCollabMailDecided() { return snapshotNow(); },
  async SetCollabMailRetention() { return snapshotNow(); },
  async SetCollabMailCleanupRule() { return snapshotNow(); },
  async CleanCollabMailNow() { return { removed: 0, snapshot: snapshotNow() }; },
  async DeleteCollabMail(ids) {
    calls.push({ name: "DeleteCollabMail", args: [ids] });
    entries = entries.filter((e) => !ids.includes(e.id));
    return snapshotNow();
  },
  async ClearAllCollabMail() {
    calls.push({ name: "ClearAllCollabMail", args: [] });
    const removed = entries.length;
    entries = [];
    return { removed, snapshot: snapshotNow() };
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
const q = <T extends HTMLElement = HTMLElement>(sel: string) => Array.from(panel!.querySelectorAll<HTMLButtonElement>(sel)).find((b) => b.textContent?.trim() === sel) as T | undefined;

// ① 进入多选：复选框出现，浮动条渲染「已选 0 项」。
const multiBtn = Array.from(panel!.querySelectorAll<HTMLButtonElement>("button")).find((b) => b.textContent === "Multi-select");
assert.ok(multiBtn, "the toolbar offers the multi-select toggle");
await act(async () => { multiBtn!.click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
const checkboxes = () => panel!.querySelectorAll<HTMLInputElement>(".collab-inbox-panel__check input");
assert.equal(checkboxes().length, 3, "each list row grows a checkbox in multi-select mode");
assert.match(panel!.querySelector(".collab-inbox-panel__batchbar")!.textContent ?? "", /0 selected/, "the batch bar starts at zero");

// ① 全选 → 计数 3 → 确认对话框带语义句 → 删除调用收到全部 id。
const selectAll = Array.from(panel!.querySelectorAll<HTMLButtonElement>(".collab-inbox-panel__batchbar button")).find((b) => b.textContent === "Select all");
assert.ok(selectAll, "the batch bar offers select-all");
await act(async () => { selectAll!.click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.match(panel!.querySelector(".collab-inbox-panel__batchbar")!.textContent ?? "", /3 selected/, "select-all counts every row");
let allChecked = Array.from(checkboxes()).every((c) => c.checked);
assert.ok(allChecked, "select-all checks every checkbox");

const deleteBtn = Array.from(panel!.querySelectorAll<HTMLButtonElement>(".collab-inbox-panel__batchbar button")).find((b) => b.textContent === "Delete");
assert.ok(deleteBtn, "the batch bar offers delete");
await act(async () => { deleteBtn!.click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
const dialog = document.querySelector(".reasonix-confirm-dialog");
assert.ok(dialog, "deleting asks for confirmation first");
const dialogText = document.body.textContent ?? "";
assert.match(dialogText, /archive/i, "the confirm names the archive semantics");
assert.match(dialogText, /never touches what a conversation already received|does not affect/i, "the confirm carries the「删除留档不影响已注入内容」semantic line");
const confirmBtn = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button")).find((b) => b.textContent === "Delete");
await act(async () => { confirmBtn!.click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
const del = calls.find((c) => c.name === "DeleteCollabMail");
assert.ok(del, "the confirmed delete reaches the backend");
assert.deepEqual([...(del!.args[0] as string[])].sort(), ["m1", "m2", "m3"], "the delete carries exactly the selected ids");
assert.equal(panel!.querySelectorAll<HTMLInputElement>(".collab-inbox-panel__check input").length, 0, "a confirmed delete exits multi-select mode");

// ③ 对话链视图：按链选择，删除展开为链内全部信件 id。
calls.length = 0;
entries = [entryOf("c1"), entryOf("c2")];
const chainsTab = Array.from(panel!.querySelectorAll<HTMLButtonElement>(".collab-inbox-panel__viewtoggle button")).find((b) => b.textContent === "Chains");
assert.ok(chainsTab, "the chain view toggle exists");
await act(async () => { chainsTab!.click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 50)); });
await act(async () => {
  multiBtn!.click(); // 进入多选（切视图后需重新开启——切视图会退出多选）
});
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
const chainCheck = panel!.querySelectorAll<HTMLInputElement>(".collab-inbox-panel__check input");
assert.equal(chainCheck.length, 1, "the chain view offers one checkbox per chain");
await act(async () => {
  (panel!.querySelector<HTMLInputElement>(".collab-inbox-panel__check input") as HTMLInputElement).click();
});
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
const chainDelete = Array.from(panel!.querySelectorAll<HTMLButtonElement>(".collab-inbox-panel__batchbar button")).find((b) => b.textContent === "Delete");
await act(async () => { chainDelete!.click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
const confirmBtn2 = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button")).find((b) => b.textContent === "Delete");
await act(async () => { confirmBtn2!.click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
const del2 = calls.find((c) => c.name === "DeleteCollabMail");
assert.ok(del2, "the chain delete reaches the backend");
assert.deepEqual([...(del2!.args[0] as string[])].sort(), ["c1", "c2"], "a chain selection expands to every entry id in the chain");

// ④ 清空全部：维护行入口 + 确认 + 620 形状收敛。
calls.length = 0;
entries = [entryOf("m9")];
await act(async () => {
  const refresh = Array.from(panel!.querySelectorAll<HTMLButtonElement>("button")).find((b) => b.textContent === "Retry") as HTMLButtonElement | undefined;
  void refresh;
});
const clearAll = Array.from(panel!.querySelectorAll<HTMLButtonElement>(".collab-inbox-panel__maint button")).find((b) => b.textContent?.startsWith("Clear all"));
assert.ok(clearAll, "the maintenance row offers clear-all");
await act(async () => { clearAll!.click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
const confirmBtn3 = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button")).find((b) => b.textContent?.startsWith("Clear all"));
assert.ok(confirmBtn3, "clearing the whole archive asks for confirmation");
await act(async () => { confirmBtn3!.click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.ok(calls.some((c) => c.name === "ClearAllCollabMail"), "the confirmed clear-all reaches the backend");
assert.match(panel!.textContent ?? "", /Snapshot 0\.0\.0/, "the panel converges on the emptied snapshot");

setCollabInboxOpen(false);
await act(async () => { root.unmount(); });
console.log("collab-inbox-delete: all assertions passed");
