// perfDebug — the window.__reasonixPerf introspection hook used by the
// real-DOM benchmark harness (desktop/frontend/bench). It is installed only
// when the page URL carries `?bench=1`, so production WebView sessions never
// see it. Everything exposed is the same content-free diagnostic state the
// crash reporter snapshots: activation timings, history page stats, cache
// weights, markdown worker counters, mounted row counts.

import {
  activationLog,
  resetSessionDiagnostics,
  sessionPipelineDiagnostics,
  type ActivationDiagnostic,
  type SessionPipelineDiagnostics,
} from "./sessionDiagnostics";
import { drainFrontendLogRing, peekFrontendLogRing } from "./frontendLog";

export interface ReasonixPerfHook {
  /** Aggregate point-in-time diagnostics snapshot. */
  stats(): SessionPipelineDiagnostics;
  /** Recent activation records (oldest first), for per-switch timings. */
  activations(): ActivationDiagnostic[];
  /** Clear activation/history counters (between benchmark scenarios). */
  reset(): void;
  /** Snapshot of the frontend log ring (hydrate decisions, fill lines). */
  frontendLog(): readonly string[];
  /** Take and clear the frontend log ring (between benchmark iterations). */
  drainLog(): string[];
}

declare global {
  interface Window {
    __reasonixPerf?: ReasonixPerfHook;
  }
}

export function installPerfDebugHook(): void {
  if (typeof window === "undefined") return;
  if (!new URLSearchParams(window.location.search).has("bench")) return;
  window.__reasonixPerf = {
    stats: () => sessionPipelineDiagnostics(),
    activations: () => activationLog(),
    reset: () => resetSessionDiagnostics(),
    frontendLog: () => peekFrontendLogRing(),
    drainLog: () => drainFrontendLogRing(),
  };
}
