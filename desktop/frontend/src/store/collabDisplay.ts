// collabDisplay holds the transcript display preference for cross-session
// messages (task 705). It is written once per settings load and read by the
// Message renderer, which sits far below the wiring that loads the settings —
// so the value lives here rather than being threaded down (task 38's
// shellPrefs pattern). Default off (铁律 2): every cross-session message
// renders in full, exactly as before the feature existed.

import { create } from "zustand";

type CollabDisplayState = {
  /** Task 705: fold over-long cross-session messages into a summary bar. */
  autoFold: boolean;
};

export const useCollabDisplayStore = create<CollabDisplayState>(() => ({
  autoFold: false,
}));

export const setCollabAutoFold = (enabled: boolean | undefined): void => {
  const next = enabled === true;
  useCollabDisplayStore.setState((current) => (current.autoFold === next ? current : { autoFold: next }));
};
