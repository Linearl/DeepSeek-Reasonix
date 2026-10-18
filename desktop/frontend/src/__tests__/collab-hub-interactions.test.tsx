// Run: tsx src/__tests__/collab-hub-interactions.test.tsx
// 任务 980（协作中心交互三缺陷，用户 2026-10-18 验收实测）：
//   ① 外点空白关闭：mousedown 在面板外 → 关闭；面板内不关；Esc 关闭；自家
//      portal 浮层（.modal-backdrop）点击不算外部（否则详情弹窗/确认框会被
//      连带卸载）；
//   ② 列表滚动：embedded（Hub 壳内）信件面板的 CSS 必须 overflow:auto（此前
//      hidden → 信件一多整块裁掉无滚动条）+ 多信件 fixture 照常渲染全部行；
//   ③ 详情弹窗：行内「详情」按钮 / 预览行点击 → portal 弹窗展示全文（ReadCollabMail
//      按需取，弹窗正文含 preview 之外的完整内容）；Esc / backdrop / 关闭按钮
//      三路关闭且只关弹窗不关 Hub。
// 壳层接线归 collab-hub-panel.test.tsx；本 harness 专注 980 三缺陷。

import * as React from "react"; // tsconfig exclude 了 __tests__，tsx 对本文件走 classic JSX——必须显式绑定 React
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
const { CollabHubPanel, setCollabHubOpen, isCollabHubOpen } = await import("../components/CollabHubPanel");
const { isCollabInboxOpen } = await import("../components/CollabInboxPanel");
const { LocaleProvider } = await import("../lib/i18n");
type CollabInboxBindings = import("../components/CollabInboxPanel").CollabInboxBindings;
type CollabMailEntry = import("../components/CollabInboxPanel").CollabMailEntry;
type CollabMailSnapshot = import("../components/CollabInboxPanel").CollabMailSnapshot;
type CollabMailDetail = import("../components/CollabInboxPanel").CollabMailDetail;

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

// 多信件 fixture（② 溢出场景的数据面）+ 一条带长正文的信（③ 全文来源）。
const LONG_BODY = "首行速览（preview 只有这一行）\n\n第二段：用户验收要看的完整正文内容。\n"
  + "第三段：详情弹窗必须原样展示，不截断。".repeat(30);
const fixtureEntries: CollabMailEntry[] = Array.from({ length: 30 }, (_, i) =>
  entry({ id: `m${i}`, preview: `triage line ${i}` }));
fixtureEntries[0] = entry({ id: "m_long", preview: "long mail", at: 1_700_000_100_000 });

const calls: { name: string; args: unknown[] }[] = [];
const snapshot: CollabMailSnapshot = {
  revision: "1.0.0",
  settings: { retention: "7d" },
  total: fixtureEntries.length,
  returned: fixtureEntries.length,
  truncated: false,
  entries: fixtureEntries,
};

const bindings: CollabInboxBindings = {
  async ListCollabMail() {
    calls.push({ name: "ListCollabMail", args: [] });
    return snapshot;
  },
  async ListCollabMailChains() {
    return { revision: "1.0.0", settings: { retention: "7d" }, total: 0, chains: [] };
  },
  async ReadCollabMail(id: string): Promise<CollabMailDetail> {
    calls.push({ name: "ReadCollabMail", args: [id] });
    const e = fixtureEntries.find((x) => x.id === id)!;
    return { id, from: e.from, to: e.to, at: e.at, threadId: e.threadId, bucket: e.bucket, preview: e.preview, body: LONG_BODY };
  },
  async CountUnreadCollabMail() { return 0; },
  async DismissCollabMail() { return snapshot; },
  async MarkCollabMailRead() { return snapshot; },
  async UndismissCollabMail() { return snapshot; },
  async MarkCollabMailDecided() { return snapshot; },
  async SetCollabMailRetention() { return snapshot; },
  async SetCollabMailCleanupRule() { return snapshot; },
  async CleanCollabMailNow() { return { removed: 0, snapshot }; },
  async DeleteCollabMail() { return snapshot; },
  async ClearAllCollabMail() { return { removed: 0, snapshot }; },
};

const hub = () => document.querySelector(".collab-hub-panel");
const mousedownOn = (target: Element | Document) =>
  target.dispatchEvent(new dom.window.MouseEvent("mousedown", { bubbles: true }));
const pressEscapeFrom = (target: Element) =>
  target.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));

const root = createRoot(document.getElementById("root")!);
window.localStorage.removeItem("collabHub.view"); // 首访 = 信件视图（不继承其他 suite 的持久化）
await act(async () => {
  root.render(
    <LocaleProvider>
      <CollabHubPanel inboxBindings={bindings} />
    </LocaleProvider>,
  );
});

// ── ① 外点空白关闭 ─────────────────────────────────────────────────────
await act(async () => { setCollabHubOpen(true); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.ok(hub(), "the hub opens");
assert.ok(document.querySelector(".collab-inbox-panel"), "the mail view embeds the inbox panel");

// 面板内 mousedown 不关（自身点击绝不能自杀）。
await act(async () => { mousedownOn(hub()!); });
assert.ok(hub(), "a mousedown INSIDE the panel keeps it open");

// 面板外空白 → 关闭。
await act(async () => { mousedownOn(document.body); });
assert.equal(hub(), null, "a mousedown on the outside blank area closes the hub (980 ①)");
assert.equal(isCollabHubOpen(), false, "the module open flag follows the outside click");
assert.equal(isCollabInboxOpen(), false, "closing via outside click resets the sub-panel module open (badge timing)");

// 重开 → Esc 关闭。
await act(async () => { setCollabHubOpen(true); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
assert.ok(hub(), "the hub reopens");
await act(async () => { pressEscapeFrom(hub()!.querySelector(".collab-hub-panel__close") as Element); });
assert.equal(hub(), null, "Escape closes the hub (980 ①)");

// 重开 → 自家 portal 浮层（.modal-backdrop）上的 mousedown 不算外部。
await act(async () => { setCollabHubOpen(true); });
await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
{
  const overlay = document.createElement("div");
  overlay.className = "modal-backdrop";
  document.body.appendChild(overlay);
  await act(async () => { mousedownOn(overlay); });
  assert.ok(hub(), "a mousedown on a hub-owned portal overlay (.modal-backdrop) does NOT close the hub");
  overlay.remove();
}

// ── ② 多信件列表照常渲染全部行（滚动由 CSS 承担，见下方 CSS 断言）────────
{
  const rows = document.querySelectorAll(".collab-inbox-panel__row");
  assert.equal(rows.length, 30, "the embedded inbox renders every fixture row (30 letters)");
}

// ── ③ 详情弹窗：全文展示（弹窗家族先例，portal 到 body）───────────────
{
  const detailButtons = Array.from(
    document.querySelectorAll<HTMLButtonElement>(".collab-inbox-panel__rowactions button"),
  ).filter((b) => b.textContent === "Details");
  assert.equal(detailButtons.length, 30, "every list row carries the explicit Details entry (980 ③)");

  await act(async () => { detailButtons[0].click(); });
  const readCall = calls.find((c) => c.name === "ReadCollabMail");
  assert.deepEqual(readCall?.args, ["m_long"], "clicking Details fetches the full body by entry id");

  const dialog = document.querySelector(".collab-inbox-detail");
  assert.ok(dialog, "the detail popup renders (modal family, portal to body)");
  assert.equal(
    document.querySelector(".collab-hub-panel .collab-inbox-detail"),
    null,
    "the popup is portaled OUTSIDE the hub shell (anti-clipping, 529 precedent)",
  );
  const bodyEl = dialog!.querySelector(".collab-inbox-detail__body");
  assert.match(bodyEl?.textContent ?? "", /第二段：用户验收要看的完整正文内容/, "the popup shows the FULL text, not the preview");
  assert.match(bodyEl?.textContent ?? "", /不截断/, "the tail of the multi-line body survives (no truncation)");

  // 弹窗内 Esc：只关弹窗，Hub 保持打开（capture 相自拦，980 ① 与 ③ 不互斥）。
  await act(async () => { pressEscapeFrom(dialog!.querySelector(".modal__actions button") as Element); });
  assert.equal(document.querySelector(".collab-inbox-detail"), null, "Escape closes the detail popup");
  assert.ok(hub(), "Escape on the popup does NOT close the hub underneath");

  // 重开详情（预览行点击 = 第二入口）→ backdrop mousedown：只关弹窗，Hub 保持。
  const previewBtn = document.querySelector<HTMLButtonElement>(".collab-inbox-panel__preview--btn");
  assert.ok(previewBtn, "the preview line is itself clickable (title hint + cursor)");
  await act(async () => { previewBtn!.click(); });
  assert.ok(document.querySelector(".collab-inbox-detail"), "clicking the preview line opens the same popup");
  {
    const backdrop = document.querySelector(".collab-inbox-detail-backdrop") as Element;
    assert.ok(backdrop, "the popup carries its backdrop");
    await act(async () => { mousedownOn(backdrop); });
    assert.equal(document.querySelector(".collab-inbox-detail"), null, "clicking the backdrop closes the popup");
    assert.ok(hub(), "the backdrop click is a hub-owned overlay — the hub stays open");
  }
}

// 收尾：外点收工（也复验弹窗关闭后外点仍然生效）。
await act(async () => { mousedownOn(document.body); });
assert.equal(hub(), null, "outside click still closes the hub after the popup round-trips");

// ── ② 的 CSS 断言：embedded 面板 overflow 必须 auto（滚动），不是 hidden ──
{
  const testDir = dirname(fileURLToPath(import.meta.url));
  const styles = readFileSync(resolve(testDir, "../styles.css"), "utf8");
  const embeddedRule = styles.slice(
    styles.indexOf(".collab-hub-panel__body .collab-inbox-panel {"),
    styles.indexOf("}", styles.indexOf(".collab-hub-panel__body .collab-inbox-panel {")),
  );
  assert.match(embeddedRule, /overflow:\s*auto/, "the embedded inbox panel scrolls (overflow:auto) — was hidden, letters got clipped with no scrollbar (980 ②)");
  assert.doesNotMatch(embeddedRule, /overflow:\s*hidden/, "overflow:hidden must not come back on the embedded panel");
}

await act(async () => { root.unmount(); });
dom.window.close();
console.log("collab hub interactions (980): all assertions passed");
