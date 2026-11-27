import { useRef, useState } from "react";
import type { Translator } from "../lib/i18n";
import { todoBatchKey, todoDockEmptyState, todoTerminalStatus, type TodoDockEmptyItemSignal, type TodoBatch } from "../lib/todoVisibility";
import { TodoTree } from "./TodoPanel";
import type { Todo } from "../lib/tools";

/**
 * Task 893-B: the right-dock "todos" tab's empty area. Three states:
 *   - never  → the session never recorded a task list ("暂无任务" copy);
 *   - sealed → a list existed but the turn-level view was recycled
 *     (tl1353 copy: the previous list is saved and collapsed — it no longer
 *     reads as data loss; when the sealed list itself is derivable, a
 *     read-only expand/collapse reveals every item);
 *   - an active list renders TodoPanel instead and never reaches this component.
 * The state is derived purely in the frontend from transcript/meta the panel
 * already has (todo_write history, archive, dismissed batches, canonical
 * snapshot) — see todoDockEmptyState in lib/todoVisibility.ts. The
 * `data-todo-empty-state` attribute is the machine-readable state bit for
 * tests and future styling hooks.
 */
export function TodoDockEmptyState({
  t,
  todos,
  archive,
  items,
  dismissedBatches,
  canonicalTodos,
}: {
  t: Translator;
  todos?: readonly Todo[] | null;
  archive?: readonly TodoBatch[] | null;
  items?: readonly TodoDockEmptyItemSignal[] | null;
  dismissedBatches?: readonly string[] | null;
  canonicalTodos?: readonly Todo[] | null;
}) {
  // tl1353: collapsed by default — the sealed list is a record, not a live
  // panel; expanding it must stay possible but never cost vertical space the
  // user did not ask for.
  const [sealedExpanded, setSealedExpanded] = useState(false);
  const view = todoDockEmptyState({ todos, archive, items, dismissedBatches, canonicalTodos });
  // tl1353: the sealed snapshot itself, best source first — the canonical
  // snapshot IS the sealed live list (todo_read reads it back verbatim), then
  // the newest archived batch, then the resolved list. No source with items →
  // the plain single-copy shape (identical markup to the pre-tl1353 state).
  // tl1836: copied into a mutable array — the shared tree unit's pure helpers
  // (todoBatchKey / TodoTree) take Todo[], and the record never mutates it.
  const sealedTodos: Todo[] = view.state === "sealed"
    ? (canonicalTodos && canonicalTodos.length > 0
      ? [...canonicalTodos]
      : archive && archive[0] && archive[0].todos.length > 0
        ? [...archive[0].todos]
        : todos && todos.length > 0 ? [...todos] : [])
    : [];
  // tl1682: the header copy must never read as "the list is empty / purely
  // historical" while the folded snapshot still holds in-progress or pending
  // work — the exact misread from feedback-20261117-143801 (the current
  // turn's seven in-progress tasks folded into the "previous turn" copy).
  // The count rides the snapshot that actually renders on expand, so the
  // header and the revealed list always agree.
  const sealedActive = sealedTodos.filter((todo) => !todoTerminalStatus(todo.status)).length;
  // tl1836: the expanded record renders the SAME tree unit as the live panel
  // (TodoTree — hierarchy codes, status chips, focus grouping). The sealed
  // list is a record of a finished turn, so the runtime presentation flags
  // are off: an in-flight item shows the paused face, never a live "waiting"
  // state. The tree's per-group disclosure state keys off the snapshot's
  // batch key, so group open/closed follows the record content the same way
  // the live panel's follows the batch.
  const sealedTreeKey = `todo-sealed:${todoBatchKey(sealedTodos)}`;
  const sealedCurrentRef = useRef<HTMLLIElement | null>(null);
  const copy = view.state === "sealed"
    ? sealedTodos.length > 0
      ? sealedActive > 0
        ? t("rightDock.todoSealedActiveCount", { count: sealedTodos.length, active: sealedActive })
        : t("rightDock.todoSealedCount", { count: sealedTodos.length })
      : t(view.count !== null ? "rightDock.todoSealedCount" : "rightDock.todoSealed", view.count !== null ? { count: view.count } : undefined)
    : t("rightDock.todoEmpty");
  if (view.state !== "sealed" || sealedTodos.length === 0) {
    return (
      <div className="workbench-dock__todo workbench-dock__todo--empty" data-todo-empty-state={view.state}>
        {copy}
      </div>
    );
  }
  return (
    <div className="workbench-dock__todo workbench-dock__todo--empty" data-todo-empty-state={view.state}>
      <div className="workbench-dock__todo-sealed">
        <div className="workbench-dock__todo-sealed-copy">{copy}</div>
        <button
          type="button"
          className="workbench-dock__todo-sealed-toggle"
          aria-expanded={sealedExpanded}
          data-todo-sealed-toggle
          onClick={() => setSealedExpanded((value) => !value)}
        >
          <span aria-hidden="true">{sealedExpanded ? "▾" : "▸"}</span>{" "}
          {t("rightDock.todoSealedExpand", { count: sealedTodos.length })}
        </button>
        {sealedExpanded && (
          <div data-todo-sealed-list>
            <TodoTree
              stateKey={sealedTreeKey}
              todos={sealedTodos}
              running={false}
              pendingPrompt={false}
              currentRef={sealedCurrentRef}
            />
          </div>
        )}
      </div>
    </div>
  );
}

