// Task 892-B ⑤: project-group roster migration — the A-slice backend keeps
// the roster in desktop-projects.json (top-level `projectGroups`, the
// "catalog"); the frontend used to keep it in localStorage only. Read via
// ManageSessionsList, write via Create/Rename/DeleteProjectGroup. One-time
// import when the catalog is empty and localStorage still has groups; from
// then on the backend is the source of truth (handoff §三.2).
//
// Residuals by design (handoff §一.1 — no backend API in 892-A):
//   • project→group ASSIGNMENTS stay localStorage (desktopProject.ProjectGroup
//     has no setter method on this surface);
//   • sortOrder stays a localStorage shadow (no reorder API for project
//     groups; the catalog appends in creation order).
// Every method is optional: without the binding (older backend / dev mock)
// pull returns null and the caller keeps the pure-local behavior.

import { app } from "./bridge";
import { asArray } from "./array";
import type { ProjectGroup } from "./projectGroups";
import type { DeleteGroupsPlan, ManageSessionsGroupResult, ManageSessionsListResult } from "./types";

export interface ProjectGroupCatalogBindings {
  ManageSessionsList?: () => Promise<ManageSessionsListResult>;
  CreateProjectGroup?: (groupID: string, title: string, parentID: string) => Promise<ManageSessionsGroupResult>;
  RenameProjectGroup?: (groupID: string, title: string) => Promise<ManageSessionsGroupResult>;
  DeleteProjectGroup?: (groupID: string, cascade: boolean, confirm: boolean) => Promise<ManageSessionsGroupResult>;
}

// The bridge proxies any method to window.go.main.App at call time, so the
// A-slice methods reach the backend even though the generated wailsjs d.ts in
// this branch predates them. Typed through this narrow optional interface.
export function defaultProjectGroupCatalogBindings(): ProjectGroupCatalogBindings {
  return app as unknown as ProjectGroupCatalogBindings;
}

// catalogToProjectGroups maps the view spelling (parentId) to the storage
// spelling (parent); sortOrder is the backend's own ranking.
export function catalogToProjectGroups(
  views: ManageSessionsListResult["projectGroups"] | undefined,
): ProjectGroup[] {
  return asArray(views).map((view, index) => ({
    id: view.id,
    title: view.title,
    sortOrder: typeof view.sortOrder === "number" ? view.sortOrder : index,
    ...(view.parentId ? { parent: view.parentId } : {}),
  }));
}

export interface ProjectGroupPull {
  roster: ProjectGroup[];
  /** imported: a one-time catalog import just ran (caller should clear the
   * localStorage roster shadow so deleted groups cannot resurrect). */
  imported: boolean;
}

// pullProjectGroupCatalog reads the catalog; null means "no binding / read
// failed — stay on localStorage". When the catalog is empty and localStorage
// still holds groups, each local group is created once (parents first so a
// nested roster imports in one pass); a failed create is skipped and the rest
// still migrate.
export async function pullProjectGroupCatalog(
  bindings: ProjectGroupCatalogBindings,
  local: ProjectGroup[],
): Promise<ProjectGroupPull | null> {
  if (typeof bindings.ManageSessionsList !== "function") return null;
  try {
    const first = await bindings.ManageSessionsList();
    let roster = catalogToProjectGroups(first.projectGroups);
    if (roster.length === 0 && local.length > 0 && typeof bindings.CreateProjectGroup === "function") {
      const parentsFirst = [...local].sort((a, b) => Number(Boolean(a.parent)) - Number(Boolean(b.parent)));
      for (const group of parentsFirst) {
        try {
          await bindings.CreateProjectGroup(group.id, group.title, group.parent ?? "");
        } catch {
          /* one bad group must not strand the rest of the migration */
        }
      }
      const again = await bindings.ManageSessionsList();
      roster = catalogToProjectGroups(again.projectGroups);
      return { roster, imported: true };
    }
    return { roster, imported: false };
  } catch {
    return null;
  }
}

// createProjectGroupViaCatalog writes one group; false when the write did not
// go through (missing binding or backend error — the caller re-pulls either
// way, so a slow failure still converges on the backend truth).
export async function createProjectGroupViaCatalog(
  bindings: ProjectGroupCatalogBindings,
  groupID: string,
  title: string,
  parentID: string | null,
): Promise<boolean> {
  if (typeof bindings.CreateProjectGroup !== "function") return false;
  try {
    await bindings.CreateProjectGroup(groupID, title, parentID ?? "");
    return true;
  } catch {
    return false;
  }
}

export async function renameProjectGroupViaCatalog(
  bindings: ProjectGroupCatalogBindings,
  groupID: string,
  title: string,
): Promise<boolean> {
  if (typeof bindings.RenameProjectGroup !== "function") return false;
  try {
    await bindings.RenameProjectGroup(groupID, title);
    return true;
  } catch {
    return false;
  }
}

// proposeProjectGroupDelete: A-slice 提案制 — confirm=false returns the plan
// with zero writes; null = no binding / proposal failed (caller falls back to
// the legacy local dissolve).
export async function proposeProjectGroupDelete(
  bindings: ProjectGroupCatalogBindings,
  groupID: string,
): Promise<DeleteGroupsPlan | null> {
  if (typeof bindings.DeleteProjectGroup !== "function") return null;
  try {
    const result = await bindings.DeleteProjectGroup(groupID, false, false);
    return result.plan ?? { deleted: [groupID] };
  } catch {
    return null;
  }
}

// executeProjectGroupDelete commits the confirmed deletion: children are
// promoted unless cascade deletes the whole subtree. Projects themselves are
// never deleted — pointing ones get ungrouped (backend clears the field).
export async function executeProjectGroupDelete(
  bindings: ProjectGroupCatalogBindings,
  groupID: string,
  cascade: boolean,
): Promise<void> {
  if (typeof bindings.DeleteProjectGroup !== "function") return;
  try {
    await bindings.DeleteProjectGroup(groupID, cascade, true);
  } catch {
    /* the caller's re-pull reflects whatever the backend actually did */
  }
}
