// Dock tab visibility (task 259): which right-dock tabs the user wants to see.
//
// Lives behind the experimental todo-sidebar switch: the settings pane that
// writes here is only enabled while that switch is on, and with the switch off
// every tab behaves exactly as it did before (all visible, no wrap). Unlike the
// switch itself this is a pure frontend preference — it applies live, no
// restart — so it persists in localStorage instead of the desktop config.

const STORAGE_KEY = "rightDockTabs:hidden";

/** Dock tab ids, matching RightDockMode plus every tab the dock can render. */
export type DockTabId = "context" | "files" | "changed" | "remote" | "todos";

export const DOCK_TAB_IDS: readonly DockTabId[] = ["context", "files", "changed", "remote", "todos"];

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
