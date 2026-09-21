import { useSyncExternalStore } from "react";

// Task 153: the guidance shelf's manual "merge next" button is an experiment
// (`collab_guidance_merge`). Off by default — the shelf then renders exactly
// the pre-153 row actions, so an untouched install sees no change.
//
// Same store shape as autoLoadOlderPreference: the composer reads the flag
// here instead of taking it as a prop, because the switch can flip while a
// composer is live and the settings snapshot arrives asynchronously.
let enabled = false;
const listeners = new Set<() => void>();

export function setCollabGuidanceMergeEnabled(next: boolean): void {
  const value = Boolean(next);
  if (value === enabled) return;
  enabled = value;
  listeners.forEach((listener) => listener());
}

export function isCollabGuidanceMergeEnabled(): boolean {
  return enabled;
}

export function subscribeCollabGuidanceMerge(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function useCollabGuidanceMergeEnabled(): boolean {
  return useSyncExternalStore(subscribeCollabGuidanceMerge, isCollabGuidanceMergeEnabled, isCollabGuidanceMergeEnabled);
}
