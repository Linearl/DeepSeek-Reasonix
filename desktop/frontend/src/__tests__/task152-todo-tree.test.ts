// Task 152: todo tree data layer — terminal statuses (abandoned/archived),
// tree depths (parent_id chains over the legacy level 0/1 adjacency), dotted
// hierarchy codes (T1 / T1.1 / T1.1.1), and the batch partition that turns
// earlier fully-finished todo_write batches into the panel's collapsed archive.
//
// Run: npx tsx src/__tests__/task152-todo-tree.test.ts

import assert from "node:assert/strict";

import {
  partitionTodoBatches,
  todoHierarchyCodes,
  todoPresentationStatus,
  todoTerminalStatus,
  todoTreeDepths,
  type TodoBatch,
} from "../lib/todoVisibility";
import { parseTodos } from "../lib/tools";

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

console.log("\ntask 152 todo tree data layer");

// ── terminal statuses ────────────────────────────────────────────────────────

check("todoTerminalStatus: completed/abandoned/archived are terminal", () => {
  assert.equal(todoTerminalStatus("completed"), true);
  assert.equal(todoTerminalStatus("abandoned"), true);
  assert.equal(todoTerminalStatus("archived"), true);
  assert.equal(todoTerminalStatus("pending"), false);
  assert.equal(todoTerminalStatus("in_progress"), false);
  assert.equal(todoTerminalStatus(undefined), false);
});

check("todoPresentationStatus surfaces abandoned/archived verbatim", () => {
  const runtime = { running: false, pendingPrompt: false };
  assert.equal(todoPresentationStatus("abandoned", runtime), "abandoned");
  assert.equal(todoPresentationStatus("archived", runtime), "archived");
});

// ── tree depths ──────────────────────────────────────────────────────────────

check("todoTreeDepths: flat list stays at depth 0", () => {
  const todos = [{ content: "a" }, { content: "b" }, { content: "c" }];
  assert.deepEqual(todoTreeDepths(todos), [0, 0, 0]);
});

check("todoTreeDepths: legacy level 0/1 adjacency maps sub-steps to depth 1", () => {
  const todos = [
    { content: "phase 1", level: 0 },
    { content: "sub 1", level: 1 },
    { content: "sub 2", level: 1 },
    { content: "phase 2", level: 0 },
  ];
  assert.deepEqual(todoTreeDepths(todos), [0, 1, 1, 0]);
});

check("todoTreeDepths: explicit parent_id chain nests past two levels", () => {
  const todos = [
    { content: "stage", step_id: "s1" },
    { content: "task", step_id: "s2", parent_id: "s1" },
    { content: "subtask", step_id: "s3", parent_id: "s2" },
    { content: "second stage", step_id: "s4" },
  ];
  assert.deepEqual(todoTreeDepths(todos), [0, 1, 2, 0]);
});

check("todoTreeDepths: dangling parent_id falls back to depth 0 (still renders)", () => {
  const todos = [
    { content: "root", step_id: "r" },
    { content: "orphan", step_id: "x", parent_id: "missing" },
  ];
  assert.deepEqual(todoTreeDepths(todos), [0, 0]);
});

// ── hierarchy codes ──────────────────────────────────────────────────────────

check("todoHierarchyCodes: flat list counts roots T1..Tn", () => {
  const todos = [{ content: "a" }, { content: "b" }, { content: "c" }];
  assert.deepEqual(todoHierarchyCodes(todos), ["T1", "T2", "T3"]);
});

check("todoHierarchyCodes: legacy level list renders T1 / T1.1 / T1.2 / T2", () => {
  const todos = [
    { content: "phase 1", level: 0 },
    { content: "sub 1", level: 1 },
    { content: "sub 2", level: 1 },
    { content: "phase 2", level: 0 },
  ];
  assert.deepEqual(todoHierarchyCodes(todos), ["T1", "T1.1", "T1.2", "T2"]);
});

check("todoHierarchyCodes: explicit tree recurses T1 / T1.1 / T1.1.1 / T2 / T2.1", () => {
  const todos = [
    { content: "stage", step_id: "s1" },
    { content: "task", step_id: "s2", parent_id: "s1" },
    { content: "subtask", step_id: "s3", parent_id: "s2" },
    { content: "second stage", step_id: "s4" },
    { content: "second task", step_id: "s5", parent_id: "s4" },
  ];
  assert.deepEqual(todoHierarchyCodes(todos), ["T1", "T1.1", "T1.1.1", "T2", "T2.1"]);
});

check("todoHierarchyCodes: sibling position follows same-parent runs", () => {
  const todos = [
    { content: "root", step_id: "r" },
    { content: "a", step_id: "a", parent_id: "r" },
    { content: "b", step_id: "b", parent_id: "r" },
    { content: "c", step_id: "c", parent_id: "r" },
  ];
  assert.deepEqual(todoHierarchyCodes(todos), ["T1", "T1.1", "T1.2", "T1.3"]);
});

// ── batch partition (include_terminal parametrization + archive) ────────────

const doneBatch: TodoBatch = {
  key: "batch-done",
  todos: [
    { content: "done", status: "completed" },
    { content: "given up", status: "abandoned" },
  ],
};
const liveBatch: TodoBatch = {
  key: "batch-live",
  todos: [
    { content: "finished", status: "completed" },
    { content: "current", status: "in_progress" },
  ],
};

check("partitionTodoBatches: includeTerminal=false keeps the single-batch behaviour", () => {
  const part = partitionTodoBatches([liveBatch, doneBatch], { includeTerminal: false });
  assert.equal(part.current, liveBatch.todos);
  assert.equal(part.archive.length, 0);
});

check("partitionTodoBatches: fully-terminal older batches become the archive, newest first", () => {
  const older: TodoBatch = { key: "batch-older", todos: [{ content: "old", status: "completed" }] };
  const part = partitionTodoBatches([liveBatch, doneBatch, older], { includeTerminal: true });
  assert.equal(part.current, liveBatch.todos);
  assert.deepEqual(part.archive.map((b) => b.key), ["batch-done", "batch-older"]);
});

check("partitionTodoBatches: a batch with open work never enters the archive", () => {
  const part = partitionTodoBatches([liveBatch, doneBatch], { includeTerminal: true });
  assert.equal(part.current, liveBatch.todos);
  assert.deepEqual(part.archive.map((b) => b.key), ["batch-done"]);
});

check("partitionTodoBatches: sidecar-dismissed batches stay retired", () => {
  const part = partitionTodoBatches([liveBatch, doneBatch], {
    includeTerminal: true,
    dismissedBatches: ["batch-done"],
  });
  assert.equal(part.archive.length, 0);
});

check("parseTodos passes parent_id/step_id through to the panel", () => {
  const todos = parseTodos(JSON.stringify({
    todos: [
      { content: "stage", status: "in_progress", step_id: "s1" },
      { content: "task", status: "pending", step_id: "s2", parent_id: "s1" },
    ],
  }));
  assert.equal(todos[1].parent_id, "s1");
  assert.equal(todos[1].step_id, "s2");
});

console.log(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
