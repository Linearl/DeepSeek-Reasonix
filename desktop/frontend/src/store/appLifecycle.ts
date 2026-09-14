// appLifecycle holds the run-time flags the shell sets once and several regions read:
// whether base appearance has been applied (the startup splash holds until it has, so the
// first frame cannot flash an unstyled shell) and whether the active session runs in
// autopilot (the composer surfaces it). They live in a store rather than App-local state so
// a region reads the flag directly instead of receiving it through a prop chain (task 38).

import { create } from "zustand";

type AppLifecycleState = {
  appearanceReady: boolean;
  autopilotEnabled: boolean;
};

export const useAppLifecycleStore = create<AppLifecycleState>(() => ({
  appearanceReady: false,
  autopilotEnabled: false,
}));

export const markAppearanceReady = (): void => {
  useAppLifecycleStore.setState((current) => (current.appearanceReady ? current : { appearanceReady: true }));
};

export const setAutopilotEnabled = (enabled: boolean): void => {
  useAppLifecycleStore.setState((current) =>
    current.autopilotEnabled === enabled ? current : { autopilotEnabled: enabled },
  );
};
