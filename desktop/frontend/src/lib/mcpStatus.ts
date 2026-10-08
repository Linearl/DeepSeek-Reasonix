// mcpStatus aggregates the bridge's MCP server snapshot (lib/types ServerView)
// into the single status-bar chip state task 559 defines: the chip only takes
// space while the fleet is NOT fully settled —
//   connecting  → "连接中 n/N" with a spinner
//   failed      → "MCP 失败 m" (click opens the MCP management page)
//   otherwise   → hidden (all connected, or only deferred/disabled servers:
//                 that steady state carries no information worth the noise).

import type { ServerView } from "./types";

export type McpStatusSummary =
  | { state: "hidden" }
  | { state: "connecting"; connecting: number; connected: number; total: number }
  | { state: "failed"; failed: number };

/** Servers expected to reach "connected" on their own; deferred/disabled are out of scope. */
export function summarizeMcpServers(servers: readonly ServerView[]): McpStatusSummary {
  let failed = 0;
  let connecting = 0;
  let connected = 0;
  for (const server of servers ?? []) {
    if (server.status === "failed") failed += 1;
    // "initializing" and a live "connecting" runtimeState both mean the server
    // is actively coming up — even a deferred/on-demand one started by hand.
    else if (server.status === "initializing" || server.runtimeState === "connecting") connecting += 1;
    else if (server.status === "connected") connected += 1;
    // disabled / deferred / unknown steady states carry no chip information
  }
  const total = connecting + connected + failed;
  if (failed > 0) return { state: "failed", failed };
  if (connecting > 0) return { state: "connecting", connecting, connected, total };
  return { state: "hidden" };
}

/**
 * Signature of the fields the chip renders. The store publishes through this
 * gate so an unchanged snapshot never triggers a React update — that is what
 * keeps the chip flicker-free across settings reloads (task 559 acceptance).
 */
export function mcpServersSignature(servers: readonly ServerView[]): string {
  return (servers ?? [])
    .map((server) => `${server.name}:${server.status}:${server.runtimeState ?? ""}:${server.availability ?? ""}`)
    .join("|");
}
