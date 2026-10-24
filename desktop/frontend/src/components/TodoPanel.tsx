import { useEffect, useMemo, useRef, useState, type RefObject } from "react";
import { useT } from "../lib/i18n";
import type { Todo } from "../lib/tools";
import {
  shouldOpenTodoPanelByDefault,
  todoFocusGroupTodos,
  todoFocusRows,
  todoHierarchyCodes,
  todoPresentationStatus,
  todoTerminalStatus,
  todoTreeDepths,
  type TodoBatch,
  type TodoFocusRow,
  type TodoPresentationStatus,
} from "../lib/todoVisibility";
import { PromptBadge, PromptHeaderAction, PromptShelf } from "./PromptShelf";

const STORAGE_KEY = "todoPanel:openStates";
const MAX_STORED_OPEN_STATES = 80;
// Task 1107: hover preview dismisses after the pointer leaves the group
// header — the same grace period the transcript's queued-message preview
// (task 446) uses so the pointer can travel into the card.
const GROUP_PREVIEW_LEAVE_MS = 150;

function loadOpenStates(): Record<string, boolean> {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (!saved) return {};
    const parsed = JSON.parse(saved) as unknown;
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    const states: Record<string, boolean> = {};
    for (const [key, value] of Object.entries(parsed)) {
      if (typeof value === "boolean") states[key] = value;
    }
    return states;
  } catch {
    return {};
  }
}

function loadOpenState(stateKey: string, defaultOpen: boolean): boolean {
  const states = loadOpenStates();
  return Object.prototype.hasOwnProperty.call(states, stateKey) ? states[stateKey] : defaultOpen;
}

function saveOpenState(stateKey: string, open: boolean): void {
  try {
    const entries = Object.entries(loadOpenStates()).filter(([key]) => key !== stateKey);
    entries.push([stateKey, open]);
    const trimmed = entries.slice(-MAX_STORED_OPEN_STATES);
    localStorage.setItem(STORAGE_KEY, JSON.stringify(Object.fromEntries(trimmed)));
  } catch {
    /* ignore quota errors */
  }
}

// Task 1107: per-group expanded flags share the shelf's store under a
// `${stateKey}\0group:${kind}` sub-key — a batch stateKey is JSON text (no
// NUL byte), so the sub-key can never collide with a plain stateKey; the
// batch-level key keeps its own entry untouched.
function loadGroupOpen(stateKey: string, kind: string, defaultOpen: boolean): boolean {
  return loadOpenState(`${stateKey}\0group:${kind}`, defaultOpen);
}

function saveGroupOpen(stateKey: string, kind: string, open: boolean): void {
  saveOpenState(`${stateKey}\0group:${kind}`, open);
}

// TodoPanel is the live task list pinned just above the composer — the kernel's
// latest todo_write call drives it, and it updates in place as the agent flips
// items to in_progress / completed. Each new todo batch starts collapsed so the
// header can show live progress and the current task without occupying extra
// space. Task 893-C: a batch that reaches completion stays visible — the
// sign-off is not recycled within the turn; the shelf keeps its final count and
// a close action, and reclaim only happens when the user closes it (then the
// dock falls back to the sealed empty state). A batch restored already-all-done
// (session reload) still does not reattach; the transcript tool call remains.
// Manual expand/collapse is restored only for the same batch.
// Task 152: the list renders as a tree (parent_id nesting with indent and
// per-parent collapse, dotted T1/T1.1 position codes) and optionally carries an
// archive of earlier fully-finished batches, collapsed by default.
// Task 1107: the live tree renders in focus mode — terminal and pending items
// outside the focus window compress into collapsed group headers (done first,
// pending last) with a fixed-position hover preview card; expanding a group
// (persisted per stateKey) reveals the plain tree rows underneath. The
// current item and the lookahead window (current + next TODO_FOCUS_LOOKAHEAD
// pending roots) stay as plain rows; a subtree always travels with its parent
// root (task 152), and an all-terminal batch keeps the plain signed-off list
// (task 893-C) instead of two empty group headers. The current item's status
// marker is the static arrow — a todo row is long-lived page chrome, and a
// spinner there would read as a loading state.
export function TodoPanel({
  stateKey,
  todos,
  running,
  pendingPrompt,
  onContinue,
  onDismiss,
  defaultOpen,
  archive,
}: {
  stateKey: string;
  todos: Todo[];
  running: boolean;
  pendingPrompt: boolean;
  onContinue?: () => void;
  onDismiss: () => void;
  /**
   * Task 259: initial open state for a surface that exists to show the list
   * (the right-dock tab). Undefined keeps the composer-shelf behaviour —
   * collapsed by default — so the footer mode stays byte-for-byte identical.
   */
  defaultOpen?: boolean;
  /** Task 152: earlier fully-terminal batches, newest first. Undefined keeps the single-batch behaviour. */
  archive?: TodoBatch[];
}) {
  const t = useT();
  const currentRef = useRef<HTMLLIElement | null>(null);

  const done = todos.filter((t) => todoTerminalStatus(t.status)).length;
  const current = todos.find((t) => t.status === "in_progress");
  const allDone = todos.length > 0 && done === todos.length;
  const summary = current?.activeForm || current?.content || todos[todos.length - 1]?.content || "";
  const [open, setOpen] = useState(() => loadOpenState(stateKey, defaultOpen ?? shouldOpenTodoPanelByDefault()));
  // Task 893-C: only a batch restored already-all-done starts hidden (session
  // reload — the sealed empty state takes over). A live batch that completes
  // mid-session keeps its visible state: sign-off is not recycled in-turn.
  const [visible, setVisible] = useState(!allDone);

  useEffect(() => {
    if (!allDone) setVisible(true);
  }, [allDone]);

  useEffect(() => {
    if (!open) return;
    currentRef.current?.scrollIntoView({ block: "nearest" });
  }, [open, current?.content, current?.activeForm]);

  if (todos.length === 0 || !visible) return null;

  return (
    <PromptShelf
      titleId="todo-shelf-title"
      title={t("todo.title")}
      badges={<PromptBadge>{done}/{todos.length}</PromptBadge>}
      meta={summary}
      role="region"
      cardClassName="prompt-shelf--todo"
      cardCollapsible
      collapsed={!open}
      onToggleCollapse={() => setOpen((value) => {
        const next = !value;
        saveOpenState(stateKey, next);
        return next;
      })}
      headerActions={allDone ? (
        <PromptHeaderAction onClick={onDismiss}>
          {t("common.close")}
        </PromptHeaderAction>
      ) : current && !running && !pendingPrompt && onContinue ? (
        <PromptHeaderAction onClick={onContinue}>
          {t("todo.continue")}
        </PromptHeaderAction>
      ) : undefined}
    >
      {open && (
        <TodoTree
          stateKey={stateKey}
          todos={todos}
          running={running}
          pendingPrompt={pendingPrompt}
          currentRef={currentRef}
        />
      )}
      {open && archive && archive.length > 0 && (
        <TodoArchive batches={archive} />
      )}
    </PromptShelf>
  );
}

// TodoTree renders one batch: task 1107 focus-mode rows (group headers +
// plain tree rows) with depth-based indent, dotted hierarchy codes, and
// per-parent collapse.
function TodoTree({
  stateKey,
  todos,
  running,
  pendingPrompt,
  currentRef,
}: {
  stateKey: string;
  todos: Todo[];
  running: boolean;
  pendingPrompt: boolean;
  currentRef: RefObject<HTMLLIElement | null>;
}) {
  const depths = todoTreeDepths(todos);
  const codes = todoHierarchyCodes(todos);
  const rows = todoFocusRows(todos);
  const allDone = todos.every((todo) => todoTerminalStatus(todo.status));
  const [collapsed, setCollapsed] = useState<Set<number>>(() => new Set());
  const [openGroups, setOpenGroups] = useState(() => ({
    done: allDone ? false : loadGroupOpen(stateKey, "done", false),
    pending: allDone ? false : loadGroupOpen(stateKey, "pending", false),
  }));
  const toggleGroup = (kind: "done" | "pending", next: boolean) => {
    setOpenGroups((groups) => ({ ...groups, [kind]: next }));
    saveGroupOpen(stateKey, kind, next);
  };
  const hidden = new Set<number>();
  depths.forEach((_depth, index) => {
    for (let parent = parentIndexFor(depths, index); parent >= 0; parent = parentIndexFor(depths, parent)) {
      if (collapsed.has(parent)) {
        hidden.add(index);
        break;
      }
    }
  });

  return (
    <ul className="todobar__list">
      {rows.flatMap((row) => {
        if (row.kind !== "done" && row.kind !== "pending") {
          return row.indices
            .filter((index) => !hidden.has(index))
            .map((index) => (
              <TodoItemRow
                key={index}
                todo={todos[index]!}
                index={index}
                depths={depths}
                codes={codes}
                collapsed={collapsed}
                setCollapsed={setCollapsed}
                running={running}
                pendingPrompt={pendingPrompt}
                currentRef={currentRef}
              />
            ));
        }
        // A batch that is all-terminal keeps the plain signed-off list
        // (task 893-C); the pure layer never emits groups for it, and the
        // guard keeps an empty header impossible if shapes drift.
        if (allDone || row.indices.length === 0) return [];
        return [
          <TodoGroupRow
            key={row.kind}
            kind={row.kind}
            todos={todos}
            row={row}
            depths={depths}
            codes={codes}
            running={running}
            pendingPrompt={pendingPrompt}
            expanded={openGroups[row.kind as "done" | "pending"]}
            onToggle={(next) => toggleGroup(row.kind as "done" | "pending", next)}
          />,
        ];
      })}
    </ul>
  );
}

// TodoGroupRow is the collapsed group header: static caret, localized title
// with the item count, and the hover preview card listing every item the
// group stands for (subtrees included).
function TodoGroupRow({
  kind,
  todos,
  row,
  depths,
  codes,
  running,
  pendingPrompt,
  expanded,
  onToggle,
}: {
  kind: "done" | "pending";
  todos: Todo[];
  row: TodoFocusRow;
  depths: number[];
  codes: string[];
  running: boolean;
  pendingPrompt: boolean;
  expanded: boolean;
  onToggle: (open: boolean) => void;
}) {
  const t = useT();
  const groupTodos = useMemo(() => todoFocusGroupTodos(todos, row), [todos, row]);
  const headRef = useRef<HTMLDivElement | null>(null);
  const cardRef = useRef<HTMLDivElement | null>(null);
  const [preview, setPreview] = useState(false);
  const hideTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => () => {
    if (hideTimer.current !== null) clearTimeout(hideTimer.current);
  }, []);

  const showPreview = () => {
    if (hideTimer.current !== null) {
      clearTimeout(hideTimer.current);
      hideTimer.current = null;
    }
    // Task 1107 pit ②: the card is position:fixed (never appended to the
    // scrolling list) and the shelf pins to the right edge, so it expands
    // leftward with an 8px edge correction — measured from the header when
    // the environment lays out. A jsdom header reports an all-zero rect; an
    // all-zero rect would park the card at the viewport corner, so the
    // fallback keeps the bottom-left position the right-dock surface needs.
    const rect = headRef.current?.getBoundingClientRect();
    const measured = !!rect && rect.width > 0;
    const viewportW = typeof window !== "undefined" ? window.innerWidth : 1440;
    const viewportH = typeof window !== "undefined" ? window.innerHeight : 900;
    const width = Math.min(360, Math.max(120, viewportW * 0.4));
    const above = measured ? rect.top + rect.height / 2 > viewportH / 2 : true;
    const left = measured ? Math.max(8, rect.left - width - 8) : Math.max(8, viewportW - width - 16);
    const top = measured
      ? (above ? rect.top - 8 : rect.bottom + 8)
      : viewportH - 96;
    const card = cardRef.current;
    if (card) {
      card.style.left = `${left}px`;
      card.style.top = `${top}px`;
      card.style.width = `${width}px`;
      card.style.transform = above ? "translateY(-100%)" : "none";
    }
    setPreview(true);
  };
  const scheduleHide = () => {
    if (hideTimer.current !== null) clearTimeout(hideTimer.current);
    hideTimer.current = setTimeout(() => {
      hideTimer.current = null;
      setPreview(false);
    }, GROUP_PREVIEW_LEAVE_MS);
  };
  const cancelHide = () => {
    if (hideTimer.current !== null) {
      clearTimeout(hideTimer.current);
      hideTimer.current = null;
    }
  };
  const title = kind === "done"
    ? t("todo.doneGroup", { n: row.indices.length })
    : t("todo.pendingGroup", { n: row.indices.length });

  return (
    <li className={`todobar__group todobar__group--${kind}`}>
      <div
        className="todobar__group-head"
        ref={headRef}
        data-todo-group={kind}
        onMouseEnter={showPreview}
        onMouseLeave={scheduleHide}
      >
        <button
          type="button"
          className="todobar__group-toggle"
          aria-expanded={expanded}
          onClick={() => onToggle(!expanded)}
        >
          <span className="todobar__group-caret" aria-hidden="true">{expanded ? "▾" : "▸"}</span>
          {title}
        </button>
      </div>
      {preview && (
        <div
          className="todobar__group-preview"
          ref={cardRef}
          data-todo-group-preview={kind}
          onMouseEnter={cancelHide}
          onMouseLeave={scheduleHide}
        >
          <div className="todobar__group-preview-title">{title}</div>
          <ul className="todobar__group-preview-list">
            {groupTodos.map((todo, index) => {
              const status = normalizeTodoStatus(todo.status);
              return (
                <li key={index} className={`todobar__group-preview-item todobar__group-preview-item--${status}`}>
                  <span className={`todobar__group-preview-icon todobar__group-preview-icon--${status}`} aria-hidden="true">
                    {status === "completed" || status === "archived"
                      ? "✔"
                      : status === "abandoned"
                        ? "✕"
                        : "○"}
                  </span>
                  <span className="todobar__group-preview-text">{todo.content}</span>
                </li>
              );
            })}
          </ul>
        </div>
      )}
      {expanded && (
        <ul className="todobar__group-list">
          {row.indices.map((anchor) => (
            <SubtreeRows
              key={anchor}
              anchor={anchor}
              todos={todos}
              depths={depths}
              codes={codes}
              collapsed={new Set<number>()}
              setCollapsed={() => {}}
              running={running}
              pendingPrompt={pendingPrompt}
              currentRef={{ current: null }}
            />
          ))}
        </ul>
      )}
    </li>
  );
}

// SubtreeRows renders one group member's root and every still-visible
// descendant; per-parent collapse inside an expanded group is inert (the
// group is the disclosure unit here).
function SubtreeRows({
  anchor,
  todos,
  depths,
  codes,
  collapsed,
  setCollapsed,
  running,
  pendingPrompt,
  currentRef,
}: {
  anchor: number;
  todos: Todo[];
  depths: number[];
  codes: string[];
  collapsed: Set<number>;
  setCollapsed: (updater: (current: Set<number>) => Set<number>) => void;
  running: boolean;
  pendingPrompt: boolean;
  currentRef: RefObject<HTMLLIElement | null>;
}) {
  const indices: number[] = [anchor];
  for (let index = anchor + 1; index < depths.length && depths[index]! > depths[anchor]!; index++) {
    indices.push(index);
  }
  return (
    <>
      {indices.map((index) => (
        <TodoItemRow
          key={index}
          todo={todos[index]!}
          index={index}
          depths={depths}
          codes={codes}
          collapsed={collapsed}
          setCollapsed={setCollapsed}
          running={running}
          pendingPrompt={pendingPrompt}
          currentRef={currentRef}
        />
      ))}
    </>
  );
}

// TodoItemRow is the plain tree row (indent + status chip/marker + code +
// text).
function TodoItemRow({
  todo,
  index,
  depths,
  codes,
  collapsed,
  setCollapsed,
  running,
  pendingPrompt,
  currentRef,
}: {
  todo: Todo;
  index: number;
  depths: number[];
  codes: string[];
  collapsed: Set<number>;
  setCollapsed: (updater: (current: Set<number>) => Set<number>) => void;
  running: boolean;
  pendingPrompt: boolean;
  currentRef: RefObject<HTMLLIElement | null>;
}) {
  const t = useT();
  const sourceStatus = normalizeTodoStatus(todo.status);
  const status = todoPresentationStatus(sourceStatus, { running, pendingPrompt });
  const depth = depths[index] ?? 0;
  const childrenCount = childCountFor(depths, index);
  const isCollapsed = collapsed.has(index);
  return (
    <li
      ref={sourceStatus === "in_progress" ? currentRef : undefined}
      className={[
        "todobar__item",
        `todobar__item--${status}`,
        depth > 0 ? "todobar__item--sub" : "",
        depth > 1 ? "todobar__item--deep" : "",
      ].filter(Boolean).join(" ")}
    >
      {sourceStatus === "in_progress" ? (
        // Task 1107 (ZCode 3.16.2 presentation note): the current item is
        // long-lived page chrome — a static arrow, never a spinner, so it
        // cannot be misread as a loading state.
        <span className={`todobar__mark todobar__mark--${status}`} aria-hidden="true">→</span>
      ) : (
        <span className={`todobar__status todobar__status--${status}`}>
          {t(todoStatusLabelKey(status))}
        </span>
      )}
      <span className="todobar__text">
        {childrenCount > 0 && (
          <button
            type="button"
            className="todobar__caret"
            aria-expanded={!isCollapsed}
            aria-label={`${isCollapsed ? t("todo.expand") : t("todo.collapse")} ${codes[index]}`}
            onClick={() => setCollapsed((current) => {
              const next = new Set(current);
              if (next.has(index)) next.delete(index);
              else next.add(index);
              return next;
            })}
          >
            {isCollapsed ? "▸" : "▾"} {childrenCount}
          </button>
        )}
        <span className="todobar__code">{codes[index]}</span>{" "}
        {sourceStatus === "in_progress" && todo.activeForm ? todo.activeForm : todo.content}
      </span>
    </li>
  );
}

// TodoArchive is the collapsed record of earlier fully-finished batches (task
// 152): completing no longer means vanishing — the history stays reachable
// without putting it back on the live spine.
function TodoArchive({ batches }: { batches: TodoBatch[] }) {
  const t = useT();
  const [openArchive, setOpenArchive] = useState(false);
  return (
    <div className="todobar__archive">
      <button
        type="button"
        className="todobar__archive-toggle"
        aria-expanded={openArchive}
        onClick={() => setOpenArchive((value) => !value)}
      >
        {openArchive ? "▾" : "▸"} {t("todo.archiveToggle", { n: batches.length })}
      </button>
      {openArchive && (
        <ul className="todobar__archive-list">
          {batches.map((batch) => (
            <TodoArchiveBatch key={batch.key} batch={batch} />
          ))}
        </ul>
      )}
    </div>
  );
}

function TodoArchiveBatch({ batch }: { batch: TodoBatch }) {
  const done = batch.todos.filter((todo) => todoTerminalStatus(todo.status)).length;
  return (
    <li className="todobar__archive-batch">
      <span className="todobar__archive-batch-title">{done}/{batch.todos.length}</span>
      <ul className="todobar__archive-items">
        {batch.todos.map((todo, index) => (
          <li key={index} className={`todobar__archive-item todobar__archive-item--${normalizeTodoStatus(todo.status)}`}>
            {todo.content}
          </li>
        ))}
      </ul>
    </li>
  );
}

function parentIndexFor(depths: readonly number[], index: number): number {
  const depth = depths[index];
  for (let i = index - 1; i >= 0; i--) {
    if (depths[i] < depth) return i;
  }
  return -1;
}

function childCountFor(depths: readonly number[], index: number): number {
  const depth = depths[index];
  let count = 0;
  for (let i = index + 1; i < depths.length && depths[i] > depth; i++) {
    if (depths[i] === depth + 1) count++;
  }
  return count;
}

function normalizeTodoStatus(status: Todo["status"]): "pending" | "in_progress" | "completed" | "abandoned" | "archived" {
  switch (String(status ?? "").trim()) {
    case "completed":
      return "completed";
    case "in_progress":
      return "in_progress";
    case "abandoned":
      return "abandoned";
    case "archived":
      return "archived";
    default:
      return "pending";
  }
}

function todoStatusLabelKey(status: TodoPresentationStatus): "todo.pending" | "todo.inProgress" | "status.runtimePendingPrompt" | "todo.paused" | "todo.completed" | "todo.abandoned" | "todo.archived" {
  switch (status) {
    case "completed":
      return "todo.completed";
    case "in_progress":
      return "todo.inProgress";
    case "abandoned":
      return "todo.abandoned";
    case "archived":
      return "todo.archived";
    case "waiting":
      return "status.runtimePendingPrompt";
    case "paused":
      return "todo.paused";
    default:
      return "todo.pending";
  }
}
