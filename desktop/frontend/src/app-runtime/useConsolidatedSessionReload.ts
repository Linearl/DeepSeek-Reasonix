import { useEffect } from "react";

import { onSessionConsolidated } from "../lib/bridge";

// tl1401/tl1402: the backend emits session:consolidated once per successful
// transcript rewrite — the merge path (desktop/recovery_consolidate.go) and
// the head-synthesis path (desktop/recovery_head_synthesize.go) share the same
// event and payload shape. Tabs showing that session reload in place — without
// the event the pane keeps its open-time snapshot and the user reads stale
// content as lost history (20261105 实测：合成成功落盘，打开的对话不刷新).
export const SESSION_CONSOLIDATED_EVENT = "session:consolidated";

/**
 * Subscribes to the consolidation-done event and funnels the merged session's
 * main path to `onConsolidated` (App passes the controller's
 * reloadTranscriptsForConsolidatedSession, which reloads every matching tab
 * with reason=synthesize-done). Malformed payloads are ignored: the sidebar's
 * project-tree:changed-v2 refresh does not depend on this channel, so a bad
 * frame must not trigger a transcript reload with an empty path.
 */
export function useConsolidatedSessionReload(onConsolidated: (mainPath: string) => void) {
  useEffect(() => {
    // The bridge wrapper no-ops (returns a no-op off) outside a live runtime,
    // so the guard lives in one place.
    return onSessionConsolidated((payload) => {
      const mainPath = payload?.mainPath;
      if (typeof mainPath === "string" && mainPath.trim()) onConsolidated(mainPath);
    });
  }, [onConsolidated]);
}
