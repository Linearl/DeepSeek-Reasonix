import { useEffect, type RefObject } from "react";
import { reportFrontendLog } from "./frontendLog";

const STALE_TURN_RECONCILE_MS = 30_000;
// tl1403: a move-follow hydration is seconds-scale; a marker older than this
// is an orphaned entry (hydrate kept failing) and must not exempt the tab
// forever — the stale verdict resumes so a genuinely dead turn still gets
// its reconcile.
const SESSION_MOVE_EXEMPTION_WINDOW_MS = 120_000;

export function sessionMoveExemptionActive(moveAt: number | undefined, now = Date.now()): boolean {
  if (moveAt === undefined || moveAt <= 0) return false;
  return now - moveAt >= 0 && now - moveAt < SESSION_MOVE_EXEMPTION_WINDOW_MS;
}

export type StaleTurnWatchdogState = {
  running: boolean;
  turnStartAt: number;
};

export function shouldReconcileStaleTurn(
  state: StaleTurnWatchdogState | undefined,
  lastTurnActivityAt: number,
  now = Date.now(),
  timeoutMs = STALE_TURN_RECONCILE_MS,
  moveInFlight = false,
): boolean {
  // tl1403: a session-move window (recovery fork/promote surface follow) is
  // the one quiet shape that is NOT a dead turn — the writer changed files and
  // the follow hydration is re-binding the surface to it. Firing here would
  // reset a live turn out from under the move (the 2026-11-05 14:10 early
  // stop), so the exemption outranks the quiet verdict.
  if (moveInFlight) return false;
  const lastEvidenceAt = lastTurnActivityAt > 0 ? lastTurnActivityAt : state?.turnStartAt ?? 0;
  if (!state?.running || lastEvidenceAt <= 0) return false;
  return Math.max(0, now - lastEvidenceAt) >= timeoutMs;
}

export function useStaleTurnWatchdog<T extends StaleTurnWatchdogState>({
  tabId,
  visibleState,
  activeTabIdRef,
  statesRef,
  lastTurnActivityAtByTab,
  sessionMoveAtByTab,
  reconcile,
}: {
  tabId?: string;
  visibleState: StaleTurnWatchdogState;
  activeTabIdRef: RefObject<string | undefined>;
  statesRef: RefObject<Map<string, T>>;
  lastTurnActivityAtByTab: RefObject<Map<string, number>>;
  /** tl1403: tabId → move-start timestamp while a session-move follow
   * hydration is in flight. An absent map keeps the legacy behavior. */
  sessionMoveAtByTab?: RefObject<Map<string, number>>;
  reconcile: (tabId: string) => Promise<void>;
}) {
  useEffect(() => {
    if (!tabId) return;
    let cancelled = false;
    let timer: number | undefined;

    const moveInFlight = () => sessionMoveExemptionActive(sessionMoveAtByTab?.current?.get(tabId));

    const schedule = (delay?: number) => {
      if (cancelled || activeTabIdRef.current !== tabId) return;
      const current = statesRef.current.get(tabId);
      const lastActivityAt = lastTurnActivityAtByTab.current.get(tabId) ?? 0;
      const lastEvidenceAt = lastActivityAt > 0 ? lastActivityAt : current?.turnStartAt ?? 0;
      if (!current?.running || lastEvidenceAt <= 0) return;
      const nextDelay = delay ?? Math.max(0, STALE_TURN_RECONCILE_MS - Math.max(0, Date.now() - lastEvidenceAt));
      timer = window.setTimeout(() => { void tick(); }, nextDelay);
    };

    const tick = async () => {
      if (cancelled || activeTabIdRef.current !== tabId) return;
      const current = statesRef.current.get(tabId);
      const lastActivityAt = lastTurnActivityAtByTab.current.get(tabId) ?? 0;
      if (!shouldReconcileStaleTurn(current, lastActivityAt, Date.now(), STALE_TURN_RECONCILE_MS, moveInFlight())) {
        schedule();
        return;
      }
      // 任务874: 看门狗触发过去完全静默——2026-10-13 19:10 的 transcript 自行
      // 重建在 desktop.log 只留下 reason=reset，查不到这一步。留一行触发证据。
      const evidenceAt = lastActivityAt > 0 ? lastActivityAt : current?.turnStartAt ?? 0;
      reportFrontendLog(
        "turn-watchdog",
        "stale-turn watchdog fired",
        `tab=${tabId} quietForMs=${Math.max(0, Date.now() - evidenceAt)}`,
        "info",
      );
      await reconcile(tabId);
      if (!cancelled && activeTabIdRef.current === tabId && statesRef.current.get(tabId)?.running) {
        schedule(STALE_TURN_RECONCILE_MS);
      }
    };

    schedule();
    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [activeTabIdRef, lastTurnActivityAtByTab, reconcile, sessionMoveAtByTab, statesRef, tabId, visibleState.running, visibleState.turnStartAt]);
}
