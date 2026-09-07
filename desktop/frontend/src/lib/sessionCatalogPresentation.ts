import type { SessionCatalogStatus } from "./sessionCatalogTypes";

export type SessionCatalogNotice = "working" | "failed" | "rebuild";

export function sessionCatalogNotice(status: SessionCatalogStatus): SessionCatalogNotice | null {
  // Two fork-only dead branches removed here (2026-09-07):
  // 1. unindexedTargetCount — the Go backend never fills it (removed with the
  //    catalog case-collision fix).
  // 2. repairPending > 0 — two legacy sessions keep falling back to
  //    turns_state='unknown' on every full scan (their listing sidecars never
  //    go fresh), so repairPending can never reach 0 and the banner stayed up
  //    permanently. Repair continues in the background; the banner now only
  //    reflects a real catalog action (opening/rebuilding).
  const working = status.state === "opening" || status.state === "rebuilding";
  if (working) return "working";
  if (status.state === "degraded" || status.lastError) return status.canRebuild === true ? "rebuild" : "failed";
  return null;
}
