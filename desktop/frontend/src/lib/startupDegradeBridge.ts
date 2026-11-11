// tl1543: frontend perception channel for the startup catalog degrade. The Go
// side emits one "app:startup-degraded" runtime event when the startup
// reconcile wave hands directories to the async queue (budget-out WARN's
// mode=deferred_to_async_queue, session_catalog_lifecycle.go). This module is
// the typed EventsOn wrapper (same shape as sessionCatalogBridge.ts) plus the
// retry delegate that rides the existing reconcile channel — no new reconcile
// mechanism, no new diagnostics surface.
import { app } from "./bridge";
import { reportFrontendLog } from "./frontendLog";
import { useStartupDegradeStore } from "../store/startupDegrade";

export const STARTUP_CATALOG_DEGRADED_EVENT = "app:startup-degraded";

export type StartupCatalogDegradedEvent = { deferred: number };

/** Subscribes to the Go degrade event and latches it into the store. Idempotent:
 *  both consumers (splash, project tree) may call it on mount. */
export function installStartupDegradeListener(): () => void {
  if (typeof window === "undefined" || !window.runtime?.EventsOn) return () => {};
  return window.runtime.EventsOn(STARTUP_CATALOG_DEGRADED_EVENT, (payload?: unknown) => {
    const deferred = typeof (payload as { deferred?: unknown } | undefined)?.deferred === "number"
      ? (payload as { deferred: number }).deferred
      : 0;
    useStartupDegradeStore.getState().markDegraded(deferred);
  });
}

/** The degrade banner's retry: re-kick every catalog directory through the
 *  existing reconcile channel (Go RetryStartupCatalogScan → the same
 *  requestProjectTreeCatalogRefresh primitive the watch loop rides) and clear
 *  the latch so the tree's regular notices take over again. */
export async function retryStartupCatalogScan(): Promise<void> {
  try {
    await app.RetryStartupCatalogScan?.();
  } catch (err) {
    // Diagnostics stay on the existing lane; the degrade latch stays up so the
    // user can retry again or use the existing reload/rebuild doors.
    reportFrontendLog("project-tree", "startup scan retry failed", `error=${err instanceof Error ? err.message : String(err)}`, "warn");
    return;
  }
  useStartupDegradeStore.getState().clearDegraded();
}
