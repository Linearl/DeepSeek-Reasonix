// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1712-host-inject-render-dedup.test.ts
//
// tl1712: the restart auto-resume injection rendered TWICE on the live surface
// (包2016 装机重启 2026-11-19 20:59: two identical "The app updated and
// restarted itself..." rows side by side; a tab switch rebuilt the transcript
// back to exactly one). 持久层只有 1 条（pre-repair 快照 447 行唯一一条
// origin=user 注入行）⇒ 双绘来自渲染层两个通道并存，且互相不去重：
//
//   form-1 — the startup hydrate page already contains the injected row
//     (persisted BEFORE the announce landed on the wire), then the no-itemId
//     `user_input` event appends a second, identical live row.
//     Red scene: hydrate prefix row + announce ⇒ exactly one row.
//   form-2 — after a reload rebase, `retainedLiveTail` keeps the live
//     announce row (id `u<seq>`) because the page row's id (`h<turn>-<n>`)
//     never collides with it — the task-316 stable-id union cannot see that
//     the two rows are the same persisted message. Red scene: live announce
//     row + history_rebase page carrying the same text ⇒ one row.
//
// The dedup key is text-equality scoped to the host-announce channel: composer
// rows never emit `user_input` (they render optimistically), durable-inbox
// events carry an itemId and keep the task-580 idempotency, so a no-itemId
// `user_input` event is ALWAYS a backend direct submission (announce shape).
// Same-text user rows outside that channel (a user typing the same sentence
// twice) must still render both rows — pinned below as no-regression.

import { initialState, reducer } from "../lib/useController";
import type { Item, State } from "../lib/useController";
import type { HistoryMessage, WireEvent } from "../lib/types";

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

function userRows(s: State): Array<Extract<Item, { kind: "user" }>> {
  return s.items.filter((it): it is Extract<Item, { kind: "user" }> => it.kind === "user");
}

const RESUME_PROMPT = "The app updated and restarted itself while this autopilot session was working. Continue from where it stopped; do not restate the plan or wait for input.";
const GOAL_PROMPT = "The process restarted while this autopilot goal was running. Continue it from where it stopped; do not restate the plan or wait for input.";
const TYPED_LINE = "用户手输的一句话";

function hydratePrefixWith(prompt: string): State {
  const messages: HistoryMessage[] = [{ role: "user", content: prompt }];
  return reducer(initialState, { type: "history", messages, remote: false });
}

function rebasePageWith(prompt: string, revision = 2): { type: "history_rebase"; items: Item[]; startTurn: number; totalTurns: number; hasOlder: boolean; revision: number } {
  return {
    type: "history_rebase",
    items: [
      { kind: "user", id: "h1-0", text: TYPED_LINE },
      { kind: "user", id: "h1-1", text: prompt },
    ],
    startTurn: 1,
    totalTurns: 2,
    hasOlder: false,
    revision,
  };
}

console.log("\ntl1712 host-injection render dedup (hydrate x live)");

// ── form-1: hydrate already drew the injected row; the announce must not ──────
// double it (包2016 20:59 scene: persisted row + live announce row side by side)
{
  const hydrated = hydratePrefixWith(RESUME_PROMPT);
  ok(userRows(hydrated).length === 1, "hydrate page renders the injected row once");
  const after = reducer(hydrated, ev({ kind: "user_input", text: RESUME_PROMPT }));
  ok(userRows(after).length === 1, "announce over an already-hydrated identical row stays one row (form-1)");
  ok(userRows(after)[0]?.text === RESUME_PROMPT, "the surviving row carries the injected prompt text");
}

// ── form-2: announce row already on the live tail; a reload rebase whose page
// carries the same persisted text must not keep both ──────────────────────────
{
  let s = reducer(initialState, ev({ kind: "user_input", text: RESUME_PROMPT }));
  ok(userRows(s).length === 1, "bare announce renders exactly one live row");
  s = reducer(s, rebasePageWith(RESUME_PROMPT));
  ok(s.items.some((item) => item.id === "h1-0") && s.items.some((item) => item.id === "h1-1"), "rebase page rows (typed line + persisted prompt) survive the merge");
  ok(userRows(s).filter((row) => row.text === RESUME_PROMPT).length === 1, "rebase collapses the live announce row into the persisted page row (form-2)");
}

// ── no-regression: bare announce (no hydrate behind it) still renders ────────
{
  const s = reducer(initialState, ev({ kind: "user_input", text: RESUME_PROMPT }));
  ok(userRows(s).length === 1, "bare announce with no hydrate page still renders (tl1643 contract)");
}

// ── no-regression: two different injected prompts each render (roster+goal) ──
{
  let s = reducer(initialState, ev({ kind: "user_input", text: RESUME_PROMPT }));
  s = reducer(s, ev({ kind: "user_input", text: GOAL_PROMPT }));
  ok(userRows(s).length === 2, "two distinct injected prompts render two rows");
}

// ── no-regression: composer-typed duplicates are NOT collapsed by the ─────────
// host-announce dedup (same text typed twice is legal, 580 case 5) ─────────────
{
  // Both rows live on the tail (no hydrate prefix behind them): the rebase page
  // below carries a DIFFERENT prompt, so nothing may drop the typed duplicates.
  let s = reducer(initialState, { type: "user", seq: 0, text: TYPED_LINE, submissionId: "sub1" } as never);
  s = reducer(s, { type: "user", seq: 1, text: TYPED_LINE, submissionId: "sub2" } as never);
  ok(userRows(s).length === 2, "composer rows render optimistically as typed");
  const page: Item[] = [{ kind: "user", id: "h1-0", text: RESUME_PROMPT }];
  s = reducer(s, { type: "history_rebase", items: page, startTurn: 1, totalTurns: 1, hasOlder: false, revision: 5 });
  ok(userRows(s).filter((row) => row.text === TYPED_LINE).length === 2, "rebase keeps both composer-typed duplicates (no host-announce marker, page has no same-text row)");
}

// ── no-regression: durable inbox events keep the itemId idempotency (580) ────
{
  let s = reducer(initialState, ev({ kind: "user_input", text: "跨会话信件", itemId: "inbox-1" }));
  s = reducer(s, ev({ kind: "user_input", text: "跨会话信件", itemId: "inbox-1" }));
  ok(userRows(s).length === 1, "inbox user_input replay with the same itemId stays one row (580 idempotency intact)");
}

// ── no-regression: announce dedup is scoped to the history prefix — a same-text
// row on the LIVE tail does not suppress a second announce (crash-analysis
// double-fire shape) ───────────────────────────────────────────────────────────
{
  const empty = initialState;
  const first = reducer(empty, ev({ kind: "user_input", text: TYPED_LINE }));
  const second = reducer(first, ev({ kind: "user_input", text: TYPED_LINE }));
  ok(userRows(second).length === 2, "two same-text announces with no history prefix both render (live-tail scope)");
}

// ── no-regression: pendingUser guard stays (tl1643 pin 3) ─────────────────────
{
  // The pendingUser guard SUPPRESSES the announce entirely (the optimistic row
  // belongs to the composer flow), so zero user rows land here.
  let s = { ...initialState, pendingUser: TYPED_LINE };
  s = reducer(s, ev({ kind: "user_input", text: TYPED_LINE }));
  ok(userRows(s).length === 0, "announce identical to the optimistic pending row is suppressed entirely");
}

console.log(`\ntl1712 host-inject render dedup: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
