// modelApplying is the transition state for model switches that took the
// fallback rebuild path (tl1389, the task-1301 effortApplying store carried
// to the model side). The backend emits "model:applying" the moment both
// per-request fast paths decline — the SetModelForTab call then stays in
// flight for the whole runtime rebuild (seconds on recovery-forked
// sessions), and without this state the composer's model name reads as
// frozen on the outgoing label. The composer shows the "applying model"
// hint beside the switcher while the tab id is in `applying`, and the same
// switch's settle clears it (the settled "tab:meta" push and the
// runtime:rebuilt fence are the backstops). Persistence is out of scope:
// the state lives exactly as long as one in-flight switch.

import { create } from "zustand";

type ModelApplyingState = {
  /** Tab ids with an in-flight fallback model rebuild. */
  applying: Record<string, true>;
  markApplying: (tabId: string) => void;
  clearApplying: (tabId: string) => void;
};

export const useModelApplyingStore = create<ModelApplyingState>((set) => ({
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
// order: the backend emits "model:applying" early inside the Go call, but a
// fast-failing call (unknown model, provider access) can return before the
// event reaches the webview. Only marks while the same tab has a switch
// actually in flight — an event reordered past its settle is dropped, so the
// transition can never stick; the accepted cost is a missed hint, never a
// stuck one (same trade the effortApplying registry documents).
const inFlight = new Set<string>();

/** Called when a SetModelForTab call starts (before the await). */
export const beginModelSwitch = (tabId: string): void => {
  if (!tabId.trim()) return;
  inFlight.add(tabId);
};

/** Called when a SetModelForTab call settles; the primary clear of the transition. */
export const endModelSwitch = (tabId: string): void => {
  if (!tabId.trim()) return;
  inFlight.delete(tabId);
  useModelApplyingStore.getState().clearApplying(tabId);
};

/** "model:applying" event ingress — guarded so late events cannot stick the hint. */
export const handleModelApplyingEvent = (tabId?: string): void => {
  if (!tabId || !inFlight.has(tabId)) return;
  useModelApplyingStore.getState().markApplying(tabId);
};

/** Non-hook clear for code outside the switch lifecycle (runtime:rebuilt backstop). */
export const clearModelApplying = (tabId: string): void => {
  useModelApplyingStore.getState().clearApplying(tabId);
};
