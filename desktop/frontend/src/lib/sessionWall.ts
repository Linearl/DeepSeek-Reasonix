// Task 505 session graph wall: the pure half of the feature. The component
// (components/SessionWallPanel.tsx) renders; this module filters, groups and
// ranks, and guards the command-palette entry. Kept free of React so the
// contract can be tested without a DOM.
//
// The wall is the enhancement half of a two-path pair (fork rule 8): the
// palette's legacy "recent sessions" slice (12 rows) stays the fallback and is
// byte-for-byte unchanged while the experimental_session_wall switch is off.

import type { SessionMeta } from "./types";
import { paletteSessionDisplayTitle, paletteSessionHint, sessionActivityTime } from "./session";

export type SessionWallGroupMode = "project" | "time";

export interface SessionWallGroup {
  // Stable identity for React keys: project root path (or the global marker),
  // or a day bucket in time mode.
  key: string;
  label: string;
  // Sessions of the group, most recent activity first.
  sessions: SessionMeta[];
}

// sessionWallHaystack is the searchable text of one session: display title,
// secondary hint, topic id and workspace root. The wall search filters on it
// with the same token semantics as the palette fuzzy scorer (every
// space-separated token must appear, in order, case-insensitively).
export function sessionWallHaystack(s: SessionMeta): string {
  return [
    paletteSessionDisplayTitle(s, ""),
    paletteSessionHint(s) ?? "",
    s.topicId ?? "",
    s.workspaceRoot ?? "",
  ]
    .join("\n")
    .toLowerCase();
}

// applySessionWallQuery filters sessions for the wall search box. An empty
// query returns the input as-is (same array) so callers can skip re-grouping.
export function applySessionWallQuery(sessions: SessionMeta[], query: string): SessionMeta[] {
  const q = query.trim().toLowerCase();
  if (!q) return sessions;
  const tokens = q.split(/\s+/);
  return sessions.filter((s) => {
    const hay = sessionWallHaystack(s);
    let cursor = 0;
    for (const tok of tokens) {
      const at = hay.indexOf(tok, cursor);
      if (at < 0) return false;
      cursor = at + tok.length;
    }
    return true;
  });
}

const GLOBAL_PROJECT_KEY = "global:";

// sessionWallProjectKey buckets a session by its workspace; sessions without
// one (or with legacy empty scope semantics) share the global bucket.
export function sessionWallProjectKey(s: SessionMeta): string {
  const root = s.workspaceRoot?.trim();
  return root || GLOBAL_PROJECT_KEY;
}

function basename(path: string): string {
  const trimmed = path.replace(/[\\/]+$/, "");
  const at = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\"));
  return at >= 0 ? trimmed.slice(at + 1) : trimmed;
}

// sessionWallProjectLabel renders a project bucket title: the workspace folder
// name, or the localized "global sessions" label for the shared bucket.
export function sessionWallProjectLabel(s: SessionMeta, globalLabel: string): string {
  const root = s.workspaceRoot?.trim();
  return root ? basename(root) : globalLabel;
}

function startOfDay(ms: number): number {
  const d = new Date(ms);
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}

// groupSessionsForWall groups and ranks sessions for the wall.
//   - "project": one bucket per workspace root (+ one shared global bucket),
//     labeled by folder name, groups ranked by their newest session.
//   - "time": one bucket per local day, labeled via dayLabel (today /
//     yesterday / locale date), groups in descending day order.
// Within every group sessions are sorted by most recent activity. Input order
// does not matter — the sort is total (ties broken by path so the result is
// deterministic).
export function groupSessionsForWall(
  sessions: SessionMeta[],
  mode: SessionWallGroupMode,
  opts: { globalLabel: string; dayLabel: (ms: number) => string },
): SessionWallGroup[] {
  const buckets = new Map<string, SessionMeta[]>();
  for (const s of sessions) {
    const key = mode === "project" ? sessionWallProjectKey(s) : String(startOfDay(sessionActivityTime(s)));
    const list = buckets.get(key);
    if (list) list.push(s);
    else buckets.set(key, [s]);
  }
  const groups: SessionWallGroup[] = [];
  for (const [key, list] of buckets) {
    list.sort((a, b) => sessionActivityTime(b) - sessionActivityTime(a) || (a.path < b.path ? -1 : a.path > b.path ? 1 : 0));
    const newest = sessionActivityTime(list[0]);
    const label =
      mode === "project"
        ? sessionWallProjectLabel(list[0], opts.globalLabel)
        : opts.dayLabel(newest);
    groups.push({ key, label, sessions: list });
  }
  groups.sort((a, b) => sessionActivityTime(b.sessions[0]) - sessionActivityTime(a.sessions[0]));
  return groups;
}

// insertSessionWallEntry is the palette gate. Off (the default): the input
// array is returned as-is — same reference, byte-for-byte identical palette.
// On: a copy with `entry` inserted right after the "cmd-reload-runtime" chip,
// so in the palette's command grid the new entry renders to its right. When
// the anchor is missing (upstream reshuffles the command list), the entry
// appends instead of vanishing.
export function insertSessionWallEntry<T extends { id: string }>(cmds: T[], enabled: boolean, entry: T): T[] {
  if (!enabled) return cmds;
  const at = cmds.findIndex((c) => c.id === "cmd-reload-runtime");
  if (at < 0) return [...cmds, entry];
  const out = cmds.slice();
  out.splice(at + 1, 0, entry);
  return out;
}
