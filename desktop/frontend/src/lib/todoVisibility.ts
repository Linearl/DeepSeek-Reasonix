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
  // Task 1107 pit ①: level is a 0/1 flag, not a nesting level — a level-1
  // item attaches to the nearest preceding DEPTH-0 root, never to the
  // root's child (which would grow a phantom second level and split the
  // root away from its sub-step in grouping). Roots are recorded wherever
  // the item resolves to depth 0 (explicit parent chains included), so a
  // root's own level-1 sub-steps do not detach their later siblings.
  const depthZeroIndices: number[] = [];
  todos.forEach((todo, index) => {
    const parentID = String(todo.parent_id ?? "").trim();
    if (parentID) {
      const parent = indexOfStepID.get(parentID);
      if (parent !== undefined && parent < index) {
        depths[index] = depths[parent] + 1;
      }
    } else if (Number(todo.level) === 1 && depthZeroIndices.length > 0) {
      const parent = depthZeroIndices[depthZeroIndices.length - 1]!;
      depths[index] = depths[parent]! + 1;
    }
    if (depths[index] === 0) depthZeroIndices.push(index);
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

// ---- Task 1107: focus-mode grouping (borrowed presentation idea from ZCode
// 3.16.2's task panel, reimplemented on our own tree layer) ----
//
// A live batch renders as at most five rows: the terminal group collapsed to
// one header, the lookahead window (current + the LOOKAHEAD pending items
// after it — the number the user pinned on 2026-10-23: do not change), the
// current item always visible, then the remaining pending group collapsed to
// one header. The headers carry the hover-preview affordance; expanding a
// group (persisted per stateKey, same store family as the shelf open state)
// reuses the plain tree rows underneath.

/**
 * Items rendered after the current one inside the focus window — the number
 * the user pinned on 2026-10-23 (do not change).
 */
export const TODO_FOCUS_LOOKAHEAD = 2;

export type TodoFocusGroupKind = "done" | "focus" | "current" | "pending";

export interface TodoFocusRow {
  kind: TodoFocusGroupKind;
  /** Item indices this row stands for (a single index for plain rows). */
  indices: number[];
}

// todoFocusRows builds the focus-mode row layout. Tree integrity (task 152)
// is the first constraint: a subtree travels with its parent, so a row can
// never be split away from its children. Anchors are therefore root-level
// (depth 0) items, and an item's anchor is its topmost ancestor — a child of
// the current item stays in the current/focus rows even when the current root
// itself sits in the done group.
//
// The current anchor and the anchors in the lookahead window become plain
// rows; every other anchor compresses into a group header (terminal group
// first when it has any items, pending group last when it has any). A batch
// that is all-terminal keeps the plain list — task 893-C's signed-off panel
// shows its items, not two empty group headers.
export function todoFocusRows(todos: Todo[]): TodoFocusRow[] {
  if (todos.length === 0) return [];
  const { anchors, anchorOf } = todoFocusUnits(todos);
  const anchorMembers = new Map<number, number[]>();
  todos.forEach((_, index) => {
    const anchor = anchorOf[index];
    const list = anchorMembers.get(anchor);
    if (list) list.push(index);
    else anchorMembers.set(anchor, [index]);
  });
  const anchorOpen = new Map<number, boolean>();
  anchors.forEach((anchor) => {
    anchorOpen.set(anchor, !anchorMembers.get(anchor)!.every((i) => todoTerminalStatus(todos[i].status)));
  });
  // The window always contains the current item's UNIT: the unit root anchors
  // the plain row (its subtree renders with it); when the in-progress item is
  // itself a child, that child's position inside the unit sets the window —
  // so the LOOKAHEAD items follow the actual current item, not the root.
  let currentAnchor = -1;
  let currentPosition = -1;
  anchors.forEach((anchor) => {
    const member = anchorMembers.get(anchor)!.find((i) => todoStatus(todos[i]!.status) === "in_progress");
    if (member !== undefined) {
      currentAnchor = anchor;
      currentPosition = member;
    }
  });

  const rows: TodoFocusRow[] = [];
  if (currentAnchor < 0) {
    // No in-progress item (a stale restored batch): the whole list keeps the
    // plain face — with no current anchor a "current row" cannot exist.
    anchors.forEach((anchor) => rows.push({ kind: "focus", indices: [anchor] }));
    return rows;
  }
  // Pass 1: the terminal group. Done anchors before the focus window collect
  // into one header; done anchors after it are folded into the pending
  // group so the rows keep list order (a done item between the lookahead
  // tail and the tail pending items stays reachable at its own position).
  const doneIndices = anchors.filter((anchor) => !anchorOpen.get(anchor) && anchor < currentPosition);
  if (doneIndices.length > 0) rows.push({ kind: "done", indices: doneIndices });
  // Pass 2: the focus window — the current unit's root (its subtree renders
  // with it), then the LOOKAHEAD units AFTER the current item, measured in
  // items (anchor index), not unit slots, so a multi-item unit never
  // shortens the window. A done unit among them stays a plain row, keeping
  // the row count stable while the item is still in view.
  // The window holds the current UNIT's anchors plus the LOOKAHEAD units
  // after it — everything the user keeps seeing without opening a group.
  const windowUnits = new Set<number>([currentAnchor]);
  for (const anchor of anchors) {
    if (windowUnits.size >= 1 + TODO_FOCUS_LOOKAHEAD) break;
    if (anchor > currentPosition) windowUnits.add(anchor);
  }
  const tailIndices: number[] = [];
  anchors.forEach((anchor) => {
    if (anchor === currentAnchor) {
      rows.push({ kind: "current", indices: [anchor] });
      return;
    }
    if (windowUnits.has(anchor)) {
      rows.push({ kind: "focus", indices: [anchor] });
      return;
    }
    // Everything outside the window folds into the tail group; a terminal
    // unit BEFORE the current item is already covered by the done header.
    if (!anchorOpen.get(anchor) && anchor < currentPosition) return;
    tailIndices.push(anchor);
  });
  // Pass 3: everything outside the window (open or done) folds into one
  // pending-group header at the tail — list order preserved, one header.
  if (tailIndices.length > 0) rows.push({ kind: "pending", indices: tailIndices });
  return rows;
}

// todoFocusGroupTodos collects every item a group header stands for, in list
// order — the hover preview lists them all. Unit membership (anchorOf) is
// recomputed the same way todoFocusRows derives it, so a child reported by
// its explicit parent_id chain never escapes its unit.
export function todoFocusGroupTodos(todos: Todo[], row: TodoFocusRow): Todo[] {
  const { anchorOf } = todoFocusUnits(todos);
  const units = new Set(row.indices);
  return todos.filter((_, index) => units.has(anchorOf[index]!));
}

// todoFocusUnits derives the grouping units (task 1107 pit ①: a subtree always
// travels with its parent). Each unit root is either a depth-0 root itself or
// a direct child of one — an explicit parent_id chain wins over position,
// same precedence as the depth pass, so a child reported by step_id never
// escapes its unit. Deeper descendants join their nearest preceding unit;
// malformed shapes still produce a unit per item.
function todoFocusUnits(todos: Todo[]): { anchors: number[]; anchorOf: number[] } {
  const depths = todoTreeDepths(todos);
  const anchors: number[] = [];
  const anchorOf = new Array<number>(todos.length).fill(-1);
  const parentOf = new Array<number>(todos.length).fill(-1);
  todos.forEach((todo, index) => {
    const parentID = String(todo.parent_id ?? "").trim();
    if (parentID) {
      const byID = todos.findIndex((other, otherIndex) => otherIndex < index && String(other.step_id ?? "").trim() === parentID);
      if (byID >= 0) {
        parentOf[index] = byID;
        // The chain pass also fixes the depth: a chained child of a root
        // sits at depth 1 like its legacy siblings (pit ①: no phantom
        // depth from the position fallback).
        depths[index] = depths[byID]! + 1;
        return;
      }
    }
    const parent = parentIndexOf(depths, index);
    if (parent >= 0) parentOf[index] = parent;
  });
  // Unit roots in one pass. Pit ① (a subtree never splits from its parent):
  //  - a depth-0 root starts its unit;
  //  - a chain-less item right after the previous ROOT is its legacy
  //    level-1 sibling and starts its own unit (the Go segment machine's
  //    shape: each level-0 phase owns its level-1 run);
  //  - a chained child of a NON-root parent joins that parent's unit, but a
  //    chained child of a ROOT starts its own unit (it renders as its own
  //    focus row under the root's row, never split from the root's group).
  // Everything else joins its nearest preceding ancestor's unit.
  let lastRoot = -1;
  todos.forEach((todo, index) => {
    const hasChain = !!String(todo.parent_id ?? "").trim();
    // Chain-less items attach to the nearest preceding DEPTH-0 root; the
    // depths array is authoritative (a legacy level-1 run under a root
    // keeps its sub-steps inside that root's unit, pit ①).
    const parent = parentOf[index]!;
    const isRoot = parent < 0;
    // A depth-0 root, a direct child of one, or the legacy level-1 item
    // right after the previous root starts its unit; everything else joins
    // the unit of its nearest preceding ancestor (pit ①: a chained child
    // of a NON-root parent never splits from that parent).
    // A chain-less item right after the previous ROOT is its legacy
    // level-1 sibling (the Go segment machine's shape). A chained child of
    // a NON-root parent never takes this branch (pit ①: no split), and a
    // chained child of a ROOT is its own unit — its row renders directly
    // under the root's row (pit ①: it stays in the root's window).
    // A chain-less item right after the previous ROOT is its legacy
    // level-1 sibling (the Go segment machine's shape); the depth pass
    // already placed chained children correctly, so `parentIsDepthZero`
    // covers both plain and chained children of a root.
    // A depth-0 root, a direct child of one (plain or chained — the depth
    // pass already placed chained children correctly), or the legacy
    // level-1 item right after the previous root starts its unit.
    const parentIsDepthZero = parent >= 0 && depths[parent] === 0;
    // Legacy adjacency uses the position parent: for a chain-less item the
    // chain pass left `parent` as the position parent (pit ①: chained
    // children of a NON-root parent never take this branch, so their
    // sibling runs stay inside their parent's unit).
    // The legacy sibling run attaches to the last ROOT: a chain-less item
    // at depth 1 right after a depth-0 root (the Go segment machine's
    // shape). Pit ①: a chained child of a NON-root parent never takes this
    // branch, so its subtree stays inside its parent's unit.
    // Pit ①: a chain-less item inside the last ROOT's subtree run (depth 1)
    // is its legacy level-1 sibling — the run starts right after the root.
    const legacySibling = !hasChain && lastRoot >= 0
      && depths[lastRoot] === 0 && depths[index] === 1 && index > lastRoot;
    // A chained child of a ROOT starts its own unit too — pit ① keeps it
    // inside the root's window (the root's row renders the subtree with it).
    const chainedChildOfRoot = hasChain && parentIsDepthZero;
    if (isRoot || parentIsDepthZero || legacySibling || chainedChildOfRoot) {
      anchors.push(index);
      anchorOf[index] = index;
      // The legacy adjacency base is the last ROOT — a unit root that is
      // itself a child must not become the base (its own next sibling would
      // otherwise split away from it, pit ①).
      if (isRoot) lastRoot = index;
      return;
    }
    for (let ancestor = parent; ancestor >= 0; ancestor = parentOf[ancestor]!) {
      if (anchorOf[ancestor] >= 0) {
        anchorOf[index] = anchorOf[ancestor];
        return;
      }
    }
    anchors.push(index); // malformed shape still renders
    anchorOf[index] = index;
  });
  return { anchors, anchorOf };
}

function parentIndexOf(depths: readonly number[], index: number): number {
  const depth = depths[index]!;
  for (let i = index - 1; i >= 0; i--) {
    if (depths[i]! < depth) return i;
  }
  return -1;
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

// ---- Task 893-B: three-state right-dock empty area ----
// The right-dock "todos" tab renders an empty copy whenever no live list is
// shown, but "no live list" has two very different meanings: a session that
// never recorded any task, versus one whose list was sealed at a turn
// boundary or retired after sign-off. The old single "暂无任务" copy read as
// "the agent is tracking nothing", which mislead users into doubting work in
// progress. All signals below are transcript/meta state the frontend already
// holds — no backend field needed:
//   - the transcript keeps every successful top-level todo_write (args
//     included on hydrate; see desktop/app_history_tools.go historyToolCall),
//   - todoArchive retains earlier finished batches,
//   - meta.dismissedTodoBatches survives reloads via the session sidecar,
//   - meta.canonicalTodos is non-nil once the kernel ever snapshotted a list.

export type TodoDockEmptyState = "never" | "sealed";

/** Structural probe for transcript tool items; App's `Item` narrows to it. */
export interface TodoDockEmptyItemSignal {
  kind?: string;
  name?: string;
  parentId?: string | null;
  status?: string;
  error?: unknown;
}

export interface TodoDockEmptyStateInput {
  /** Currently resolved panel list (may be empty). */
  todos?: readonly Todo[] | null;
  /** Collapsed archive of earlier finished batches. */
  archive?: readonly TodoBatch[] | null;
  /** Session transcript items; any successful top-level todo_write counts. */
  items?: readonly TodoDockEmptyItemSignal[] | null;
  /** Sidecar-recorded dismissed completed batches (session + lineage). */
  dismissedBatches?: readonly string[] | null;
  /** Authoritative canonical list from Meta (nil = no snapshot, [] = cleared). */
  canonicalTodos?: readonly Todo[] | null;
}

export interface TodoDockEmptyStateResult {
  state: TodoDockEmptyState;
  /**
   * Items in the most recently known list, when derivable — feeds the
   * "上一回合 N 项" copy. Null when only an empty clear was ever observed.
   */
  count: number | null;
}

function transcriptEverWroteTodos(items: readonly TodoDockEmptyItemSignal[] | null | undefined): boolean {
  if (!items) return false;
  for (let index = items.length - 1; index >= 0; index--) {
    const item = items[index];
    if (item?.kind === "tool" && item?.name === "todo_write" && !item?.parentId && item?.status === "done" && !item?.error) {
      return true;
    }
  }
  return false;
}

// todoDockEmptyState picks the copy for the dock's empty todos area. "sealed"
// means the session demonstrably had a task list that is now out of the live
// view; "never" keeps the original "no tasks" copy. The count is best-effort
// (live list > newest archived batch > canonical snapshot) so the sealed copy
// can say how many tasks were tracked when that is known.
export function todoDockEmptyState(input: TodoDockEmptyStateInput): TodoDockEmptyStateResult {
  const todosCount = input.todos?.length ?? 0;
  const archiveCount = input.archive?.[0]?.todos.length ?? 0;
  const canonicalCount = input.canonicalTodos?.length ?? 0;
  const everHadTodos = todosCount > 0
    || archiveCount > 0
    || canonicalCount > 0
    || (input.dismissedBatches?.length ?? 0) > 0
    || transcriptEverWroteTodos(input.items);
  if (!everHadTodos) return { state: "never", count: null };
  const count = todosCount > 0 ? todosCount : archiveCount > 0 ? archiveCount : canonicalCount > 0 ? canonicalCount : null;
  return { state: "sealed", count };
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
