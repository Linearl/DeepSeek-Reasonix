// tl1543: session-latched degrade state for the loading surface. The startup
// catalog wave hands past-budget directories to the async reconcile queue and
// emits one "app:startup-degraded" event (Go: session_catalog_lifecycle.go);
// this store is the single latch both consumers read — the startup splash and
// the project tree's catalog-progress slot — so navigating away and back does
// not silently drop the degrade banner. Same zustand mechanism as overlays.
import { create } from "zustand";

export type StartupDegradeState = {
  degraded: boolean;
  deferredDirs: number;
  markDegraded: (deferredDirs: number) => void;
  clearDegraded: () => void;
};

export const useStartupDegradeStore = create<StartupDegradeState>((set) => ({
  degraded: false,
  deferredDirs: 0,
  markDegraded: (deferredDirs) => set({ degraded: true, deferredDirs: Math.max(0, Math.floor(deferredDirs) || 0) }),
  clearDegraded: () => set({ degraded: false, deferredDirs: 0 }),
}));
