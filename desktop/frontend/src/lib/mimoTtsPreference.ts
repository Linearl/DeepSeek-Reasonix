import { useSyncExternalStore } from "react";

// tl1817: the read-aloud MiMo TTS gate (`experimental_mimo_tts`). Off by
// default — the speak path is then exactly the system speechSynthesis chain
// (tl1709/tl1815, byte-for-byte).
//
// Same store shape as selectionActionsPreference: the speak path reads the
// flag here instead of taking it as a prop, because the switch can flip
// while the transcript is live and the settings snapshot arrives
// asynchronously. Boot-snapshot fed (App.tsx applies it on every settings
// load, task 514 rule), live flip on settings save — the speak path ALSO
// re-checks the credential server-side at click time, so this store is the
// gate, not the credential truth.
let enabled = false;
const listeners = new Set<() => void>();

export function setMimoTtsEnabled(next: boolean): void {
  const value = Boolean(next);
  if (value === enabled) return;
  enabled = value;
  listeners.forEach((listener) => listener());
}

export function isMimoTtsEnabled(): boolean {
  return enabled;
}

export function subscribeMimoTts(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function useMimoTtsEnabled(): boolean {
  return useSyncExternalStore(subscribeMimoTts, isMimoTtsEnabled, isMimoTtsEnabled);
}
