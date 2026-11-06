// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/todo-dock-empty-state.test.tsx
// Task 893-B: the right-dock "todos" tab's empty area must distinguish
// "never had tasks" from "tasks existed but the turn-level view was sealed",
// so a signed-off list recycled at a turn boundary stops reading as "the
// agent is tracking nothing" (user screenshot 2026-10-14: 「待办为空，确定立了？」).
//
// tl1353: the sealed state no longer reads as data loss — the copy says the
// previous list is saved and collapsed (the canonical snapshot is intact),
// and when the sealed list itself is derivable a read-only expand/collapse
// reveals every item. Contract additions:
// - sealed + derivable items: collapsed by default, the toggle flips
//   aria-expanded and the read-only list renders/collapses on click;
// - sealed without derivable items: unchanged single-copy shape (no toggle);
// - never state and the active-list dock path: byte-for-byte unchanged
//   (default view zero change outside the sealed state);
// - i18n: all three locales carry the expand key and no longer say
//   「按回合封存」/ "was sealed when the turn ended".
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
// - i18n: the sealed/expand keys present in all three locales.

import { readFileSync } from "node:fs";
import { createElement } from "react";
import { act } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";
import type { Todo } from "../lib/tools";

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { url: "http://localhost/" });
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
  // tl1353: with derivable items the sealed state carries the read-only
  // expand affordance, collapsed by default (no list markup until clicked).
  eq(sealedCount.includes("data-todo-sealed-toggle"), true, "render sealed+items: expand toggle present");
  eq(sealedCount.includes("rightDock.todoSealedExpand"), true, "render sealed+items: toggle label key");
  eq(sealedCount.includes("aria-expanded=\"false\""), true, "render sealed+items: toggle collapsed by default");
  eq(sealedCount.includes("data-todo-sealed-list"), false, "render sealed+items: no list markup while collapsed");

  // tl1353: no derivable items → the pre-tl1353 single-copy shape, no toggle.
  const sealedPlain = mount({ dismissedBatches: ["x"] });
  eq(sealedPlain.includes("rightDock.todoSealedCount"), false, "render sealed without count: no count copy");
  eq(sealedPlain.includes("rightDock.todoSealed"), true, "render sealed without count: generic copy");
  eq(sealedPlain.includes("data-todo-empty-state=\"sealed\""), true, "render sealed: state bit");
  eq(sealedPlain.includes("data-todo-sealed-toggle"), false, "render sealed without items: no expand toggle");

  // tl1353: default view zero change — the never state carries no sealed
  // affordance at all.
  const neverFull = mount({ todos: [], items: [] });
  eq(neverFull.includes("data-todo-sealed-toggle"), false, "render never: no expand toggle (default view unchanged)");
  eq(neverFull.includes("data-todo-sealed-list"), false, "render never: no sealed list (default view unchanged)");

  // Real translator interpolation (en — zh/zh-TW dicts lazy-load in node, the
  // translate() fallback path still exercises {count} substitution): the count
  // variant must carry the number through {count}.
  const localized = mount({ todos: completed3 }, tFor("en"));
  eq(localized.includes("3"), true, "render: {count} interpolates to 3 under the real translator");
  eq(localized.includes("{count}"), false, "render: no leftover {count} placeholder");
  eq(localized.includes("saved and collapsed"), true, "render: en sealed copy is the softened tl1353 string, not the key");
  eq(localized.includes("was sealed when the turn ended"), false, "render: pre-tl1353 data-loss wording is gone");
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

// 5. tl1353 interactive: the sealed expand/collapse — click reveals the
// previous turn's items read-only, clicking again collapses; nothing in the
// toggle path mutates the list (the onClick only flips local state).
{
  const rootEl = document.getElementById("root")!;
  const root = createRoot(rootEl);
  type SealedProps = Omit<Parameters<typeof TodoDockEmptyState>[0], "t">;
  const sealedInput: SealedProps = { canonicalTodos: completed3 };
  await act(async () => {
    root.render(createElement(TodoDockEmptyState, { ...sealedInput, t: tFor("en") }));
  });
  const toggle = () => document.querySelector<HTMLElement>("[data-todo-sealed-toggle]")!;
  eq(Boolean(toggle()), true, "interactive sealed: toggle renders");
  eq(toggle().getAttribute("aria-expanded"), "false", "interactive sealed: collapsed initially");

  await act(async () => {
    toggle().click();
  });
  const list = document.querySelector<HTMLElement>("[data-todo-sealed-list]");
  eq(Boolean(list), true, "interactive sealed: expanded list renders on click");
  eq(toggle().getAttribute("aria-expanded"), "true", "interactive sealed: aria-expanded flips");
  eq(list!.querySelectorAll("li").length, 3, "interactive sealed: every previous-turn item listed");
  eq(list!.textContent!.includes("Ship the fix"), true, "interactive sealed: item content readable");
  // The expand is read-only: no interactive row controls (no buttons) inside.
  eq(list!.querySelectorAll("button").length, 0, "interactive sealed: expanded list has no mutating controls");

  await act(async () => {
    toggle().click();
  });
  eq(Boolean(document.querySelector("[data-todo-sealed-list]")), false, "interactive sealed: collapses back on second click");

  await act(async () => {
    root.unmount();
  });
  eq(rootEl.innerHTML, "", "interactive sealed: unmount cleans up");
}

// 6. i18n: the sealed/expand keys in all three locales, with the softened
// tl1353 wording replacing the data-loss phrasing.
{
  const locales = ["zh.ts", "zh-TW.ts", "en.ts"].map((name) =>
    readFileSync(new URL(`../locales/${name}`, import.meta.url), "utf8"));
  for (const key of ["rightDock.todoSealed", "rightDock.todoSealedCount", "rightDock.todoSealedExpand"]) {
    eq(locales.every((source) => source.includes(`"${key}"`)), true, `locale key ${key} present in zh/zh-TW/en`);
  }
  for (const [name, source] of locales.map((source, index) => [["zh", "zh-TW", "en"][index]!, source] as const)) {
    eq(source.includes("按回合封存"), false, `locale ${name}: 「按回合封存」 wording removed (tl1353)`);
    eq(/已收起保存|saved and collapsed/.test(source), true, `locale ${name}: softened saved-and-collapsed wording present`);
  }
}

console.log(`\ntodo dock empty state: ${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
