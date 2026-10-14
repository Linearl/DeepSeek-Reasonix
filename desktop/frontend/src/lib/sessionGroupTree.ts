// Task 350 / Task 892-B — session group hierarchy helpers.
//
// 层级≠指挥权 (the design rule fixed 2026-09-28): the parent pointer is a
// DISPLAY organization only. Nothing in here may gate permissions, addressing,
// or delivery — group ids stay flat, and every helper below mutates nothing but
// the `parent` field. The backend mirrors this with validateSessionGroupHierarchy
// (max depth SESSION_GROUP_MAX_DEPTH, cycle rejection, unknown-parent rejection).
import type { SessionGroup } from "./types";

/** The furthest nesting the tree allows. Task 892-B aligns the frontend with
 * the backend's maxSessionGroupDepth: the task-350 depth-2 cap is gone (arbitrary
 * nesting), this bound is only the sanity ceiling against pathological data —
 * cycles are rejected by the ancestor walk, not by this number. */
export const SESSION_GROUP_MAX_DEPTH = 64;

function parentOf(group: SessionGroup | undefined): string {
  const parent = (group?.parent ?? "").trim();
  // A pointer at a missing group renders as top-level (defensive against a
  // concurrently dissolved parent — the backend rejects the stale save).
  return parent || "";
}

/** True when `id` resolves to a group on screen. */
export function isKnownGroup(groups: SessionGroup[], id: string): boolean {
  return groups.some((group) => group.id === id);
}

/** Groups rendered at the top level: no parent, or a parent that is gone. */
export function topLevelGroups(groups: SessionGroup[]): SessionGroup[] {
  const known = new Set(groups.map((group) => group.id));
  return groups.filter((group) => {
    const parent = parentOf(group);
    return parent === "" || !known.has(parent);
  });
}

/** Child groups nested under `parentID`, in roster order. */
export function childGroups(groups: SessionGroup[], parentID: string): SessionGroup[] {
  return groups.filter((group) => parentOf(group) === parentID);
}

/** All descendant group ids of `id`, any depth (task-892: the depth-2 cap is
 * gone). Breadth-first in roster order, cycle-guarded — a malformed pointer
 * loop terminates instead of hanging the walk. */
export function descendantGroupIDs(groups: SessionGroup[], id: string): string[] {
  const out: string[] = [];
  const seen = new Set<string>([id]);
  const queue = childGroups(groups, id).map((group) => group.id);
  while (queue.length > 0) {
    const next = queue.shift()!;
    if (seen.has(next)) continue;
    seen.add(next);
    out.push(next);
    queue.push(...childGroups(groups, next).map((group) => group.id));
  }
  return out;
}

/** Ancestor link count from `id` up to its root (0 = top level). `null` when
 * the chain is broken (a parent pointer at a missing group) or loops — the
 * backend rejects both on save, so such a group cannot legally be the parent
 * side of a nest either. */
export function groupDepth(groups: SessionGroup[], id: string): number | null {
  const byID = new Map(groups.map((group) => [group.id, group]));
  const seen = new Set<string>();
  let current = id;
  let depth = 0;
  for (;;) {
    const group = byID.get(current);
    if (!group) return null;
    const parent = parentOf(group);
    if (parent === "") return depth;
    if (seen.has(parent) || !byID.has(parent)) return null;
    seen.add(current);
    current = parent;
    depth += 1;
  }
}

/** Longest chain (in links) from `id` down to a leaf of its own subtree.
 * Cycle-guarded: a malformed loop contributes 0 rather than hanging. */
export function subtreeHeight(groups: SessionGroup[], id: string): number {
  const memo = new Map<string, number>();
  const visit = (current: string, stack: Set<string>): number => {
    const cached = memo.get(current);
    if (cached !== undefined) return cached;
    if (stack.has(current)) return 0;
    stack.add(current);
    let height = 0;
    for (const child of childGroups(groups, current)) {
      height = Math.max(height, 1 + visit(child.id, stack));
    }
    stack.delete(current);
    memo.set(current, height);
    return height;
  };
  return visit(id, new Set());
}

/** Every member topic id of `id` plus its whole descendant subtree — the
 * inherited set a collapsed header aggregates its activity dot from. Flat and
 * leaf groups are unchanged — the inherited set equals the own set. */
export function inheritedMemberIDs(groups: SessionGroup[], id: string): string[] {
  const byID = new Map(groups.map((group) => [group.id, group]));
  const own = byID.get(id);
  const out = [...(own?.topicIds ?? [])];
  for (const descendantID of descendantGroupIDs(groups, id)) {
    out.push(...(byID.get(descendantID)?.topicIds ?? []));
  }
  return out;
}

/** Nesting `childID` under `parentID` is allowed only when:
 *  - neither id is missing and they differ;
 *  - `parentID` is not `childID` itself or inside its subtree (cycle — a nest
 *    that would close a loop is rejected before any save);
 *  - `parentID`'s own ancestor chain is intact (backend rejects broken chains);
 *  - the resulting deepest chain stays within SESSION_GROUP_MAX_DEPTH.
 * The hierarchy never gates anything else. */
export function canNestUnder(groups: SessionGroup[], childID: string, parentID: string): boolean {
  if (childID === parentID) return false;
  if (!isKnownGroup(groups, childID) || !isKnownGroup(groups, parentID)) return false;
  if (descendantGroupIDs(groups, childID).includes(parentID)) return false;
  const parentDepth = groupDepth(groups, parentID);
  if (parentDepth === null) return false;
  return parentDepth + 1 + subtreeHeight(groups, childID) <= SESSION_GROUP_MAX_DEPTH;
}

/** Eligible nest targets for `id` — every group the nest would be legal under
 * (any depth, cycle- and broken-chain-safe), including `id`'s current parent
 * (the pure helper says "legal", the menu filters out the no-op re-nest).
 * Orphaned groups are excluded: the backend rejects them as parents. */
export function nestTargets(groups: SessionGroup[], id: string): SessionGroup[] {
  return groups.filter((group) => canNestUnder(groups, id, group.id));
}

/** nestGroup returns the roster with the parent pointer set; everything else
 * about the group is preserved bit for bit. */
export function nestGroup(groups: SessionGroup[], childID: string, parentID: string): SessionGroup[] {
  if (!canNestUnder(groups, childID, parentID)) return groups;
  return groups.map((group) => group.id === childID ? { ...group, parent: parentID } : group);
}

/** unnestGroup clears the pointer; the group keeps its id, title and members. */
export function unnestGroup(groups: SessionGroup[], id: string): SessionGroup[] {
  return groups.map((group) => group.id === id ? { ...group, parent: undefined } : group);
}

/** The 层级≠指挥权 guard used by tests: comparing before/after rosters, a
 * hierarchy mutation may only differ in `parent` fields — never in ids,
 * titles, or membership. */
export function hierarchyMutationIsDisplayOnly(
  before: SessionGroup[],
  after: SessionGroup[],
): boolean {
  if (before.length !== after.length) return false;
  const beforeByID = new Map(before.map((group) => [group.id, group]));
  for (const group of after) {
    const was = beforeByID.get(group.id);
    if (!was) return false;
    if (was.title !== group.title) return false;
    if (JSON.stringify(was.topicIds ?? []) !== JSON.stringify(group.topicIds ?? [])) return false;
  }
  return true;
}
