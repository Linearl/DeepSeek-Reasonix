// mcpStatus is the global home for the MCP server snapshot the status-bar chip
// (task 559) renders from. The list used to live only inside the capabilities
// drawer / MCP settings page local state, which the status bar cannot reach —
// so it is lifted here. Persistence is out of scope: the chip refetches on
// mount and while it is visible, and the management pages publish their fresh
// reads into this store so user actions (retry, connect) reflect immediately.
//
// tl1496b（provider-poll 同类面 扩展，用户 2026-11-08 两裁定）：失败有界——
// MCPServers() 连续失败达 MCP_STATUS_FAILURE_LIMIT（3 次，与 planUsage 刀①
// 同档）即置 paused：芯片的固定节拍停发（tick 零调用），最后一次快照保持
// 上屏并在 tooltip 标注暂停。恢复=任一次成功读数（publish 路径统一清零，
// 含管理页发布/焦点单发重查/芯片重挂载的一次性查询）；手动级路径永远单发、
// 不被 paused 拦截（与 planUsage「手动刷新永远单发」同语义）。无 MCP 配置
// （空快照 → summarize=hidden）芯片不挂载，本来就零调用——无活跃门控由
// 渲染层既有判定承载，store 不再重复。

import { create } from "zustand";

import { app } from "../lib/bridge";
import { mcpServersSignature } from "../lib/mcpStatus";
import type { ServerView } from "../lib/types";

type McpStatusState = {
  servers: ServerView[];
  /** tl1496b: consecutive failed reads of the polling query; reaching
   * MCP_STATUS_FAILURE_LIMIT sets paused (ticks stop until a success). */
  failStreak: number;
  /** tl1496b: polling paused after consecutive failures. Single-shot paths
   * (page publish / focus re-check / remount) are never gated by this. */
  paused: boolean;
  /** Publish a snapshot already in hand (e.g. from the settings page reload). */
  publish: (servers: ServerView[]) => void;
  /** Best-effort re-read of app.MCPServers(); errors keep the prior snapshot
   * and count toward the bounded-retry pause. */
  refresh: () => Promise<void>;
};

// The bridge read is an in-memory Host snapshot, but the chip polls while it is
// visible — this floor keeps overlapping callers (chip + pages) from stacking
// reads, so the refresh cadence stays controlled instead of scaling with UI.
const REFRESH_MIN_INTERVAL_MS = 2000;

// tl1496b: bounded-retry limit — same tier as planUsage 刀① (3 consecutive
// failures stop the fixed cadence until one success re-verifies).
export const MCP_STATUS_FAILURE_LIMIT = 3;

let lastFetchStartedAt = 0;
let inFlight: Promise<void> | null = null;

export const useMcpStatusStore = create<McpStatusState>((set, get) => ({
  servers: [],
  failStreak: 0,
  paused: false,
  publish: (servers) => {
    const next = Array.isArray(servers) ? servers : [];
    // tl1496b: a landed read — any provenance — is success evidence; the
    // bounded-retry gates clear even when the snapshot itself is unchanged
    // (they describe query health, not payload content).
    if (get().failStreak !== 0 || get().paused) set({ failStreak: 0, paused: false });
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
        // status chip is best-effort; keep the last known snapshot. The
        // failure still counts toward the bounded-retry pause.
        const streak = get().failStreak + 1;
        if (streak >= MCP_STATUS_FAILURE_LIMIT) set({ failStreak: streak, paused: true });
        else set({ failStreak: streak });
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

// ── Test hooks (tl1496b) ────────────────────────────────────────────────────
// The 2s refresh floor is wall-clock module state, so a same-ms test sequence
// of failures would be floored into zero calls before the bounded-retry limit
// can trigger. The bypass clears ONLY the floor timestamps; query gates
// (failStreak/paused) stay untouched so the streak test drives the real
// accounting. The full reset is the suite's arrange/teardown (planUsage's
// stopPlanUsagePollingForTest convention).
export const bypassMcpRefreshFloorForTest = (): void => {
  lastFetchStartedAt = 0;
};

export const resetMcpStatusStoreForTest = (): void => {
  lastFetchStartedAt = 0;
  inFlight = null;
  useMcpStatusStore.setState({ servers: [], failStreak: 0, paused: false });
};
