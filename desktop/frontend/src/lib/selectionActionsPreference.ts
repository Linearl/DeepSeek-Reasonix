import { useSyncExternalStore } from "react";

// Task 369: the selection quick-actions experiment (`experimental_selection_actions`).
// Off by default — the selection menu then renders exactly the pre-369 surface.
//
// Same store shape as collabGuidanceMergePreference: the menu reads the flag
// here instead of taking it as a prop, because the switch can flip while the
// transcript is live and the settings snapshot arrives asynchronously.
let enabled = false;
const listeners = new Set<() => void>();

export function setSelectionActionsEnabled(next: boolean): void {
  const value = Boolean(next);
  if (value === enabled) return;
  enabled = value;
  listeners.forEach((listener) => listener());
}

export function isSelectionActionsEnabled(): boolean {
  return enabled;
}

export function subscribeSelectionActions(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function useSelectionActionsEnabled(): boolean {
  return useSyncExternalStore(subscribeSelectionActions, isSelectionActionsEnabled, isSelectionActionsEnabled);
}
