// Run: tsx src/__tests__/tl1458-notify-archive.test.tsx
// tl1458（协作中心『通知』栏 = 历史通知留档）acceptance harness:
//   ① 通知 tab 出现：hub 第三视图渲染 + aria-selected + collabHub.view 持久化/恢复；
//   ② 留档写入断言：发通知（四源）→ 留档有记录（端到端：面板行同步出现）；
//   ③ 级别分档：error/warn/info 行 data-level + 语言中立字形（!/⚠/ℹ）+ 按级筛选；
//   ④ 懒加载 20 条：首屏 20 + 滚动近底 +20（tl1230 信件列表同方案），到底自停；
//   ⑤ 清空按钮：ConfirmDialog danger 二次确认（取消不清 / 确认清空 + 盘面空）；
//   ⑥ 存储数据面：200 条 FIFO 上限 + 同源同文案 1s 去重 + agent:event 订阅缝
//      （kind=notice 过滤 / level 映射 / 幂等 / 退订）；
//   ⑦ 既有展示行为零变化：toast 照常弹出、照常自动消失，留档≠拦截（生命周期
//      独立——toast 没了留档仍在）。
// The storage module owns the contract; this harness owns the wiring and the
// "archive never intercepts" invariant.

import * as React from "react"; // tsconfig exclude 了 __tests__，tsx 对本文件走 classic JSX——必须显式绑定 React
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
const { CollabHubPanel, setCollabHubOpen } = await import("../components/CollabHubPanel");
const { LocaleProvider } = await import("../lib/i18n");
const {
  recordNotification,
  listNotifications,
  clearNotifications,
  installNoticeEventArchive,
  resetNotifyArchiveForTest,
  NOTIFY_ARCHIVE_LIMIT,
} = await import("../lib/notifyArchive");

const root = createRoot(document.getElementById("root")!);
await act(async () => {
  root.render(
    <LocaleProvider>
      <CollabHubPanel />
    </LocaleProvider>,
  );
});

const segButtons = () => Array.from(document.querySelectorAll<HTMLButtonElement>(".collab-hub-panel__view"));
const notifPanel = () => document.querySelector<HTMLElement>(".collab-notif-panel");
const notifRows = () => document.querySelectorAll(".collab-notif-panel__row");
const pinMetric = (el: HTMLElement, prop: string, value: number) =>
  Object.defineProperty(el, prop, { configurable: true, value });
/** 段间隔离：盘面清空 + 内存镜像复位（面板若挂载则由订阅缝同步空态）。 */
const wipe = () => {
  window.localStorage.removeItem("notifyArchive.v1");
  resetNotifyArchiveForTest();
};
const scrollNearBottom = () => {
  const el = notifPanel()!;
  pinMetric(el, "scrollTop", 600);
  pinMetric(el, "clientHeight", 400);
  pinMetric(el, "scrollHeight", 1000);
  return el.dispatchEvent(new dom.window.Event("scroll", { bubbles: true }));
};

// ── ① 通知 tab 出现：三视图壳 + 持久化 + 重开恢复 ──────────────────────────
await act(async () => { setCollabHubOpen(true); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.equal(segButtons().length, 3, "the hub shell offers mail/chat/notifications");
const notifTab = segButtons().find((b) => b.textContent === "Notifications");
assert.ok(notifTab, "the notifications tab is rendered");
await act(async () => { notifTab!.click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.ok(notifPanel(), "switching to the third view mounts the notification archive panel");
assert.equal(notifTab!.getAttribute("aria-selected"), "true", "the notifications tab is marked selected");
assert.equal(window.localStorage.getItem("collabHub.view"), "notifications", "the notifications choice persists to collabHub.view");
assert.ok(document.querySelector(".collab-notif-panel__empty"), "the empty archive renders the empty state");
await act(async () => {
  (document.querySelector(".collab-hub-panel__close") as HTMLButtonElement).click();
});
await act(async () => { setCollabHubOpen(true); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.ok(notifPanel(), "reopening the hub restores the notifications view (persisted)");

// ── ② 留档写入断言：四源发通知 → 面板行同步出现（端到端） ─────────────────
await act(async () => {
  recordNotification({ level: "info", source: "toast", text: "toast-archived" });
  recordNotification({ level: "warn", source: "ask-bg", text: "askbg-archived", detail: "tab 1 · ask 2" });
  recordNotification({ level: "error", source: "rt-notice", text: "rt-archived", code: "pending_crash" });
  recordNotification({ level: "info", source: "notice-event", text: "evt-archived", code: "operator_notice" });
});
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.equal(listNotifications().length, 4, "the archive stores all four source families");
assert.equal(notifRows().length, 4, "the panel receives the archive through the change subscription (live refresh)");
const stored = listNotifications();
assert.deepEqual(stored.map((e) => e.source), ["notice-event", "rt-notice", "ask-bg", "toast"], "entries stay in descending time order (newest first)");
assert.deepEqual(stored.map((e) => e.level), ["info", "error", "warn", "info"], "levels are kept verbatim per entry");
assert.equal(stored.find((e) => e.code === "pending_crash")?.text, "rt-archived", "the Go-side notice code rides along");

// ── ③ 级别分档：行 data-level + 字形 + 按级筛选 ───────────────────────────
const rowBy = (level: string) => document.querySelector(`.collab-notif-panel__row[data-level="${level}"]`);
assert.ok(rowBy("info") && rowBy("warn") && rowBy("error"), "each level renders with its data-level bucket");
assert.equal(rowBy("error")!.querySelector(".collab-notif-panel__icon")!.textContent, "!", "error rows carry the ! glyph");
assert.equal(rowBy("warn")!.querySelector(".collab-notif-panel__icon")!.textContent, "⚠", "warn rows carry the ⚠ glyph");
assert.equal(rowBy("info")!.querySelector(".collab-notif-panel__icon")!.textContent, "ℹ", "info rows carry the ℹ glyph");
const levelButton = (lv: string) => document.querySelector<HTMLButtonElement>(`.collab-notif-panel__level[data-level="${lv}"]`)!;
await act(async () => { levelButton("warn").click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.equal(notifRows().length, 1, "the warn filter narrows the list to warn rows only");
assert.ok(rowBy("warn"), "the surviving row is the warn one");
assert.ok(levelButton("warn").classList.contains("collab-notif-panel__level--on"), "the active filter button is marked on");
await act(async () => {
  (document.querySelector(".collab-notif-panel__levels .collab-notif-panel__level") as HTMLButtonElement).click();
});
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.equal(notifRows().length, 4, "the all filter restores the full list");

// ── ④ 懒加载 20 条：首屏 20 + 滚动近底 +20 + 到底自停 ─────────────────────
await act(async () => { wipe(); }); // 面板挂载中：订阅缝会同步空态，须包 act
await act(async () => {
  for (let i = 1; i <= 50; i++) {
    recordNotification({ level: "info", source: "toast", text: `page-${i}` });
  }
});
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.equal(notifRows().length, 20, "the first page renders exactly 20 rows (tl1230 parity)");
const more = () => document.querySelector(".collab-notif-panel__more")?.textContent ?? "";
assert.match(more(), /^30 more/, "the truncation hint reports the remaining 30");
await act(async () => { scrollNearBottom(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.equal(notifRows().length, 40, "scrolling near the bottom grows the page by 20");
assert.match(more(), /^10 more/, "the truncation hint reports the remaining 10");
await act(async () => { scrollNearBottom(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.equal(notifRows().length, 50, "the second scroll loads the tail page");
assert.equal(more(), "", "no truncation hint once everything is loaded");
await act(async () => { scrollNearBottom(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.equal(notifRows().length, 50, "scrolling past the end is a no-op (bail-out, no loop)");

// ── ⑤ 清空按钮：danger 二次确认（取消不清 / 确认清空 + 盘面空） ────────────
await act(async () => { clearNotifications(); });
await act(async () => {
  recordNotification({ level: "info", source: "toast", text: "clear-a" });
  recordNotification({ level: "warn", source: "toast", text: "clear-b" });
});
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.equal(notifRows().length, 2, "two entries seeded for the clear flow");
const clearButton = document.querySelector<HTMLButtonElement>(".collab-notif-panel__clear")!;
assert.ok(!clearButton.disabled, "the clear button is enabled with entries present");
await act(async () => { clearButton.click(); });
assert.ok(document.querySelector(".reasonix-confirm-dialog"), "clearing opens the confirm dialog");
assert.ok(document.querySelector(".reasonix-confirm-dialog__actions .btn--danger"), "the confirm action carries the danger tone");
const cancel = document.querySelector<HTMLButtonElement>(".reasonix-confirm-dialog__actions .btn:not(.btn--danger)")!;
await act(async () => { cancel.click(); });
assert.equal(document.querySelector(".reasonix-confirm-dialog"), null, "cancelling dismisses the dialog");
assert.equal(notifRows().length, 2, "cancelling keeps the archive intact");
await act(async () => { clearButton.click(); });
const confirmBtn = document.querySelector<HTMLButtonElement>(".reasonix-confirm-dialog__actions .btn--danger")!;
await act(async () => { confirmBtn.click(); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.equal(document.querySelector(".reasonix-confirm-dialog"), null, "confirming dismisses the dialog");
assert.equal(notifRows().length, 0, "confirming empties the panel");
assert.ok(document.querySelector(".collab-notif-panel__empty"), "the empty state returns");
assert.equal(window.localStorage.getItem("notifyArchive.v1"), "[]", "the store on disk is emptied too");
assert.ok(clearButton.disabled, "the clear button is disabled on an empty archive");

// ── ⑥ 存储数据面：FIFO 上限 / 去重窗口 / agent:event 订阅缝 ───────────────
await act(async () => { setCollabHubOpen(false); });
wipe();
assert.equal(NOTIFY_ARCHIVE_LIMIT, 200, "the archive cap is pinned at 200");
for (let i = 1; i <= NOTIFY_ARCHIVE_LIMIT + 10; i++) {
  recordNotification({ level: "info", source: "toast", text: `fifo-${i}` });
}
let all = listNotifications();
assert.equal(all.length, NOTIFY_ARCHIVE_LIMIT, "the archive never exceeds the 200-entry cap (FIFO)");
assert.equal(all[0].text, `fifo-${NOTIFY_ARCHIVE_LIMIT + 10}`, "the newest entry survives at the head");
assert.equal(all[all.length - 1].text, "fifo-11", "the ten oldest entries are trimmed from the tail");

wipe();
const first = recordNotification({ level: "info", source: "toast", text: "dup" });
assert.ok(first, "the first occurrence records");
assert.equal(recordNotification({ level: "info", source: "toast", text: "dup" }), null, "same source + same text within the dedup window records null");
assert.equal(listNotifications().length, 1, "the duplicate does not land");
assert.ok(recordNotification({ level: "info", source: "rt-notice", text: "dup" }), "a different source is never deduped");
assert.ok(recordNotification({ level: "warn", source: "toast", text: "dup2" }), "a different text is never deduped");

wipe();
const listeners = new Set<(e: { kind?: string; level?: string; text?: unknown; detail?: unknown; code?: unknown }) => void>();
const fakeOnEvent = (cb: (e: { kind?: string; level?: string; text?: unknown; detail?: unknown; code?: unknown }) => void) => {
  listeners.add(cb);
  return () => { listeners.delete(cb); };
};
const fireNotice = (e: { kind?: string; level?: string; text?: unknown; detail?: unknown; code?: unknown }) => {
  for (const cb of [...listeners]) cb(e);
};
const off = installNoticeEventArchive(fakeOnEvent);
assert.equal(listeners.size, 1, "the archive installs exactly one agent:event subscriber");
installNoticeEventArchive(fakeOnEvent);
assert.equal(listeners.size, 1, "installing twice is idempotent (no double subscriber)");
fireNotice({ kind: "notice", level: "warn", text: "evt-warn", detail: "d1", code: "op_cooldown" });
let head = listNotifications()[0];
assert.ok(head && head.source === "notice-event" && head.level === "warn" && head.text === "evt-warn", "notice events record with source=notice-event");
assert.equal(head.detail, "d1", "event detail rides along");
assert.equal(head.code, "op_cooldown", "event code rides along");
fireNotice({ kind: "notice", text: "evt-default" });
assert.equal(listNotifications()[0].level, "info", "an unmapped level defaults to info");
fireNotice({ kind: "chat", text: "not-a-notice" });
fireNotice({ kind: "notice", text: "" });
fireNotice({ kind: "notice", text: 42 });
assert.equal(listNotifications().length, 2, "non-notice kinds and empty/non-string texts never record");
off();
fireNotice({ kind: "notice", text: "after-off" });
assert.equal(listNotifications().length, 2, "unsubscribing stops the recording");

// ── ⑦ 既有展示行为零变化：toast 照常弹出/消失，留档≠拦截 ──────────────────
wipe();
const { ToastProvider, useToast } = await import("../lib/toast");
let showToast!: (text: string, level?: "info" | "warn" | "error") => void;
function ToastFixture() {
  const toast = useToast();
  showToast = toast.showToast;
  return null;
}
await act(async () => {
  root.render(
    <LocaleProvider>
      <ToastProvider>
        <ToastFixture />
      </ToastProvider>
    </LocaleProvider>,
  );
});
await act(async () => { showToast("zero-change-toast", "warn"); });
assert.ok(document.querySelector(".toast--warn"), "the toast still pops as before (display path untouched)");
assert.equal(document.querySelector(".toast__text")?.textContent, "zero-change-toast", "the toast carries its text as before");
assert.equal(listNotifications().length, 1, "the same fire archives one entry");
assert.equal(listNotifications()[0].source, "toast", "the archived entry is tagged source=toast");
await act(async () => { await new Promise((r) => setTimeout(r, 2700)); });
assert.equal(document.querySelector(".toast"), null, "the toast still auto-dismisses as before (2.5s default)");
assert.equal(listNotifications().length, 1, "the archive outlives the toast (archive never intercepts)");

await act(async () => { root.unmount(); });
console.log("tl1458-notify-archive: all assertions passed");
