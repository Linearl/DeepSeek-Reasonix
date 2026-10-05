// Dock tab visibility (task 259): which right-dock tabs the user wants to see.
//
// Lives behind the experimental todo-sidebar switch: the settings pane that
// writes here is only enabled while that switch is on, and with the switch off
// every tab behaves exactly as it did before (all visible, no wrap). Unlike the
// switch itself this is a pure frontend preference — it applies live, no
// restart — so it persists in localStorage instead of the desktop config.

const STORAGE_KEY = "rightDockTabs:hidden";

/** Dock tab ids, matching RightDockMode plus every tab the dock can render. */
export type DockTabId = "context" | "files" | "changed" | "remote" | "todos" | "artifacts" | "references" | "subagents";

// Task 260: artifacts/references (session write-path/read-path files) join the
// same experimental sidebar family as todos — hideable while the switch is on.
// Task 495: "subagents" is hideable too, but under its own switch.
export const DOCK_TAB_IDS: readonly DockTabId[] = ["context", "files", "changed", "remote", "todos", "artifacts", "references", "subagents"];

function loadHiddenTabs(): readonly DockTabId[] {
  try {
    const saved = window.localStorage.getItem(STORAGE_KEY);
    if (!saved) return [];
    const parsed = JSON.parse(saved) as unknown;
    if (!Array.isArray(parsed)) return [];
    const out = parsed.filter((entry): entry is DockTabId => typeof entry === "string" && DOCK_TAB_IDS.includes(entry as DockTabId));
    return out;
  } catch {
    return [];
  }
}

let hiddenTabs: readonly DockTabId[] = typeof window === "undefined" ? [] : loadHiddenTabs();
const listeners = new Set<(tabs: readonly DockTabId[]) => void>();

function persist(): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(hiddenTabs));
  } catch {
    /* ignore quota errors */
  }
}

export function loadHiddenDockTabs(): readonly DockTabId[] {
  return hiddenTabs;
}

export function isDockTabHidden(id: DockTabId): boolean {
  return hiddenTabs.includes(id);
}

/** Show/hide one tab. The change notifies subscribers immediately (live apply). */
export function setDockTabHidden(id: DockTabId, hidden: boolean): void {
  const next = hidden
    ? Array.from(new Set([...hiddenTabs, id]))
    : hiddenTabs.filter((entry) => entry !== id);
  if (next.length === hiddenTabs.length) return;
  hiddenTabs = next;
  persist();
  for (const listener of listeners) listener(hiddenTabs);
}

export function onHiddenDockTabsChange(listener: (tabs: readonly DockTabId[]) => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

// The overview tab renders behind a constant gate today (App.tsx
// SHOW_CONTEXT_DOCK = true); kept here so the renderable-set math lives in one
// place next to the tab ids it reasons about.
const CONTEXT_TAB_GATE = true;

export type DockTabRenderContext = {
  /** The switch itself: with it off the dock renders its original literals. */
  todoSidebar: boolean;
  /** Remote hosts exist, so the remote tab can actually render. */
  remoteAvailable: boolean;
  /** Creation layout hides the overview tab. */
  creation: boolean;
  /** Task 495: the subagent panel switch (its tab renders only with it on). */
  subagentsPanel?: boolean;
};

/**
 * Task 259 (audit-2 minor c): the tabs that can actually render right now.
 *
 * The "keep at least one tab" guard must count only renderable tabs — hidden
 * but gated-off tabs (remote with no hosts, overview in creation) must not
 * satisfy it, otherwise the user can hide everything that is really shown and
 * the dock ends up with zero tabs. Consumers: the settings pane's last-tab
 * checkbox lock, and tests.
 */
export function renderableDockTabs(ctx: DockTabRenderContext): DockTabId[] {
  if (!ctx.todoSidebar) return [];
  const out: DockTabId[] = [];
  if (CONTEXT_TAB_GATE && !ctx.creation) out.push("context");
  out.push("files", "changed");
  if (ctx.remoteAvailable) out.push("remote");
  out.push("todos");
  // Task 260: the two session side-files tabs render alongside todos while the
  // family switch is on, so the last-tab guard must count them the same way.
  out.push("artifacts", "references");
  // Task 495: the subagent tab counts as renderable only under its own switch.
  if (ctx.subagentsPanel) out.push("subagents");
  return out;
}

/**
 * True when `id` is the only still-visible tab among the currently renderable
 * ones — its checkbox must not be uncheckable, or the dock loses every tab.
 *
 * Audit-2 fast-verify: a hidden tab never trips this guard. Without the early
 * return, an all-hidden state made `every(...)` true for every id at once,
 * locking all checkboxes with nothing checkable back (a state the previous
 * release could persist, so an upgrade could boot straight into it).
 */
export function isLastRenderableVisibleTab(id: DockTabId, ctx: DockTabRenderContext): boolean {
  if (isDockTabHidden(id)) return false;
  const renderable = renderableDockTabs(ctx);
  if (!renderable.includes(id)) return false;
  return renderable.every((other) => other === id || isDockTabHidden(other));
}

