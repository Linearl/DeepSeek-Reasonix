// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/todo-dock-empty-state.test.tsx
// Task 893-B: the right-dock "todos" tab's empty area must distinguish
// "never had tasks" from "tasks existed but the turn-level view was sealed",
// so a signed-off list recycled at a turn boundary stops reading as "the
// agent is tracking nothing" (user screenshot 2026-10-14: 「待办为空，确定立了？」).
//
// Contract under test:
// - todoDockEmptyState (pure): never when no signal exists; sealed once ANY
//   signal exists (live list > archive > canonical snapshot > sidecar
//   dismissed batches > transcript todo_write), with the count taken from the
//   best available source;
// - TodoDockEmptyState (render): three states → three copies, each carrying a
//   machine-readable `data-todo-empty-state` bit;
// - WorkspaceDockRegion: todos mode without a payload keeps the original
//   "never" copy byte-for-byte when no signals are passed (back-compat), and
//   renders the sealed copy once todoEmpty signals say the session had tasks;
// - the user's screenshot scenario: a signed-off (all-completed, dismissed)
//   3-item list must NOT render the "暂无任务" copy;
// - i18n: both new keys present in all three locales.

import { readFileSync } from "node:fs";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { JSDOM } from "jsdom";
import type { Todo } from "../lib/tools";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
dom.window.HTMLElement.prototype.scrollIntoView = () => {};

let passed = 0;
let failed = 0;
function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

const { todoDockEmptyState } = await import("../lib/todoVisibility");
const { TodoDockEmptyState } = await import("../components/TodoDockEmptyState");
const { WorkspaceDockRegion } = await import("../app-shell/WorkspaceDockRegion");
const { tFor } = await import("../lib/i18n");

const t = ((key: string) => key) as Parameters<typeof WorkspaceDockRegion>[0]["t"];
const noop = () => {};

const completed3: Todo[] = [
  { content: "Inspect the report", status: "completed" },
  { content: "Ship the fix", status: "completed" },
  { content: "Write the handoff", status: "completed" },
];

console.log("\ntodo dock empty state (893-B)");

// 1. Pure state machine: never vs sealed, and count sourcing.
{
  eq(todoDockEmptyState({}).state, "never", "pure: no signals → never");
  eq(todoDockEmptyState({ todos: [], archive: [], items: [], dismissedBatches: [], canonicalTodos: [] }).state, "never",
    "pure: all-empty arrays (authoritative clear) → never");

  eq(todoDockEmptyState({ todos: completed3 }).state, "sealed", "pure: live list → sealed");
  eq(todoDockEmptyState({ todos: completed3 }).count, 3, "pure: count from live list");

  eq(todoDockEmptyState({ archive: [{ key: "k", todos: completed3 }] }).state, "sealed", "pure: archive batch → sealed");
  eq(todoDockEmptyState({ archive: [{ key: "k", todos: completed3 }] }).count, 3, "pure: count from archive");

  eq(todoDockEmptyState({ canonicalTodos: completed3 }).state, "sealed", "pure: canonical snapshot → sealed");
  eq(todoDockEmptyState({ canonicalTodos: completed3 }).count, 3, "pure: count from canonical");

  eq(todoDockEmptyState({ dismissedBatches: ['[{"content":"Ship"}]'] }).state, "sealed",
    "pure: sidecar dismissed batch → sealed");
  eq(todoDockEmptyState({ dismissedBatches: ['[{"content":"Ship"}]'] }).count, null,
    "pure: dismissed batch carries no derivable count → null (generic sealed copy)");

  // The transcript signal: hydrated sessions keep todo_write args (host
  // historyToolCall); a round-recycled turn where canonicalTodos came back
  // [] still has the todo_write tool item in state.items.
  const transcriptTodoWrite = [{ kind: "tool", name: "todo_write", parentId: undefined, status: "done", error: undefined }];
  eq(todoDockEmptyState({ items: transcriptTodoWrite, canonicalTodos: [] }).state, "sealed",
    "pure: transcript todo_write + authoritative empty canonical → sealed (user screenshot path)");
  eq(todoDockEmptyState({ items: transcriptTodoWrite, canonicalTodos: [] }).count, null,
    "pure: no derivable count from transcript alone → null");

  const failedWrite = [{ kind: "tool", name: "todo_write", status: "error", error: "boom" }];
  eq(todoDockEmptyState({ items: failedWrite }).state, "never",
    "pure: failed todo_write is not evidence of a list");
  const subagentWrite = [{ kind: "tool", name: "todo_write", parentId: "parent-1", status: "done" }];
  eq(todoDockEmptyState({ items: subagentWrite }).state, "never",
    "pure: subagent (parentId) todo_write does not count for the main panel");
}

// 2. Render: three states → three copies + state bit.
{
  type EmptyStateProps = Omit<Parameters<typeof TodoDockEmptyState>[0], "t">;
  const mount = (props: EmptyStateProps, translator = t): string => {
    return renderToStaticMarkup(createElement(TodoDockEmptyState, { ...props, t: translator }));
  };

  const never = mount({ todos: [], items: [] });
  eq(never.includes("rightDock.todoEmpty"), true, "render never: original copy");
  eq(never.includes("data-todo-empty-state=\"never\""), true, "render never: state bit");

  const sealedCount = mount({ todos: completed3 });
  eq(sealedCount.includes("rightDock.todoSealedCount"), true, "render sealed+count: count copy");
  eq(sealedCount.includes("data-todo-empty-state=\"sealed\""), true, "render sealed+count: state bit");

  const sealedPlain = mount({ dismissedBatches: ["x"] });
  eq(sealedPlain.includes("rightDock.todoSealedCount"), false, "render sealed without count: no count copy");
  eq(sealedPlain.includes("rightDock.todoSealed"), true, "render sealed without count: generic copy");
  eq(sealedPlain.includes("data-todo-empty-state=\"sealed\""), true, "render sealed: state bit");

  // Real translator interpolation (en — zh/zh-TW dicts lazy-load in node, the
  // translate() fallback path still exercises {count} substitution): the count
  // variant must carry the number through {count}.
  const localized = mount({ todos: completed3 }, tFor("en"));
  eq(localized.includes("3"), true, "render: {count} interpolates to 3 under the real translator");
  eq(localized.includes("{count}"), false, "render: no leftover {count} placeholder");
  eq(localized.includes("sealed"), true, "render: en sealed copy is the real string, not the key");
}

// 3. WorkspaceDockRegion integration: back-compat default + sealed signals.
{
  const renderDock = (overrides: Partial<Parameters<typeof WorkspaceDockRegion>[0]> = {}): string => {
    const props = {
      visible: true, overlay: false, mode: "todos" as const,
      creation: false, remoteAvailable: true, showContext: true, todoSidebar: true,
      t, onMode: noop, onRemote: noop,
      remote: { onClose: noop },
      context: {},
      workspaceKey: "fixture",
      workspace: { open: true, maximized: false, onClose: noop, onToggleMaximized: noop },
      ...overrides,
    } as Parameters<typeof WorkspaceDockRegion>[0];
    return renderToStaticMarkup(createElement(WorkspaceDockRegion, props));
  };

  const legacy = renderDock();
  eq(legacy.includes("rightDock.todoEmpty"), true, "dock: no todoEmpty prop keeps the original never copy (back-compat)");
  eq(legacy.includes("data-todo-empty-state=\"never\""), true, "dock: default state bit is never");

  const sealed = renderDock({ todoEmpty: { todos: completed3 } });
  eq(sealed.includes("rightDock.todoSealedCount"), true, "dock: todoEmpty signals render the sealed copy");
  eq(sealed.includes("rightDock.todoEmpty"), false, "dock: sealed state does not show the misleading never copy");

  // State ③ (active tasks): with a live payload the dock renders TodoPanel
  // and never reaches the empty state — no empty-state copy, no state bit.
  const activeTodos: Todo[] = [
    { content: "Inspect the report", status: "completed" },
    { content: "Ship the fix", status: "in_progress", activeForm: "Shipping the fix" },
    { content: "Write the handoff", status: "pending" },
  ];
  const active = renderDock({
    todo: { identity: "probe-active", props: { stateKey: "probe-active", todos: activeTodos, running: false, pendingPrompt: false, onDismiss: noop } },
    todoEmpty: { todos: activeTodos },
  });
  eq(active.includes("rightDock.todoEmpty"), false, "active: no never copy while a live list renders");
  eq(active.includes("rightDock.todoSealed"), false, "active: no sealed copy while a live list renders");
  eq(active.includes("data-todo-empty-state"), false, "active: no empty-state bit while a live list renders");
}

// 4. The user's screenshot scenario: 3 items written, signed off (all
//    completed), next turn the panel is empty — must not read "暂无任务".
{
  const afterSignoff = todoDockEmptyState({
    todos: completed3,
    dismissedBatches: ['[{"content":"Inspect the report","level":0,"parent_id":""}]'],
    canonicalTodos: [],
    items: [
      { kind: "tool", name: "todo_write", status: "done" },
    ],
  });
  eq(afterSignoff.state, "sealed", "screenshot: signed-off session is sealed, not never");
  eq(afterSignoff.count, 3, "screenshot: the sealed copy can say 上一回合 3 项");
}

// 5. i18n: both new keys in all three locales.
{
  const locales = ["zh.ts", "zh-TW.ts", "en.ts"].map((name) =>
    readFileSync(new URL(`../locales/${name}`, import.meta.url), "utf8"));
  for (const key of ["rightDock.todoSealed", "rightDock.todoSealedCount"]) {
    eq(locales.every((source) => source.includes(`"${key}"`)), true, `locale key ${key} present in zh/zh-TW/en`);
  }
}

console.log(`\ntodo dock empty state: ${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
