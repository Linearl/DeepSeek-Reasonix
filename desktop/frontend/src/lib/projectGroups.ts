// Project-level grouping: organize projects into user-defined groups (e.g.
// "Work", "Personal") so different project types are easy to manage (#9222).
// A project belongs to at most one group (single-select). Storage is local-only,
// following the existing project-tree localStorage precedent (WORKBENCH_*_KEY).

export interface ProjectGroup {
  id: string;
  title: string;
  sortOrder: number;
  // Task 892-B ⑤: arbitrary nesting, storage spelling `parent` (A-slice
  // handoff §2.1 — tool input uses parent_id, view/response uses parentId).
  // Absent = top level; legacy flat rosters load without ever growing it.
  parent?: string;
}

const GROUPS_KEY = "projectTree:groups";
const ASSIGN_KEY = "projectTree:projectGroupAssign";
const COLLAPSED_KEY = "projectTree:projectGroupCollapsed";

let groupSeq = 0;
// Exported for 892-B: the catalog write path generates ids frontend-side too
// (CreateProjectGroup takes an explicit groupID), same as the session path.
export function nextProjectGroupID(): string {
  groupSeq += 1;
  return `group-${groupSeq}-${Date.now().toString(36)}`;
}
const nextGroupID = nextProjectGroupID;

function read<T>(key: string): T | null {
  try {
    const raw = localStorage.getItem(key);
    if (!raw) return null;
    return JSON.parse(raw) as T;
  } catch {
    return null;
  }
}

function write(key: string, value: unknown): void {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    /* storage unavailable — grouping degrades to in-memory */
  }
}

export function loadProjectGroups(): ProjectGroup[] {
  const groups = read<ProjectGroup[]>(GROUPS_KEY);
  return Array.isArray(groups) ? groups : [];
}

function persistGroups(groups: ProjectGroup[]): void {
  write(GROUPS_KEY, groups);
}

export function loadProjectGroupAssign(): Record<string, string> {
  const assign = read<Record<string, string>>(ASSIGN_KEY);
  return assign && typeof assign === "object" ? assign : {};
}

function persistAssign(assign: Record<string, string>): void {
  write(ASSIGN_KEY, assign);
}

// loadProjectGroupCollapsed restores the per-group expanded/collapsed state
// across app restarts and project-tree remounts. Only collapsed ids are
// persisted, so groups created later default to expanded. Local-only, same
// precedent as the groups/assign keys.
export function loadProjectGroupCollapsed(): ReadonlySet<string> {
  const raw = read<string[]>(COLLAPSED_KEY);
  return new Set(Array.isArray(raw) ? raw : []);
}

export function persistProjectGroupCollapsed(ids: ReadonlySet<string>): void {
  write(COLLAPSED_KEY, [...ids]);
}

// Session groups (#8194 family, ProjectTreeOrganization) carry their own
// collapse state keyed by `${organizationKey}|${groupId}`. It used to live
// only in component memory, so every reopen auto-expanded every group —
// persist it exactly like the project-group collapse above.
const SESSION_GROUP_COLLAPSED_KEY = "projectTree:sessionGroupCollapsed";

export function loadSessionGroupCollapsed(): ReadonlySet<string> {
  const raw = read<string[]>(SESSION_GROUP_COLLAPSED_KEY);
  return new Set(Array.isArray(raw) ? raw : []);
}

export function persistSessionGroupCollapsed(ids: ReadonlySet<string>): void {
  write(SESSION_GROUP_COLLAPSED_KEY, [...ids]);
}

// dropProjectGroupCollapsed removes a deleted group's collapsed marker so the
// persisted set never accumulates stale ids.
export function dropProjectGroupCollapsed(ids: ReadonlySet<string>, id: string): ReadonlySet<string> {
  if (!ids.has(id)) return ids;
  const next = new Set(ids);
  next.delete(id);
  persistProjectGroupCollapsed(next);
  return next;
}

export function addProjectGroup(title: string, groups: ProjectGroup[], parent?: string): { groups: ProjectGroup[]; group: ProjectGroup } {
  const next = [...groups];
  const order = next.reduce((max, g) => Math.max(max, g.sortOrder), -1) + 1;
  // parent is only set when given, so the legacy flat roster never grows a
  // `parent` key (A-slice migration compatibility, handoff §1.4).
  const group: ProjectGroup = { id: nextGroupID(), title: title.trim() || "Group", sortOrder: order, ...(parent ? { parent } : {}) };
  next.push(group);
  persistGroups(next);
  return { groups: next, group };
}

export function renameProjectGroup(groups: ProjectGroup[], id: string, title: string): ProjectGroup[] {
  const next = groups.map((g) => (g.id === id ? { ...g, title: title.trim() || g.title } : g));
  persistGroups(next);
  return next;
}

// deleteProjectGroup removes the group and its project assignments. If
// deleteProjects is true the caller is expected to also remove the projects;
// here we only clear the assignment (projects return to "ungrouped").
export function deleteProjectGroup(
  groups: ProjectGroup[],
  assign: Record<string, string>,
  id: string,
): { groups: ProjectGroup[]; assign: Record<string, string> } {
  const nextGroups = groups.filter((g) => g.id !== id);
  persistGroups(nextGroups);
  const nextAssign: Record<string, string> = {};
  for (const [root, groupId] of Object.entries(assign)) {
    if (groupId !== id) nextAssign[root] = groupId;
  }
  persistAssign(nextAssign);
  return { groups: nextGroups, assign: nextAssign };
}

// Task 827 ④: reorderProjectGroups moves one group before/after another and
// rewrites sortOrder to 0..n-1 so the field stays a dense ranking. The dropped
// order is persisted to the same GROUPS_KEY immediately, so a refresh or an
// app restart reads it back (acceptance ④). A drag whose endpoints vanished
// (another window dissolved a group mid-drag) is a no-op, mirroring the
// session-group moveGroup behaviour.
export function reorderProjectGroups(
  groups: ProjectGroup[],
  draggedId: string,
  targetId: string,
  position: "before" | "after",
): ProjectGroup[] {
  if (draggedId === targetId) return groups;
  const from = groups.findIndex((g) => g.id === draggedId);
  const to = groups.findIndex((g) => g.id === targetId);
  if (from < 0 || to < 0) return groups;
  const next = groups.slice();
  const [moved] = next.splice(from, 1);
  const targetIndex = next.findIndex((g) => g.id === targetId);
  next.splice(position === "before" ? targetIndex : targetIndex + 1, 0, moved);
  const ordered = next.map((g, index) => (g.sortOrder === index ? g : { ...g, sortOrder: index }));
  persistGroups(ordered);
  return ordered;
}

// moveProjectToGroup assigns a project root to a single group (pass null to
// ungroup). Single-select semantics, per #9222.
export function moveProjectToGroup(
  assign: Record<string, string>,
  root: string,
  groupId: string | null,
): Record<string, string> {
  const next = { ...assign };
  if (groupId === null) {
    delete next[root];
  } else {
    next[root] = groupId;
  }
  persistAssign(next);
  return next;
}

export function groupForProject(assign: Record<string, string>, root?: string): string | null {
  if (!root) return null;
  return assign[root] ?? null;
}

// Task 892-B ⑤: after a catalog pull the local roster shadow is cleared — the
// backend is the source of truth from then on, and a leftover copy would
// resurrect deleted groups the next time the catalog comes back empty.
export function clearProjectGroupRoster(): void {
  persistGroups([]);
}

// pruneProjectGroupAssign drops assignments whose group id left the roster
// (deleted directly, or cascade-deleted as a child). Same-reference when
// nothing changed so setState can skip the render; persists when it did.
export function pruneProjectGroupAssign(
  assign: Record<string, string>,
  validIDs: ReadonlySet<string>,
): Record<string, string> {
  const next: Record<string, string> = {};
  let changed = false;
  for (const [root, groupId] of Object.entries(assign)) {
    if (validIDs.has(groupId)) next[root] = groupId;
    else changed = true;
  }
  if (!changed) return assign;
  persistAssign(next);
  return next;
}
