// tl1682: an ops-based todo_write ({"ops":[...]} — the shape the tool schema
// tells the model to prefer for small edits) must not collapse the panel into
// the sealed "previous turn" fold. feedback-20261117-143801: a long session
// added seven in-progress tasks across turns; the panel parsed the ops args
// as an empty list, resolved the live projection to [], and the dock fell
// into the sealed empty state with the kernel's own canonical snapshot folded
// away behind the "previous turn" copy.
//
// Contract under test:
// - todoLiveParse distinguishes an explicit empty write ({"todos":[]} — still
//   authoritative, the clearing semantics) from an unexpandable shape (ops
//   write, malformed JSON → null = fall back to the canonical snapshot);
// - resolveTodoPanelTodos keeps the #7642 live preference for real lists and
//   the authoritative clear, and falls back only on null;
// - the end-to-end panel decision on the feedback scenario: with only an ops
//   write in the transcript, the in-progress/pending items stay visible
//   (shouldShowTodoPanel true) instead of sealing.
//
// Run: npx tsx src/__tests__/tl1682-todo-ops-panel-visibility.test.ts

import assert from "node:assert/strict";

import { parseTodos, todoLiveParse, type Todo } from "../lib/tools";
import {
  resolveTodoPanelTodos,
  shouldShowTodoPanel,
} from "../lib/todoVisibility";

let passed = 0;
let failed = 0;
function check(label: string, fn: () => void) {
  try {
    fn();
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } catch (err) {
    process.stdout.write(`  FAIL  ${label}\n      ${err}\n`);
    failed += 1;
  }
}

console.log("\ntl1682 todo ops panel visibility");

// 1. todoLiveParse: three args shapes, three verdicts.
{
  check("ops write → null (not expandable on the frontend)", () => {
    assert.deepEqual(
      todoLiveParse(`{"ops":[{"op":"insert","item":{"content":"gamma","status":"pending","step_id":"s3"}}]}`),
      null,
    );
  });
  check("malformed JSON → null", () => {
    assert.equal(todoLiveParse(`{"todos":`), null);
  });
  check("full todos list → the list itself", () => {
    const todos: Todo[] = [{ content: "alpha", status: "completed" }];
    assert.deepEqual(todoLiveParse(`{"todos":[{"content":"alpha","status":"completed"}]}`), todos);
  });
  check("explicit empty todos → [] (authoritative clear, NOT null)", () => {
    assert.deepEqual(todoLiveParse(`{"todos":[]}`), []);
  });
  // parseTodos keeps its legacy face: every shape above collapses to [].
  check("parseTodos legacy face unchanged (ops write also parses [])", () => {
    assert.deepEqual(parseTodos(`{"ops":[]}`), []);
    assert.deepEqual(parseTodos(`{"todos":[]}`), []);
  });
}

// 2. resolveTodoPanelTodos: live preference (task #7642) vs the tl1682 fallback.
{
  const canonical: Todo[] = [
    { content: "alpha", status: "completed" },
    { content: "beta", status: "in_progress" },
  ];
  const live: Todo[] = [{ content: "live", status: "pending" }];

  check("real live list still wins over canonical (#7642 face preserved)", () => {
    assert.deepEqual(resolveTodoPanelTodos(canonical, live), live);
  });
  check("null live (ops write / unparseable) falls back to canonical", () => {
    assert.deepEqual(resolveTodoPanelTodos(canonical, null), canonical);
  });
  check("undefined live falls back to canonical (existing face)", () => {
    assert.deepEqual(resolveTodoPanelTodos(canonical, undefined), canonical);
  });
  check("explicit empty live array stays authoritative (clear survives)", () => {
    assert.deepEqual(resolveTodoPanelTodos(canonical, []), []);
  });
  check("null live + no canonical → empty", () => {
    assert.deepEqual(resolveTodoPanelTodos(undefined, null), []);
  });
}

// 3. The feedback scenario end to end: the transcript holds only an ops
// write; the kernel canonical snapshot carries the seven items (five done,
// two active). The old chain (parseTodos → [] → authoritative) resolved the
// panel to empty and the dock sealed the fold; the tl1682 chain falls back to
// the canonical snapshot and keeps the active items on the panel.
{
  const kernelCanonical: Todo[] = [
    { content: "Inspect the report", status: "completed" },
    { content: "Ship the fix", status: "completed" },
    { content: "Write the handoff", status: "completed" },
    { content: "Update the docs", status: "completed" },
    { content: "Notify the reviewer", status: "completed" },
    { content: "Append the new task", status: "in_progress", activeForm: "Appending the new task" },
    { content: "Verify the panel", status: "pending" },
  ];
  const opsArgs = `{"ops":[{"op":"insert","item":{"content":"Verify the panel","status":"pending","step_id":"s7"}}]}`;

  check("old chain (parseTodos) resolved empty — the regression face", () => {
    const todos = resolveTodoPanelTodos(kernelCanonical, parseTodos(opsArgs));
    assert.equal(todos.length, 0);
    assert.equal(shouldShowTodoPanel("[]", null, todos), false);
  });
  check("new chain (todoLiveParse) keeps the seven items on the panel", () => {
    const todos = resolveTodoPanelTodos(kernelCanonical, todoLiveParse(opsArgs));
    assert.equal(todos.length, 7);
    assert.equal(shouldShowTodoPanel("nonempty", null, todos), true);
  });
  check("the explicit clear still hides the panel end to end", () => {
    const todos = resolveTodoPanelTodos(kernelCanonical, todoLiveParse(`{"todos":[]}`));
    assert.equal(todos.length, 0);
    assert.equal(shouldShowTodoPanel("[]", null, todos), false);
  });
  check("a dismissed all-completed list still hides (sealed fold intact)", () => {
    const finished: Todo[] = [
      { content: "alpha", status: "completed" },
      { content: "beta", status: "completed" },
    ];
    const todos = resolveTodoPanelTodos(finished, todoLiveParse(`{"ops":[{"op":"replace","step_id":"s1","item":{"content":"alpha","status":"completed"}}]}`));
    // The ops write completes the last item; the fallback keeps the list on
    // the panel for the fold decision — dismissal then seals it.
    assert.equal(todos.length, 2);
    assert.equal(shouldShowTodoPanel("finished-key", "finished-key", todos), false);
  });
}

console.log(`\ntl1682 todo ops panel visibility: ${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
