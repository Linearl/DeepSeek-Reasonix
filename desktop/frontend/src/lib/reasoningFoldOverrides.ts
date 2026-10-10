// 任务 765: user-expressed reasoning fold intent, surviving streaming
// re-renders and remounts. The B-layer panels keep their open state in
// component-local useState + a userOverridden ref, both of which die with the
// component instance: virtualization (block scrolled out of the window),
// surface rebuilds (tab switch and back), and the live→history handoff all
// remount the panel, after which the auto-expand branches re-derive the open
// state from the presentation and may override what the user just chose.
// This table is the per-session memory of that choice, mirroring the
// task-124 transcriptFoldOverrides storage shape (in-memory LRU, per
// sessionKey; deliberately NOT localStorage — after a restart a finished
// turn's panels re-derive from the tier default, which is the pre-765
// behavior for everything the user did not touch this session).

const MAX_SESSIONS = 12;
const MAX_ENTRIES_PER_SESSION = 512;
const overrides = new Map<string, Map<string, boolean>>();

function sessionMap(sessionKey: string): Map<string, boolean> {
  const existing = overrides.get(sessionKey);
  if (existing) {
    overrides.delete(sessionKey);
    overrides.set(sessionKey, existing);
    return existing;
  }
  const created = new Map<string, boolean>();
  overrides.set(sessionKey, created);
  while (overrides.size > MAX_SESSIONS) overrides.delete(overrides.keys().next().value!);
  return created;
}

/** The user's explicit open/closed choice for one reasoning panel, or
 * undefined when the user never touched it (tier semantics apply). */
export function readReasoningFoldOverride(sessionKey: string, itemId: string): boolean | undefined {
  return sessionMap(sessionKey).get(itemId);
}

export function writeReasoningFoldOverride(sessionKey: string, itemId: string, open: boolean): void {
  const stored = sessionMap(sessionKey);
  stored.delete(itemId);
  stored.set(itemId, open);
  while (stored.size > MAX_ENTRIES_PER_SESSION) stored.delete(stored.keys().next().value!);
}

/** A tier switch re-baselines every panel (same contract as the fold-map
 * reconcile clearing userOverridden on preferenceChanged): the old tier's
 * manual choices must not leak into the new tier's semantics. */
export function clearReasoningFoldOverrides(sessionKey: string): void {
  overrides.delete(sessionKey);
}

export function clearReasoningFoldOverrideSessionForTest(sessionKey: string): void {
  overrides.delete(sessionKey);
}
