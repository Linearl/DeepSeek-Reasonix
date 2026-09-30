import { useEffect, useRef, useState, type RefObject } from "react";
import { useT } from "../lib/i18n";
import type { Todo } from "../lib/tools";
import {
  shouldOpenTodoPanelByDefault,
  todoHierarchyCodes,
  todoPresentationStatus,
  todoTerminalStatus,
  todoTreeDepths,
  type TodoBatch,
  type TodoPresentationStatus,
} from "../lib/todoVisibility";
import { PromptBadge, PromptHeaderAction, PromptShelf } from "./PromptShelf";

const STORAGE_KEY = "todoPanel:openStates";
const MAX_STORED_OPEN_STATES = 80;
const COMPLETION_HOLD_MS = 900;
const COMPLETION_FADE_MS = 240;

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

// TodoPanel is the live task list pinned just above the composer — the kernel's
// latest todo_write call drives it, and it updates in place as the agent flips
// items to in_progress / completed. Each new todo batch starts collapsed so the
// header can show live progress and the current task without occupying extra
// space. A batch that just reached completion briefly shows its final count,
// then leaves the composer shelf; the transcript tool call remains.
// Manual expand/collapse is restored only for the same batch.
// Task 152: the list renders as a tree (parent_id nesting with indent and
// per-parent collapse, dotted T1/T1.1 position codes) and optionally carries an
// archive of earlier fully-finished batches, collapsed by default.
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
  const [visible, setVisible] = useState(!allDone);

  useEffect(() => {
    if (!allDone) {
      setVisible(true);
      return;
    }
    if (!visible) return;

    saveOpenState(stateKey, false);
    setOpen(false);
    const dismissTimer = window.setTimeout(() => setVisible(false), COMPLETION_HOLD_MS + COMPLETION_FADE_MS);
    return () => {
      window.clearTimeout(dismissTimer);
    };
  }, [allDone, stateKey, visible]);

  useEffect(() => {
    if (!open) return;
    currentRef.current?.scrollIntoView({ block: "nearest" });
  }, [open, current?.content, current?.activeForm]);

  if (todos.length === 0 || !visible) return null;

  return (
    <PromptShelf
      className={allDone ? "todo-exit" : undefined}
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

// TodoTree renders one batch: depth-based indent, dotted hierarchy codes, and
// per-parent collapse.
function TodoTree({
  todos,
  running,
  pendingPrompt,
  currentRef,
}: {
  todos: Todo[];
  running: boolean;
  pendingPrompt: boolean;
  currentRef: RefObject<HTMLLIElement | null>;
}) {
  const t = useT();
  const depths = todoTreeDepths(todos);
  const codes = todoHierarchyCodes(todos);
  const [collapsed, setCollapsed] = useState<Set<number>>(() => new Set());
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
      {todos.map((todo, index) => {
        if (hidden.has(index)) return null;
        const sourceStatus = normalizeTodoStatus(todo.status);
        const status = todoPresentationStatus(sourceStatus, { running, pendingPrompt });
        const depth = depths[index];
        const childrenCount = childCountFor(depths, index);
        const isCollapsed = collapsed.has(index);
        return (
          <li
            key={index}
            ref={sourceStatus === "in_progress" ? currentRef : undefined}
            className={[
              "todobar__item",
              `todobar__item--${status}`,
              depth > 0 ? "todobar__item--sub" : "",
              depth > 1 ? "todobar__item--deep" : "",
            ].filter(Boolean).join(" ")}
          >
            <span className={`todobar__status todobar__status--${status}`}>
              {t(todoStatusLabelKey(status))}
            </span>
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
      })}
    </ul>
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
