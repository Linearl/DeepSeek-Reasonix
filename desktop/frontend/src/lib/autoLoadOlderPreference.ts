import { useSyncExternalStore } from "react";

// Fork (task 160): loading older history by scrolling up at the transcript top is
// an experiment (`experimental_auto_load_older`). The default path is the explicit
// "load older" button, which does not depend on scroll geometry at all.
//
// The transcript surface reads the flag through this store instead of taking it as
// a prop: the surface is mounted deep under the chat shell and remounts per tab,
// while the preference arrives with the desktop settings view and can change while
// a surface is live (flipping the switch applies without a restart).
let enabled = false;
const listeners = new Set<() => void>();

export function setAutoLoadOlderEnabled(next: boolean): void {
  const value = Boolean(next);
  if (value === enabled) return;
  enabled = value;
  listeners.forEach((listener) => listener());
}

export function isAutoLoadOlderEnabled(): boolean {
  return enabled;
}

export function subscribeAutoLoadOlder(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function useAutoLoadOlderEnabled(): boolean {
  return useSyncExternalStore(subscribeAutoLoadOlder, isAutoLoadOlderEnabled, isAutoLoadOlderEnabled);
}
