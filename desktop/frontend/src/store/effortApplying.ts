// effortApplying is the transition state for effort switches that took the
// fallback rebuild path (task 1301). The backend emits "effort:applying" the
// moment both per-request fast paths decline. Since tl1388 the rebuild itself
// runs in the background: the SetEffortForTab call returns at once, and the
// "applying reasoning effort" hint stays up for the rebuild's whole duration.
// It is cleared by the rebuild's runtime:rebuilt fence, by the backend's
// effort:settled event (an exit that never swaps — stale decline or failure),
// or by the call's own settle when no applying event was seen (fast path, or
// an event reordered past the settle). Persistence is out of scope: the state
// lives exactly as long as one in-flight rebuild.

import { create } from "zustand";

type EffortApplyingState = {
  /** Tab ids with an in-flight fallback effort rebuild. */
  applying: Record<string, true>;
  markApplying: (tabId: string) => void;
  clearApplying: (tabId: string) => void;
};

export const useEffortApplyingStore = create<EffortApplyingState>((set) => ({
  applying: {},
  markApplying: (tabId) => {
    const id = tabId.trim();
    if (!id) return;
    set((state) => (state.applying[id] ? state : { applying: { ...state.applying, [id]: true } }));
  },
  clearApplying: (tabId) => {
    const id = tabId.trim();
    if (!id) return;
    set((state) => {
      if (!state.applying[id]) return state;
      const next = { ...state.applying };
      delete next[id];
      return { applying: next };
    });
  },
}));

// The in-flight registry guards the mark side against cross-channel delivery
// order: the backend emits "effort:applying" early inside the Go call, but the
// event rides the tab sink's async queue and cannot be ordered against the
// call's response (desktop/tabs.go notifyTabEffortApplying; task 1323 corrects
// the earlier "one ordered transport" claim here). Only marks while the same
// tab has a switch actually in flight — an event reordered past its settle is
// dropped, so the transition can never stick; the accepted cost is a missed
// hint, never a stuck one. Since tl1388 the settle side keeps a marked hint
// up (the rebuild continues in the background — the fence and effort:settled
// own that clear), so a dropped event still means at most a missed hint.
// Known gap (task 1303 line owns the fix): the runtime:rebuilt backstop
// (clearEffortApplying) clears only the store, not this registry, so a late
// event after a backstop-only clear can re-light until the next settle.
const inFlight = new Set<string>();

/** Called when a SetEffortForTab call starts (before the await). */
export const beginEffortSwitch = (tabId: string): void => {
  if (!tabId.trim()) return;
  inFlight.add(tabId);
};

/**
 * Called when a SetEffortForTab call settles. With the async rebuild
 * (tl1388) a settle is no longer the end of the switch: when the
 * "effort:applying" event already marked the hint, the rebuild is still
 * running in the background and the runtime:rebuilt fence / effort:settled
 * event own the clear — clearing here would cut the transition short. An
 * unmarked settle (fast path, or a dropped event) still clears.
 */
export const endEffortSwitch = (tabId: string): void => {
  const id = tabId.trim();
  if (!id) return;
  inFlight.delete(id);
  if (useEffortApplyingStore.getState().applying[id]) return;
  useEffortApplyingStore.getState().clearApplying(id);
};

/** "effort:applying" event ingress — guarded so late events cannot stick the hint. */
export const handleEffortApplyingEvent = (tabId?: string): void => {
  if (!tabId || !inFlight.has(tabId)) return;
  useEffortApplyingStore.getState().markApplying(tabId);
};

/**
 * "effort:settled" event ingress (tl1388): the background rebuild finished —
 * swapped (the runtime:rebuilt fence lands alongside), turned out already
 * applied, or failed (the warn notice rides the chat). Full clear: the hint
 * and the in-flight guard, so a later event for this tab cannot re-light.
 */
export const handleEffortSettledEvent = (tabId?: string): void => {
  if (!tabId || !tabId.trim()) return;
  inFlight.delete(tabId);
  useEffortApplyingStore.getState().clearApplying(tabId);
};

/** Non-hook clear for code outside the switch lifecycle (runtime:rebuilt backstop). */
export const clearEffortApplying = (tabId: string): void => {
  useEffortApplyingStore.getState().clearApplying(tabId);
};
