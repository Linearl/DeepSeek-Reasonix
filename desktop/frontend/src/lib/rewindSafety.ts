// tl1476: pure classification helpers for the rewind preflight dialog.
//
// The backend (checkpoints_app.go) computes a per-file verdict in
// plan.fileSafety: safe files restore cleanly, unsafe files carry a stable
// reason (path_unsafe → outside workspace, missing_payload → checkpoint
// payload deleted/expired, permission_denied → unwritable target, or any
// other conflict reason). For bridges that predate the field (remote legacy,
// old mocks) the classifier falls back to parsing plan.conflicts strings,
// which the backend renders as "path: reason".
//
// Pure functions only — the dialog rendering lives in RewindPreflightList and
// the gating lives in rewindCommit.ts.

import type { RewindPlanView } from "./rewindTypes";

export type RewindUnsafeReasonKey =
  | "outside_workspace"
  | "missing_payload"
  | "permission_denied"
  | "changed";

export interface RewindPreflightFile {
  path: string;
  safe: boolean;
  /** Stable reason key for i18n labels; null when safe. */
  reasonKey: RewindUnsafeReasonKey | null;
}

const CONFLICT_REASON_KEYS: Record<string, RewindUnsafeReasonKey> = {
  path_unsafe: "outside_workspace",
  missing_payload: "missing_payload",
  permission_denied: "permission_denied",
};

/** Maps a backend reason (conflict vocabulary) to its i18n reason key. */
export function mapUnsafeReasonKey(reason?: string): RewindUnsafeReasonKey {
  if (reason && CONFLICT_REASON_KEYS[reason]) return CONFLICT_REASON_KEYS[reason];
  return "changed";
}

/**
 * Classifies a rewind plan's files for the preflight dialog. Empty result
 * means "no file scenario" — the rewind flow must then behave exactly as
 * before the preflight existed (zero change).
 */
export function classifyRewindFiles(plan: RewindPlanView): RewindPreflightFile[] {
  const fileSafety = plan.fileSafety;
  if (fileSafety && fileSafety.length > 0) {
    return fileSafety.map((entry) => ({
      path: entry.path,
      safe: entry.safe === true,
      reasonKey: entry.safe === true ? null : mapUnsafeReasonKey(entry.reason),
    }));
  }
  // Fallback: derive from "path: reason" conflict strings. Entries without a
  // path prefix (busy writer, expired payload, ...) are plan-level and skipped.
  // Windows drive letters contain a colon but never colon-space, so
  // indexOf(": ") cannot split inside a path.
  const unsafeByPath = new Map<string, RewindUnsafeReasonKey>();
  for (const entry of plan.conflicts ?? []) {
    const sep = entry.indexOf(": ");
    if (sep <= 0) continue;
    const path = entry.slice(0, sep);
    if (!path) continue;
    if (!unsafeByPath.has(path)) unsafeByPath.set(path, mapUnsafeReasonKey(entry.slice(sep + 2)));
  }
  if (unsafeByPath.size === 0) return [];
  const classified: RewindPreflightFile[] = [];
  const listed = plan.files ?? [];
  for (const path of listed) {
    const key = unsafeByPath.get(path);
    classified.push({ path, safe: key === undefined, reasonKey: key ?? null });
  }
  for (const [path, key] of unsafeByPath) {
    if (!listed.includes(path)) classified.push({ path, safe: false, reasonKey: key });
  }
  return classified;
}
