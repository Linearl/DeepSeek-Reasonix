// Task 350 — session group hierarchy helpers.
//
// 层级≠指挥权 (the design rule fixed 2026-09-28): the parent pointer is a
// DISPLAY organization only. Nothing in here may gate permissions, addressing,
// or delivery — group ids stay flat, and every helper below mutates nothing but
// the `parent` field. The backend mirrors this with validateSessionGroupHierarchy
// (max depth 2, unknown-parent rejection).
import type { SessionGroup } from "./types";

/** The furthest nesting the tree allows: top-level group → child group. */
export const SESSION_GROUP_MAX_DEPTH = 2;

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

/** All descendant group ids of `id` (depth ≤ 2, so at most one hop today). */
export function descendantGroupIDs(groups: SessionGroup[], id: string): string[] {
  const out: string[] = [];
  for (const group of groups) {
    if (parentOf(group) === id) out.push(group.id);
  }
  return out;
}

/** Every member topic id of `id` plus its descendants — the inherited set the
 * collapsed parent header aggregates its activity dot from. */
export function inheritedMemberIDs(groups: SessionGroup[], id: string): string[] {
  const own = groups.find((group) => group.id === id);
  const out = [...(own?.topicIds ?? [])];
  for (const child of childGroups(groups, id)) {
    out.push(...(child.topicIds ?? []));
  }
  return out;
}

/** Nesting `childID` under `parentID` is allowed only when the parent is a
 * top-level group, is not the child itself, and the child has no children
 * (depth cap). The hierarchy never gates anything else. */
export function canNestUnder(groups: SessionGroup[], childID: string, parentID: string): boolean {
  if (childID === parentID) return false;
  if (!isKnownGroup(groups, childID) || !isKnownGroup(groups, parentID)) return false;
  const parent = groups.find((group) => group.id === parentID);
  if (parentOf(parent) !== "") return false; // parent must be top-level
  if (childGroups(groups, childID).length > 0) return false; // depth cap
  return true;
}

/** Eligible nest targets for `id` — for the context menu. Orphaned groups
 * (pointer at a dissolved parent) are excluded: the backend would reject them
 * as parents. */
export function nestTargets(groups: SessionGroup[], id: string): SessionGroup[] {
  return topLevelGroups(groups).filter((group) => group.id !== id && parentOf(group) === "");
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
