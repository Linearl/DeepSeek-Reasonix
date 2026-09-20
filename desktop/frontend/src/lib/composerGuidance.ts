export function guidanceNeedsRetry(state?: string): boolean {
  return state === "uncertain" || state === "blocked";
}

export function guidanceIsInFlight(state?: string): boolean {
	return state === "steer_accepted";
}

// The backend state machine has six states (internal/sessioninbox/types.go:
// queued / steer_accepted / steer_consumed / running / blocked / uncertain).
// Every one of them must be recognised here: a state missing from this list is
// silently treated as "unknown" by the shelf, which greys the row's actions out
// without ever saying why (task 159: `running` and `steer_consumed` were absent).
export function guidanceHasKnownPendingState(state?: string): boolean {
	return state === undefined || state === "" || state === "queued" || state === "blocked" || state === "uncertain" || state === "steer_accepted" || state === "running" || state === "steer_consumed";
}

// Picked up (`running`) or already handed to the agent (`steer_consumed`): the
// entry has left the cancellable queue — the backend's `cancellable` set stops at
// queued / steer_accepted / blocked / uncertain — so the destructive actions stay
// disabled, but through this explicit branch with a stated reason instead of the
// silent "unknown state" fallback.
export function guidanceIsDelivering(state?: string): boolean {
	return state === "running" || state === "steer_consumed";
}

export function guidanceIsEditable(item: { id?: string; state?: string; source?: string; paused?: boolean }): boolean {
  if (!item.id || item.id.startsWith("local-")) return false;
  if (item.paused || guidanceIsInFlight(item.state) || guidanceNeedsRetry(item.state)) return false;
  return item.state === "queued" || item.state === undefined || item.state === "";
}

// Task 181: the main-composer editor accepts a wider set than the old inline
// one-liner did. A multi-line guidance body needs a textarea, so the pencil now
// loads the entry into the composer instead of editing it in the shelf, and that
// path must also cover the two cases the inline editor refused:
//   - local-* optimistic entries, whose body is already in item.text (there is no
//     durable inbox row to read back),
//   - blocked/uncertain entries, which are edited here and then re-queued through
//     the existing RetryInboxItem path.
// In-flight (steer_accepted) and delivering (running/steer_consumed) entries stay
// closed: they are already on their way to the model, and the commit step asks
// what to do if an entry crosses that line while it is being edited.
export function guidanceEditableInComposer(item: { id?: string; state?: string; paused?: boolean }): boolean {
  if (!item.id) return false;
  if (item.paused) return false;
  if (guidanceIsInFlight(item.state) || guidanceIsDelivering(item.state)) return false;
  return guidanceHasKnownPendingState(item.state);
}

export function markGuidanceQueued<T extends { id: string; state?: string; paused?: boolean }>(items: T[], id: string): T[] {
  return items.map((item) => item.id === id ? { ...item, state: "queued", paused: false } : item);
}

// Resuming an already-unpaused inbox is an idempotent Controller drain kick.
export async function kickIdleGuidance(
  setPaused: (tabId: string, paused: boolean) => Promise<void>,
  tabId: string,
  refresh: () => void,
): Promise<void> {
  await setPaused(tabId, false);
  refresh();
}

// Exact equality avoids consuming a different queued item whose text merely
// contains the accepted steer (#6238).
export function guidanceTextMatches(queued: string, consumed: string): boolean {
  const left = queued.trim();
  const right = consumed.trim();
  return Boolean(left && right && left === right);
}
