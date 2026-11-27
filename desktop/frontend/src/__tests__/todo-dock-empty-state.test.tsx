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
// - i18n: the sealed/expand keys present in all three locales;
// - tl1836: the expanded sealed record renders the SAME tree unit as the live
//   panel (TodoTree) — hierarchy T-codes, status chips, focus-group folding
//   with per-group counts; the old glyph+text bare list is retired (user
//   screenshots 20261127: the previous-turn expand showed plain circles, the
//   current turn showed the full badge/code/group presentation).

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
// tl1836: the shared tree unit persists group disclosure state per batch key —
// wire the jsdom storage in so the interactive sections exercise the real
// load/save path (each section clears it for a deterministic default).
globalThis.localStorage = dom.window.localStorage;
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
const { tFor, LocaleProvider } = await import("../lib/i18n");

const t = ((key: string) => key) as Parameters<typeof WorkspaceDockRegion>[0]["t"];
const noop = () => {};

const completed3: Todo[] = [
  { content: "Inspect the report", status: "completed" },
  { content: "Ship the fix", status: "completed" },
  { content: "Write the handoff", status: "completed" },
];

// tl1682/tl1836 shared fixture: a sealed snapshot that still holds work —
// 5 completed, 1 in-flight, 1 pending (the feedback-20261117-143801 shape).
const mixed7: Todo[] = [
  { content: "Inspect the report", status: "completed" },
  { content: "Ship the fix", status: "completed" },
  { content: "Write the handoff", status: "completed" },
  { content: "Update the docs", status: "completed" },
  { content: "Notify the reviewer", status: "completed" },
  { content: "Append the new task", status: "in_progress", activeForm: "Appending the new task" },
  { content: "Verify the panel", status: "pending" },
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
// tl1836: the revealed list is now the shared TodoTree unit (LocaleProvider
// wraps the mount because TodoTree resolves labels through useT). An
// all-terminal snapshot keeps the plain signed-off face (task 893-C parity
// with the live panel): every item as a row, no group headers, and — for a
// flat batch — no disclosure buttons at all.
{
  localStorage.clear();
  const rootEl = document.getElementById("root")!;
  const root = createRoot(rootEl);
  type SealedProps = Omit<Parameters<typeof TodoDockEmptyState>[0], "t">;
  const sealedInput: SealedProps = { canonicalTodos: completed3 };
  await act(async () => {
    root.render(
      createElement(LocaleProvider, null, createElement(TodoDockEmptyState, { ...sealedInput, t: tFor("en") })),
    );
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
  // All-terminal + flat → the plain signed-off face: shared tree unit, no
  // groups, and no disclosure controls inside (still a read-only record).
  eq(Boolean(list!.querySelector(".todobar__list")), true, "interactive sealed: renders the shared todobar tree unit");
  eq(Boolean(list!.querySelector(".todobar__group")), false, "interactive sealed: all-terminal batch keeps the plain face (no group headers)");
  eq(list!.querySelectorAll("button").length, 0, "interactive sealed: flat all-terminal list has no mutating controls");

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

// 7. tl1682 (feedback-20261117-143801): the folded snapshot still holding
// in-progress/pending work must not read as the "previous turn" history —
// the header names the in-progress count instead, and the counts always
// agree with the list the expand reveals.
{
  type EmptyStateProps = Omit<Parameters<typeof TodoDockEmptyState>[0], "t">;
  const mount = (props: EmptyStateProps, translator = t): string => {
    return renderToStaticMarkup(createElement(TodoDockEmptyState, { ...props, t: translator }));
  };

  const sealedActive = mount({ canonicalTodos: mixed7 });
  eq(sealedActive.includes("rightDock.todoSealedActiveCount"), true, "tl1682: snapshot with active work uses the active-count copy");
  eq(sealedActive.includes("rightDock.todoSealedCount"), false, "tl1682: active work never gets the previous-turn-only copy");
  eq(sealedActive.includes("data-todo-sealed-toggle"), true, "tl1682: expand affordance stays available");
  eq(sealedActive.includes("data-todo-empty-state=\"sealed\""), true, "tl1682: state bit stays sealed");

  // Real translator: both numbers ride through ({count} then {active}).
  const localizedActive = mount({ canonicalTodos: mixed7 }, tFor("en"));
  eq(localizedActive.includes("7"), true, "tl1682: {count} interpolates to 7");
  eq(localizedActive.includes("2"), true, "tl1682: {active} interpolates to 2");
  eq(localizedActive.includes("in progress"), true, "tl1682: en copy names the in-progress count");
  eq(localizedActive.includes("{active}"), false, "tl1682: no leftover {active} placeholder");
  // The header copy itself drops the previous-turn claim (the pre-existing
  // expand toggle label keeps its own wording — out of scope here).
  eq(localizedActive.includes("The previous turn's task list"), false, "tl1682: active-work header drops the previous-turn claim");

  // All-terminal snapshot keeps the softened tl1353 count copy.
  const sealedDone = mount({ todos: completed3 });
  eq(sealedDone.includes("rightDock.todoSealedCount"), true, "tl1682: all-terminal snapshot keeps the count copy");
  eq(sealedDone.includes("rightDock.todoSealedActiveCount"), false, "tl1682: all-terminal snapshot never claims active work");

  // No derivable snapshot keeps the generic sealed copy (pre-tl1353 shape).
  const sealedGeneric = mount({ dismissedBatches: ["x"] });
  eq(sealedGeneric.includes("rightDock.todoSealed"), true, "tl1682: no snapshot keeps the generic sealed copy");
  eq(sealedGeneric.includes("rightDock.todoSealedActiveCount"), false, "tl1682: no snapshot never claims active work");

  // The seven-item copy agrees with the expandable list (header ↔ reveal).
  // tl1836: the reveal is the shared TodoTree unit, so the snapshot renders
  // in the focus-mode presentation — the 5 completed items fold into the
  // done group header (counted), the in-flight item and the lookahead
  // pending item stay as plain rows. The header↔reveal agreement is the
  // accounting 5 (group) + 2 (plain rows) = 7, proven by expanding the group.
  localStorage.clear();
  const rootEl2 = document.getElementById("root")!;
  const root2 = createRoot(rootEl2);
  await act(async () => {
    root2.render(
      createElement(LocaleProvider, null, createElement(TodoDockEmptyState, { canonicalTodos: mixed7, t: tFor("en") })),
    );
  });
  await act(async () => {
    document.querySelector<HTMLElement>("[data-todo-sealed-toggle]")!.click();
  });
  const revealed = document.querySelector<HTMLElement>("[data-todo-sealed-list]")!;
  const doneGroup = revealed.querySelector<HTMLElement>('[data-todo-group="done"]')!;
  eq(Boolean(doneGroup), true, "tl1682: revealed tree folds the completed items into a done group");
  eq(doneGroup.querySelector<HTMLElement>(".todobar__group-toggle")!.textContent!.includes("5"), true,
    "tl1682: done group header counts its 5 items");
  eq(revealed.querySelectorAll(".todobar__item").length, 2, "tl1682: current + lookahead items stay as plain rows");
  await act(async () => {
    doneGroup.querySelector<HTMLElement>(".todobar__group-toggle")!.click();
  });
  eq(revealed.querySelectorAll(".todobar__item").length, 7,
    "tl1682: expanding the group reveals all 7 items the header counts (5 + 2)");
  await act(async () => {
    root2.unmount();
  });
}

// 8. tl1836 (user screenshots 20261127): the expanded sealed record renders
// the SAME presentation as the live panel — T-codes, status chips, group
// folding. The old glyph+text bare list is retired; the default collapsed
// behaviour (tl1353) is untouched.
{
  localStorage.clear();
  const rootEl3 = document.getElementById("root")!;
  const root3 = createRoot(rootEl3);
  await act(async () => {
    root3.render(
      createElement(LocaleProvider, null, createElement(TodoDockEmptyState, { canonicalTodos: mixed7, t: tFor("en") })),
    );
  });
  await act(async () => {
    document.querySelector<HTMLElement>("[data-todo-sealed-toggle]")!.click();
  });
  const revealed = document.querySelector<HTMLElement>("[data-todo-sealed-list]")!;

  // Pin ①: the shared tree unit replaced the bespoke bare list.
  eq(Boolean(revealed.querySelector(".todobar__list")), true, "tl1836: expanded record renders the live panel's todobar tree unit");
  eq(revealed.querySelectorAll(".workbench-dock__todo-sealed-item").length, 0, "tl1836: the old glyph+text sealed rows are gone");

  // Pin ② (分组折叠): the done group renders collapsed, its toggle flips
  // aria-expanded and reveals the folded rows.
  const doneGroup = revealed.querySelector<HTMLElement>('[data-todo-group="done"]')!;
  eq(doneGroup.querySelector<HTMLElement>(".todobar__group-toggle")!.getAttribute("aria-expanded"), "false",
    "tl1836: done group starts collapsed");
  await act(async () => {
    doneGroup.querySelector<HTMLElement>(".todobar__group-toggle")!.click();
  });
  eq(doneGroup.querySelector<HTMLElement>(".todobar__group-toggle")!.getAttribute("aria-expanded"), "true",
    "tl1836: done group toggle flips aria-expanded");
  eq(revealed.querySelectorAll(".todobar__group-list .todobar__item").length, 5,
    "tl1836: expanding the done group reveals its 5 folded rows");

  // Pin ③ (编号): dotted T-codes ride the same todoHierarchyCodes pass.
  const codes = Array.from(revealed.querySelectorAll<HTMLElement>(".todobar__code")).map((el) => el.textContent);
  for (let n = 1; n <= 7; n++) {
    eq(codes.includes(`T${n}`), true, `tl1836: hierarchy code T${n} present in the sealed record`);
  }

  // Pin ④ (徽章): status chips on the record rows; the item that was
  // in-flight when the turn ended shows the paused arrow (record face — the
  // turn is over, never a live waiting state).
  eq(revealed.querySelectorAll(".todobar__status--completed").length, 5, "tl1836: completed rows carry the completed status chip");
  eq(revealed.querySelectorAll(".todobar__status--pending").length, 1, "tl1836: pending row carries the pending status chip");
  const marks = revealed.querySelectorAll<HTMLElement>(".todobar__mark");
  eq(marks.length, 1, "tl1836: the in-flight item renders the arrow mark");
  eq(marks[0]!.getAttribute("class"), "todobar__mark todobar__mark--paused", "tl1836: sealed in-flight arrow uses the paused face (not live running/waiting)");

  // Read-only record guard: every control inside the reveal is a disclosure
  // button (group toggle / subtree caret) — nothing mutates the list data.
  const controls = Array.from(revealed.querySelectorAll("button, input, [role='checkbox']"));
  eq(controls.every((el) => el.tagName === "BUTTON" && /todobar__group-toggle|todobar__caret/.test(el.className)), true,
    "tl1836: the only controls in the sealed record are disclosure buttons");

  await act(async () => {
    root3.unmount();
  });
}

console.log(`\ntodo dock empty state: ${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
