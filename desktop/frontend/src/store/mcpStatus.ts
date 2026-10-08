// mcpStatus is the global home for the MCP server snapshot the status-bar chip
// (task 559) renders from. The list used to live only inside the capabilities
// drawer / MCP settings page local state, which the status bar cannot reach —
// so it is lifted here. Persistence is out of scope: the chip refetches on
// mount and while it is visible, and the management pages publish their fresh
// reads into this store so user actions (retry, connect) reflect immediately.

import { create } from "zustand";

import { app } from "../lib/bridge";
import { mcpServersSignature } from "../lib/mcpStatus";
import type { ServerView } from "../lib/types";

type McpStatusState = {
  servers: ServerView[];
  /** Publish a snapshot already in hand (e.g. from the settings page reload). */
  publish: (servers: ServerView[]) => void;
  /** Best-effort re-read of app.MCPServers(); errors keep the prior snapshot. */
  refresh: () => Promise<void>;
};

// The bridge read is an in-memory Host snapshot, but the chip polls while it is
// visible — this floor keeps overlapping callers (chip + pages) from stacking
// reads, so the refresh cadence stays controlled instead of scaling with UI.
const REFRESH_MIN_INTERVAL_MS = 2000;

let lastFetchStartedAt = 0;
let inFlight: Promise<void> | null = null;

export const useMcpStatusStore = create<McpStatusState>((set, get) => ({
  servers: [],
  publish: (servers) => {
    const next = Array.isArray(servers) ? servers : [];
    // Diff gate: identical snapshots never touch state, so React does not
    // re-render the chip within a refresh cycle (flicker-free acceptance).
    if (mcpServersSignature(get().servers) === mcpServersSignature(next)) return;
    set({ servers: next });
  },
  refresh: () => {
    if (inFlight) return inFlight;
    if (Date.now() - lastFetchStartedAt < REFRESH_MIN_INTERVAL_MS) return Promise.resolve();
    lastFetchStartedAt = Date.now();
    inFlight = app
      .MCPServers()
      .then((servers) => {
        get().publish(servers);
      })
      .catch(() => {
        /* status chip is best-effort; keep the last known snapshot */
      })
      .finally(() => {
        inFlight = null;
      });
    return inFlight;
  },
}));

/** Non-hook publisher for code outside React (settings pages share their fresh reads). */
export const publishMcpServers = (servers: ServerView[]): void => {
  useMcpStatusStore.getState().publish(servers);
};
