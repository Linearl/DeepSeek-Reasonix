import type { Translator } from "../lib/i18n";
import { todoDockEmptyState, type TodoDockEmptyItemSignal, type TodoBatch } from "../lib/todoVisibility";
import type { Todo } from "../lib/tools";

/**
 * Task 893-B: the right-dock "todos" tab's empty area. Three states:
 *   - never  → the session never recorded a task list ("暂无任务" copy);
 *   - sealed → a list existed but the turn-level view was recycled
 *     ("已按回合封存" copy, with the last item count when derivable);
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
  const view = todoDockEmptyState({ todos, archive, items, dismissedBatches, canonicalTodos });
  const copy = view.state === "sealed"
    ? t(view.count !== null ? "rightDock.todoSealedCount" : "rightDock.todoSealed", view.count !== null ? { count: view.count } : undefined)
    : t("rightDock.todoEmpty");
  return (
    <div className="workbench-dock__todo workbench-dock__todo--empty" data-todo-empty-state={view.state}>
      {copy}
    </div>
  );
}
