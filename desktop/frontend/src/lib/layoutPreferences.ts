export type LayoutSizeKey =
  | "sidebarWidth"
  | "sidebarWidthGraphite"
  | "rightDockWidth"
  | "rightDockTreeWidth"
  | "rightDockPreviewWidth"
  | "workspaceFileTreePanelWidth"
  | "workspaceTreeWidth"
  | "composerHeight"
  | "drawerWidth"
  | "settingsDrawerWidth";

type LayoutPreferences = {
  sizes?: Partial<Record<LayoutSizeKey, number>>;
  // layoutStyle is cached here for the FIRST PAINT only: the authoritative value
  // lives in the desktop config and arrives asynchronously, so without a
  // synchronous copy a classic-layout user sees workbench for a few frames
  // (task 40 / #9796 fallout).
  layoutStyle?: string;
  // theme and themeStyle are cached for the same reason. Without it a user who
  // picked the amber accent starts every launch on the default blue and watches
  // it turn orange once the config lands.
  theme?: string;
  themeStyle?: string;
};

const STORAGE_KEY = "reasonix.layoutPreferences.v1";

const LEGACY_SIZE_KEYS: Record<LayoutSizeKey, string[]> = {
  sidebarWidth: ["reasonix.sidebar.width"],
  sidebarWidthGraphite: [],
  rightDockWidth: [],
  rightDockTreeWidth: [],
  rightDockPreviewWidth: [],
  workspaceFileTreePanelWidth: [],
  workspaceTreeWidth: ["reasonix.workspaceTree.width"],
  composerHeight: ["reasonix.composerHeight"],
  drawerWidth: ["reasonix.drawer.width"],
  settingsDrawerWidth: ["reasonix.settingsDrawer.width"],
};

type ClampSize = (value: number) => number;

function readPrefs(): LayoutPreferences {
  if (typeof window === "undefined") return {};
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as LayoutPreferences;
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

function writePrefs(prefs: LayoutPreferences): void {
  if (typeof window === "undefined") return;
  try {
    const payload: LayoutPreferences = { sizes: prefs.sizes ?? {} };
    if (prefs.layoutStyle) payload.layoutStyle = prefs.layoutStyle;
    if (prefs.theme) payload.theme = prefs.theme;
    if (prefs.themeStyle) payload.themeStyle = prefs.themeStyle;
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(payload));
  } catch {
    /* ignore storage failures */
  }
}

/** loadCachedLayoutStyle returns the synchronously readable layout style, or
 * null when nothing usable is cached (first install, cleared storage, older
 * cache shape). Callers fall back to the upstream default, workbench. */
export function loadCachedLayoutStyle(): string | null {
  const prefs = readPrefs();
  const style = prefs.layoutStyle;
  return typeof style === "string" && style.trim() !== "" ? style : null;
}

/** saveCachedLayoutStyle mirrors the authoritative desktop preference into the
 * first-paint cache. Blank input is ignored so a cleared config cannot pin a
 * stale style. */
export function saveCachedLayoutStyle(style: string): void {
  const trimmed = typeof style === "string" ? style.trim() : "";
  if (trimmed === "") return;
  const prefs = readPrefs();
  writePrefs({ ...prefs, layoutStyle: trimmed });
}

/** loadCachedAppearance returns the synchronously readable desktop appearance,
 * or null when nothing usable is cached. Callers fall back to the built-in
 * defaults, which the async config load then corrects. */
export function loadCachedAppearance(): { theme: string; themeStyle: string } | null {
  const prefs = readPrefs();
  const theme = typeof prefs.theme === "string" ? prefs.theme.trim() : "";
  if (theme === "") return null;
  return { theme, themeStyle: typeof prefs.themeStyle === "string" ? prefs.themeStyle.trim() : "" };
}

/** saveCachedAppearance mirrors the authoritative appearance into the first-paint
 * cache. An empty theme is ignored so a cleared config cannot pin a stale look. */
export function saveCachedAppearance(theme: string, themeStyle: string): void {
  const trimmedTheme = typeof theme === "string" ? theme.trim() : "";
  if (trimmedTheme === "") return;
  const prefs = readPrefs();
  writePrefs({
    ...prefs,
    theme: trimmedTheme,
    themeStyle: typeof themeStyle === "string" ? themeStyle.trim() : "",
  });
}

function readLegacySize(key: LayoutSizeKey): number | null {
  if (typeof window === "undefined") return null;
  for (const legacyKey of LEGACY_SIZE_KEYS[key]) {
    try {
      const raw = Number(window.localStorage.getItem(legacyKey));
      if (Number.isFinite(raw) && raw > 0) return raw;
    } catch {
      /* keep trying other keys */
    }
  }
  return null;
}

function normalizeSize(value: number, clamp?: ClampSize): number {
  const rounded = Math.round(value);
  return clamp ? clamp(rounded) : rounded;
}

export function loadLayoutSize(key: LayoutSizeKey, fallback: number, clamp?: ClampSize): number {
  const prefs = readPrefs();
  const saved = prefs.sizes?.[key];
  const value = Number.isFinite(saved) && saved! > 0 ? saved! : readLegacySize(key);
  return value === null ? normalizeSize(fallback, clamp) : normalizeSize(value, clamp);
}

export function loadOptionalLayoutSize(key: LayoutSizeKey, clamp?: ClampSize): number | null {
  const prefs = readPrefs();
  const saved = prefs.sizes?.[key];
  const value = Number.isFinite(saved) && saved! > 0 ? saved! : readLegacySize(key);
  return value === null ? null : normalizeSize(value, clamp);
}

export function saveLayoutSize(key: LayoutSizeKey, value: number, clamp?: ClampSize): void {
  const prefs = readPrefs();
  const sizes = { ...(prefs.sizes ?? {}), [key]: normalizeSize(value, clamp) };
  writePrefs({ ...prefs, sizes });
}

export function clearLayoutSize(key: LayoutSizeKey): void {
  const prefs = readPrefs();
  const sizes = { ...(prefs.sizes ?? {}) };
  delete sizes[key];
  writePrefs({ ...prefs, sizes });
}
