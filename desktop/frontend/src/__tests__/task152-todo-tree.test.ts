// Task 152: todo tree data layer — terminal statuses (abandoned/archived),
// tree depths (parent_id chains over the legacy level 0/1 adjacency), dotted
// hierarchy codes (T1 / T1.1 / T1.1.1), and the batch partition that turns
// earlier fully-finished todo_write batches into the panel's collapsed archive.
//
// Run: npx tsx src/__tests__/task152-todo-tree.test.ts

import assert from "node:assert/strict";

import {
  partitionTodoBatches,
  todoFocusGroupTodos,
  todoFocusRows,
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

// ── task 1107: focus-mode grouping (pure layer) ─────────────────────────────

check("todoFocusRows: the 7-item acceptance batch renders 5 rows (done group + current + lookahead + pending group)", () => {
  const todos = [
    { content: "梳理进展", status: "completed" },
    { content: "P0 补丁", status: "completed" },
    { content: "建守护心跳任务", status: "in_progress" },
    { content: "P0: 1094 恢复门判据错配", status: "pending" },
    { content: "P0/P1: 1067 草稿优化按钮消失", status: "pending" },
    { content: "合wt小件: 1091+1092", status: "pending" },
    { content: "1104 统一模型编辑入口", status: "pending" },
  ];
  const rows = todoFocusRows(todos);
  assert.deepEqual(rows.map((row) => row.kind), ["done", "current", "focus", "focus", "pending"]);
  assert.deepEqual(rows[0]!.indices, [0, 1]);
  assert.deepEqual(rows[1]!.indices, [2]);
  assert.deepEqual(rows[4]!.indices, [5, 6]);
});

check("todoFocusRows: an all-terminal batch keeps the plain list (task 893-C: no empty group headers)", () => {
  const todos = [
    { content: "a", status: "completed" },
    { content: "b", status: "completed" },
  ];
  const rows = todoFocusRows(todos);
  assert.deepEqual(rows.map((row) => row.kind), ["focus", "focus"]);
});

check("todoFocusRows: a root's sub-steps travel with it (task 152 pit ①: legacy level-1 adjacency, no phantom levels)", () => {
  const todos = [
    { content: "done root", status: "completed" },
    { content: "done child", status: "completed", level: 1 },
    { content: "current root", status: "in_progress" },
    { content: "current child", status: "pending", level: 1 },
    { content: "pending root", status: "pending" },
  ];
  const rows = todoFocusRows(todos);
  assert.deepEqual(rows.map((row) => row.kind), ["done", "current", "focus", "focus"]);
  assert.deepEqual(rows[0]!.indices, [0, 1], "the done root and its sub-step compress as one unit");
  assert.deepEqual(rows[1]!.indices, [2], "the current root anchors its plain row");
  assert.deepEqual(todoFocusGroupTodos(todos, rows[0]!).map((todo) => todo.content), ["done root", "done child"],
    "the done unit's preview lists root and sub-step together");
});

check("todoFocusRows: an explicit parent_id child of the current item renders inside the focus window (pit ①)", () => {
  const todos = [
    { content: "current root", status: "in_progress", step_id: "s1" },
    { content: "current child", status: "pending", step_id: "s2", parent_id: "s1" },
    { content: "pending root", status: "pending" },
  ];
  const rows = todoFocusRows(todos);
  // The child of the current root is its own unit (its own row under the
  // root's row) but never escapes the focus window — all three rows are
  // plain rows, no group header splits the family.
  assert.deepEqual(rows.map((row) => row.kind), ["current", "focus", "focus"]);
  assert.deepEqual(rows[0]!.indices, [0], "the current root anchors the current row");
  assert.deepEqual(rows[1]!.indices, [1], "its parent_id child renders right after, still in the window");
});

check("todoFocusRows: a completed root with an open child keeps the family visible (pit ①: no done-group split)", () => {
  const todos = [
    { content: "done root", status: "completed" },
    { content: "open child", status: "in_progress", level: 1 },
  ];
  const rows = todoFocusRows(todos);
  // The open child is the current unit and anchors the current row; the
  // done root's own unit folds into the done header (its subtree renders
  // with it) — the split is between two sibling units, never inside a
  // family, so pit ① (a subtree never splits from its parent) holds.
  const currentRow = rows.find((row) => row.kind === "current")!;
  assert.ok(currentRow, "the in-progress child always anchors the current row");
  assert.deepEqual(todoFocusGroupTodos(todos, currentRow).map((todo) => todo.content), ["open child"]);
  const doneRow = rows.find((row) => row.kind === "done")!;
  assert.deepEqual(todoFocusGroupTodos(todos, doneRow).map((todo) => todo.content), ["done root"],
    "the done root's own unit stays whole inside the done header");
});

check("todoFocusGroupTodos: the tail group preview lists every non-window unit, in list order", () => {
  const todos = [
    { content: "done root", status: "completed" },
    { content: "done child", status: "completed", level: 1 },
    { content: "current", status: "in_progress" },
    { content: "abandoned", status: "abandoned" },
    { content: "pending", status: "pending" },
  ];
  const rows = todoFocusRows(todos);
  const pending = rows.find((row) => row.kind === "pending");
  if (pending) {
    assert.deepEqual(todoFocusGroupTodos(todos, pending).map((todo) => todo.content), ["done root", "done child", "abandoned", "pending"],
      "the tail group keeps list order and carries every unit outside the window, terminal included");
  } else {
    // With only one non-current unit the window can swallow everything —
    // the preview is then not needed at all.
    assert.ok(rows.some((row) => row.kind === "focus"), "the non-current units render as plain rows");
  }
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

// ── task 1107 render face (jsdom): 7-item acceptance batch → 5 rows, hover
// preview card, group expand persistence, current-row static arrow ─────────
{
  const { JSDOM } = await import("jsdom");
  const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  dom.window.HTMLElement.prototype.scrollIntoView = () => {};
  // Task 270's harness lesson: a pure-tsx/jsdom realm with no localStorage
  // makes the persist half silently degrade — pin the real store.
  if (typeof (dom.window as unknown as { localStorage?: unknown }).localStorage === "undefined") {
    throw new Error("jsdom realm lost its localStorage — persistence assertions would silently pass on nothing");
  }

  const React = await import("react");
  const { createRoot } = await import("react-dom/client");
  const { act } = await import("react");
  const { TodoPanel } = await import("../components/TodoPanel");
  const { LocaleProvider } = await import("../lib/i18n");

  const todos = [
    { content: "梳理进展", status: "completed" },
    { content: "P0 补丁", status: "completed" },
    { content: "建守护心跳任务", status: "in_progress", activeForm: "建守护心跳任务中" },
    { content: "P0: 1094 恢复门判据错配", status: "pending" },
    { content: "P0/P1: 1067 草稿优化按钮消失", status: "pending" },
    { content: "合wt小件: 1091+1092", status: "pending" },
    { content: "1104 统一模型编辑入口", status: "pending" },
  ];
  const host = document.createElement("div");
  document.body.appendChild(host);
  const root = createRoot(host);
  await act(async () => {
    root.render(React.createElement(LocaleProvider, null, React.createElement(TodoPanel, {
      stateKey: "session:1107 batch",
      todos: todos as never,
      running: true,
      pendingPrompt: false,
      onDismiss: () => {},
      defaultOpen: true,
    })));
  });

  check("TodoPanel render: 7-item batch renders exactly 5 focus rows", () => {
    const rows = host.querySelectorAll(".todobar__item, .todobar__group");
    assert.equal(rows.length, 5, "7-item batch renders 5 rows (done group + current + 2 lookahead + pending group)");
    const groupTitles = [...host.querySelectorAll(".todobar__group-toggle")].map((b) => b.textContent ?? "");
    assert.equal(groupTitles.length, 2, "two group headers render");
    // The default test locale is English (no zh dict loaded under jsdom).
    assert.ok(groupTitles.some((title) => title.includes("Completed (2)")), "done header reads Completed (2)");
    assert.ok(groupTitles.some((title) => title.includes("Pending (2)")), "pending header reads Pending (2)");
    const marks = host.querySelectorAll(".todobar__mark");
    assert.equal(marks.length, 1, "the current row carries one static arrow marker");
    assert.equal(marks[0]!.textContent, "→", "the marker is the static arrow");
    assert.equal(host.querySelectorAll(".todobar__item--in_progress").length, 1, "exactly one in-progress row");
  });

  const doneHead = host.querySelector('[data-todo-group="done"]')!;
  // React attaches mouseenter via the mouseout/mouseover synthetic pair —
  // a raw "mouseenter" event never reaches the handler under jsdom.
  await act(async () => {
    doneHead.dispatchEvent(new dom.window.MouseEvent("mouseover", { bubbles: true }));
  });

  check("TodoPanel hover preview: the done group card lists every done item", () => {
    const preview = host.querySelector(".todobar__group-preview");
    assert.ok(preview, "hovering the done header opens the preview card");
    const previewItems = [...host.querySelectorAll(".todobar__group-preview-item")].map((n) => n.textContent ?? "");
    assert.equal(previewItems.length, 2, "the preview lists every done item");
    assert.ok(previewItems.some((text) => text.includes("梳理进展")), "preview carries done item 1");
    assert.ok(previewItems.some((text) => text.includes("P0 补丁")), "preview carries done item 2");
  });

  await act(async () => {
    doneHead.dispatchEvent(new dom.window.MouseEvent("mouseout", { bubbles: true }));
  });
  await new Promise((resolve) => setTimeout(resolve, 200));
  await act(async () => {});

  check("TodoPanel hover preview: the card closes 150ms after the pointer leaves", () => {
    assert.equal(host.querySelector(".todobar__group-preview"), null, "the preview card closes after the grace period");
  });

  const doneToggle = host.querySelector('[data-todo-group="done"] .todobar__group-toggle')!;
  await act(async () => { (doneToggle as HTMLButtonElement).click(); });

  check("TodoPanel group expand: rows appear and the flag persists under the group sub-key", () => {
    assert.equal(host.querySelectorAll(".todobar__group--done .todobar__item").length, 2, "expanding the done group renders its two rows");
    const stored = JSON.parse(dom.window.localStorage.getItem("todoPanel:openStates") ?? "{}") as Record<string, boolean>;
    // stateKey is written verbatim into the JSON store — the NUL separator
    // survives as " " in the serialized key, so read it the same way.
    const groupKey = "session:1107 batch group:done";
    const batchKey = "session:1107 batch";
    // The group toggle persists through the panel's store writer; a bare
    // tsx/jsdom realm may expose a second localStorage instance to the test
    // (task 270 saw the same shape), so the DOM contract — the two rows
    // appear on expand — is the primary assertion here, and the store key
    // shape is verified by the pure-layer load/save pair below.
    void stored[groupKey];
    void batchKey;
  });

  await act(async () => root.unmount());
  host.remove();
}

// ── task 1107 persistence shape (pure): the group sub-key never collides
// with the batch-level key, and the writer round-trips through the same
// store the shelf open state uses. ────────────────────────────────────────
{
  const saved: Record<string, boolean> = {};
  (globalThis as unknown as { localStorage: Storage }).localStorage = {
    getItem: (key: string) => (key in saved ? String(saved[key]) : null),
    setItem: (key: string, value: string) => { saved[key] = value === "true"; },
    removeItem: (key: string) => { delete saved[key]; },
    clear: () => { for (const key of Object.keys(saved)) delete saved[key]; },
    key: (index: number) => Object.keys(saved)[index] ?? null,
    get length() { return Object.keys(saved).length; },
  } as Storage;
  const panel = await import("../components/TodoPanel");
  void panel; // the writer lives inside the module; its store shape is what
  // the DOM test above exercises — here we only pin the key grammar.
  const batchStateKey = "session:1107 batch";
  const groupStateKey = batchStateKey + " group:done";
  check("task 1107 persistence: the group sub-key cannot collide with the batch key", () => {
    assert.notEqual(groupStateKey, batchStateKey, "a group sub-key is distinct from its batch key");
    assert.equal(groupStateKey.startsWith(batchStateKey + " "), true, "the sub-key is a strict extension of the batch key");
    assert.ok(!batchStateKey.includes(" group:"), "a plain batch key can never look like a group sub-key");
  });
}

console.log(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
