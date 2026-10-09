import type { SessionCatalogStatus } from "./sessionCatalogTypes";

export type SessionCatalogNotice = "indexing" | "repair-active" | "repair-deferred" | "repair-blocked" | "failed" | "rebuild";

// 703: a repair banner must survive this long before it is painted. Every
// indexing wave claims its rows for a few hundred milliseconds, and a status
// poll landing inside a wave used to flash "正在修复历史记录" / "历史记录稍后重试"
// for a blink. Startup-facing notices (indexing / failed / rebuild) render
// immediately — hiding a real degraded state behind a delay would trade a
// flash for a blind spot.
export const REPAIR_NOTICE_HOLD_MS = 2000;

const IMMEDIATE_NOTICES: ReadonlySet<SessionCatalogNotice> = new Set(["indexing", "failed", "rebuild"]);

/**
 * createRepairNoticeGate delays repair-family notices until their condition
 * has held for REPAIR_NOTICE_HOLD_MS, so transient repair waves never paint a
 * status row. Non-repair notices pass through unchanged, and a return to null
 * is always immediate (nothing lingers on screen). A change to a different
 * repair notice restarts the hold — only a continuously-true condition earns
 * the banner. Inject `now` in tests; default is the wall clock.
 */
export function createRepairNoticeGate(now: () => number = Date.now): (notice: SessionCatalogNotice | null) => SessionCatalogNotice | null {
  let firstSeenAt = 0;
  let firstSeenNotice: SessionCatalogNotice | null = null;
  return (notice) => {
    if (notice === null || IMMEDIATE_NOTICES.has(notice)) {
      firstSeenAt = 0;
      firstSeenNotice = null;
      return notice;
    }
    if (firstSeenNotice !== notice) {
      firstSeenNotice = notice;
      firstSeenAt = now();
      return null;
    }
    if (now() - firstSeenAt >= REPAIR_NOTICE_HOLD_MS) {
      return notice;
    }
    return null;
  };
}

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
