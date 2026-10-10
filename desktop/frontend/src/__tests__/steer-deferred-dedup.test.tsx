// Run: tsx src/__tests__/steer-deferred-dedup.test.tsx
//
// 任务723: a receipt-time steer bubble (guidance_bubble, ↪ prefix, tagged with
// the durable inboxItemId) whose message LATER arrives as a NEW turn's
// user_input (injected-but-unconsumed / image-rejected-to-followup — the
// dispatch pump admits it as its own turn) used to render TWICE: the ↪ bubble
// mid-previous-turn plus the independent user bubble at the tail. Data layer
// verified single-delivery; this pins the render-layer dedup keyed by
// inboxItemId (NEVER by text — same text from different items is legal,
// 580 case 5): the user row is the entity, the older ↪ row downgrades to the
// collapsed "sent · queued for the next turn" placeholder (expandable).
// Consumed steers (no matching user row) keep the full bubble untouched.

import { JSDOM } from "jsdom";
import { registerHooks } from "node:module";
import React, { act } from "react";
import { createRoot } from "react-dom/client";

// TranscriptCards pulls transitive CSS imports; tsx has no asset loader, so
// redirect them to the shared stub the way Vite handles them.
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.endsWith(".css") || specifier.endsWith(".svg")) {
      return nextResolve("./asset-stub-for-tests.ts", { ...context, parentURL: import.meta.url });
    }
    return nextResolve(specifier, context);
  },
});

import { initialState, reducer, STEER_NOTICE_PREFIX, isSteerNoticeText } from "../lib/useController";
import type { Item, State } from "../lib/useController";
import type { WireEvent } from "../lib/types";

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

function ev(e: Record<string, unknown>): { type: "event"; e: WireEvent } {
  return { type: "event", e: { turnId: "t1", ...e } as unknown as WireEvent };
}

function steerBubbles(s: State): Array<Extract<Item, { kind: "notice" }>> {
  return s.items.filter((it): it is Extract<Item, { kind: "notice" }> => it.kind === "notice" && isSteerNoticeText(it.text));
}

function userRows(s: State): Array<Extract<Item, { kind: "user" }>> {
  return s.items.filter((it): it is Extract<Item, { kind: "user" }> => it.kind === "user");
}

const BODY = "看看 723 的截图\n第二行只有展开才可见";

console.log("\nsteer deferred dedup (任务723)");

// ── 1. user_input downgrades the receipt bubble to the deferred placeholder ──
{
  let s = reducer(initialState, { type: "guidance_bubble", text: BODY, inboxItemId: "ib-1" });
  ok(steerBubbles(s).length === 1 && steerBubbles(s)[0]?.steerDeferred !== true, "receipt bubble starts as a full ↪ bubble (no flag)");
  s = reducer(s, ev({ kind: "user_input", text: BODY, itemId: "ib-1" }));
  const bubbles = steerBubbles(s);
  const rows = userRows(s);
  ok(bubbles.length === 1, "the ↪ row is kept as the placeholder (not deleted)");
  ok(bubbles[0]?.steerDeferred === true, "user_input with the same inboxItemId marks the bubble deferred");
  ok(rows.length === 1 && rows[0]?.inboxItemId === "ib-1", "the user row is the single entity");
}

// ── 2. replay of the same user_input is idempotent ───────────────────────────
{
  let s = reducer(initialState, { type: "guidance_bubble", text: BODY, inboxItemId: "ib-2" });
  s = reducer(s, ev({ kind: "user_input", text: BODY, itemId: "ib-2" }));
  s = reducer(s, ev({ kind: "user_input", text: BODY + "（重投递）", itemId: "ib-2" }));
  ok(userRows(s).length === 1, "replayed user_input appends no second user row");
  ok(steerBubbles(s).length === 1 && steerBubbles(s)[0]?.steerDeferred === true, "replay keeps exactly one deferred placeholder");
}

// ── 3. consume-time steer event after deferral adds nothing and un-defers nothing ─
{
  let s = reducer(initialState, { type: "guidance_bubble", text: BODY, inboxItemId: "ib-3" });
  s = reducer(s, ev({ kind: "user_input", text: BODY, itemId: "ib-3" }));
  s = reducer(s, ev({ kind: "steer", text: BODY, itemId: "ib-3" }));
  ok(steerBubbles(s).length === 1 && steerBubbles(s)[0]?.steerDeferred === true, "late consume event keeps one deferred placeholder");
}

// ── 4. normal consumed steer (no user row) stays a full bubble — 543/580 语义不回归 ─
{
  let s = reducer(initialState, { type: "guidance_bubble", text: "先跑测试再改", inboxItemId: "ib-4" });
  s = reducer(s, ev({ kind: "steer", text: "先跑测试再改", itemId: "ib-4" }));
  const bubbles = steerBubbles(s);
  ok(bubbles.length === 1, "receipt bubble + consume event still render exactly one bubble");
  ok(bubbles[0]?.steerDeferred !== true, "consumed steer keeps the full bubble (no deferral)");
}

// ── 5. dedup key is the inboxItemId, never the text ──────────────────────────
{
  let s = reducer(initialState, { type: "guidance_bubble", text: "同样的文本", inboxItemId: "ib-5" });
  s = reducer(s, ev({ kind: "user_input", text: "同样的文本", itemId: "ib-9" }));
  const bubbles = steerBubbles(s);
  ok(bubbles.length === 1 && bubbles[0]?.steerDeferred !== true, "same text under a DIFFERENT itemId does not defer the bubble");
  ok(userRows(s).length === 1, "the other item's user row renders independently");
}

// ── 6. user_input without an itemId marks nothing ────────────────────────────
{
  let s = reducer(initialState, { type: "guidance_bubble", text: BODY, inboxItemId: "ib-6" });
  s = reducer(s, ev({ kind: "user_input", text: "无 id 的输入" }));
  ok(steerBubbles(s)[0]?.steerDeferred !== true, "user_input with no itemId leaves the bubble untouched");
}

// ── 7. plain notices are never marked ────────────────────────────────────────
{
  let s = reducer(initialState, ev({ kind: "notice", level: "warn", text: "普通告警", code: "unapplied_steer" }));
  s = reducer(s, ev({ kind: "user_input", text: "普通告警", itemId: "plain-1" }));
  const plains = s.items.filter((it): it is Extract<Item, { kind: "notice" }> => it.kind === "notice" && !isSteerNoticeText(it.text));
  ok(plains.length === 1 && plains[0]?.steerDeferred !== true, "non-steer notices are never deferred-marked");
}

// ── component: SteerCard placeholder collapses by default, expands on click ──
{
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

  const { LocaleProvider } = await import("../lib/i18n");
  const { SteerCard } = await import("../components/TranscriptCards");

  const rootEl = document.getElementById("root")!;
  const host = document.createElement("div");
  rootEl.appendChild(host);
  const root = createRoot(host);

  await act(async () => {
    root.render(
      <LocaleProvider>
        <SteerCard id="s-def" text={`${STEER_NOTICE_PREFIX}${BODY}`} deferred={true} />
      </LocaleProvider>,
    );
  });
  let html = host.innerHTML;
  const statusLabel = host.querySelector(".steer-line__status")?.textContent ?? "";
  ok(statusLabel.trim().length > 0, "collapsed placeholder shows the deferred status label");
  ok(html.includes("看看 723 的截图"), "collapsed placeholder still shows the body's first line");
  ok(!html.includes("第二行"), "collapsed placeholder hides the body's later lines");
  ok(html.includes("aria-expanded=\"false\""), "placeholder renders as an expandable control (collapsed)");

  const button = host.querySelector("button.steer-line__bubble--deferred") as HTMLButtonElement | null;
  ok(button !== null, "placeholder is a button (click to expand)");
  await act(async () => { button!.click(); });
  html = host.innerHTML;
  ok(html.includes("第二行"), "expanding reveals the full body");
  ok(html.includes("aria-expanded=\"true\""), "expanded state is exposed to assistive tech");

  await act(async () => { button!.click(); });
  ok(!host.innerHTML.includes("第二行"), "collapsing again hides the body tail");

  await act(async () => {
    root.render(
      <LocaleProvider>
        <SteerCard id="s-full" text={`${STEER_NOTICE_PREFIX}完整气泡`} deferred={false} />
      </LocaleProvider>,
    );
  });
  html = host.innerHTML;
  ok(html.includes("完整气泡") && !html.includes("steer-line__bubble--deferred"), "non-deferred steer keeps the full bubble (no status label)");

  await act(async () => { root.unmount(); });
  host.remove();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
