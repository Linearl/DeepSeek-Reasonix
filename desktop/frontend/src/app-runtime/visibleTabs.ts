// The ordered, profile-decorated tab list the tab bar renders (task 38 batch C1).
//
// Pure derivation: it reads the tab registry, the manual order and the composer
// profiles, then returns what the tab bar shows. Nothing here mutates state, so
// the block can leave App.tsx without changing any behaviour.
import { useMemo } from "react";
import type { TabMeta } from "../lib/types";
import {
  composerProfileFromTab,
  composerProfileMode,
  displayedComposerProfileCollaborationMode,
  type ComposerProfile,
} from "../lib/composerProfile";

export function useVisibleTabs(input: {
  tabMetas: TabMeta[];
  tabOrderIds: string[];
  composerProfilesByTab: Record<string, ComposerProfile>;
  running: boolean;
  visibleTabId?: string;
}) {
  const { tabMetas, tabOrderIds, composerProfilesByTab, running, visibleTabId } = input;
  return useMemo(() => {
    const byId = new Map(tabMetas.map((tab) => [tab.id, tab]));
    const ordered = tabOrderIds.map((id) => byId.get(id)).filter((tab): tab is TabMeta => Boolean(tab));
    const missing = tabMetas.filter((tab) => !tabOrderIds.includes(tab.id));
    return [...ordered, ...missing].map((tab) => {
      const profile = composerProfilesByTab[tab.id] ?? composerProfileFromTab(tab);
      return {
        ...tab,
        running: tab.id === visibleTabId ? tab.running || running : tab.running,
        mode: composerProfileMode(profile),
        collaborationMode: displayedComposerProfileCollaborationMode(profile),
        toolApprovalMode: profile.toolApprovalMode,
        goal: profile.goal,
        active: tab.id === visibleTabId,
      };
    });
  }, [composerProfilesByTab, running, tabMetas, tabOrderIds, visibleTabId]);
}
