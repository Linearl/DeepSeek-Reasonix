// effortApplying is the transition state for effort switches that took the
// fallback rebuild path (task 1301). The backend emits "effort:applying" the
// moment both per-request fast paths decline — the SetEffortForTab call then
// stays in flight for the whole runtime rebuild (seconds on recovery-forked
// sessions), and without this state the composer reads as frozen. The
// composer shows the "applying reasoning effort" hint beside the effort
// control while the tab id is in `applying`, and the same switch's settle
// (success or error) clears it. Persistence is out of scope: the state lives
// exactly as long as one in-flight switch.

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
// order: the backend emits "effort:applying" early inside the Go call, but a
// fast-failing call (unknown model, unsupported level) can return before the
// event reaches the webview. Only marks while the same tab has a switch
// actually in flight — a late event for a settled switch is ignored, so the
// transition can never stick. Wails delivers responses and events over one
// ordered transport, so within one switch the event (mid-call) always lands
// before the settle clear.
const inFlight = new Set<string>();

/** Called when a SetEffortForTab call starts (before the await). */
export const beginEffortSwitch = (tabId: string): void => {
  if (!tabId.trim()) return;
  inFlight.add(tabId);
};

/** Called when a SetEffortForTab call settles; the primary clear of the transition. */
export const endEffortSwitch = (tabId: string): void => {
  if (!tabId.trim()) return;
  inFlight.delete(tabId);
  useEffortApplyingStore.getState().clearApplying(tabId);
};

/** "effort:applying" event ingress — guarded so late events cannot stick the hint. */
export const handleEffortApplyingEvent = (tabId?: string): void => {
  if (!tabId || !inFlight.has(tabId)) return;
  useEffortApplyingStore.getState().markApplying(tabId);
};

/** Non-hook clear for code outside the switch lifecycle (runtime:rebuilt backstop). */
export const clearEffortApplying = (tabId: string): void => {
  useEffortApplyingStore.getState().clearApplying(tabId);
};
