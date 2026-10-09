// 任务676: a history read that lands while a writer holds the session files
// (busy / lock contention — same root cause task 671 traced on the catalog
// side) is a TRANSIENT failure, but the hydrate path used to surface it to the
// user as the "failed to load conversation history" banner even though a
// manual retry always succeeded. Governance (user decision 20261009): a
// BOUNDED silent auto-retry — at most 3 attempts with a 500ms backoff — so the
// transient window heals without any UI; only when every attempt fails does
// the manual-retry banner appear. Unbounded retrying is explicitly forbidden:
// the limits are module constants, not caller-tunable knobs.

/** Maximum number of AUTOMATIC retries after the initial attempt (1 + 3 = 4 reads total). */
export const HISTORY_AUTO_RETRY_LIMIT = 3;
/** Backoff before each automatic retry. Worst case adds ~1.5s before the banner. */
export const HISTORY_AUTO_RETRY_DELAY_MS = 500;

export interface BoundedAutoRetryOptions {
  /** Overrides for tests only; production always uses the constants above. */
  retries?: number;
  delayMs?: number;
  /** Injectable clock so tests never wait real time. Defaults to setTimeout. */
  sleep?: (ms: number) => Promise<void>;
  /**
   * Abort predicate checked before every retry (not before the first attempt):
   * when the tab switched away or the session changed, stop silently — a stale
   * hydrate must neither retry nor report failure.
   */
  shouldContinue?: () => boolean;
  /** Called before retry attempt `attempt` (1-based) with the backoff pending. */
  onRetry?: (attempt: number) => void;
}

/**
 * Run `attempt` and, while it resolves `undefined` (the caller's shape for
 * "failed, cause already recorded"), retry it up to HISTORY_AUTO_RETRY_LIMIT
 * more times with HISTORY_AUTO_RETRY_DELAY_MS backoff. Returns the first
 * defined result, or undefined once the budget is exhausted — the caller then
 * owns surfacing the manual-retry banner exactly as before.
 */
export async function runBoundedAutoRetry<T>(
  attempt: () => Promise<T | undefined>,
  options: BoundedAutoRetryOptions = {},
): Promise<T | undefined> {
  const retries = options.retries ?? HISTORY_AUTO_RETRY_LIMIT;
  const delayMs = options.delayMs ?? HISTORY_AUTO_RETRY_DELAY_MS;
  const sleep = options.sleep ?? ((ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms)));
  let value = await attempt();
  for (let n = 1; value === undefined && n <= retries && (options.shouldContinue?.() ?? true); n++) {
    options.onRetry?.(n);
    await sleep(delayMs);
    value = await attempt();
  }
  return value;
}
