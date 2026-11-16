// Run: npx tsx src/__tests__/tl1643-resume-user-input-render.test.ts
//
// tl1643: the restart auto-resume injection ("The app updated and restarted
// itself...") announces its row through the SAME wire channel as task 580's
// durable-inbox consumption — a `user_input` event with NO itemId (host
// injections carry no durable inbox item). The composer renders its own rows
// optimistically and never emits this kind, so this channel is the ONLY way a
// host-injected prompt shows up on first render; without it the row stayed
// invisible until a tab switch re-read history. Pins the no-itemId arrival
// shape the Go fix (desktop/autonomous_update_resume.go + tabs.go) now emits.

import { initialState, reducer } from "../lib/useController";
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

function userRows(s: State): Array<Extract<Item, { kind: "user" }>> {
  return s.items.filter((it): it is Extract<Item, { kind: "user" }> => it.kind === "user");
}

const RESUME_PROMPT = "The app updated and restarted itself while this autopilot session was working. Continue from where it stopped; do not restate the plan or wait for input.";
const GOAL_PROMPT = "The process restarted while this autopilot goal was running. Continue it from where it stopped; do not restate the plan or wait for input.";

console.log("\ntl1643 resume injection user_input rendering (no itemId)");

// ── 1. injected arrival (no itemId) renders the row immediately ──────────────
{
  let s = reducer(initialState, ev({ kind: "user_input", text: RESUME_PROMPT }));
  const rows = userRows(s);
  ok(rows.length === 1, "injected user_input without itemId appends exactly one user row");
  ok(rows[0]?.text === RESUME_PROMPT, "the row carries the injected prompt text");
  ok(rows[0]?.inboxItemId === undefined, "host-injected rows carry no durable inbox item id");

  // ── 2. both restart-injection shapes render (roster + goal) ────────────────
  s = reducer(s, ev({ kind: "user_input", text: GOAL_PROMPT }));
  const both = userRows(s);
  ok(both.length === 2, "a second injected prompt (goal shape) renders its own row");
  ok(both[1]?.text === GOAL_PROMPT, "the goal-shape row carries its own text");
}

// ── 3. no phantom dup against an optimistic composer row ─────────────────────
{
  // A composer submission keeps its optimistic text in pendingUser; the wire
  // event for an injected row must still render (different text), while the
  // pre-existing pendingUser rule keeps identical text from doubling.
  let s = initialState;
  s = { ...s, pendingUser: "用户自己输入的原文" };
  s = reducer(s, ev({ kind: "user_input", text: RESUME_PROMPT }));
  ok(userRows(s).length === 1, "injected row renders alongside an unrelated optimistic pending row");
  s = reducer(s, ev({ kind: "user_input", text: "用户自己输入的原文" }));
  ok(userRows(s).length === 1, "identical text to the optimistic pending row is not doubled");
}

console.log(`\ntl1643 resume injection render: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
