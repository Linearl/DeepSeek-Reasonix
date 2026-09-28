// Task 181 acceptance (pencil → full body in the MAIN composer textarea):
//  A. 占位 branch — an undelivered entry edited and saved keeps its queue
//     position: UpdateInboxItem in place, ZERO EnqueueInboxFollowup;
//  B. 重排 branch — an entry that went in-flight while the edit was open is
//     re-queued at the TAIL on confirm: EnqueueInboxFollowup, ZERO
//     UpdateInboxItem (the delivery in motion is left alone);
//  C. draft stash — the draft already in the composer is restored verbatim,
//     via the banner Cancel (C1) and via Escape (C2), with zero inbox writes;
//  D. wiring guards — full body read by id, submit routes to the save while
//     composing, banner/stash/requeue/RetryInboxItem present.
//
// Editing note: setArea goes through select()+paste because the component's
// own onPaste (Composer.tsx) is the channel proven to work in this harness —
// the synthetic `input` event never reached the textarea's onChange here
// (draft injection via paste works; identical input dispatch does not).
//
// Run: npx tsx src/__tests__/task181-guidance-edit-fulltext.test.tsx

import { act } from "react";
import { installDom, installBridgeApp, renderComposer } from "./composerInboxHarness";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

async function waitFor(label: string, check: () => boolean) {
  for (let i = 0; i < 40; i += 1) {
    if (check()) return;
    await act(async () => { await flushTimers(); });
  }
  throw new Error(`timeout: ${label}`);
}

const FULL = ["[跨会话消息] 来自 contact_id=abc", "第一行：长引导正文超过预览上限的填充内容继续写下去。", "第二行：尾部。"].join("\n");
const PREVIEW = FULL.slice(0, 120).replace(/\n/g, " ");
const DRAFT = "我的原始草稿";

const AREA = "textarea.composer__input:not([aria-hidden=true])";
const area = () => document.querySelector(AREA) as HTMLTextAreaElement;

async function pasteInto(text: string, selectAll: boolean) {
  const el = area();
  if (selectAll) el.select();
  await act(async () => {
    const paste = new window.Event("paste", { bubbles: true, cancelable: true });
    Object.defineProperty(paste, "clipboardData", { value: { files: [], items: [], types: ["text/plain"], getData: () => text } });
    el.dispatchEvent(paste);
    await flushTimers();
  });
}

async function openPencil() {
  const pencil = document.querySelector(".composer-guidance-item__action") as HTMLButtonElement;
  await act(async () => { pencil.click(); await flushTimers(); });
}

async function pressSend() {
  const send = document.querySelector(".composer__btn--send") as HTMLButtonElement;
  await act(async () => { send.click(); await flushTimers(); });
}

interface Calls { update: unknown[][]; enqueue: unknown[][] }

{
  const dom = installDom();
  const calls: Calls = { update: [], enqueue: [] };
  let items = [{ id: "d1", state: "queued" as string, preview: PREVIEW }];
  let revision = 5;
  const snapshotFn = async () => ({
    revision,
    paused: false,
    recovered: false,
    sessionPath: "session-a",
    items: items.map((it, i) => ({ ...it, intent: "followup", source: "desktop", byteSize: 512, position: i + 1 })),
    itemsCount: items.length,
    bytes: items.length * 512,
    maxItems: 64,
    maxBytes: 64 * 1024 * 1024,
  });
  installBridgeApp({
    InboxSnapshot: snapshotFn,
    ReadInboxItem: async () => ({ id: "d1", displayText: FULL, rawText: FULL, submitText: FULL }),
    UpdateInboxItem: async (...args: unknown[]) => { calls.update.push(args); },
    EnqueueInboxFollowup: async (...args: unknown[]) => { calls.enqueue.push(args); },
    RetryInboxItem: async () => {},
  });
  const originalConfirm = window.confirm;
  window.confirm = () => true;
  const resetQueue = () => {
    calls.update.length = 0;
    calls.enqueue.length = 0;
    items = [{ id: "d1", state: "queued", preview: PREVIEW }];
    revision = 5;
  };

  // ── A. 占位 branch: queued entry saves in place ────────────────────────────
  {
    resetQueue();
    const { root } = await renderComposer({ running: false });
    await waitFor("row rendered (A)", () => document.querySelectorAll(".composer-guidance-item").length === 1);
    await pasteInto(DRAFT, false);
    await openPencil();
    await waitFor("body loaded (A)", () => area().value === FULL);
    ok(true, "A: pencil loads the FULL body into the main textarea (ReadInboxItem by id)");
    ok(document.querySelector(".composer-guidance-edit-banner") !== null, "A: editing banner is visible");
    await pasteInto("占位编辑后的全文\n第二行", true);
    ok(area().value === "占位编辑后的全文\n第二行", "A: edited text lands in the textarea (select+paste)");
    await pressSend();
    ok(calls.update.length === 1 && calls.enqueue.length === 0, `A: save keeps the queue position (update=${calls.update.length}, enqueue=${calls.enqueue.length})`);
    const args = calls.update[0] as unknown[] | undefined;
    ok(Boolean(args && args[2] === "占位编辑后的全文\n第二行"), "A: UpdateInboxItem receives the edited multi-line body verbatim");
    await act(async () => { root.unmount(); });
  }

  // ── B. 重排 branch: in-flight while editing → tail re-queue on confirm ────
  {
    resetQueue();
    const { root, rerender } = await renderComposer({ running: false });
    await waitFor("row rendered (B)", () => document.querySelectorAll(".composer-guidance-item").length === 1);
    await openPencil();
    await waitFor("body loaded (B)", () => area().value === FULL);
    // The entry enters delivery while the edit is open; flip the snapshot and
    // re-render (running is a refresh-effect dependency) so the queue re-reads it.
    items = items.map((it) => ({ ...it, state: "steer_accepted" }));
    revision += 1;
    await rerender({ running: true });
    await act(async () => { await flushTimers(); });
    await pasteInto("重排后的正文", true);
    ok(area().value === "重排后的正文", "B: edited text lands in the textarea (select+paste)");
    await pressSend();
    ok(calls.enqueue.length === 1 && calls.update.length === 0, `B: in-flight edit re-queues at the tail (enqueue=${calls.enqueue.length}, update=${calls.update.length})`);
    const enq = calls.enqueue[0] as unknown[] | undefined;
    ok(Boolean(enq && String(enq[3]).startsWith("guidance-requeue-d1-")), "B: re-queue uses the guidance-requeue idempotency key");
    ok(Boolean(enq && enq[1] === "重排后的正文" && enq[2] === "重排后的正文"), "B: re-queued body is the edited full text");
    await act(async () => { root.unmount(); });
  }

  // ── C. stash: draft restored verbatim, zero inbox writes ──────────────────
  for (const mode of ["cancel-button", "escape"] as const) {
    resetQueue();
    const { root } = await renderComposer({ running: false });
    await waitFor(`row rendered (${mode})`, () => document.querySelectorAll(".composer-guidance-item").length === 1);
    await pasteInto(DRAFT, false);
    ok(area().value === DRAFT, `(${mode}): draft injected before editing`);
    await openPencil();
    await waitFor(`body loaded (${mode})`, () => area().value === FULL);
    ok(area().value === FULL, `(${mode}): full body replaces the draft on entry (stash taken)`);
    if (mode === "cancel-button") {
      const cancel = document.querySelector(".composer-guidance-edit-banner__cancel") as HTMLButtonElement;
      await act(async () => { cancel.click(); await flushTimers(); });
    } else {
      await act(async () => {
        area().dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
        await flushTimers();
      });
    }
    const restored = area().value;
    ok(restored === DRAFT, `(${mode}): stashed draft restored verbatim (len=${restored.length}/${DRAFT.length})`);
    ok(calls.update.length === 0 && calls.enqueue.length === 0, `(${mode}): cancel is zero-side-effect (no inbox writes)`);
    ok(document.querySelector(".composer-guidance-edit-banner") === null, `(${mode}): editing banner closed`);
    await act(async () => { root.unmount(); });
  }
  window.confirm = originalConfirm;
  dom.window.close();
}

// ── D. wiring guards ─────────────────────────────────────────────────────────
{
  const fs = await import("node:fs");
  const read = (rel: string) => fs.readFileSync(new URL(rel, import.meta.url), "utf8");
  const composer = read("../components/Composer.tsx");
  ok(composer.includes("const [guidanceCompose, setGuidanceCompose]"), "guard: compose mode state exists");
  ok(composer.includes("guidanceComposeStashRef"), "guard: draft stash ref exists");
  ok(composer.includes("app.ReadInboxItem(tabId || \"\", item.id)"), "guard: full body read by id");
  ok(composer.includes("await saveGuidanceCompose();"), "guard: send key routes to save while composing");
  ok(composer.includes("app.UpdateInboxItem(target.tabId, target.id, body, body)"), "guard: in-place save (占位)");
  ok(composer.includes("guidance-requeue-"), "guard: tail re-queue path (重排)");
  ok(composer.includes("app.RetryInboxItem(target.tabId, target.id)"), "guard: needsRetry re-queues through RetryInboxItem");
}

if (failed > 0) {
  console.error(`\n${failed} check(s) failed`);
  process.exit(1);
}
console.log(`\nall checks passed (${passed} assertions)`);
