import type { Todo } from "./tools";

export interface TodoPanelScopeInput {
  activeTabId?: string | null;
  activeTab?: {
    id?: string | null;
    scope?: string | null;
    workspaceRoot?: string | null;
    topicId?: string | null;
    sessionPath?: string | null;
  } | null;
  eventChannel?: string | null;
}

export type TodoPresentationStatus =
  | "pending"
  | "in_progress"
  | "waiting"
  | "paused"
  | "completed"
  | "abandoned"
  | "archived";

export interface TodoRuntimePresentation {
  running: boolean;
  pendingPrompt: boolean;
}

// Task 152: terminal todo statuses. "abandoned" marks work the agent gave up;
// "archived" marks completed work kept for the record. Terminal items are
// converged — they neither hold the panel open nor block a parent's sign-off.
export function todoTerminalStatus(status: Todo["status"]): boolean {
  switch (todoStatus(status)) {
    case "completed":
    case "abandoned":
    case "archived":
      return true;
    default:
      return false;
  }
}

export function todoPresentationStatus(
  status: Todo["status"],
  runtime: TodoRuntimePresentation,
): TodoPresentationStatus {
  switch (todoStatus(status)) {
    case "completed":
      return "completed";
    case "abandoned":
      return "abandoned";
    case "archived":
      return "archived";
    case "in_progress":
      if (runtime.pendingPrompt) return "waiting";
      return runtime.running ? "in_progress" : "paused";
    default:
      return "pending";
  }
}

// todoTreeDepths computes each item's nesting depth (0 = root). An explicit
// parent_id chain wins; the legacy level 0/1 adjacency applies otherwise — a
// level-1 item belongs to the nearest preceding level-0 item, exactly like the
// Go segment machine reads it. Dangling parent_id references fall back to the
// flat position so a malformed batch still renders.
export function todoTreeDepths(todos: Todo[]): number[] {
  const depths = new Array<number>(todos.length).fill(0);
  const indexOfStepID = new Map<string, number>();
  todos.forEach((todo, index) => {
    const id = String(todo.step_id ?? "").trim();
    if (id) indexOfStepID.set(id, index);
  });
  let lastRoot = -1;
  todos.forEach((todo, index) => {
    const parentID = String(todo.parent_id ?? "").trim();
    if (parentID) {
      const parent = indexOfStepID.get(parentID);
      if (parent !== undefined && parent < index) {
        depths[index] = depths[parent] + 1;
      }
    } else if (Number(todo.level) === 1 && lastRoot >= 0) {
      depths[index] = depths[lastRoot] + 1;
    }
    if (Number(todo.level) === 0 && depths[index] === 0 && !parentID) {
      lastRoot = index;
    }
  });
  return depths;
}

// todoHierarchyCodes renders the dotted position code of every item (task 152):
// roots count T1, T2, …; a child appends its 1-based sibling position — T1.1,
// T1.2 under T1, T1.1.1 under T1.1. Derived from tree position, not an
// identity: step_id stays the stable handle.
export function todoHierarchyCodes(todos: Todo[]): string[] {
  const depths = todoTreeDepths(todos);
  const codes = new Array<string>(todos.length).fill("");
  const counters: number[] = [];
  todos.forEach((_, index) => {
    const depth = depths[index];
    counters.length = depth + 1;
    counters[depth] = (counters[depth] ?? 0) + 1;
    codes[index] = `T${counters.slice(0, depth + 1).join(".")}`;
  });
  return codes;
}

export interface TodoBatch {
  key: string;
  todos: Todo[];
}

export interface TodoBatchPartition {
  current: Todo[];
  /** Historical batches whose items all reached a terminal status, newest first. */
  archive: TodoBatch[];
}

// partitionTodoBatches splits newest-first todo_write batches into the live
// current list and the collapsed archive of fully finished history (task 152,
// include_terminal parametrization: includeTerminal=false keeps the active-only
// behaviour). Batches already dismissed through the sidecar stay retired —
// dismissal must not be undone by the archive.
export function partitionTodoBatches(
  batches: readonly TodoBatch[],
  options: { includeTerminal?: boolean; dismissedBatches?: readonly string[] | null } = {},
): TodoBatchPartition {
  const current = batches.length > 0 ? batches[0].todos : [];
  const archive: TodoBatch[] = [];
  if (options.includeTerminal) {
    const dismissed = options.dismissedBatches;
    for (let i = 1; i < batches.length; i++) {
      const batch = batches[i];
      if (batch.todos.length === 0) continue;
      if (!batch.todos.every((todo) => todoTerminalStatus(todo.status))) continue;
      if (dismissed?.includes(batch.key)) continue;
      archive.push(batch);
    }
  }
  return { current, archive };
}

export function todoContinueTarget(
  targetTabId: string | null | undefined,
  activeTabId: string | null | undefined,
  runtime: TodoRuntimePresentation & { ready: boolean; readOnly?: boolean },
): string | null {
  const target = String(targetTabId ?? "").trim();
  if (!target || target !== String(activeTabId ?? "").trim()) return null;
  if (!runtime.ready || runtime.readOnly || runtime.running || runtime.pendingPrompt) return null;
  return target;
}

export function resolveTodoPanelTodos(
  canonical: Todo[] | null | undefined,
  live?: Todo[] | null,
): Todo[] {
  // `live` is set only when the transcript has a completed top-level todo_write.
  // Prefer it over MetaForTab — meta only refreshes on turn_done / focus change,
  // so mid-turn status flips otherwise freeze until the user switches tabs (#7642).
  if (live !== undefined && live !== null) return live;
  return Array.isArray(canonical) ? canonical : [];
}

export function sameTodoList(a: Todo[] | null | undefined, b: Todo[] | null | undefined): boolean {
  if (a === b) return true;
  if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
  return a.every((todo, index) => {
    const other = b[index];
    return (
      todo.content === other.content &&
      todo.status === other.status &&
      todo.activeForm === other.activeForm &&
      todo.level === other.level
    );
  });
}

export function todoDismissalKey(todos: Todo[]): string {
  if (todos.length === 0) return "";
  return JSON.stringify(todos.map((todo) => ({
    content: String(todo.content ?? ""),
    status: todoStatus(todo.status),
    activeForm: String(todo.activeForm ?? ""),
    level: typeof todo.level === "number" ? todo.level : 0,
    // Task 152: the tree shape is part of a batch's identity.
    parent_id: String(todo.parent_id ?? ""),
  })));
}

export function todoPanelScope({ activeTab, activeTabId, eventChannel }: TodoPanelScopeInput): string {
  const tabId = String(activeTabId ?? "").trim();
  const tab = !tabId || activeTab?.id === tabId ? activeTab : null;
  const sessionPath = tab?.sessionPath?.trim();
  if (sessionPath) return `session:${sessionPath}`;
  if (tabId) return `tab:${tabId}`;
  const topicId = tab?.topicId?.trim();
  if (tab && topicId) return `topic:${tab.scope ?? ""}:${tab.workspaceRoot ?? ""}:${topicId}`;
  const channel = String(eventChannel ?? "").trim();
  return channel ? `event:${channel}` : "";
}

export function scopedTodoDismissalKey(scope: string | null | undefined, todoKey: string | null | undefined): string {
  const key = String(todoKey ?? "").trim();
  if (!key) return "";
  const prefix = String(scope ?? "").trim();
  return prefix ? `${prefix}\0${key}` : key;
}

export function dismissedTodoKeyForScope(
  scope: string | null | undefined,
  dismissedKeys: ReadonlySet<string> | null | undefined,
  todoKey: string | null | undefined,
): string | null {
  const scopedKey = scopedTodoDismissalKey(scope, todoKey);
  if (!scopedKey || !dismissedKeys?.has(scopedKey)) return null;
  return todoKey ?? null;
}

export function todoBatchKey(todos: Todo[]): string {
  if (todos.length === 0) return "";
  return JSON.stringify(todos.map((todo) => ({
    content: String(todo.content ?? ""),
    level: typeof todo.level === "number" ? todo.level : 0,
    parent_id: String(todo.parent_id ?? ""),
  })));
}

export function scopedTodoBatchKey(scope: string | null | undefined, batchKey: string | null | undefined): string {
  const key = String(batchKey ?? "").trim();
  if (!key) return "";
  const prefix = String(scope ?? "").trim();
  return prefix ? `${prefix}\0${key}` : key;
}

export function shouldShowTodoPanel(
  todoKey: string | null | undefined,
  dismissedTodoKey: string | null,
  todos: Todo[],
  persisted?: { batchKey?: string | null; batches?: readonly string[] | null },
): boolean {
  if (!todoKey || todos.length === 0) return false;
  if (hasIncompleteTodos(todos)) return true;
  if (todoKey === dismissedTodoKey) return false;
  const batchKey = String(persisted?.batchKey ?? "").trim();
  if (batchKey && persisted?.batches?.includes(batchKey)) return false;
  return true;
}

export function sameStringList(a?: readonly string[] | null, b?: readonly string[] | null): boolean {
  if (a === b) return true;
  const left = Array.isArray(a) ? a : [];
  const right = Array.isArray(b) ? b : [];
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

export function shouldOpenTodoPanelByDefault(): boolean {
  return false;
}

function todoStatus(status: unknown): string {
  const normalized = String(status ?? "").trim();
  return normalized || "pending";
}

function hasIncompleteTodos(todos: Todo[]): boolean {
  return todos.some((todo) => !todoTerminalStatus(todo.status));
}
