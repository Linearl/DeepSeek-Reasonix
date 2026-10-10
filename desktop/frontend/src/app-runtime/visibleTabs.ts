// The ordered, profile-decorated tab list the tab bar renders (task 38 batch C1).
//
// Pure derivation: it reads the tab registry, the manual order and the composer
// profiles, then returns what the tab bar shows. Nothing here mutates state, so
// the block can leave App.tsx without changing any behaviour.
import { useMemo } from "react";
import type { TabMeta } from "../lib/types";
import type { ComposerProfile } from "../lib/composerProfile";
import { projectVisibleTabs } from "./controllerProfileOwner";

export function useVisibleTabs(input: {
  tabMetas: TabMeta[];
  tabOrderIds: string[];
  composerProfilesByTab: Record<string, ComposerProfile>;
  running: boolean;
  pendingPrompt?: boolean;
  visibleTabId?: string;
}) {
  const { tabMetas, tabOrderIds, composerProfilesByTab, running, pendingPrompt, visibleTabId } = input;
  return useMemo(
    // 任务 730: single shared projection (waiting-on-prompt is not running) —
    // the hook used to duplicate it and the two copies could drift.
    () => projectVisibleTabs({
      tabs: tabMetas, orderIds: tabOrderIds, profiles: composerProfilesByTab,
      visibleTabId, running, pendingPrompt,
    }),
    [composerProfilesByTab, pendingPrompt, running, tabMetas, tabOrderIds, visibleTabId],
  );
}
