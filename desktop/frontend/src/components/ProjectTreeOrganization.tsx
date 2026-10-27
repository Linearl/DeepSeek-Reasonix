import { useCallback, useEffect, useRef, useState, lazy, Suspense, type DragEvent, type HTMLAttributes, type ReactNode } from "react";
import { CheckCheck, Eraser, FolderMinus, FolderPlus, GripVertical, Pencil, Trash2 } from "lucide-react";
import { app } from "../lib/bridge";
import { asArray } from "../lib/array";
import type { Translator } from "../lib/i18n";
import { useConfirmDialog } from "./ConfirmDialog";
import { isTopicNode, projectTreeGroupActiveCount, projectTreeGroupDotStatus, projectTreeTopicArchiveBlocked } from "../lib/projectTreeTopic";
import { loadSessionGroupCollapsed, persistSessionGroupCollapsed } from "../lib/projectGroups";
import { canNestUnder, childGroups, inheritedMemberIDs, nestGroup as nestInRoster, nestTargetRows, placeGroup, topLevelGroups, unnestGroup as unnestInRoster, type GroupDropZone } from "../lib/sessionGroupTree";
import type { ProjectTreeRefresh } from "../lib/projectTreeArchive";
import type { DeleteGroupsPlan, ProjectNode, ProjectTreeOrganizationBindings, SessionGroup } from "../lib/types";
import { ContextMenu, contextMenuPointFromEvent, type ContextMenuItem, type ContextMenuPoint } from "./ContextMenu";
import { reportFrontendLog } from "../lib/frontendLog";

export type ProjectDropPosition = "before" | "after";

export const GLOBAL_PROJECT_ORDER_KEY = "__global__";
const TOPIC_DRAG_TYPE = "application/x-reasonix-topic-id";

function projectOrderKey(node: ProjectNode): string {
  if (node.kind === "global_folder") return GLOBAL_PROJECT_ORDER_KEY;
  if (node.kind === "project" && node.root) return node.root;
  return "";
}

export function projectTreeProjectRoots(nodes: ProjectNode[]): string[] {
  return nodes.map(projectOrderKey).filter((key) => key !== "");
}

export function reorderedProjectRoots(
  nodes: ProjectNode[],
  draggedRoot: string,
  targetRoot: string,
  position: ProjectDropPosition,
): string[] {
  const roots = projectTreeProjectRoots(nodes);
  if (draggedRoot === targetRoot || !roots.includes(draggedRoot) || !roots.includes(targetRoot)) return roots;
  const next = roots.filter((root) => root !== draggedRoot);
  const targetIndex = next.indexOf(targetRoot);
  if (targetIndex < 0) return roots;
  next.splice(position === "before" ? targetIndex : targetIndex + 1, 0, draggedRoot);
  return next;
}

export function applyProjectOrder(nodes: ProjectNode[], roots: string[]): ProjectNode[] {
  const entries = nodes.map((node): [string, ProjectNode] => [projectOrderKey(node), node]).filter(([key]) => key !== "");
  const byRoot = new Map(entries);
  const ordered = roots.map((root) => byRoot.get(root)).filter((node): node is ProjectNode => Boolean(node));
  const orderedKeys = new Set(roots);
  return [...nodes.filter((node) => !orderedKeys.has(projectOrderKey(node))), ...ordered];
}

export function manualTopicOrder(a: ProjectNode, b: ProjectNode): number {
  const aOrder = typeof a.sortOrder === "number" && a.sortOrder >= 0 ? a.sortOrder : Number.MAX_SAFE_INTEGER;
  const bOrder = typeof b.sortOrder === "number" && b.sortOrder >= 0 ? b.sortOrder : Number.MAX_SAFE_INTEGER;
  return aOrder === bOrder ? 0 : aOrder - bOrder;
}

export function projectTreeOrganizationKey(node: ProjectNode): string {
  return node.kind === "global_folder" || node.kind === "global_topic" ? "global|" : `project|${node.root ?? ""}`;
}

function splitOrganizationKey(key: string): { scope: "global" | "project"; root: string } {
  return key === "global|" ? { scope: "global", root: "" } : { scope: "project", root: key.slice("project|".length) };
}

export function reorderedTopicIDs(
  nodes: ProjectNode[],
  scope: "global" | "project",
  root: string,
  draggedID: string,
  targetID: string,
  position: ProjectDropPosition,
): string[] | null {
  const parent = nodes.find((node) => projectTreeOrganizationKey(node) === (scope === "global" ? "global|" : `project|${root}`));
  if (!parent) return null;
  const ids = asArray(parent.children)
    .filter((node) => isTopicNode(node) && !node.runtimeOnly && node.topicId)
    .map((node) => node.topicId as string);
  if (draggedID === targetID || !ids.includes(draggedID) || !ids.includes(targetID)) return null;
  const rest = ids.filter((id) => id !== draggedID);
  const targetIndex = rest.indexOf(targetID);
  const insertAt = position === "before" ? targetIndex : targetIndex + 1;
  return [...rest.slice(0, insertAt), draggedID, ...rest.slice(insertAt)];
}

/** Task 950: the same-before/after splice as reorderedTopicIDs, but against a
 * group's own member order (group.topicIds) — the order the group actually
 * renders. Returns the target group id, the insertion index for the backend
 * ReorderTopicInGroup (remove-then-insert, Go clamps stale indexes), and the
 * full reordered member list for the optimistic paint. Null when dragged and
 * target do not share a group: cross-group and top-level drops keep the
 * existing semantics (moveTopicInRoster append / flat ReorderTopics). */
export function reorderedGroupTopicIDs(
  groups: SessionGroup[],
  draggedID: string,
  targetID: string,
  position: ProjectDropPosition,
): { groupID: string; index: number; topicIds: string[] } | null {
  if (!draggedID || !targetID || draggedID === targetID) return null;
  for (const group of groups) {
    const ids = group.topicIds ?? [];
    if (!ids.includes(draggedID) || !ids.includes(targetID)) continue;
    const rest = ids.filter((id) => id !== draggedID);
    const targetIndex = rest.indexOf(targetID);
    if (targetIndex < 0) return null;
    const insertAt = position === "before" ? targetIndex : targetIndex + 1;
    return { groupID: group.id, index: insertAt, topicIds: [...rest.slice(0, insertAt), draggedID, ...rest.slice(insertAt)] };
  }
  return null;
}

/** Task 950: a group renders its members in the persisted topicIds order —
 * the order ReorderTopicInGroup writes and moveTopicInRoster appends to.
 * Without this the member rows follow the flat children order (project
 * sortOrder/activity) and a group-internal reorder lands on disk but never
 * paints. Members absent from topicIds keep the incoming order at the tail
 * (they cannot occur through the filtered ownMembers set today, but a stale
 * roster must not drop rows). */
export function orderedGroupMembers<T>(members: T[], topicIds: string[] | undefined, idOf: (member: T) => string): T[] {
  const rank = new Map((topicIds ?? []).map((id, index) => [id, index]));
  return members
    .map((member, index): [T, number, number] => [member, rank.get(idOf(member)) ?? Number.MAX_SAFE_INTEGER, index])
    .sort((a, b) => (a[1] - b[1]) || (a[2] - b[2]))
    .map(([member]) => member);
}

type TopicRowDragProps = Pick<HTMLAttributes<HTMLDivElement>, "draggable" | "onDragStart" | "onDragOver" | "onDragLeave" | "onDrop" | "onDragEnd">;

// Task 720: the one move semantics for a topic — leave every group it is listed
// in and join the target (the roster's single-group constraint, task 170). Both
// entry points run this same closure, a drag drop onto a group header and the
// topic context menu's "move to group", so the two cannot diverge.
export function moveTopicInRoster(groups: SessionGroup[], topicID: string, groupID: string): SessionGroup[] {
  return groups.map((group) => {
    const withoutTopic = (group.topicIds ?? []).filter((id) => id !== topicID);
    return group.id === groupID ? { ...group, topicIds: [...withoutTopic, topicID] } : { ...group, topicIds: withoutTopic };
  });
}

export interface ProjectTreeOrganizationController {
  topicRow(node: ProjectNode, disabled: boolean): { className: string; props: TopicRowDragProps };
  topicMenuItems(node: ProjectNode, t: Translator): ContextMenuItem[];
  createGroup(folder: ProjectNode, title: string): void;
  /** createSubGroup appends a child group under parentID (task 892-B). With the
   * A-slice CreateSessionGroup binding the write goes through the backend
   * (server validation + op-log + reason=metadata v2 event); older mocks fall
   * back to the CAS roster append with the `parent` field set. */
  createSubGroup(folder: ProjectNode, parentID: string, title: string): void;
  groupsFor(folder: ProjectNode): SessionGroup[];
  groupCollapsed(key: string, id: string): boolean;
  toggleGroup(key: string, id: string): void;
  renameGroup(key: string, id: string, title: string): void;
  /** dropGroup applies a group drag release (task 50 flat reorder generalized
   * by 892-B multi-level semantics): `into` appends the dragged group at the
   * target's subtree tail, `before`/`after` joins the target's own level.
   * Cycle-safe through the pure placeGroup — an illegal drop is a no-op. */
  dropGroup(key: string, fromID: string, toID: string, zone: GroupDropZone): void;
  /** nestGroup moves a group under another (parentID) or to the top level
   * (null) — task 350, generalized by 892-B to arbitrary depth. Display-only:
   * the hierarchy never touches ids, membership, or permissions (层级≠指挥权);
   * the backend accepts any chain ≤ 64 and rejects cycles. */
  nestGroup(key: string, childID: string, parentID: string | null): void;
  /** clearGroup keeps the group shell and drops its sessions (task 17). */
  clearGroup(key: string, id: string): void;
  /** dissolveGroup removes the shell; its sessions return to ungrouped. This is
   * what the old deleteGroup always did - the label was simply wrong. */
  dissolveGroup(key: string, id: string): void;
  /** deleteGroupPropose runs the A-slice group_delete proposal (confirm=false,
   * zero write) and returns the plan — null when the binding is absent (older
   * backend/mock: caller falls back to the plain CAS dissolve). */
  deleteGroupPropose(key: string, groupID: string): Promise<DeleteGroupsPlan | null>;
  /** deleteGroupExecute commits the confirmed deletion (confirm=true). Cascade
   * deletes the whole subtree; otherwise children are promoted and the group's
   * sessions are only unfiled. Falls back to the CAS dissolve without the
   * A-slice binding. */
  deleteGroupExecute(key: string, groupID: string, cascade: boolean): Promise<void>;
  canDropTopicInto(key: string): boolean;
  dropTopicInto(key: string, groupID: string): void;
  /** moveTopicToGroup files a topic into an existing group from the topic
   * context menu (task 720). Same mutation as a drag drop — the topic leaves
   * every other group and joins the target — persisted through the same
   * SaveSessionGroupsVersioned CAS chain, so the two entry points can never
   * fight over the roster (task 170's single-group constraint). */
  moveTopicToGroup(key: string, topicID: string, groupID: string): void;
}

export function useProjectTreeOrganization({
  tree,
  refresh,
  onTopicsChanged,
  organizationRevision = 0,
  bindings = app,
  onLocalTopicReorder,
}: {
  tree: ProjectNode[];
  refresh: ProjectTreeRefresh;
  onTopicsChanged?: () => Promise<void> | void;
  organizationRevision?: number;
  bindings?: ProjectTreeOrganizationBindings;
  /** Optimistic local reorder: the topics page is signature-cached, so without
   *  this the dropped order only lands after some later unrelated reload. */
  onLocalTopicReorder?: (scope: "global" | "project", workspaceRoot: string, orderedTopicIDs: string[]) => void;
}): ProjectTreeOrganizationController {
  const [dragTopicID, setDragTopicID] = useState<string | null>(null);
  const [dropTopic, setDropTopic] = useState<{ topicID: string; position: ProjectDropPosition } | null>(null);
  const dragContextRef = useRef<{ scope: "global" | "project"; root: string } | null>(null);
  const [groupsByKey, setGroupsByKey] = useState<Record<string, SessionGroup[]>>({});
  const groupsRef = useRef(groupsByKey);
  const mountedRef = useRef(false);
  const loadedGroupsRef = useRef(new Set<string>());
  const loadingGroupsRef = useRef(new Set<string>());
  const groupMutationVersionsRef = useRef<Record<string, number>>({});
  const groupLoadSequencesRef = useRef<Record<string, number>>({});
  const groupSaveChainsRef = useRef(new Map<string, Promise<void>>());
  /** Task 950: in-flight direct A-slice writes (ReorderTopicInGroup) per key.
   * loadGroups refuses to apply a snapshot taken while one is pending — the
   * read raced the write and carries the pre-write order (the 981 event-flow
   * reset family). The settle path re-pulls after clearing the count. */
  const groupDirectWritesRef = useRef<Record<string, number>>({});
  const organizationRevisionRef = useRef(organizationRevision);
  const [collapsedGroups, setCollapsedGroups] = useState<ReadonlySet<string>>(loadSessionGroupCollapsed);

  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; };
  }, []);

  const setKeyGroups = useCallback((key: string, groups: SessionGroup[]) => {
    groupsRef.current = { ...groupsRef.current, [key]: groups };
    setGroupsByKey(groupsRef.current);
  }, []);

  const loadGroups = useCallback((key: string, force = false) => {
    if (!force && (loadedGroupsRef.current.has(key) || loadingGroupsRef.current.has(key))) return;
    const sequence = (groupLoadSequencesRef.current[key] ?? 0) + 1;
    groupLoadSequencesRef.current[key] = sequence;
    const mutationVersion = groupMutationVersionsRef.current[key] ?? 0;
    loadingGroupsRef.current.add(key);
    const { scope, root } = splitOrganizationKey(key);
    const read = typeof bindings.GetProjectGroups === "function"
      ? bindings.GetProjectGroups(scope, root)
      : bindings.ListProjectGroups(scope, root).then((groups) => ({ groups, revision: 0, applied: true }));
    void read.then((snapshot) => {
      if (!mountedRef.current || groupLoadSequencesRef.current[key] !== sequence) return;
      if ((groupMutationVersionsRef.current[key] ?? 0) !== mutationVersion) return;
      // Never replace an optimistic state while its semantic mutations are
      // queued. The CAS path reads the newest server snapshot before applying.
      if (groupSaveChainsRef.current.has(key)) return;
      // Task 950: same refusal while a direct ReorderTopicInGroup is in
      // flight — the snapshot may predate the write (event-flow reset guard).
      if ((groupDirectWritesRef.current[key] ?? 0) > 0) return;
      loadedGroupsRef.current.add(key);
      setKeyGroups(key, asArray(snapshot.groups));
    }).catch(() => {}).finally(() => {
      if (groupLoadSequencesRef.current[key] === sequence) loadingGroupsRef.current.delete(key);
    });
  }, [bindings, setKeyGroups]);

  useEffect(() => {
    const force = organizationRevisionRef.current !== organizationRevision;
    organizationRevisionRef.current = organizationRevision;
    for (const folder of tree) {
      if (folder.kind !== "project" && folder.kind !== "global_folder") continue;
      const key = projectTreeOrganizationKey(folder);
      loadGroups(key, force);
    }
  }, [loadGroups, organizationRevision, tree]);

  const persistGroups = useCallback((key: string, update: (groups: SessionGroup[]) => SessionGroup[], legacyGroups: SessionGroup[]) => {
    const version = groupMutationVersionsRef.current[key] ?? 0;
    const { scope, root } = splitOrganizationKey(key);
    const previous = groupSaveChainsRef.current.get(key) ?? Promise.resolve();
    let settledGroups: SessionGroup[] | null = null;
    const pending = previous.catch(() => {}).then(async () => {
      if (typeof bindings.GetProjectGroups !== "function" || typeof bindings.SaveSessionGroupsVersioned !== "function") {
        await bindings.SaveSessionGroups(scope, root, legacyGroups);
        return;
      }
      for (let attempt = 0; attempt < 5; attempt++) {
        const current = await bindings.GetProjectGroups(scope, root);
        const next = update(asArray(current.groups));
        const saved = await bindings.SaveSessionGroupsVersioned(scope, root, current.revision, next);
        if (saved.applied) {
          settledGroups = asArray(saved.groups);
          return;
        }
      }
      throw new Error("session groups changed too frequently; retrying from server state");
    });
    groupSaveChainsRef.current.set(key, pending);
    void pending.then(() => {
      if (mountedRef.current && (groupMutationVersionsRef.current[key] ?? 0) === version && settledGroups) {
        setKeyGroups(key, settledGroups);
      }
    }).catch(() => {
      if ((groupMutationVersionsRef.current[key] ?? 0) !== version) return;
      if (groupSaveChainsRef.current.get(key) === pending) groupSaveChainsRef.current.delete(key);
      loadedGroupsRef.current.delete(key);
      loadGroups(key, true);
    }).finally(() => {
      if (groupSaveChainsRef.current.get(key) === pending) groupSaveChainsRef.current.delete(key);
    });
  }, [bindings, loadGroups, setKeyGroups]);

  const mutateGroups = useCallback((key: string, update: (groups: SessionGroup[]) => SessionGroup[]) => {
    const next = update(groupsRef.current[key] ?? []);
    groupMutationVersionsRef.current[key] = (groupMutationVersionsRef.current[key] ?? 0) + 1;
    loadedGroupsRef.current.add(key);
    setKeyGroups(key, next);
    persistGroups(key, update, next);
  }, [persistGroups, setKeyGroups]);

  /** Task 950: the group-internal topic reorder the flat ReorderTopics chain
   * cannot express. Optimistic paint first (the member rows re-render in the
   * dropped order at once), then the direct ReorderTopicInGroup write — the
   * backend moves the member in its own TopicIDs order and logs a
   * reorder_session op. Both settle paths (success and rejection) re-pull the
   * server truth, so a rejected write never leaves a phantom order on screen
   * and a metadata re-pull after the write reads back the persisted order
   * (no event-flow reset — the 981 family). Older mocks without the binding
   * fall back to the same versioned CAS chain every other roster mutation
   * uses, so the browser dev surface stays functional. */
  const reorderTopicWithinGroup = useCallback((key: string, scope: "global" | "project", root: string, topicID: string, order: { groupID: string; index: number; topicIds: string[] }) => {
    groupMutationVersionsRef.current[key] = (groupMutationVersionsRef.current[key] ?? 0) + 1;
    loadedGroupsRef.current.add(key);
    const applyOrder = (groups: SessionGroup[]) => groups.map((group) => group.id === order.groupID ? { ...group, topicIds: order.topicIds } : group);
    const next = applyOrder(groupsRef.current[key] ?? []);
    setKeyGroups(key, next);
    if (typeof bindings.ReorderTopicInGroup !== "function") {
      persistGroups(key, applyOrder, next);
      return;
    }
    groupDirectWritesRef.current[key] = (groupDirectWritesRef.current[key] ?? 0) + 1;
    const settle = () => {
      groupDirectWritesRef.current[key] = Math.max(0, (groupDirectWritesRef.current[key] ?? 1) - 1);
      // Write landed (or failed): the next snapshot is post-write truth, so
      // the re-pull reads back the persisted order instead of repainting old.
      loadGroups(key, true);
    };
    void bindings.ReorderTopicInGroup(scope, root, topicID, order.groupID, order.index).then(settle).catch((error) => {
      reportFrontendLog("project-groups", "reorder topic in group failed", `group=${order.groupID} error=${String(error)}`);
      settle();
    });
  }, [bindings, loadGroups, persistGroups, setKeyGroups]);

  const clearTopicDrag = useCallback(() => {
    dragContextRef.current = null;
    setDragTopicID(null);
    setDropTopic(null);
  }, []);

  useEffect(() => {
    if (!dragTopicID) return;
    window.addEventListener("dragend", clearTopicDrag);
    window.addEventListener("drop", clearTopicDrag);
    window.addEventListener("blur", clearTopicDrag);
    return () => {
      window.removeEventListener("dragend", clearTopicDrag);
      window.removeEventListener("drop", clearTopicDrag);
      window.removeEventListener("blur", clearTopicDrag);
    };
  }, [clearTopicDrag, dragTopicID]);

  const topicRow = useCallback((node: ProjectNode, disabled: boolean) => {
    const topicID = node.topicId ?? "";
    const key = projectTreeOrganizationKey(node);
    const draggable = !disabled && !node.runtimeOnly && topicID !== "";
    const sameScope = dragContextRef.current !== null && key === (dragContextRef.current.scope === "global" ? "global|" : `project|${dragContextRef.current.root}`);
    const className = sameScope && dropTopic?.topicID === topicID && dragTopicID !== topicID ? ` project-tree__topic--drop-${dropTopic.position}` : "";
    const props: TopicRowDragProps = { draggable };
    if (!draggable) return { className, props };
    props.onDragStart = (event) => {
      const context = splitOrganizationKey(key);
      dragContextRef.current = context;
      event.dataTransfer.setData(TOPIC_DRAG_TYPE, topicID);
      event.dataTransfer.setData("text/plain", topicID);
      event.dataTransfer.effectAllowed = "move";
      setDragTopicID(topicID);
    };
    props.onDragOver = (event) => {
      if (!sameScope || !dragTopicID || dragTopicID === topicID) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = "move";
      const rect = event.currentTarget.getBoundingClientRect();
      const position = event.clientY < rect.top + rect.height / 2 ? "before" : "after";
      setDropTopic((current) => current?.topicID === topicID && current.position === position ? current : { topicID, position });
    };
    props.onDragLeave = () => setDropTopic((current) => current?.topicID === topicID ? null : current);
    props.onDrop = (event: DragEvent<HTMLDivElement>) => {
      event.preventDefault();
      const draggedID = event.dataTransfer.getData(TOPIC_DRAG_TYPE) || dragTopicID;
      const context = dragContextRef.current;
      if (draggedID && context && sameScope) {
        const rect = event.currentTarget.getBoundingClientRect();
        const position = event.clientY < rect.top + rect.height / 2 ? "before" : "after";
        // Task 950: a drop inside one group's members reorders that group's
        // own topicIds (the order the group renders) through the direct
        // ReorderTopicInGroup write — the flat ReorderTopics chain below only
        // rewrites top-level sortOrder and silently dropped this case.
        const groupOrder = reorderedGroupTopicIDs(groupsRef.current[key] ?? [], draggedID, topicID, position);
        if (groupOrder) {
          reorderTopicWithinGroup(key, context.scope, context.root, draggedID, groupOrder);
        } else {
          const ordered = reorderedTopicIDs(tree, context.scope, context.root, draggedID, topicID, position);
          if (ordered) {
            // Paint the dropped order immediately: the topics page reload is
            // signature-cached and would otherwise keep the stale order on
            // screen until some unrelated refresh happened to pull it.
            onLocalTopicReorder?.(context.scope, context.root, ordered);
            void bindings.ReorderTopics(context.scope, context.root, ordered).then(() => refresh()).then(() => onTopicsChanged?.()).catch(() => refresh());
          }
        }
      }
      clearTopicDrag();
    };
    props.onDragEnd = clearTopicDrag;
    return { className, props };
  }, [bindings, clearTopicDrag, dragTopicID, dropTopic, onLocalTopicReorder, onTopicsChanged, refresh, reorderTopicWithinGroup, tree]);

  const removeTopicFromGroups = useCallback((node: ProjectNode) => {
    const topicID = node.topicId;
    if (!topicID) return;
    const key = projectTreeOrganizationKey(node);
    mutateGroups(key, (groups) => groups.map((group) => ({ ...group, topicIds: (group.topicIds ?? []).filter((id) => id !== topicID) })));
  }, [mutateGroups]);

  const moveTopicIntoGroup = useCallback((key: string, topicID: string, groupID: string) => {
    if (!topicID || !groupID) return;
    // The target may have been dissolved between the menu render and the
    // click; leave the roster alone rather than round-tripping a no-op save.
    if (!(groupsRef.current[key] ?? []).some((group) => group.id === groupID)) return;
    mutateGroups(key, (groups) => moveTopicInRoster(groups, topicID, groupID));
  }, [mutateGroups]);

  return {
    topicRow,
    topicMenuItems(node, t) {
      const topicID = node.topicId;
      if (!topicID) return [];
      const key = projectTreeOrganizationKey(node);
      const groups = groupsRef.current[key] ?? [];
      const ownGroups = new Set(groups.filter((group) => group.topicIds?.includes(topicID)).map((group) => group.id));
      // Task 720: right-click move — picking a group runs the same full-roster
      // move as a drag drop. The group the topic already sits in is disabled,
      // and the standalone "remove from group" row (task 144) stays the way
      // out.
      // tl 1149b 返修: these rows render inside ProjectTree's 「分组 ▸」
      // second-level section (tl 1103), and the sidebar ContextMenu expands
      // exactly ONE submenu level — a nested submenu renders disabled there
      // ("one level is the deal"), which is how the old "move-to-group ▸"
      // flyout-in-a-submenu went dead (包30 user report: 对话→分组→移动到分组
      // 点不动). The roster is therefore flat leaves at THIS level: one row
      // per group, then the escape hatch. Same moves, same order as the
      // retired flyout.
      return [
        ...groups.map((group): ContextMenuItem => ({
          key: `move:${group.id}`,
          label: group.title,
          disabled: ownGroups.has(group.id),
          onSelect: () => moveTopicIntoGroup(key, topicID, group.id),
        })),
        ...(ownGroups.size > 0 ? [{ type: "separator" as const, key: "move-sep" }] : []),
        ...(ownGroups.size > 0
          ? [{ key: "remove-from-group", icon: <FolderMinus size={13} />, label: t("projectTree.removeFromGroup"), onSelect: () => removeTopicFromGroups(node) }]
          : []),
      ];
    },
    createGroup(folder, title) {
      const key = projectTreeOrganizationKey(folder);
      const suffix = typeof crypto !== "undefined" && crypto.randomUUID ? crypto.randomUUID() : `${Date.now()}-${Math.random().toString(16).slice(2)}`;
      mutateGroups(key, (groups) => [...groups, { id: `group-${suffix}`, title, topicIds: [] }]);
    },
    createSubGroup(folder, parentID, title) {
      const key = projectTreeOrganizationKey(folder);
      const suffix = typeof crypto !== "undefined" && crypto.randomUUID ? crypto.randomUUID() : `${Date.now()}-${Math.random().toString(16).slice(2)}`;
      const groupID = `group-${suffix}`;
      const { scope, root } = splitOrganizationKey(key);
      if (typeof bindings.CreateSessionGroup === "function") {
        // Task 892-B: the write goes through the A-slice host — server-side
        // validation (cycle/depth), op-log, and the reason=metadata v2 event
        // that re-pulls every open window. The roster refreshes from the
        // backend; the local optimistic append is only the paint meanwhile.
        groupMutationVersionsRef.current[key] = (groupMutationVersionsRef.current[key] ?? 0) + 1;
        loadedGroupsRef.current.add(key);
        setKeyGroups(key, [...(groupsRef.current[key] ?? []), { id: groupID, title, parent: parentID, topicIds: [] }]);
        void bindings.CreateSessionGroup(scope, root, groupID, title, parentID)
          .then(() => loadGroups(key, true))
          .catch((error) => {
            reportFrontendLog("project-groups", "create sub-group failed", `parent=${parentID} error=${String(error)}`);
            // The optimistic paint must not survive a rejected write: re-pull
            // the server truth (which never saw the group).
            loadGroups(key, true);
          });
        return;
      }
      mutateGroups(key, (groups) => [...groups, { id: groupID, title, parent: parentID, topicIds: [] }]);
    },
    groupsFor(folder) { return groupsByKey[projectTreeOrganizationKey(folder)] ?? []; },
    groupCollapsed(key, id) { return collapsedGroups.has(`${key}|${id}`); },
    toggleGroup(key, id) {
      setCollapsedGroups((current) => {
        const next = new Set(current), collapseKey = `${key}|${id}`;
        if (next.has(collapseKey)) next.delete(collapseKey); else next.add(collapseKey);
        // Recorded because the state is persisted: "why did this group open collapsed" is
        // otherwise unanswerable from a restart, and the state outlives the session.
        reportFrontendLog("project-groups", next.has(collapseKey) ? "group collapsed" : "group expanded",
          `group=${id}`);
        // Persist so the collapse state survives app restarts and project-tree
        // remounts — it used to live only in component memory, so every reopen
        // auto-expanded every session group.
        persistSessionGroupCollapsed(next);
        return next;
      });
    },
    renameGroup(key, id, title) {
      const trimmed = title.trim();
      mutateGroups(key, (groups) => trimmed ? groups.map((group) => group.id === id ? { ...group, title: trimmed } : group) : groups.filter((group) => group.id !== id));
    },
    dropGroup(key, fromID, toID, zone) {
      if (fromID === toID) return;
      // Illegal drops (cycle into one's own subtree, ghost endpoints, depth
      // cap) leave placeGroup's reference untouched — no CAS write for them.
      const current = groupsRef.current[key] ?? [];
      if (placeGroup(current, fromID, toID, zone) === current) return;
      mutateGroups(key, (groups) => placeGroup(groups, fromID, toID, zone));
    },
    clearGroup(key, id) { mutateGroups(key, (groups) => groups.map((group) => group.id === id ? { ...group, topicIds: [] } : group)); },
    dissolveGroup(key, id) { mutateGroups(key, (groups) => groups.filter((group) => group.id !== id)); },
    async deleteGroupPropose(key, groupID) {
      if (typeof bindings.DeleteSessionGroup !== "function") return null;
      const { scope, root } = splitOrganizationKey(key);
      try {
        // Task 892-A 提案制：confirm=false 只返回 plan，零写入。
        const result = await bindings.DeleteSessionGroup(scope, root, groupID, false, false);
        return result.plan ?? { deleted: [groupID] };
      } catch (error) {
        reportFrontendLog("project-groups", "delete proposal failed", `group=${groupID} error=${String(error)}`);
        return null;
      }
    },
    async deleteGroupExecute(key, groupID, cascade) {
      if (typeof bindings.DeleteSessionGroup !== "function") {
        // Pre-892 path: the CAS roster filter (children would orphan-render top-level).
        mutateGroups(key, (groups) => groups.filter((group) => group.id !== groupID));
        return;
      }
      const { scope, root } = splitOrganizationKey(key);
      // confirm=true executes with fresh plan semantics backend-side (children
      // promoted unless cascade). A rejected write re-pulls server truth.
      try {
        await bindings.DeleteSessionGroup(scope, root, groupID, cascade, true);
      } catch (error) {
        reportFrontendLog("project-groups", "delete execute failed", `group=${groupID} cascade=${String(cascade)} error=${String(error)}`);
      }
      loadGroups(key, true);
    },
    nestGroup(key, childID, parentID) {
      // Task 350: hierarchy is display organization — the roster helpers only
      // rewrite the `parent` pointer, never ids or membership.
      mutateGroups(key, (groups) => parentID ? nestInRoster(groups, childID, parentID) : unnestInRoster(groups, childID));
    },
    canDropTopicInto(key) {
      const context = dragContextRef.current;
      return Boolean(dragTopicID && context && key === (context.scope === "global" ? "global|" : `project|${context.root}`));
    },
    dropTopicInto(key, groupID) {
      const topicID = dragTopicID;
      if (!topicID) return;
      mutateGroups(key, (groups) => moveTopicInRoster(groups, topicID, groupID));
      clearTopicDrag();
    },
    moveTopicToGroup: moveTopicIntoGroup,
  };
}

export function ProjectTreeGroupRows({
  folder,
  children,
  depth,
  section,
  visible,
  organization,
  renderNode,
  t,
  onMarkAllRead,
}: {
  folder: ProjectNode;
  children: ProjectNode[];
  depth: number;
  section: "pinned" | "projects";
  visible: boolean;
  organization: ProjectTreeOrganizationController;
  renderNode: (node: ProjectNode, depth: number, section: "pinned" | "projects", visible: boolean) => ReactNode;
  t: Translator;
  // wt-zcode-288：分组头右键「全部已读」——把该自定义分组的成员会话标记已读；
  // 可选 prop，未接线时菜单里不出现该项（测试挂载点无需感知）。
  onMarkAllRead?: (members: ProjectNode[]) => void;
}) {
  const [menuGroup, setMenuGroup] = useState<string | null>(null);
  const [menuPoint, setMenuPoint] = useState<ContextMenuPoint | null>(null);
  const [editingGroup, setEditingGroup] = useState<string | null>(null);
  // Task 892-B: 「新建子分组」inline composer — the group id whose child-create
  // input is open (same inline pattern as rename; 827 form on the other tree).
  const [creatingUnder, setCreatingUnder] = useState<string | null>(null);
  // Task 892-B: the dissolve flow is proposal-first (confirm=false → plan →
  // dialog → confirm=true); the dialog lives on this component.
  const { confirm: confirmDelete, dialog: deleteDialog } = useConfirmDialog();
  // Task 50: long-press a group header to drag it within its project. The reorder is
  // committed once on release rather than per hover, so a drag writes the roster a
  // single time, and a press that never became a drag stays a plain click.
  // Task 169: the same release-commit machine is also entered instantly from the
  // header's drag handle — long-press stays for touch, the handle is the desktop
  // affordance (a 350ms invisible press is undiscoverable with a mouse).
  const [draggingGroup, setDraggingGroup] = useState<string | null>(null);
  const [groupDropTarget, setGroupDropTarget] = useState<{ id: string; zone: GroupDropZone } | null>(null);
  const groupDragRef = useRef<{ from: string | null; to: string | null; zone: GroupDropZone; moved: boolean }>({ from: null, to: null, zone: "after", moved: false });
  const groupPressTimerRef = useRef<number | null>(null);
  const groupSuppressClickRef = useRef(false);
  const [groupDraft, setGroupDraft] = useState("");
  const key = projectTreeOrganizationKey(folder);
  const groups = organization.groupsFor(folder);
  const roots = topLevelGroups(groups);
  const groupedIDs = new Set(groups.flatMap((group) => group.topicIds ?? []));
  useEffect(() => {
    if (!draggingGroup) return;
    const finish = (event: PointerEvent) => {
      if (groupPressTimerRef.current !== null) {
        window.clearTimeout(groupPressTimerRef.current);
        groupPressTimerRef.current = null;
      }
      const { from, to, zone, moved } = groupDragRef.current;
      if (moved && from && to && from !== to) {
        // Task 350: Alt+drop keeps its force-nest meaning (display-only
        // hierarchy, cycle/depth-guarded by canNestUnder).
        // Task 892-B: a plain drop follows the zone the pointer hovered —
        // ON the target row = into its subtree (append at tail), BETWEEN
        // rows = same-level reorder at the target's level. Both land through
        // the one pure placeGroup (cycle-safe).
        if (event.altKey && canNestUnder(groups, from, to)) organization.nestGroup(key, from, to);
        else organization.dropGroup(key, from, to, zone);
      }
      // Swallow the click the release would otherwise produce: a drag must not also
      // toggle the group it started on.
      groupSuppressClickRef.current = moved;
      groupDragRef.current = { from: null, to: null, zone: "after", moved: false };
      setDraggingGroup(null);
      setGroupDropTarget(null);
    };
    window.addEventListener("pointerup", finish);
    window.addEventListener("pointercancel", finish);
    return () => {
      window.removeEventListener("pointerup", finish);
      window.removeEventListener("pointercancel", finish);
    };
  }, [draggingGroup, groups, key, organization]);

  const beginGroupPress = (id: string) => {
    groupDragRef.current = { from: id, to: id, zone: "after", moved: false };
    // The flag only exists to swallow the click of the release that just
    // dragged. Scoped to the gesture: if no click materialised (release over
    // another header fires it on a common ancestor), the stale flag must not
    // eat the user's next real click on a group header.
    groupSuppressClickRef.current = false;
    groupPressTimerRef.current = window.setTimeout(() => {
      groupPressTimerRef.current = null;
      setDraggingGroup(id);
    }, 350);
  };
  // Task 169: the drag handle skips the long-press delay and enters the very same
  // drag state immediately. No pointer capture is taken, so the sibling headers'
  // pointerenter hover tracking (the drop-target recorder) keeps working.
  const beginGroupDrag = (id: string) => {
    groupDragRef.current = { from: id, to: id, zone: "after", moved: false };
    groupSuppressClickRef.current = false;
    setDraggingGroup(id);
  };
  const cancelGroupPress = () => {
    if (groupPressTimerRef.current !== null) {
      window.clearTimeout(groupPressTimerRef.current);
      groupPressTimerRef.current = null;
    }
    // A press released before the long-press fired is an ordinary click.
    if (!draggingGroup) groupDragRef.current = { from: null, to: null, zone: "after", moved: false };
  };
  // Task 892-B: the hover zone decides what a plain drop MEANS — the pointer
  // band within the target header (top third = sibling before, middle third =
  // into the group, bottom third = sibling after). A header with no measurable
  // box (jsdom / pre-layout) falls back to "after", which keeps the task-50
  // flat-reorder contract intact for every legacy drop.
  const hoverGroup = (id: string, clientY?: number, target?: Element | null) => {
    if (!draggingGroup) return;
    let zone: GroupDropZone = "after";
    if (typeof clientY === "number" && target) {
      const rect = target.getBoundingClientRect();
      if (rect.height > 0) {
        const rel = (clientY - rect.top) / rect.height;
        zone = rel < 1 / 3 ? "before" : rel > 2 / 3 ? "after" : "into";
      }
    }
    const prev = groupDragRef.current;
    if (prev.to === id) {
      if (prev.zone === zone) return;
      groupDragRef.current = { ...prev, zone };
    } else {
      groupDragRef.current = { ...prev, to: id, zone, moved: true };
    }
    // Pointermove fires continuously within a header; keep the drop paint to
    // renders where the (id, zone) pair actually changed.
    setGroupDropTarget((current) => (current && current.id === id && current.zone === zone ? current : { id, zone }));
  };

  const commitRename = (id: string) => {
    organization.renameGroup(key, id, groupDraft);
    setEditingGroup(null);
  };
  // Task 892-B: commit the inline 子分组 composer. A blank draft cancels (blur
  // cannot create empty groups); Enter/blur share the path like rename.
  const commitSubGroup = (parentID: string) => {
    const title = groupDraft.trim();
    setCreatingUnder(null);
    if (title) organization.createSubGroup(folder, parentID, title);
  };
  // Task 350: groups render as a tree of arbitrary depth (task-892 removed the
  // depth-2 cap; the backend accepts any chain ≤ 64). `level` is the nesting
  // level within this folder (0 = top level). `roots` hides orphaned pointers
  // (parent dissolved elsewhere), so a stale pointer can never swallow a group
  // off the sidebar.
  const renderGroupRow = (group: SessionGroup, level: number) => {
      const nested = level > 0;
      const collapsed = organization.groupCollapsed(key, group.id);
      // The count badge reads the persisted roster (already filtered against
      // the session catalog by the backend), not the windowed render list —
      // collapsed groups render no member rows, so the badge is the only
      // signal and must not shrink with classic preview / pagination (#9518).
      const memberCount = group.topicIds?.length ?? 0;
      // Task 950: members paint in the persisted topicIds order (the order a
      // group-internal drag writes and a move-to-group appends to), not the
      // flat children order — otherwise the reorder lands on disk and the
      // sidebar keeps showing the pre-drag arrangement.
      const ownMembers = orderedGroupMembers(
        children.filter((child) => group.topicIds?.includes(child.topicId ?? "")),
        group.topicIds,
        (child) => child.topicId ?? "",
      );
      // Task 350 activity inheritance, task-892 generalized: every header's
      // dot stands for its own members plus its ENTIRE descendant subtree (any
      // depth), so a collapsed tree still shows where things run. Flat and
      // leaf groups are unchanged — the inherited set equals the own set.
      const dotMemberIDs = new Set(inheritedMemberIDs(groups, group.id));
      const dotMembers = children.filter((child) => dotMemberIDs.has(child.topicId ?? ""));
      const members = dotMembers;
      // Task-892: children render at every level (the depth-2 gate is gone).
      const kids = childGroups(groups, group.id);
      const canDrop = organization.canDropTopicInto(key);
      // tl 1198: the old cap of four inline nest targets (the slice(0, 4)
      // menu-economy cut) silently hid every group past the roster's first
      // four — a deep group like reasonix-for-ai at depth 2 was impossible to
      // reach from the menu even though nestGroup accepts any depth. The menu
      // now walks the whole tree: every legal target at any depth, in tree
      // order, each row carrying its ancestor path ("4.reasonix /
      // reasonix-for-ai") so same-titled groups at different depths stay
      // distinguishable. The group's own parent is excluded (re-nesting where
      // it already sits is a no-op); itself and its subtree are excluded by
      // canNestUnder's cycle guard. The panel scrolls (max-height + overflow),
      // so a big roster no longer truncates the choices.
      const nestInto = nestTargetRows(groups, group.id);
      // tl 1149: the flat "move under X / move to top level" rows collapsed
      // into one 「分组移动 ▸」 submenu. Same moves, same order (nest targets
      // first, then the top-level escape hatch for nested groups) — one level
      // less of menu length per group in the roster.
      // tl 1198: with no other legal target left (empty roster, or the group
      // is the only one), the submenu stays visible but disabled instead of
      // disappearing — the entry point is discoverable even when unusable.
      const groupMoveItems: ContextMenuItem[] = [
        // Task 350 → 892: display-only hierarchy moves — nest under any
        // legal group at any depth (cycle- and depth-guarded by canNestUnder).
        // No authority follows the pointer (层级≠指挥权).
        ...nestInto.map((target) => ({
          key: `nest:${target.group.id}`,
          icon: <FolderMinus size={13} />,
          label: target.ancestorTitles.length > 0
            ? t("projectTree.nestGroupUnder", { title: `${target.ancestorTitles.join(" / ")} / ${target.group.title}` })
            : t("projectTree.nestGroupUnder", { title: target.group.title }),
          onSelect: () => { organization.nestGroup(key, group.id, target.group.id); setMenuGroup(null); },
        })),
        ...(nested ? [{
          key: "move-to-top",
          icon: <FolderMinus size={13} />,
          label: t("projectTree.moveGroupToTopLevel"),
          onSelect: () => { organization.nestGroup(key, group.id, null); setMenuGroup(null); },
        }] : []),
      ];
      return <div key={group.id} className={`project-tree__group${collapsed ? " project-tree__group--collapsed" : ""}${nested ? " project-tree__group--nested" : ""}`}>
        <div
          role="button"
          tabIndex={0}
          className={`project-tree__group-main${canDrop ? " project-tree__group-main--drop-target" : ""}${groupDropTarget && groupDropTarget.id === group.id && groupDropTarget.id !== draggingGroup ? (groupDropTarget.zone === "into" ? " project-tree__group-main--nest-target" : " project-tree__group-main--reorder-target") : ""}${draggingGroup === group.id ? " project-tree__group-main--dragging" : ""}`}
          // Task 313: the group header used to ride the children depth (the
          // caller passes folder depth + 1), sitting 22px right of the project
          // folder row. Rewriting to the folder row's own formula —
          // 8 + (depth - 1) * 16 — puts the collapse chevron exactly on the
          // folder icon column. Member rows keep the depth they are rendered
          // with, so the project tree and expanded children do not move.
          // Task 892: a nested group rides one extra 16px step PER LEVEL
          // (arbitrary depth replaced task-350's single `nested` step).
          style={{ paddingLeft: 8 + (depth - 1) * 16 + level * 16 }}
          title={group.title}
          onClick={() => {
            // A press that became a drag already did its work on release; the click it
            // would also emit must not toggle the group.
            if (groupSuppressClickRef.current) {
              groupSuppressClickRef.current = false;
              return;
            }
            organization.toggleGroup(key, group.id);
          }}
          onPointerDown={(event) => {
            if (event.button === 0) beginGroupPress(group.id);
          }}
          onPointerUp={cancelGroupPress}
          onPointerLeave={cancelGroupPress}
          // Task 892-B: enter AND move both feed the zone tracker — the band
          // within one header can change (before → into → after) as the
          // pointer crosses it. hoverGroup bails unless dragging.
          onPointerEnter={(event) => hoverGroup(group.id, event.clientY, event.currentTarget)}
          onPointerMove={(event) => hoverGroup(group.id, event.clientY, event.currentTarget)}
          onKeyDown={(event) => {
            if (editingGroup === group.id) return;
            if (event.key === "Enter" || event.key === " ") organization.toggleGroup(key, group.id);
          }}
          onContextMenu={(event) => {
            event.preventDefault();
            setMenuGroup(group.id);
            setMenuPoint(contextMenuPointFromEvent(event));
          }}
          onDragOver={(event) => {
            if (!canDrop) return;
            event.preventDefault();
            event.dataTransfer.dropEffect = "move";
          }}
          onDrop={(event) => {
            event.preventDefault();
            if (canDrop) organization.dropTopicInto(key, group.id);
          }}
        >
          {/* Task 169: desktop affordance for the task-50 drag. Hidden until the
              header is hovered (CSS), pressed-and-held to drag without the
              long-press delay. Purely a pointer affordance: aria-hidden and
              unfocusable — collapse stays on the header (Enter/Space), and a
              keyboard reorder would need menu items, not this handle. Alt+drop
              on another header nests instead of reordering (task 350). */}
          <span
            className="project-tree__group-drag"
            aria-hidden="true"
            onPointerDown={(event) => {
              if (event.button !== 0) return;
              // Not a click on the header: no long-press timer, and the release
              // must not fall through to the collapse toggle.
              event.preventDefault();
              event.stopPropagation();
              beginGroupDrag(group.id);
            }}
            onClick={(event) => event.stopPropagation()}
          >
            <GripVertical size={12} />
          </span>
          <span className="project-tree__group-chevron" aria-hidden="true">{collapsed ? "▸" : "▾"}</span>
          {editingGroup === group.id ? <input
            autoFocus
            className="project-tree__group-input"
            value={groupDraft}
            onChange={(event) => setGroupDraft(event.target.value)}
            onFocus={(event) => event.target.select()}
            onKeyDown={(event) => {
              if (event.key === "Enter") commitRename(group.id);
              if (event.key === "Escape") setEditingGroup(null);
            }}
            onBlur={() => commitRename(group.id)}
            onClick={(event) => event.stopPropagation()}
          /> : <span className="project-tree__group-title">{group.title}</span>}
          <span className="project-tree__group-count">{memberCount}</span>
          {/* Task 312: collapsed group shows the member activity dot beside the
              count — same .project-tree__topic-state visual the rows use, driven
              by the same topicStatus pipeline (running folds to streaming), so it
              appears and clears with the rows at the same rate.
              Task 313: the dot is joined by the number of members it stands
              for; the badge renders inside the same dot guard, so count and dot
              live and die together (all idle → both gone) and the expanded
              view still shows neither (rows own their own indicators).
              Task 350: for a parent the dot/count aggregate child groups too. */}
          {(() => {
            const dot = collapsed ? projectTreeGroupDotStatus(members) : "";
            if (!dot) return null;
            const activeCount = projectTreeGroupActiveCount(members);
            return <>
              <span className={`project-tree__group-state project-tree__topic-state project-tree__topic-state--${dot}`} aria-hidden="true" />
              {activeCount > 0 && <span className="project-tree__group-active-count" aria-hidden="true">{activeCount}</span>}
            </>;
          })()}
        </div>
        {/* Task 892-B: inline 子分组 composer — indented one level past this
            group's header (same grid as its first child), so the to-be-created
            child appears exactly where it will render. */}
        {creatingUnder === group.id && <div
          className="project-tree__group-subgroup-create"
          style={{ paddingLeft: 8 + (depth - 1) * 16 + (level + 1) * 16 }}
        >
          <input
            autoFocus
            className="project-tree__group-input"
            value={groupDraft}
            placeholder={t("projectTree.createSubGroupPlaceholder")}
            aria-label={t("projectTree.createSubGroup")}
            onChange={(event) => setGroupDraft(event.target.value)}
            onFocus={(event) => event.target.select()}
            onKeyDown={(event) => {
              if (event.key === "Enter") commitSubGroup(group.id);
              if (event.key === "Escape") setCreatingUnder(null);
            }}
            onBlur={() => commitSubGroup(group.id)}
            onClick={(event) => event.stopPropagation()}
          />
        </div>}
        {menuGroup === group.id && <ContextMenu
          open
          point={menuPoint}
          items={[
            // wt-zcode-288：会话分组头右键「全部已读」——成员会话一次标记已读。
            // tl 1149：分组语境的「全部已读」保留原名（单条会话菜单才叫「已读」）。
            ...(onMarkAllRead && members.length > 0 ? [{
              key: "mark-all-read",
              icon: <CheckCheck size={13} />,
              label: t("projectTree.markAllRead"),
              onSelect: () => { onMarkAllRead(members); setMenuGroup(null); },
            }] : []),
            // tl 1149：N 条「移到 X 子层 / 移到顶层」一级平铺收进「分组移动 ▸」
            // 二级——分组多时一级菜单不再随分组数线性变长。语义与方向不变：
            // 仍是 display-only 层级移动（cycle/depth-guarded nestGroup）。
            // tl 1198：无任何合法目标时该项保留但禁用（点不开），不再整项
            // 消失——入口可发现，且语义是「无处可移」而非「没有此功能」。
            {
              type: "submenu" as const,
              key: "group-move",
              icon: <FolderMinus size={13} />,
              label: t("projectTree.groupMove"),
              disabled: groupMoveItems.length === 0,
              items: groupMoveItems,
            },
            // Task 892-B: 「新建子分组」(827 form on the session tree) — opens an
            // inline composer right below this group's header; Enter files it
            // as this group's child via the A-slice CreateSessionGroup (parentId
            // pass-through), Escape/blur cancels. Works at every nesting level.
            {
              key: "create-subgroup",
              icon: <FolderPlus size={13} />,
              label: t("projectTree.createSubGroup"),
              onSelect: () => { setCreatingUnder(group.id); setGroupDraft(""); setMenuGroup(null); if (collapsed) organization.toggleGroup(key, group.id); },
            },
            { key: "rename", icon: <Pencil size={13} />, label: t("projectTree.renameGroup"), onSelect: () => { setEditingGroup(group.id); setGroupDraft(group.title); setMenuGroup(null); } },
            // tl 1149：低频管理操作（清空/解散/删除及会话）收进「分组管理 ▸」
            // 二级；高频读写项（已读/分组移动/新建子分组/重命名）留在一级。
            // 取舍按使用频率：重命名与新建子分组是整理动作（常用），清空与两级
            // 删除是破坏性动作（罕用+均有确认/回收路径兜底）。
            {
              type: "submenu" as const,
              key: "group-manage",
              icon: <Eraser size={13} />,
              label: t("projectTree.groupManage"),
              items: [
                { key: "clear", icon: <Eraser size={13} />, label: t("projectTree.clearGroup"), onSelect: () => { organization.clearGroup(key, group.id); setMenuGroup(null); } },
                // Task 892-B ④: proposal-first delete — confirm=false returns a
                // zero-write plan; the dialog then offers the A-slice's two options:
                // children promoted to top (default) or cascade (whole subtree).
                // No binding → pre-892 immediate CAS dissolve (same promote result).
                { key: "dissolve", icon: <FolderMinus size={13} />, label: t("projectTree.dissolveGroup"), onSelect: () => {
                  setMenuGroup(null);
                  void (async () => {
                    const plan = await organization.deleteGroupPropose(key, group.id);
                    if (plan === null) { organization.dissolveGroup(key, group.id); return; }
                    const promoted = plan.promoted ?? [];
                    const unfiled = plan.unfiledTopics ?? [];
                    // cascadeRef: the checkbox lives inside the dialog's ReactNode
                    // message; its value is read back after the dialog resolves.
                    const cascadeRef = { current: false };
                    const titles = promoted.map((id) => groups.find((candidate) => candidate.id === id)?.title ?? id);
                    const confirmed = await confirmDelete({
                      title: t("projectTree.dissolveGroup"),
                      message: <>
                        <p>{t("projectTree.deleteGroupPlanIntro", { title: group.title })}</p>
                        {unfiled.length > 0 && <p>{t("projectTree.deleteGroupPlanUnfiled", { count: String(unfiled.length) })}</p>}
                        {promoted.length > 0 && <>
                          <p>{t("projectTree.deleteGroupPlanPromote", { count: String(promoted.length), titles: titles.join(", ") })}</p>
                          <label className="reasonix-confirm-dialog__cascade">
                            <input
                              type="checkbox"
                              aria-label={t("projectTree.deleteGroupPlanCascade", { count: String(promoted.length) })}
                              onChange={(event) => { cascadeRef.current = event.target.checked; }}
                            />
                            {t("projectTree.deleteGroupPlanCascade", { count: String(promoted.length) })}
                          </label>
                        </>}
                        {plan.batch && unfiled.length > 0 && <p>{t("projectTree.deleteGroupPlanBatch", { count: String(unfiled.length) })}</p>}
                      </>,
                      confirmLabel: t("projectTree.dissolveGroup"),
                      cancelLabel: t("common.cancel"),
                      tone: "danger",
                      wide: Boolean(plan.batch),
                    });
                    if (!confirmed) return;
                    await organization.deleteGroupExecute(key, group.id, cascadeRef.current);
                  })();
                } },
                { key: "delete-sessions", icon: <Trash2 size={13} />, label: t("projectTree.deleteGroupSessions"), danger: true, onSelect: () => {
                  setMenuGroup(null);
                  // Destructive variant of dissolve (task 17 S3): every session goes
                  // to the recycle path first - never a hard delete - and one
                  // failure must not strand the remaining sessions.
                  void (async () => {
                    const members = (group.topicIds ?? []).slice();
                    if (members.length > 0) {
                      const confirmed = await app.ConfirmAction({
                        title: t("projectTree.deleteGroupSessions"),
                        message: t("projectTree.deleteGroupSessionsConfirm", { count: String(members.length) }),
                        detail: t("projectTree.deleteGroupSessionsDetail"),
                        confirmLabel: t("projectTree.deleteGroupSessions"),
                        cancelLabel: t("common.cancel"),
                        destructive: true,
                      });
                      if (!confirmed) return;
                      for (const topicID of members) {
                        try {
                          await app.TrashTopic(topicID);
                        } catch (error) {
                          console.warn("trash topic failed", topicID, error);
                        }
                      }
                    }
                    // Task 892-B: route the shell removal through the A-slice too
                    // (children promote, cascade=false); without the binding this
                    // lands on the same CAS filter the old dissolveGroup used.
                    await organization.deleteGroupExecute(key, group.id, false);
                  })();
                } },
              ],
            },
          ]}
          minWidth={178}
          ariaLabel={t("projectTree.renameGroup")}
          onClose={() => setMenuGroup(null)}
        />}
        {!collapsed && ownMembers.length > 0 && <div className="project-tree__group-children">
          {ownMembers.map((child) => renderNode(child, depth, section, visible))}
        </div>}
        {!collapsed && kids.length > 0 && <div className="project-tree__group-subtree">
          {kids.map((kid) => renderGroupRow(kid, level + 1))}
        </div>}
      </div>;
  };
  return <>
    {/* Task 892-B ④: the proposal confirm dialog (portal target is body, but
        the element must mount inside the component tree that owns the hook). */}
    {deleteDialog}
    {children.filter((child) => !groupedIDs.has(child.topicId ?? "")).map((child) => renderNode(child, depth, section, visible))}
    {roots.map((group) => renderGroupRow(group, 0))}
  </>;
}

export function projectTreeFolderHasActiveRuntime(folder: ProjectNode): boolean {
  return asArray(folder.children).some(projectTreeTopicArchiveBlocked);
}

const FolderActivity = lazy(() => import("./ProjectTreeFolderActivity"));
export function ProjectTreeFolderActivity({ folder }: { folder: ProjectNode }) {
  return <Suspense fallback={null}><FolderActivity folder={folder} /></Suspense>;
}
