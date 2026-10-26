// Session wall preferences (tl 1175): the「仅已打开」filter state, persisted
// across restarts.
//
// Like the dock-tab visibility preference (dockTabs.ts) this is a pure
// frontend preference that applies live, so it lives in localStorage instead
// of the desktop config. Malformed shapes degrade to defaults rather than
// throwing; quota errors are ignored (best-effort).

const STORAGE_KEY = "sessionWall:prefs:v1";

export interface SessionWallPrefs {
  // tl 1175: the wall's「仅已打开」filter, as the user last left it.
  openOnly?: boolean;
  // tl 1176: collapsed project groups (workspace roots / the global marker),
  // as the user last left them. Only project-mode groups persist — time-mode
  // day buckets are not foldable, so they never appear here.
  collapsed?: string[];
}

function loadPrefs(): SessionWallPrefs {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as unknown;
    if (typeof parsed !== "object" || parsed === null) return {};
    const out: SessionWallPrefs = {};
    if ((parsed as SessionWallPrefs).openOnly === true) out.openOnly = true;
    const rawCollapsed: unknown = (parsed as { collapsed?: unknown }).collapsed;
    if (Array.isArray(rawCollapsed)) {
      out.collapsed = rawCollapsed.filter((k): k is string => typeof k === "string");
    }
    return out;
  } catch {
    return {};
  }
}

function savePrefs(patch: SessionWallPrefs): void {
  try {
    const merged = { ...loadPrefs(), ...patch };
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(merged));
  } catch {
    /* ignore quota errors — the preference is best-effort */
  }
}

export function loadSessionWallOpenOnly(): boolean {
  return loadPrefs().openOnly === true;
}

export function saveSessionWallOpenOnly(value: boolean): void {
  savePrefs({ openOnly: value });
}

// tl 1176: the collapsed set persists as a whole (toggle → rewrite the array);
// group keys are workspace roots or the global marker, both restart-stable.
export function loadSessionWallCollapsed(): string[] {
  return loadPrefs().collapsed ?? [];
}

export function saveSessionWallCollapsed(keys: string[]): void {
  savePrefs({ collapsed: keys });
}
