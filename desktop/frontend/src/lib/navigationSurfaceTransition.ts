export type SurfaceDataOutcome = "ready" | "failed" | "cancelled" | "superseded";

export type SurfaceDataCommit = {
  intent: number;
  outcome: SurfaceDataOutcome;
  tabId?: string;
  surfaceKey?: string;
  error?: string;
};

export type NavigationResult<T> = {
  value: T;
  surfaceReady: Promise<SurfaceDataCommit>;
};

export type NavigationSurfaceState = null | {
  intent: number;
  phase: "source-retained" | "target-masked";
  targetTabId?: string;
  targetSurfaceKey?: string;
};

/**
 * 任务 1026（fork issue #61）：导航遮罩的超时兜底窗口。settle 只能由后端导航
 * 回调驱动（App.tsx settleNavigationSurface），后端 hydrate/锁等待卡住不返回时
 * 遮罩会永久悬挂（现场表现为「加载中」遮罩只能靠强退重启解开）。此前 begin/
 * settle 只有两态、无任何前端自有出口——本常量是逃生口的时限，非加载预期时长。
 */
export const NAVIGATION_SURFACE_TIMEOUT_MS = 20_000;

export type SurfacePaintProgress = { attempts: number; stableFrames: number; geometryKey?: string };
export type SurfacePaintDecision = {
  progress: SurfacePaintProgress;
  outcome?: "ready" | "degraded";
  requestRecovery: boolean;
};

/** Deterministic paint gate shared by Transcript and its fake-clock tests. */
export function advanceSurfacePaintCommit(
  current: SurfacePaintProgress,
  sample: { rendered: boolean; placementReady: boolean; geometryReady: boolean; geometryKey?: string },
): SurfacePaintDecision {
  const attempts = current.attempts + 1;
  const ready = sample.rendered && sample.placementReady && sample.geometryReady && Boolean(sample.geometryKey);
  const stableFrames = ready
    ? (current.geometryKey === sample.geometryKey ? current.stableFrames + 1 : 1)
    : 0;
  const geometryKey = ready ? sample.geometryKey : undefined;
  if (stableFrames >= 2) {
    return { progress: { attempts, stableFrames, geometryKey }, outcome: "ready", requestRecovery: false };
  }
  if (attempts >= 180) {
    return { progress: { attempts, stableFrames, geometryKey }, outcome: "degraded", requestRecovery: false };
  }
  return {
    progress: { attempts, stableFrames, geometryKey },
    requestRecovery: attempts === 60 || attempts === 120,
  };
}

export type NavigationSurfaceIntent = number | null;

export type NavigationSurfaceTicket = Readonly<{
  token: string;
  intent: number;
  targetTabId: string;
  targetSessionKey: string;
}>;

let nextPaintReceipt = 0;

/** Opaque public token plus the complete internal target identity. */
export function createNavigationSurfaceTicket(
  intent: number,
  targetTabId: string,
  targetSessionKey: string,
): NavigationSurfaceTicket {
  return Object.freeze({
    token: `navigation-${intent}-${++nextPaintReceipt}`,
    intent,
    targetTabId,
    targetSessionKey,
  });
}

export function matchesNavigationSurfaceTicket(
  ticket: NavigationSurfaceTicket | null,
  token: string,
  intent: number | null,
  targetTabId: string | undefined,
  targetSessionKey: string,
): boolean {
  return Boolean(
    ticket
    && intent !== null
    && ticket.token === token
    && ticket.intent === intent
    && ticket.targetTabId === targetTabId
    && ticket.targetSessionKey === targetSessionKey,
  );
}

export function beginNavigationSurfaceState(intent: number): NavigationSurfaceState {
  return { intent, phase: "source-retained" };
}

/** The Wails/navigation call returned; target data may still be hydrating. */
export function markNavigationTargetMasked(
  current: NavigationSurfaceState,
  intent: number,
  targetTabId?: string,
  targetSurfaceKey?: string,
): NavigationSurfaceState {
  if (current?.intent !== intent) return current;
  return { ...current, phase: "target-masked", targetTabId, targetSurfaceKey };
}

/** Only a target paint terminal may release the opaque surface mask. */
export function settleNavigationSurfaceState(
  current: NavigationSurfaceState,
  completedIntent: number,
): NavigationSurfaceState {
  return current?.intent === completedIntent ? null : current;
}

/** Older completions must never release the latest navigation surface mask. */
export function settleNavigationSurfaceIntent(
  current: NavigationSurfaceIntent,
  completedIntent: number,
): NavigationSurfaceIntent {
  return current === completedIntent ? null : current;
}

type BackendNavigationResultGuard = {
  intent: number;
  targetTabId: string;
  kind: string;
  isIntentCurrent: (intent: number) => boolean;
  reassert: (kind: string, staleTabId: string) => Promise<void>;
};

/**
 * Backend reveal calls activate their returned tab before resolving. A stale
 * frontend result therefore needs an active repair, not just an ignored value.
 */
export async function guardBackendNavigationResult({
  intent,
  targetTabId,
  kind,
  isIntentCurrent,
  reassert,
}: BackendNavigationResultGuard): Promise<boolean> {
  if (isIntentCurrent(intent)) return true;
  await reassert(kind, targetTabId);
  return false;
}
