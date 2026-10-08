// Run: npx tsx src/__tests__/inbox-user-input-render.test.ts
//
// 任务580: a cross-session (collab mail) or guidance message consumed as a NEW
// turn's input arrives as a `user_input` wire event. The wire protocol has no
// other user-message channel — composer submissions render their row
// optimistically and history rows only land on hydrate — so without this case
// the transcript stayed without the message until a full reload, and the
// switch-back reuse path (zero fetch) never corrected it. Pins: append on
// arrival, idempotent on itemId (ClaimItem admits once; replays carry the same
// id), blank text ignored, an identical optimistic pendingUser row is not
// duplicated, and two DIFFERENT items with identical text both render.

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

const BODY = "[跨会话消息] 来自 contact_id=alpha → 发至 contact_id=beta\n帮忙确认 644 的验收矩阵";

console.log("\ninbox user_input rendering (任务580)");

// ── 1. arrival appends the user row immediately ──────────────────────────────
{
  let s = reducer(initialState, ev({ kind: "user_input", text: BODY, itemId: "item-1" }));
  const rows = userRows(s);
  ok(rows.length === 1, "user_input appends exactly one user row");
  ok(rows[0]?.text === BODY, "the row carries the display text (跨会话消息 header included)");
  ok(rows[0]?.inboxItemId === "item-1", "the row is tagged with the durable inbox item id");
}

// ── 2. replay of the same item id is idempotent ──────────────────────────────
{
  let s = reducer(initialState, ev({ kind: "user_input", text: BODY, itemId: "item-1" }));
  s = reducer(s, ev({ kind: "user_input", text: BODY, itemId: "item-1" }));
  s = reducer(s, ev({ kind: "user_input", text: BODY + "（重投递）", itemId: "item-1" }));
  ok(userRows(s).length === 1, "re-delivered user_input with the same itemId renders once");
}

// ── 3. blank text is ignored ─────────────────────────────────────────────────
{
  let s = reducer(initialState, ev({ kind: "user_input", text: "   ", itemId: "item-2" }));
  s = reducer(s, ev({ kind: "user_input", text: "", itemId: "item-3" }));
  ok(userRows(s).length === 0, "blank/empty user_input never appends a row");
}

// ── 4. composer race: identical optimistic pendingUser row is not duplicated ─
{
  let s = reducer(initialState, { type: "user", text: "排队中的指引", seq: 0, submissionId: "sub-1" });
  ok(userRows(s).length === 1, "optimistic composer row present");
  s = reducer(s, ev({ kind: "user_input", text: "排队中的指引", itemId: "item-4" }));
  ok(userRows(s).length === 1, "user_input matching the optimistic pendingUser text does not duplicate");
}

// ── 5. two different items with identical text both render ───────────────────
{
  let s = reducer(initialState, ev({ kind: "user_input", text: "同样的文本", itemId: "item-5" }));
  s = reducer(s, ev({ kind: "user_input", text: "同样的文本", itemId: "item-6" }));
  const rows = userRows(s);
  ok(rows.length === 2, "distinct inbox items with identical text are distinct rows");
  ok(rows[0]?.inboxItemId === "item-5" && rows[1]?.inboxItemId === "item-6", "each row keeps its own itemId");
}

// ── 6. the row survives a turn_started that follows the announcement ─────────
{
  let s = reducer(initialState, ev({ kind: "user_input", text: BODY, itemId: "item-7" }));
  s = reducer(s, ev({ kind: "turn_started", status: "in_progress" }));
  const rows = userRows(s);
  ok(rows.length === 1 && rows[0]?.text === BODY, "turn_started right after user_input keeps the user row");
  ok(s.running === true && s.turnActive === true, "turn_started still owns the running lifecycle");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
