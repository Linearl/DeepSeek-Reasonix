// Session info panel (ContextPanel) display logic, kept as pure functions so the
// group membership and recovery-copy resolution can be tested without the bridge.
// wt-zcode-285: the panel shows which session group the open conversation belongs
// to and what role its physical copy plays in the recovery lineage.

import type { DictKey } from "./i18n";
import type { SessionGroup } from "./sessionCatalogTypes";
import type { RecoveryLineageView } from "./types";

export interface SessionPanelIdentity {
  topicId?: string;
  scope?: string; // "project" | "global"
  workspaceRoot?: string;
  sessionPath?: string;
}

// resolveSessionGroupTitle returns the title of the session group containing
// topicId, or null when the conversation is ungrouped (the panel hides the row).
export function resolveSessionGroupTitle(
  groups: readonly SessionGroup[] | null | undefined,
  topicId: string | undefined,
): string | null {
  if (!topicId || !Array.isArray(groups)) return null;
  for (const group of groups) {
    if (!group || !Array.isArray(group.topicIds) || !group.topicIds.includes(topicId)) continue;
    const title = typeof group.title === "string" ? group.title.trim() : "";
    if (title) return title;
  }
  return null;
}

const RECOVERY_ROLE_LABEL_KEYS: Record<string, DictKey> = {
  normal: "recovery.role.normal",
  covered_copy: "recovery.role.covered_copy",
  adopted: "recovery.role.adopted",
  preferred: "recovery.role.preferred",
  diverged: "recovery.role.diverged",
};

export interface SessionRecoveryStatus {
  role: string;
  labelKey: DictKey;
}

// sessionRecoveryDisplay picks the lineage member representing THIS session
// (by path, then the selected head, then the canonical one) and reports its
// copy role. Null when there is nothing worth surfacing: a lineage with a
// single normal original is the everyday case and stays invisible.
export function sessionRecoveryDisplay(
  view: Pick<RecoveryLineageView, "members"> | null | undefined,
  sessionPath?: string,
): SessionRecoveryStatus | null {
  const members = Array.isArray(view?.members) ? view.members : [];
  if (members.length === 0) return null;
  const path = typeof sessionPath === "string" ? sessionPath : "";
  const member =
    (path && members.find((candidate) => candidate.path === path)) ||
    members.find((candidate) => candidate.selected) ||
    members.find((candidate) => candidate.canonical) ||
    members[0];
  if (!member) return null;
  const role = typeof member.role === "string" && member.role ? member.role : "normal";
  const isRecoveryVersion = member.versionKind === "recovery";
  if (role === "normal" && !isRecoveryVersion && members.length <= 1) return null;
  return { role, labelKey: RECOVERY_ROLE_LABEL_KEYS[role] ?? "recovery.role.normal" };
}
