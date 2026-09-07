import type { SessionCatalogStatus } from "./sessionCatalogTypes";

export type SessionCatalogNotice = "working" | "failed" | "rebuild";

export function sessionCatalogNotice(status: SessionCatalogStatus): SessionCatalogNotice | null {
  // unindexedTargetCount was a fork-only dead branch: the Go backend never
  // fills it, so the > 0 test could never fire (removed with the catalog
  // case-collision fix).
  const working = status.state === "opening"
    || status.state === "rebuilding"
    || status.repairPending > 0;
  if (working) return "working";
  if (status.state === "degraded" || status.lastError) return status.canRebuild === true ? "rebuild" : "failed";
  return null;
}
