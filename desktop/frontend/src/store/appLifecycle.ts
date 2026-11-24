// appLifecycle holds the run-time flags the shell sets once and several regions read:
// whether base appearance has been applied (the startup splash holds until it has, so the
// first frame cannot flash an unstyled shell). They live in a store rather than App-local
// state so a region reads the flag directly instead of receiving it through a prop chain
// (task 38).
//
// tl1796 r2：autopilotEnabled（composer autopilot 档显隐门）随撤开关退役——
// autopilot 档恒显，无键可关，本 store 不再承载该旗。

import { create } from "zustand";

type AppLifecycleState = {
  appearanceReady: boolean;
};

export const useAppLifecycleStore = create<AppLifecycleState>(() => ({
  appearanceReady: false,
}));

export const markAppearanceReady = (): void => {
  useAppLifecycleStore.setState((current) => (current.appearanceReady ? current : { appearanceReady: true }));
};
