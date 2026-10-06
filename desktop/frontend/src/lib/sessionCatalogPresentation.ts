import type { SessionCatalogStatus } from "./sessionCatalogTypes";

export type SessionCatalogNotice = "indexing" | "repair-active" | "repair-deferred" | "repair-blocked" | "failed" | "rebuild";

export function sessionCatalogNotice(s: SessionCatalogStatus): SessionCatalogNotice | null {
  const { state, repairActive, repairDeferred, repairBlocked } = s;
  return state === "opening" || state === "rebuilding"
    ? "indexing"
    : state === "degraded" || s.lastError
      ? (s.canRebuild ? "rebuild" : "failed")
      : (s.unindexedTargetCount ?? 0) > 0
        ? "indexing"
        // Task 550 ①: only the precise per-state repair counters may raise a
        // repair notice. The legacy repairPending field counts every
        // unknown-turn session whether or not a repair is queued, so a missing
        // repairActive must read as 0 — the old fallback manufactured a stuck
        // "repairing history" banner from a stale field, and two renders of the
        // same catalog could disagree about the banner (the flash).
        : (repairActive ?? 0) > 0
          ? "repair-active"
          : (repairDeferred ?? 0) > 0
            ? "repair-deferred"
            : (repairBlocked ?? 0) > 0
              ? "repair-blocked"
              : null;
}
